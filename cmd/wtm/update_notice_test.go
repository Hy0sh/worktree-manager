package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCache(t *testing.T, c latestCache) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "latest.json")
	data, _ := json.Marshal(c)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The cache alone decides the line: no latestURL, so nothing goes out.
func TestUpdateNoticeReadsTheCache(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name, local, cached string
		want                bool
	}{
		{name: "behind", local: "v1.3.0", cached: "v1.4.0", want: true},
		{name: "current", local: "v1.4.0", cached: "v1.4.0"},
		{name: "working copy", local: "devel", cached: "v1.4.0"},
		{name: "not a tag", local: "v1.3.0", cached: "v1.4.0\x1b[2J"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeCache(t, latestCache{Checked: now, Version: tc.cached})
			got := (&app{}).updateNotice(path, tc.local, now)
			if (got != "") != tc.want {
				t.Fatalf("notice = %q, want one: %v", got, tc.want)
			}
			if tc.want && !strings.Contains(got, tc.cached+" is published") {
				t.Fatalf("notice = %q", got)
			}
		})
	}
}

func TestUpdateNoticeWithoutCacheSaysNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.json")
	if got := (&app{}).updateNotice(path, "v1.3.0", time.Now()); got != "" {
		t.Fatalf("notice = %q", got)
	}
}

func TestRefreshLatestRecordsThePublishedVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Version":"v1.4.0"}`))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "sub", "latest.json")
	now := time.Now().Truncate(time.Second)
	if err := refreshLatest(context.Background(), srv.URL, path, now); err != nil {
		t.Fatal(err)
	}
	got := (&app{}).updateNotice(path, "v1.3.0", now)
	if !strings.Contains(got, "v1.4.0 is published") {
		t.Fatalf("notice after refresh = %q", got)
	}
}

// An unreachable proxy leaves the cache, and so the last known answer, alone.
func TestRefreshLatestKeepsTheCacheOnFailure(t *testing.T) {
	path := writeCache(t, latestCache{Version: "v1.4.0"})
	if err := refreshLatest(context.Background(), "http://127.0.0.1:0/nothing-listens-here", path, time.Now()); err == nil {
		t.Fatal("refresh against nothing succeeded")
	}
	if got := (&app{}).updateNotice(path, "v1.3.0", time.Now()); got == "" {
		t.Fatal("the cached version was lost")
	}
}
