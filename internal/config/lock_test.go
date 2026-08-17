package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func lockTestPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.json")
}

func TestWithLockSerialisesConcurrentWriters(t *testing.T) {
	path := lockTestPath(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := WithLock(path, func(c *Config) error {
				c.Projects[strings.Repeat("p", n+1)] = Project{Dir: "/x"}
				return nil
			})
			if err != nil {
				t.Errorf("writer %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 10 {
		t.Fatalf("expected 10 projects to survive concurrent writes, got %d", len(cfg.Projects))
	}
}

func TestWithLockDoesNotSaveOnError(t *testing.T) {
	path := lockTestPath(t)
	if err := WithLock(path, func(c *Config) error {
		c.Projects["kept"] = Project{Dir: "/x"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wantErr := WithLock(path, func(c *Config) error {
		c.Projects["dropped"] = Project{Dir: "/y"}
		return os.ErrInvalid
	})
	if wantErr == nil {
		t.Fatal("fn error should propagate")
	}
	cfg, _ := Load(path)
	if _, ok := cfg.Projects["dropped"]; ok {
		t.Fatal("a failed fn must not be saved")
	}
}

func TestWithLockTakesOverAStaleLock(t *testing.T) {
	path := lockTestPath(t)
	lock := filepath.Join(filepath.Dir(path), "config.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := WithLock(path, func(c *Config) error { return nil }); err != nil {
		t.Fatalf("a stale lock must be taken over: %v", err)
	}
}

func TestWithLockFailsOnAHeldLock(t *testing.T) {
	path := lockTestPath(t)
	lock := filepath.Join(filepath.Dir(path), "config.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { lockRetry = d }(lockRetry)
	lockRetry = 100 * time.Millisecond
	err := WithLock(path, func(c *Config) error { return nil })
	if err == nil {
		t.Fatal("a fresh lock held by someone else must time out")
	}
	if !strings.Contains(err.Error(), lock) {
		t.Fatalf("the error must name the lock file to delete, got: %v", err)
	}
}

func TestWorktreeIndicesRoundTrip(t *testing.T) {
	path := lockTestPath(t)
	if err := WithLock(path, func(c *Config) error {
		c.Projects["app"] = Project{Dir: "/x", WorktreeIndices: map[string]int{"feat/x": 3}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Projects["app"].WorktreeIndices["feat/x"]; got != 3 {
		t.Fatalf("worktree_indices did not survive a round trip, got %d", got)
	}
}
