// Package safefile writes at paths a git checkout controls, where a component
// may be a symlink a hostile branch committed: a link at the leaf is replaced
// by a regular file, a directory chain resolving outside the root is refused.
package safefile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Write puts data at path, which must live under root once every directory
// symlink is resolved. MkdirAll can still create directories through a hostile
// link first: empty directories outside the root are harmless, content is not.
func Write(root, path string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", root, err)
	}
	dirReal, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("resolving %s: %w", filepath.Dir(path), err)
	}
	if dirReal != rootReal && !strings.HasPrefix(dirReal, rootReal+string(os.PathSeparator)) {
		return fmt.Errorf("%s resolves outside %s (a symlink the checkout laid down?): move it away and retry", path, root)
	}
	// The leaf is written at the resolved directory so the check above and
	// the write below talk about the same location.
	dst := filepath.Join(dirReal, filepath.Base(path))
	if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink != 0 {
		// A branch-controlled link could point anywhere, outside the root
		// included: the file must be the tree's own, so the link goes.
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("replacing the symlink at %s: %w", dst, err)
		}
	}
	return os.WriteFile(dst, data, perm)
}
