package config

import "fmt"

// RecordWorktreePath remembers where a branch's worktree stands, and forgets it
// when path is empty. A project gone from the registry is not an error: the
// command carries on either way, and this only feeds a diagnosis.
func RecordWorktreePath(configPath, project, branch, path string) error {
	return WithLock(configPath, func(c *Config) error {
		p, ok := c.Projects[project]
		if !ok {
			return nil
		}
		switch {
		case path == "":
			delete(p.WorktreePaths, branch)
		case p.WorktreePaths == nil:
			p.WorktreePaths = map[string]string{branch: path}
		default:
			p.WorktreePaths[branch] = path
		}
		c.Projects[project] = p
		return nil
	})
}

// RekeyWorktree moves a worktree's index and recorded path from one branch to
// another in a single locked write: the index, and so the ports, stay the same.
func RekeyWorktree(configPath, project, from, to string) error {
	return WithLock(configPath, func(c *Config) error {
		p, ok := c.Projects[project]
		if !ok {
			return fmt.Errorf("project %q is not registered", project)
		}
		if n := p.WorktreeIndices[from]; n > 0 {
			delete(p.WorktreeIndices, from)
			p.WorktreeIndices[to] = n
		}
		if at := p.WorktreePaths[from]; at != "" {
			delete(p.WorktreePaths, from)
			p.WorktreePaths[to] = at
		}
		c.Projects[project] = p
		return nil
	})
}
