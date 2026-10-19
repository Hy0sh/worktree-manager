package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const releaseDownloadURL = "https://github.com/Hy0sh/worktree-manager/releases/download"

// updateTimeout covers the lookup and both downloads: a release archive is a
// few megabytes.
const updateTimeout = 2 * time.Minute

// maxArchive caps what a download may weigh; a release archive is ~5 MiB, the
// binary inside ~13 MiB.
const maxArchive = 100 << 20

var errNotPublished = errors.New("not published")

// binaryVersion reads the module version the toolchain stamped into a binary,
// without running it. A variable so tests can stand in for a real release.
var binaryVersion = func(path string) (string, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", err
	}
	return info.Main.Version, nil
}

func newUpdateCmd(a *app) *cobra.Command {
	var check, force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Replaces this binary with the latest release",
		Long: "Downloads the latest release built for this platform, checks it against\n" +
			"the SHA256SUMS published with it, and puts it in place of the wtm being\n" +
			"run. A symbolic link is kept: the file it points to is what gets replaced.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Skips the registry: a config.json this build cannot read must not
		// stand between the user and the build that can.
		PersistentPreRunE: func(*cobra.Command, []string) error {
			a.out = os.Stdout
			a.latestURL = latestModuleURL
			a.releaseURL = releaseDownloadURL
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locating the wtm binary to replace: %w", err)
			}
			target, err := filepath.EvalSymlinks(exe)
			if err != nil {
				return fmt.Errorf("locating the wtm binary to replace: %w", err)
			}
			return a.update(cmd.Context(), target, version(), check, force)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only say whether a newer release is published")
	cmd.Flags().BoolVar(&force, "force", false, "install the latest release even when this build is not behind it")
	return cmd
}

// update replaces target with the latest release, local being the version of
// the binary at target. Nothing is written to target until the download has
// matched its checksum and carries the version it was published as.
func (a *app) update(ctx context.Context, target, local string, check, force bool) error {
	platform, err := releasePlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	published, err := latestVersion(ctx, a.latestURL)
	if err != nil {
		return fmt.Errorf("looking up the latest release: %w", err)
	}

	local, _, _ = strings.Cut(local, " ")
	if !force && !olderVersion(local, published) {
		if _, _, ok := semverFields(local); !ok {
			return fmt.Errorf("this wtm is %s, built from a working copy no release compares to: "+
				"`wtm update --force` replaces it with %s", local, published)
		}
		fmt.Fprintf(a.out, "wtm %s is up to date, the latest release is %s\n", local, published)
		return nil
	}
	if check {
		fmt.Fprintf(a.out, "%s is published, this is %s: `wtm update` installs it\n", published, local)
		return nil
	}

	// Created first, so a directory the user cannot write to fails before
	// anything is downloaded. Next to target, so the rename stays on one filesystem.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".wtm-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s, update it with the rights to write there: %w", target, err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	asset := "wtm_" + published + "_" + platform + ".tar.gz"
	base := a.releaseURL + "/" + published + "/"
	sums, err := download(ctx, base+"SHA256SUMS")
	if err == nil {
		var archive []byte
		archive, err = download(ctx, base+asset)
		if err == nil {
			err = verifyChecksum(sums, asset, archive)
		}
		if err == nil {
			err = extractBinary(archive, tmp)
		}
	}
	if errors.Is(err, errNotPublished) {
		return fmt.Errorf("%s is tagged but its binaries are not published yet, retry in a few minutes", published)
	}
	if err != nil {
		return fmt.Errorf("downloading %s: %w", published, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if v, err := binaryVersion(tmp.Name()); err != nil || v != published {
		return fmt.Errorf("the downloaded binary does not report %s, %s is left as it was", published, target)
	}

	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	// A new inode rather than a write in place: a running wtm keeps the old
	// one, and macOS kills a signed binary whose pages change under it.
	if err := os.Rename(tmp.Name(), target); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "wtm %s installed at %s, replacing %s\n", published, target, local)
	return nil
}

// releasePlatform names the platform the way release.yml names its archives,
// and refuses one it builds nothing for.
func releasePlatform(goos, goarch string) (string, error) {
	if (goos == "darwin" || goos == "linux") && (goarch == "amd64" || goarch == "arm64") {
		return goos + "_" + goarch, nil
	}
	return "", fmt.Errorf("no release is built for %s/%s: install with "+
		"`go install github.com/Hy0sh/worktree-manager/cmd/wtm@latest`", goos, goarch)
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, errNotPublished
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxArchive+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxArchive {
		return nil, fmt.Errorf("%s weighs over %d MiB", url, maxArchive>>20)
	}
	return body, nil
}

// verifyChecksum checks archive against its line in sums, which `sha256sum`
// writes as "<hex>  <file>".
func verifyChecksum(sums []byte, asset string, archive []byte) error {
	for _, line := range strings.Split(string(sums), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || name != asset {
			continue
		}
		got := sha256.Sum256(archive)
		if hex.EncodeToString(got[:]) != sum {
			return fmt.Errorf("%s does not match its SHA256SUMS line", asset)
		}
		return nil
	}
	return fmt.Errorf("SHA256SUMS has no line for %s", asset)
}

// extractBinary writes the regular file named wtm at the root of the archive
// to dst; LICENSE and README.md travel with it and are left out.
func extractBinary(archive []byte, dst io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return errors.New("the archive holds no wtm binary")
		}
		if err != nil {
			return err
		}
		if hdr.Name != "wtm" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size > maxArchive {
			return fmt.Errorf("the wtm in the archive weighs over %d MiB", maxArchive>>20)
		}
		_, err = io.Copy(dst, tr)
		return err
	}
}
