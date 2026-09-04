package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/dockermem"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// reportPortClashes says which ports two stacks would fight over, between
// projects and between worktrees of one. Everything it needs is already
// recorded: each project's offset, stride, compose ports and branch indices.
func (a *app) reportPortClashes() {
	var holders []portHolder
	for _, name := range a.cfg.Names() {
		p := a.cfg.Projects[name]
		services, err := compose.MergedServicePorts(p.Dir)
		if err != nil {
			continue // no compose file, or a directory that moved: nothing to compute
		}
		stride := stack.Stride(p.Dir)
		branches := make([]string, 0, len(p.WorktreeIndices))
		for branch := range p.WorktreeIndices {
			branches = append(branches, branch)
		}
		sort.Strings(branches)
		for _, branch := range branches {
			allocations, err := stack.Allocate(services, p.WorktreeIndices[branch], stride, p.PortOffset)
			if err != nil {
				continue
			}
			for _, al := range allocations {
				label := al.Var
				if label == "" {
					label = al.Service + ":" + al.Container
				}
				holders = append(holders, portHolder{Port: al.Port, Project: name, Branch: branch, Label: label})
			}
		}
	}
	if clashes := portClashes(holders); len(clashes) > 0 {
		fmt.Fprintln(a.out)
		fmt.Fprintln(a.out, "port clashes between projects (those stacks cannot run at the same time):")
		for _, line := range clashes {
			fmt.Fprintf(a.out, "  %s\n", line)
		}
		fmt.Fprintln(a.out, "  offsets are handed out once, at registration: keep those stacks from running together, or")
		fmt.Fprintln(a.out, "  raise `port_offset` for one project in config.json and recreate its worktrees, whose .env carry the old ports")
	}
	if clashes := intraProjectClashes(holders); len(clashes) > 0 {
		fmt.Fprintln(a.out)
		fmt.Fprintln(a.out, "port clashes between worktrees of one project (those two cannot run at the same time):")
		for _, line := range clashes {
			fmt.Fprintf(a.out, "  %s\n", line)
		}
		fmt.Fprintln(a.out, "  the stride is too small for ports that sit close together: set `portStride` in the project's")
		fmt.Fprintln(a.out, "  .wtcrc.json above their spread, then recreate the worktrees, whose .env carry the old ports")
	}
}

// repoWorktrees pairs a registered project's repository name with the compose
// project of every worktree it still has. A project whose git cannot answer is
// left out: assuming everything it owns is orphan would be worse.
type repoWorktrees struct {
	Repo string
	Name string // project name in the registry, for the command lines
	Live []string
	// Unindexed holds the branches whose index the registry does not carry,
	// created before indices were recorded or started while docker was down.
	// Their compose project name cannot be derived, so none can be called orphan.
	Unindexed []string
	// Stale are branches with a recorded index and no worktree, left by a removal
	// outside wtm. Each pushes new worktrees one index further out, and makes a
	// foreign worktree on that branch read as managed.
	Stale []string
}

func (a *app) liveProjects(ctx context.Context, names []string) []repoWorktrees {
	var out []repoWorktrees
	for _, name := range names {
		p := a.cfg.Projects[name]
		// Managed is what lets an adopted worktree count as present: without
		// it every adopted branch would read as stale.
		client := &stack.Client{Runner: a.runner, Dir: p.Dir, Managed: managed(p)}
		worktrees, err := client.Worktrees(ctx)
		if err != nil {
			continue
		}
		repo := filepath.Base(p.Dir)
		live := make([]string, 0, len(worktrees))
		var unindexed []string
		present := map[string]bool{}
		for _, wt := range worktrees {
			present[wt.Branch] = true
			if idx := p.WorktreeIndices[wt.Branch]; idx > 0 {
				live = append(live, stack.ProjectName(repo, idx, wt.Branch))
				continue
			}
			unindexed = append(unindexed, wt.Branch)
		}
		var stale []string
		for branch := range p.WorktreeIndices {
			if !present[branch] {
				stale = append(stale, branch)
			}
		}
		sort.Strings(stale)
		out = append(out, repoWorktrees{Repo: repo, Name: name, Live: live, Unindexed: unindexed, Stale: stale})
	}
	return out
}

func (a *app) reportStaleIndices(stale []staleIndex) {
	var lines, cmds []string
	for _, s := range stale {
		lines = append(lines, fmt.Sprintf("%s: index %d is recorded for %s, which has no worktree", s.Project, s.Index, s.Branch))
		cmds = append(cmds, fmt.Sprintf("wtm remove %s %s", s.Project, s.Branch))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, "recorded indices with no worktree behind them (each pushes new worktrees one index further out):")
	for _, l := range lines {
		fmt.Fprintf(a.out, "  %s\n", l)
	}
	fmt.Fprintf(a.out, "  release them with `%s`\n", strings.Join(cmds, "`, `"))
}

// reportOrphanStacks lists the containers of worktrees that no longer exist.
// The index allocator sees them and refuses their index; until this report,
// nothing told the developer, so `wtm clean` said "done" and left them running.
func (a *app) reportOrphanStacks(orphans []orphanStack) {
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	fmt.Fprintf(a.out, "%d stack(s) of removed worktrees, still holding their containers "+
		"and the indices their ports came from:\n", len(orphans))
	cmds := make([]string, 0, len(orphans))
	for _, o := range orphans {
		fmt.Fprintf(a.out, "  %s\n", o.Stack)
		cmds = append(cmds, fmt.Sprintf("docker compose -p %s down --volumes", o.Stack))
	}
	fmt.Fprintf(a.out, "  take them down with `%s`\n", strings.Join(cmds, "`, `"))
}

