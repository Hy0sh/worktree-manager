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

// reportPortClashes says which ports two projects would fight over. Everything
// it needs is already recorded: each project's offset, stride, compose ports
// and the index of every branch that owns a stack.
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
	clashes := portClashes(holders)
	if len(clashes) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, "port clashes between projects (those stacks cannot run at the same time):")
	for _, line := range clashes {
		fmt.Fprintf(a.out, "  %s\n", line)
	}
	fmt.Fprintln(a.out, "  offsets are handed out once, at registration: keep those stacks from running together, or")
	fmt.Fprintln(a.out, "  raise `port_offset` for one project in config.json and recreate its worktrees, whose .env carry the old ports")
}

// repoWorktrees pairs a registered project's repository name with the compose
// project of every worktree it still has. A project whose git cannot answer is
// left out: assuming everything it owns is orphan would be worse.
type repoWorktrees struct {
	Repo string
	Live []string
}

func (a *app) liveProjects(ctx context.Context) []repoWorktrees {
	var out []repoWorktrees
	for _, name := range a.cfg.Names() {
		p := a.cfg.Projects[name]
		client := &stack.Client{Runner: a.runner, Dir: p.Dir}
		worktrees, err := client.Worktrees(ctx)
		if err != nil {
			continue
		}
		repo := filepath.Base(p.Dir)
		live := make([]string, 0, len(worktrees))
		for _, wt := range worktrees {
			if idx := p.WorktreeIndices[wt.Branch]; idx > 0 {
				live = append(live, stack.ProjectName(repo, idx, wt.Branch))
			}
		}
		out = append(out, repoWorktrees{Repo: repo, Live: live})
	}
	return out
}

// reportOrphanVolumes lists the volumes of worktrees that no longer exist.
// They squat the indices their stacks were created at, which pushes every new
// worktree further out, and nothing else ever mentions them.
func (a *app) reportOrphanVolumes(ctx context.Context) {
	res, err := a.runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"volume", "ls", "-q"}})
	if err != nil {
		return
	}
	all := strings.Fields(res.Stdout)
	var orphans []string
	for _, rw := range a.liveProjects(ctx) {
		orphans = append(orphans, orphanVolumes(all, rw.Repo, rw.Live)...)
	}
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

// reportOrphanImages lists what worktrees that no longer exist had compose
// build for them. A stack builds one image per service, so this list is several
// times longer than the volume one for the same removed worktrees.
func (a *app) reportOrphanImages(ctx context.Context) {
	res, err := a.runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"images", "--format", "{{.Repository}}"},
	})
	if err != nil {
		return
	}
	all := strings.Fields(res.Stdout)
	var orphans []string
	for _, rw := range a.liveProjects(ctx) {
		orphans = append(orphans, orphanImages(all, rw.Repo, rw.Live)...)
	}
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

// buildCache is what buildkit keeps between builds. It is reported and never
// removed: the cache is shared by every project on the machine and buildkit
// attributes none of it, so only the developer can decide it is expendable.
// `docker system df` also has the number but walks the whole image store for
// it, which took a minute on a machine busy building; `buildx du` prints its
// Private/Reclaimable/Total summary in under a second.
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
			fmt.Fprintf(a.out, "config   %s\n", a.cfgPath)
			fmt.Fprintf(a.out, "backups  %s\n", a.backups)
			if u, err := dockermem.Read(cmd.Context(), a.runner); err == nil && u.Total > 0 {
				fmt.Fprintf(a.out, "docker   %s used out of %s, %d stack(s) running (~%s per stack)\n",
					dockermem.Human(u.Used), dockermem.Human(u.Total), u.Projects, dockermem.Human(u.PerProject()))
				if msg := u.Warning(); msg != "" {
					fmt.Fprintln(a.out, msg)
				}
			}
			if line := a.buildCache(cmd.Context()); line != "" {
				fmt.Fprintf(a.out, "cache    %s\n", line)
			}
			if len(a.cfg.Projects) == 0 {
				return nil
			}
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
			a.reportOrphanVolumes(cmd.Context())
			a.reportOrphanImages(cmd.Context())
			return nil
		},
	}
}
