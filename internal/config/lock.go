package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// Lock timing. Vars rather than consts so the timeout test does not take 5s.
var (
	lockRetry = 5 * time.Second
	lockPoll  = 50 * time.Millisecond
)

// WithLock saves the registry when fn returns nil. The lock is an OS file lock,
// so the kernel releases it when the process dies and a killed wtm can never
// leave the registry locked; the lock file staying on disk carries no state.
func WithLock(path string, fn func(*Config) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	lock := flock.New(filepath.Join(dir, "config.lock"))
	ctx, cancel := context.WithTimeout(context.Background(), lockRetry)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, lockPoll)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("taking %s: %w", lock.Path(), err)
	}
	if !locked {
		return fmt.Errorf("the registry is locked by another running wtm (%s)", lock.Path())
	}
	defer lock.Unlock()

	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return cfg.save(path)
}
