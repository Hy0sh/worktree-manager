package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The Go module proxy answers with what `go install ...@latest` would resolve,
// and needs no token where the GitHub API rate-limits anonymous callers.
// Uppercase letters of a module path are escaped with a bang, hence `!hy0sh`.
const latestModuleURL = "https://proxy.golang.org/github.com/!hy0sh/worktree-manager/@latest"

// latestTimeout is the whole budget of the check. A read command never blocks
// on the network, the same rule `wtm list` follows with docker: past this the
// check is dropped, silently, and never retried.
const latestTimeout = 2 * time.Second

// newerRelease returns the published version when it is newer than this build,
// and "" for everything else: a build made from a working copy (no tag to
// compare), an unreachable proxy, a timeout, a version this build is ahead of.
func (a *app) newerRelease(ctx context.Context) string {
	if a.latestURL == "" {
		return ""
	}
	local, _, _ := strings.Cut(version(), " ")
	ctx, cancel := context.WithTimeout(ctx, latestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.latestURL, nil)
	if err != nil {
		return ""
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	var answer struct{ Version string }
	if err := json.NewDecoder(res.Body).Decode(&answer); err != nil {
		return ""
	}
	if !olderVersion(local, answer.Version) {
		return ""
	}
	return answer.Version
}

// olderVersion compares two "vX.Y.Z" tags, plus the pre-release shape a build
// installed from a commit carries. Anything else, "devel" included, answers
// false: the check exists to point at an upgrade, never to guess at one.
func olderVersion(local, published string) bool {
	l, localPre, ok := semverFields(local)
	if !ok {
		return false
	}
	p, publishedPre, ok := semverFields(published)
	if !ok {
		return false
	}
	for i := range l {
		if l[i] != p[i] {
			return l[i] < p[i]
		}
	}
	// Same numbers: semver puts a pre-release before the release it leads to,
	// which is how a build installed from a commit is told the tag is out.
	return localPre && !publishedPre
}

// semverFields reads the numbers of a version, and whether a pre-release suffix
// follows them. `go install ...@main` stamps "v0.8.1-0.20260828143234-4c0fbbb",
// which split on dots alone read as four fields and never compared as behind.
func semverFields(v string) (out [3]int, pre bool, ok bool) {
	// The toolchain stamps a build made over local edits "v0.4.8+dirty", and
	// semver ignores what follows the plus: the tag is what gets compared.
	number, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "+")
	number, suffix, hasSuffix := strings.Cut(number, "-")
	parts := strings.Split(number, ".")
	if len(parts) != 3 {
		return out, false, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false, false
		}
		out[i] = n
	}
	return out, hasSuffix && suffix != "", true
}
