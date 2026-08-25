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
// which is the only number worth comparing a build against, and it needs no
// token where the GitHub API rate-limits anonymous callers. Uppercase letters
// of a module path are escaped with a bang, hence `!hy0sh`.
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

// olderVersion compares two "vX.Y.Z" tags, the only shape this project ever
// published. Anything else, "devel" included, is not comparable and answers
// false: the check exists to point at an upgrade, never to guess at one.
func olderVersion(local, published string) bool {
	l, ok := semverFields(local)
	if !ok {
		return false
	}
	p, ok := semverFields(published)
	if !ok {
		return false
	}
	for i := range l {
		if l[i] != p[i] {
			return l[i] < p[i]
		}
	}
	return false
}

func semverFields(v string) ([3]int, bool) {
	var out [3]int
	// The toolchain stamps a build made over local edits "v0.4.8+dirty", and
	// semver ignores what follows the plus: the tag is what gets compared.
	number, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "+")
	parts := strings.Split(number, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
