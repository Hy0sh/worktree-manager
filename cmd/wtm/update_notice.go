package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EnvNoUpdateCheck silences the notice and the lookup behind it.
const EnvNoUpdateCheck = "WTM_NO_UPDATE_CHECK"

// noticeEvery spaces the lookups: the module proxy is asked once a day at most.
const noticeEvery = 24 * time.Hour

type latestCache struct {
	Checked time.Time `json:"checked"`
	Version string    `json:"version"`
}

// updateNotice returns the line to print under a command when the cache at path
// knows of a release newer than local, and refreshes a cache older than a day
// in the background. A command shorter than the lookup leaves the cache as it
// was: the next longer one refreshes it, no command ever waits on the network.
func (a *app) updateNotice(path, local string, now time.Time) string {
	if _, _, ok := semverFields(local); !ok {
		return ""
	}
	var c latestCache
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if a.latestURL != "" && now.Sub(c.Checked) >= noticeEvery {
		go refreshLatest(context.Background(), a.latestURL, path, now)
	}
	if !olderVersion(local, c.Version) {
		return ""
	}
	return fmt.Sprintf("%s is published, this is %s: `wtm update` installs it", c.Version, local)
}

// refreshLatest records the published version at path. Written aside then
// renamed, since the process may exit in the middle of it.
func refreshLatest(ctx context.Context, url, path string, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, latestTimeout)
	defer cancel()
	published, err := latestVersion(ctx, url)
	if err != nil {
		return err
	}
	data, err := json.Marshal(latestCache{Checked: now, Version: published})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".latest-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
