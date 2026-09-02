package main

import (
	"runtime/debug"
	"strings"
)

// version reports what this binary was built from. Nothing is stamped at build
// time: the Go toolchain records the module version when installed from a tag
// and the commit otherwise, which spares wtm any ldflags dance.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	var revision, time string
	dirty := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.time":
			time = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}

	v := info.Main.Version
	// A build straight from a working copy has no module version to report.
	if v == "" || v == "(devel)" {
		v = "devel"
	}

	var b strings.Builder
	b.WriteString(v)
	if revision != "" {
		b.WriteString(" (")
		b.WriteString(shortRev(revision))
		if time != "" {
			b.WriteString(", ")
			b.WriteString(time)
		}
		if dirty {
			b.WriteString(", uncommitted changes")
		}
		b.WriteString(")")
	}
	return b.String()
}
