package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// portHolder is one published port a worktree stack would take, and who takes
// it. The project name is what makes a clash reportable: within one project
// the allocator already refuses to hand the same port twice.
type portHolder struct {
	Port    int
	Project string
	Branch  string
	Label   string
}

// portClashes reports the ports two different projects would both publish. The
// offset step is smaller than the spread of the default ports it shifts, so two
// stacks can refuse to start together on a bind error naming neither of them.
func portClashes(holders []portHolder) []string {
	byPort := map[int][]portHolder{}
	for _, h := range holders {
		byPort[h.Port] = append(byPort[h.Port], h)
	}
	var ports []int
	for port, hs := range byPort {
		projects := map[string]bool{}
		for _, h := range hs {
			projects[h.Project] = true
		}
		if len(projects) > 1 {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)

	var out []string
	for _, port := range ports {
		hs := byPort[port]
		sort.Slice(hs, func(i, j int) bool {
			if hs[i].Project != hs[j].Project {
				return hs[i].Project < hs[j].Project
			}
			return hs[i].Branch < hs[j].Branch
		})
		who := make([]string, 0, len(hs))
		for _, h := range hs {
			who = append(who, fmt.Sprintf("%s/%s %s", h.Project, h.Branch, h.Label))
		}
		out = append(out, fmt.Sprintf("port %d is claimed by %s", port, strings.Join(who, " and ")))
	}
	return out
}

// staleIndex names one recorded index no worktree stands behind, and the
// project that recorded it: what `wtm remove <project> <branch>` releases.
type staleIndex struct {
	Project string
	Branch  string
	Index   int
}

func (a *app) staleIndices(rws []repoWorktrees) []staleIndex {
	var out []staleIndex
	for _, rw := range rws {
		p := a.cfg.Projects[rw.Name]
		for _, branch := range rw.Stale {
			out = append(out, staleIndex{Project: rw.Name, Branch: branch, Index: p.WorktreeIndices[branch]})
		}
	}
	return out
}

// The two below answer nothing at all when docker cannot be reached, which a
// caller cannot tell from a machine that holds no leftovers. That suits both
// of them: the report has nothing to print, and the cleanup nothing to drop.
func (a *app) orphanVolumeNames(ctx context.Context, rws []repoWorktrees) []string {
	res, err := a.runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"volume", "ls", "-q"}})
	if err != nil {
		return nil
	}
	all := strings.Fields(res.Stdout)
	var out []string
	for _, rw := range rws {
		out = append(out, rw.orphanVolumes(all)...)
	}
	return out
}

func (a *app) orphanImageNames(ctx context.Context, rws []repoWorktrees) []string {
	res, err := a.runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"images", "--format", "{{.Repository}}"},
	})
	if err != nil {
		return nil
	}
	all := strings.Fields(res.Stdout)
	var out []string
	for _, rw := range rws {
		out = append(out, rw.orphanImages(all)...)
	}
	return out
}

// Both answer nothing while a worktree of the project has no recorded index: it
// cannot be turned into the compose project name that would claim its volumes,
// and a wrong `docker rmi` costs a live worktree.
func (rw repoWorktrees) orphanVolumes(all []string) []string {
	if len(rw.Unindexed) > 0 {
		return nil
	}
	return unclaimed(all, rw.Repo, "_", rw.Live)
}

// An untagged leftover of a rebuild carries no "<project>-<service>" name at
// all, and stays for `docker image prune`.
func (rw repoWorktrees) orphanImages(all []string) []string {
	if len(rw.Unindexed) > 0 {
		return nil
	}
	return unclaimed(all, rw.Repo, "-", rw.Live)
}

// unclaimed keeps the names built on a worktree project of repoName that none of
// the live projects owns. sep is what compose puts between the project and the
// resource: "_" for a volume, "-" for an image repository.
func unclaimed(all []string, repoName, sep string, live []string) []string {
	prefix := stack.WorktreePrefix(repoName)
	var out []string
	for _, name := range all {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		claimed := false
		for _, project := range live {
			if strings.HasPrefix(name, project+sep) {
				claimed = true
				break
			}
		}
		if !claimed {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// intraProjectClashes reports the ports two worktrees of the same project would
// both publish. Until the allocator learnt to skip clashing indices, worktrees
// recorded before can still overlap; portStride spreads them.
func intraProjectClashes(holders []portHolder) []string {
	type key struct {
		project string
		port    int
	}
	byKey := map[key][]portHolder{}
	for _, h := range holders {
		k := key{h.Project, h.Port}
		byKey[k] = append(byKey[k], h)
	}
	var keys []key
	for k, hs := range byKey {
		branches := map[string]bool{}
		for _, h := range hs {
			branches[h.Branch] = true
		}
		if len(branches) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].project != keys[j].project {
			return keys[i].project < keys[j].project
		}
		return keys[i].port < keys[j].port
	})
	var out []string
	for _, k := range keys {
		hs := byKey[k]
		sort.Slice(hs, func(i, j int) bool { return hs[i].Branch < hs[j].Branch })
		who := make([]string, 0, len(hs))
		for _, h := range hs {
			who = append(who, h.Branch+" "+h.Label)
		}
		out = append(out, fmt.Sprintf("%s: port %d is claimed by %s", k.project, k.port, strings.Join(who, " and ")))
	}
	return out
}
