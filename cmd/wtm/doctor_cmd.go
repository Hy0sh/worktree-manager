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
	for _, name := range a.cfg.Names() {
		p := a.cfg.Projects[name]
		client := &stack.Client{Runner: a.runner, Dir: p.Dir}
		worktrees, err := client.Worktrees(ctx)
		if err != nil {
			continue // git could not answer: assuming everything is orphan would be worse
		}
		repo := filepath.Base(p.Dir)
		live := make([]string, 0, len(worktrees))
		for _, wt := range worktrees {
			if idx := p.WorktreeIndices[wt.Branch]; idx > 0 {
				live = append(live, stack.ProjectName(repo, idx, wt.Branch))
			}
		}
		orphans = append(orphans, orphanVolumes(all, repo, live)...)
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
			if len(a.cfg.Projects) == 0 {
				return nil
			}
			fmt.Fprintln(a.out)
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROJECT\tDIRECTORY\tSTRIDE\tOFFSET\tENGINE")
			for _, name := range a.cfg.Names() {
				p := a.cfg.Projects[name]
				// BackupConfig defaults the engine to postgres whether or not the
				// project has a database at all, so a project without a dump would
				// be reported as running one.
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
			return nil
		},
	}
}
