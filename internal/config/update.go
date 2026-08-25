package config

import "fmt"

// ProjectUpdate carries the fields an edit actually names. A nil pointer means
// "leave it alone": an edit must not reset what it says nothing about, the port
// offset and the recorded worktree indices first of all.
type ProjectUpdate struct {
	Dir          *string
	BaseBranch   *string
	Dump         *bool
	GitContainer *bool
	PostCreate   *string

	DBService      *string
	DBUser         *string
	DBEngine       *string
	DBPath         *string
	AppService     *string
	DepsCommand    *string
	MigrateCommand *string
	// Env replaces the whole set when given: a map merged field by field
	// would leave no way to drop a variable.
	Env map[string]string
}

// IsEmpty reports an update that names nothing, which is how a command tells
// "change this one field" from "walk me through the settings".
func (u ProjectUpdate) IsEmpty() bool {
	return u.Dir == nil && u.BaseBranch == nil && u.Dump == nil &&
		u.GitContainer == nil && u.PostCreate == nil && !u.touchesBackup()
}

type FieldChange struct {
	Field string
	From  string
	To    string
}

// Apply returns the edited project along with what actually changed. A field
// set to the value it already had is not a change and is not reported.
func (u ProjectUpdate) Apply(p Project) (Project, []FieldChange) {
	var changes []FieldChange
	str := func(field string, target *string, given *string) {
		if given == nil || *given == *target {
			return
		}
		changes = append(changes, FieldChange{field, *target, *given})
		*target = *given
	}
	boolean := func(field string, target *bool, given *bool) {
		if given == nil || *given == *target {
			return
		}
		changes = append(changes, FieldChange{field, fmt.Sprint(*target), fmt.Sprint(*given)})
		*target = *given
	}

	str("dir", &p.Dir, u.Dir)
	str("base_branch", &p.BaseBranch, u.BaseBranch)
	boolean("dump", &p.Dump, u.Dump)
	boolean("git_container", &p.GitContainer, u.GitContainer)
	str("post_create", &p.PostCreate, u.PostCreate)

	if !u.touchesBackup() {
		return p, changes
	}
	// A project registered without any backup settings gets the section on
	// its first backup flag rather than an error.
	b := Backup{}
	if p.Backup != nil {
		b = *p.Backup
	}
	str("db_service", &b.DBService, u.DBService)
	str("db_user", &b.DBUser, u.DBUser)
	str("db_engine", &b.DBEngine, u.DBEngine)
	str("db_path", &b.DBPath, u.DBPath)
	str("app_service", &b.AppService, u.AppService)
	str("deps_command", &b.DepsCommand, u.DepsCommand)
	str("migrate_command", &b.MigrateCommand, u.MigrateCommand)
	if u.Env != nil && !sameEnv(b.Env, u.Env) {
		changes = append(changes, FieldChange{"env", fmt.Sprint(b.Env), fmt.Sprint(u.Env)})
		b.Env = u.Env
	}
	p.Backup = &b
	return p, changes
}

func (u ProjectUpdate) touchesBackup() bool {
	return u.DBService != nil || u.DBUser != nil || u.DBEngine != nil ||
		u.DBPath != nil || u.AppService != nil || u.DepsCommand != nil ||
		u.MigrateCommand != nil || u.Env != nil
}

func sameEnv(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
