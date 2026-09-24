package compose

import "testing"

// The .git-container question only makes sense for a compose file that mounts
// the git directory, so the stepper asks it on that evidence alone.
func TestGitDirMountReadsBothVolumeSyntaxes(t *testing.T) {
	for name, body := range map[string]string{
		"short": "services:\n  app:\n    volumes:\n      - ./.git:/app/.git:ro\n",
		"long":  "services:\n  app:\n    volumes:\n      - type: bind\n        source: ./.git-container\n        target: /app/.git\n",
	} {
		dir := t.TempDir()
		write(t, dir, "compose.yaml", body)
		if _, ok := GitDirMount(dir); !ok {
			t.Errorf("%s syntax: the git-dir mount was missed", name)
		}
	}
}

func TestGitDirMountIgnoresOtherVolumes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", "services:\n  app:\n    volumes:\n      - .:/app\n      - ./.gitignore:/app/.gitignore\n      - data:/var/lib\n")
	if source, ok := GitDirMount(dir); ok {
		t.Fatalf("no git-dir is mounted, got %q", source)
	}
}
