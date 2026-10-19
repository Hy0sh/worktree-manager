package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

const testRelease = "v1.2.0"

// fakeRelease serves what the module proxy and the release page would for
// testRelease, and counts the downloads of release assets.
type fakeRelease struct {
	asset     string
	archive   []byte
	sums      string
	assets404 bool
	downloads atomic.Int32
}

func newFakeRelease(t *testing.T, binary string) *fakeRelease {
	t.Helper()
	platform, err := releasePlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"LICENSE": "MIT", "wtm": binary} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	r := &fakeRelease{asset: "wtm_" + testRelease + "_" + platform + ".tar.gz", archive: buf.Bytes()}
	sum := sha256.Sum256(r.archive)
	r.sums = hex.EncodeToString(sum[:]) + "  " + r.asset + "\n"
	return r
}

func (r *fakeRelease) serve(t *testing.T) *app {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/latest":
			_, _ = w.Write([]byte(`{"Version":"` + testRelease + `"}`))
			return
		case "/" + testRelease + "/SHA256SUMS":
			r.downloads.Add(1)
			_, _ = w.Write([]byte(r.sums))
			return
		case "/" + testRelease + "/" + r.asset:
			r.downloads.Add(1)
			if !r.assets404 {
				_, _ = w.Write(r.archive)
				return
			}
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	return &app{out: &bytes.Buffer{}, latestURL: srv.URL + "/latest", releaseURL: srv.URL}
}

// stubBinaryVersion makes every download report v, since no test can produce
// a Go binary stamped with a release tag.
func stubBinaryVersion(t *testing.T, v string) {
	t.Helper()
	prev := binaryVersion
	binaryVersion = func(string) (string, error) { return v, nil }
	t.Cleanup(func() { binaryVersion = prev })
}

func installedBinary(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "wtm")
	if err := os.WriteFile(target, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	return target
}

// Whatever went wrong, the binary in place must still be the old one and no
// temporary file may be left next to it.
func assertUntouched(t *testing.T, target string) {
	t.Helper()
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("the installed binary was changed to %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".wtm-update-*")); len(left) > 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestUpdateReplacesTheBinaryKeepingItsMode(t *testing.T) {
	r := newFakeRelease(t, "new")
	a := r.serve(t)
	stubBinaryVersion(t, testRelease)
	target := installedBinary(t)

	if err := a.update(context.Background(), target, "v1.1.0 (abc1234)", false, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("installed %q, want the wtm of the archive", got)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o750 {
		t.Fatalf("mode %v, want the one of the replaced binary", info.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".wtm-update-*")); len(left) > 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestUpdateLeavesTheBinaryAloneOnABadDownload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		spoil   func(*fakeRelease)
		version string
		want    string
	}{
		{name: "checksum mismatch", spoil: func(r *fakeRelease) {
			r.sums = strings.Repeat("0", 64) + "  " + r.asset + "\n"
		}, version: testRelease, want: "does not match"},
		{name: "no checksum line", spoil: func(r *fakeRelease) {
			r.sums = strings.Repeat("0", 64) + "  wtm_" + testRelease + "_plan9_386.tar.gz\n"
		}, version: testRelease, want: "no line for"},
		// release.yml creates the release before it uploads the binaries.
		{name: "assets not uploaded yet", spoil: func(r *fakeRelease) { r.assets404 = true },
			version: testRelease, want: "not published yet"},
		{name: "another version inside", spoil: func(*fakeRelease) {}, version: "v1.1.0",
			want: "does not report " + testRelease},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRelease(t, "new")
			tc.spoil(r)
			a := r.serve(t)
			stubBinaryVersion(t, tc.version)
			target := installedBinary(t)

			err := a.update(context.Background(), target, "v1.1.0", false, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error saying %q", err, tc.want)
			}
			assertUntouched(t, target)
		})
	}
}

// Without the stub, the version is read from the binary itself: a file that is
// not a Go binary never gets installed.
func TestUpdateRefusesWhatIsNotAGoBinary(t *testing.T) {
	r := newFakeRelease(t, "#!/bin/sh\necho pwned\n")
	a := r.serve(t)
	target := installedBinary(t)

	if err := a.update(context.Background(), target, "v1.1.0", false, false); err == nil {
		t.Fatal("a shell script was installed as wtm")
	}
	assertUntouched(t, target)
}

func TestUpdateFailsBeforeDownloadingIntoAReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	r := newFakeRelease(t, "new")
	a := r.serve(t)
	target := installedBinary(t)
	dir := filepath.Dir(target)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := a.update(context.Background(), target, "v1.1.0", false, false)
	if err == nil || !strings.Contains(err.Error(), "cannot write next to") {
		t.Fatalf("got %v", err)
	}
	if n := r.downloads.Load(); n != 0 {
		t.Fatalf("%d download(s) before finding out the binary cannot be replaced", n)
	}
	assertUntouched(t, target)
}

// Only an install that is behind downloads anything, and --check never does.
func TestUpdateDownloadsOnlyWhenBehind(t *testing.T) {
	for _, tc := range []struct {
		name    string
		local   string
		check   bool
		force   bool
		wantErr string
		wantOut string
	}{
		{name: "up to date", local: testRelease, wantOut: "up to date"},
		{name: "ahead", local: "v1.3.0", wantOut: "up to date"},
		{name: "check behind", local: "v1.1.0", check: true, wantOut: testRelease + " is published"},
		{name: "check up to date", local: testRelease, check: true, wantOut: "up to date"},
		{name: "working copy", local: "devel (abc1234)", wantErr: "--force"},
		{name: "check working copy", local: "devel", check: true, force: true,
			wantOut: testRelease + " is published"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRelease(t, "new")
			a := r.serve(t)
			target := installedBinary(t)

			err := a.update(context.Background(), target, tc.local, tc.check, tc.force)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %v, want an error saying %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if out := a.out.(*bytes.Buffer).String(); !strings.Contains(out, tc.wantOut) {
				t.Fatalf("output %q, want %q", out, tc.wantOut)
			}
			if n := r.downloads.Load(); n != 0 {
				t.Fatalf("%d download(s)", n)
			}
			assertUntouched(t, target)
		})
	}
}

func TestReleasePlatformMatchesTheArchivesReleaseBuilds(t *testing.T) {
	if got, err := releasePlatform("linux", "arm64"); err != nil || got != "linux_arm64" {
		t.Fatalf("linux/arm64: %q, %v", got, err)
	}
	if _, err := releasePlatform("windows", "amd64"); err == nil {
		t.Fatal("windows/amd64 has no archive to download")
	}
}
