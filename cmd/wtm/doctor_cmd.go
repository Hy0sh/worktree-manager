package main

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
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
		for _, branch := range slices.Sorted(maps.Keys(p.WorktreeIndices)) {
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
	a.section("port clashes between projects (those stacks cannot run at the same time):", portClashes(holders),
		"offsets are handed out once, at registration: keep those stacks from running together, or",
		"raise `port_offset` for one project in config.json and recreate its worktrees, whose .env carry the old ports")
	a.section("port clashes between worktrees of one project (those two cannot run at the same time):", intraProjectClashes(holders),
		"the stride is too small for ports that sit close together: set `portStride` in the project's",
		".wtcrc.json above their spread, then recreate the worktrees, whose .env carry the old ports")
}

// section prints one report of the diagnosis, and nothing at all without lines.
func (a *app) section(title string, lines []string, hints ...string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, title)
	for _, l := range slices.Concat(lines, hints) {
		fmt.Fprintf(a.out, "  %s\n", l)
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
	// Drifted are branches whose worktree still stands where it was recorded
	// but now holds another branch. They look exactly like Stale to git, and
	// releasing one takes down a stack somebody is working in.
	Drifted []string
	// Abandoned are the directories still on disk that git no longer lists,
	// left by a pruned administrative directory. They hold no stack, and no
	// other report can see them: everything else keys off git's listing.
	Abandoned []string
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
		// A project with no compose file starts no stack, so none of its
		// worktrees ever gets an index and nothing docker holds can be theirs:
		// counting them would hold back every report below, forever.
		if !compose.Has(p.Dir) {
			unindexed = nil
		}
		// A worktree still standing where a branch was recorded did not vanish:
		// somebody switched branches in it, which takes the old name out of
		// git's listing while the worktree, and its stack, are very much alive.
		livePaths := map[string]bool{}
		for _, wt := range worktrees {
			livePaths[filepath.Clean(wt.Path)] = true
		}
		var stale, drifted []string
		for branch := range p.WorktreeIndices {
			if present[branch] {
				continue
			}
			if at := p.WorktreePaths[branch]; at != "" && livePaths[filepath.Clean(at)] {
				drifted = append(drifted, branch)
				continue
			}
			stale = append(stale, branch)
		}
		sort.Strings(stale)
		sort.Strings(drifted)
		// A git that cannot answer already dropped the project above, so a
		// failure here is the filesystem's: nothing to report either way.
		abandoned, _ := client.Abandoned(ctx)
		out = append(out, repoWorktrees{Repo: repo, Name: name, Live: live,
			Unindexed: unindexed, Stale: stale, Drifted: drifted, Abandoned: abandoned})
	}
	return out
}

// reportDrifted names the worktrees whose branch was switched under them. Not
// a leftover to clean: the index stays, the stack stays, and saying so is the
// whole point, since every other report would call this one stale.
func (a *app) reportDrifted(rws []repoWorktrees) {
	var lines []string
	for _, rw := range rws {
		p := a.cfg.Projects[rw.Name]
		for _, branch := range rw.Drifted {
			lines = append(lines, fmt.Sprintf("%s: index %d is recorded for %s, whose worktree at %s now holds another branch",
				rw.Name, p.WorktreeIndices[branch], branch, p.WorktreePaths[branch]))
		}
	}
	a.section("worktrees whose branch was switched (their stack still answers to the old name):", lines,
		"nothing to clean: address that stack by its recorded branch, or move the worktree back to it")
}

func (a *app) reportStaleIndices(stale []staleIndex) {
	var lines, cmds []string
	for _, s := range stale {
		lines = append(lines, fmt.Sprintf("%s: index %d is recorded for %s, which has no worktree", s.Project, s.Index, s.Branch))
		cmds = append(cmds, fmt.Sprintf("wtm remove %s %s", s.Project, s.Branch))
	}
	a.section("recorded indices with no worktree behind them (each pushes new worktrees one index further out):", lines,
		fmt.Sprintf("release them with `%s`", strings.Join(cmds, "`, `")))
}

// reportUnindexed names the worktrees the registry holds no index for. Every
// leftover report holds back for their whole project, which used to happen
// without a word: doctor answered "nothing" where it meant "cannot tell".
func (a *app) reportUnindexed(rws []repoWorktrees) {
	var lines []string
	for _, rw := range rws {
		for _, branch := range rw.Unindexed {
			lines = append(lines, fmt.Sprintf("%s: %s", rw.Name, branch))
		}
	}
	a.section("worktrees with no recorded index, which holds back every leftover report of their project\n"+
		"(a stack of theirs cannot be told from one a removed worktree left):", lines,
		"`wtm start <branch>` records the index, `wtm remove <branch>` takes the worktree out")
}