// reportOrphanVolumes lists the volumes of worktrees that no longer exist.
// They squat the indices their stacks were created at, which pushes every new
// worktree further out, and nothing else ever mentions them.
func (a *app) reportOrphanVolumes(orphans []string) {
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	fmt.Fprintf(a.out, "%d volume(s) of removed worktrees, squatting the indices their ports came from:\n", len(orphans))
	for _, name := range orphans {
		fmt.Fprintf(a.out, "  %s\n", name)
	}
	fmt.Fprintf(a.out, "  drop them with `docker volume rm %s`\n", strings.Join(orphans, " "))
}

// reportAnonymousVolumes counts what the orphan report cannot see: volumes an
// image created on its own, labelled anonymous, that no container mounts.
// Machine-wide by nature, hence a count and a command rather than an attribution.
func (a *app) reportAnonymousVolumes(ctx context.Context) {
	res, err := a.runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"volume", "ls", "-q",
		"--filter", "dangling=true", "--filter", "label=com.docker.volume.anonymous"}})
	if err != nil {
		return
	}
	ids := strings.Fields(res.Stdout)
	if len(ids) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	fmt.Fprintf(a.out, "%d anonymous volume(s) no container mounts, left by images that name their own data directory:\n", len(ids))
	// The same filters that found them, rather than a line of 64-character ids.
	fmt.Fprintln(a.out, "  drop them with `docker volume rm $(docker volume ls -q "+
		"--filter dangling=true --filter label=com.docker.volume.anonymous)`")
}

// reportOrphanImages lists what worktrees that no longer exist had compose
// build for them. A stack builds one image per service, so this list is several
// times longer than the volume one for the same removed worktrees.
func (a *app) reportOrphanImages(orphans []string) {
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	fmt.Fprintf(a.out, "%d image(s) built for removed worktrees, several GB each:\n", len(orphans))
	for _, name := range orphans {
		fmt.Fprintf(a.out, "  %s\n", name)
	}
	fmt.Fprintf(a.out, "  drop them with `docker rmi %s`\n", strings.Join(orphans, " "))
}

// buildCache is reported and never removed: buildkit attributes none of it, so
// only the developer can decide it is expendable. `docker system df` has the
// number too, but walks the image store for it where `buildx du` takes a second.
func (a *app) buildCache(ctx context.Context) string {
	res, err := a.runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"buildx", "du"}})
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "Total:" {
			continue
		}
		return fmt.Sprintf("%s of build cache (`docker builder prune`)", fields[1])
	}
	return ""
}

func newDoctorCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:           "doctor",
		Short:         "Diagnoses the configuration, the Docker VM and what removed worktrees left behind",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(a.out, "version  %s\n", version())
			if latest := a.newerRelease(cmd.Context()); latest != "" {
				fmt.Fprintf(a.out, "         %s is published, upgrade with "+
					"`go install github.com/Hy0sh/worktree-manager/cmd/wtm@latest`\n", latest)
			}
			fmt.Fprintf(a.out, "config   %s\n", a.cfgPath)
			fmt.Fprintf(a.out, "backups  %s\n", a.backups)
			if u, err := dockermem.Read(cmd.Context(), a.runner); err == nil && u.Total > 0 {
				// A native Linux docker shares the machine's memory, so Used
				// covers the desktop too and the line has to say whose it is.
				if u.Shared {
					fmt.Fprintf(a.out, "memory   %s used out of %s on this machine, %d stack(s) "+
						"accounting for %s (~%s per stack)\n",
						dockermem.Human(u.Used), dockermem.Human(u.Total), u.Projects,
						dockermem.Human(u.StackUsed), dockermem.Human(u.PerProject()))
				} else {
					fmt.Fprintf(a.out, "docker   %s used out of %s, %d stack(s) running (~%s per stack)\n",
						dockermem.Human(u.Used), dockermem.Human(u.Total), u.Projects,
						dockermem.Human(u.PerProject()))
				}
				if msg := u.Warning(); msg != "" {
					fmt.Fprintln(a.out, msg)
				}
			}
			if line := a.buildCache(cmd.Context()); line != "" {
				fmt.Fprintf(a.out, "cache    %s\n", line)
			}
			if len(a.cfg.Projects) > 0 {
				fmt.Fprintln(a.out)
				w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "PROJECT\tDIRECTORY\tSTRIDE\tOFFSET\tENGINE")
				for _, name := range a.cfg.Names() {
					p := a.cfg.Projects[name]
					// BackupConfig defaults to postgres even without a database.
					engine := "-"
					if p.Dump {
						engine = p.BackupConfig().DBEngine
					}
					fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", name, p.Dir, stack.Stride(p.Dir), p.PortOffset, engine)
				}
				if err := w.Flush(); err != nil {
					return err
				}
				a.reportPortClashes()
				rws := a.liveProjects(cmd.Context(), a.cfg.Names())
				stale := a.staleIndices(rws)
				stacks := a.orphanStackNames(cmd.Context(), rws)
				volumes := a.orphanVolumeNames(cmd.Context(), rws)
				images := a.orphanImageNames(cmd.Context(), rws)
				a.reportStaleIndices(stale)
				a.reportOrphanStacks(stacks)
				a.reportOrphanVolumes(volumes)
				a.reportOrphanImages(images)
				// Each block above ends on its own command line, one per
				// finding: seven of them on a busy machine, and nothing would
				// otherwise say a single verb covers the lot.
				if len(stale)+len(stacks)+len(volumes)+len(images) > 0 {
					fmt.Fprintln(a.out, "\n`wtm clean` runs all of that in one go.")
				}
			}
			// Anonymous volumes are machine-wide, not tied to a registered
			// project: the only report an empty registry still has an answer for.
			a.reportAnonymousVolumes(cmd.Context())
			return nil
		},
	}
}
