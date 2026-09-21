package config

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
