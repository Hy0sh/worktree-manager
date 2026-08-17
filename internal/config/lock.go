package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Lock timing. Vars rather than consts so the timeout test does not take 5s.
var (
	lockRetry = 5 * time.Second
	lockStale = 30 * time.Second
	lockPoll  = 50 * time.Millisecond
)

// WithLock loads the registry under an exclusive lock, hands it to fn, and
// saves it when fn returns nil. The lock is a plain O_EXCL file next to
// config.json — flock would be nicer but has no Windows equivalent, and this
// project carries no build tags. The critical section is a read-modify-write
// of a small JSON file, never a docker command, so a lock older than
// lockStale can only be the debris of a killed process and is taken over.
func WithLock(path string, fn func(*Config) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	lock := filepath.Join(dir, "config.lock")
	deadline := time.Now().Add(lockRetry)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("taking %s: %w", lock, err)
		}
		if st, sErr := os.Stat(lock); sErr == nil && time.Since(st.ModTime()) > lockStale {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the registry is locked by %s; if no wtm is running, delete that file", lock)
		}
		time.Sleep(lockPoll)
	}
	defer os.Remove(lock)

	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return cfg.Save(path)
}
