// Package mark maintains a delimited block inside a file whose other lines
// belong to someone else: a project's .env, a repository's info/exclude. The
// markers are what make the block recognisable, so a rewrite replaces it
// instead of piling copies up.
package mark

import "strings"

type Block struct {
	Start string
	End   string
}

// Rewrite returns content with the block, and it alone, carrying lines. The
// block goes last: whatever else the file holds keeps its place and its order.
func (b Block) Rewrite(content string, lines []string) string {
	var out strings.Builder
	if body := strings.TrimRight(b.Strip(content), "\n"); body != "" {
		out.WriteString(body)
		out.WriteString("\n\n")
	}
	out.WriteString(b.Start + "\n")
	for _, line := range lines {
		out.WriteString(line + "\n")
	}
	out.WriteString(b.End + "\n")
	return out.String()
}

// Strip removes a block written earlier. A file holding the opening marker
// alone is left as it is: cutting from there to the end would take content the
// block never owned.
func (b Block) Strip(content string) string {
	start := strings.Index(content, b.Start)
	if start == -1 {
		return content
	}
	end := strings.Index(content[start:], b.End)
	if end == -1 {
		return content
	}
	return content[:start] + content[start+end+len(b.End):]
}