// reportOrphanStacks lists the containers of worktrees that no longer exist.
// The index allocator sees them and refuses their index; until this report,
// nothing told the developer, so `wtm clean` said "done" and left them running.
func (a *app) reportOrphanStacks(orphans []orphanStack) {
	var lines, cmds []string
	for _, o := range orphans {
		lines = append(lines, o.Stack)
		cmds = append(cmds, fmt.Sprintf("docker compose -p %s down --volumes", o.Stack))
	}
	a.section(fmt.Sprintf("%d stack(s) of removed worktrees, still holding their containers "+
		"and the indices their ports came from:", len(lines)), lines,
		fmt.Sprintf("take them down with `%s`", strings.Join(cmds, "`, `")))
}

// reportAbandonedWorktrees lists the directories git has forgotten, which `wtm
// clean` leaves on purpose: nothing can read whether they hold uncommitted work.
// They also make `wtm create` refuse the branch.
func (a *app) reportAbandonedWorktrees(rws []repoWorktrees) {
	var lines, cmds []string
	for _, rw := range rws {
		for _, path := range rw.Abandoned {
			lines = append(lines, fmt.Sprintf("%s: %s", rw.Name, path))
			cmds = append(cmds, fmt.Sprintf("wtm remove %s %s --force",
				rw.Name, abandonedBranch(a.cfg.Projects[rw.Name].Dir, path)))
		}
	}
	a.section(fmt.Sprintf("%d directory(ies) still on disk that git no longer lists as worktrees "+
		"(`wtm create` refuses their branch):", len(lines)), lines,
		fmt.Sprintf("delete them with `%s`, which `wtm clean` will not do "+
			"(no one can tell any more whether they hold uncommitted work)", strings.Join(cmds, "`, `")))
}

// reportOrphanVolumes lists the volumes of worktrees that no longer exist.
// They squat the indices their stacks were created at, which pushes every new
// worktree further out, and nothing else ever mentions them.
func (a *app) reportOrphanVolumes(orphans []string) {
	a.section(fmt.Sprintf("%d volume(s) of removed worktrees, squatting the indices their ports came from:", len(orphans)),
		orphans, fmt.Sprintf("drop them with `docker volume rm %s`", strings.Join(orphans, " ")))
}

// reportAnonymousVolumes counts what the orphan report cannot see: volumes an
// image created on its own, labelled anonymous, that no container mounts.
// Machine-wide by nature, hence a count and a command rather than an attribution.
func (a *app) reportAnonymousVolumes(ctx context.Context) {
	ids := a.anonymousVolumeIDs(ctx)
	if len(ids) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	fmt.Fprintf(a.out, "%d anonymous volume(s) no container mounts, left by images that name their own data directory:\n", len(ids))
	fmt.Fprintf(a.out, "  drop them with `%s`\n", anonymousVolumeCommand)
}

// reportOrphanImages lists what worktrees that no longer exist had compose
// build for them. A stack builds one image per service, so this list is several
// times longer than the volume one for the same removed worktrees.
func (a *app) reportOrphanImages(orphans []string) {
	a.section(fmt.Sprintf("%d image(s) built for removed worktrees, several GB each:", len(orphans)),
		orphans, fmt.Sprintf("drop them with `docker rmi %s`", strings.Join(orphans, " ")))
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
				a.reportDrifted(rws)
				// Before the three reports it holds back, so a reader meeting
				// an empty one knows why it is empty.
				a.reportUnindexed(rws)
				a.reportOrphanStacks(stacks)
				a.reportOrphanVolumes(volumes)
				a.reportOrphanImages(images)
				// Each block above ends on its own command line, one per
				// finding: seven of them on a busy machine, and nothing would
				// otherwise say a single verb covers the lot.
				if len(stale)+len(stacks)+len(volumes)+len(images) > 0 {
					fmt.Fprintln(a.out, "\n`wtm clean` runs all of that in one go.")
				}
				// Last, and outside that sentence: clean does not touch these.
				a.reportAbandonedWorktrees(rws)
			}
			// Anonymous volumes are machine-wide, not tied to a registered
			// project: the only report an empty registry still has an answer for.
			a.reportAnonymousVolumes(cmd.Context())
			return nil
		},
	}
}
