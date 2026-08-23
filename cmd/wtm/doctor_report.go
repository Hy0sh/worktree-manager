package main

import (
	"fmt"
	"sort"
	"strings"
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
// projects can land on the same host port, and their stacks then refuse to
// start together with a docker bind error naming neither of them.
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

// orphanVolumes returns the volumes of worktree stacks of repoName that no live
// worktree accounts for. They matter beyond disk: the index allocator steps over
// any index docker still holds volumes for, so a new worktree lands further out
// with higher ports. live holds the compose project name of every worktree that
// still exists, and compose names a volume "<project>_<volume>".
func orphanVolumes(all []string, repoName string, live []string) []string {
	prefix := repoName + "-wt-"
	var out []string
	for _, name := range all {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		claimed := false
		for _, project := range live {
			if strings.HasPrefix(name, project+"_") {
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
