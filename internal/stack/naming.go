package stack

import (
	"fmt"
	"regexp"
	"strings"
)

// ProjectName is the compose project wtc gives a worktree. Mirrors its
// composeProjectName/sanitize pair, which is the only way to address that
// stack's containers and volumes from the outside.
func ProjectName(repoName string, index int, branch string) string {
	return sanitize(fmt.Sprintf("%s-wt-%d-%s", repoName, index, branch))
}

// ProjectPrefix is what every compose project of a worktree at this index
// starts with, whatever its branch. It is how leftovers of a removed
// worktree are spotted before the index is handed to a new one.
func ProjectPrefix(repoName string, index int) string {
	return sanitize(fmt.Sprintf("%s-wt-%d", repoName, index)) + "-"
}

func sanitize(input string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(input) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	collapsed := regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-")
	return strings.Trim(collapsed, "-")
}
