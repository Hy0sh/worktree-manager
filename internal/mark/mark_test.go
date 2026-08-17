package mark

import (
	"strings"
	"testing"
)

var block = Block{Start: "# --- start ---", End: "# --- end ---"}

func TestRewriteIsIdempotentAndKeepsTheRest(t *testing.T) {
	content := "SECRET=keep-me\n"
	for range 3 {
		content = block.Rewrite(content, []string{"A=1", "B=2"})
	}
	if !strings.Contains(content, "SECRET=keep-me") {
		t.Fatalf("the rest of the file must survive:\n%s", content)
	}
	if n := strings.Count(content, block.Start); n != 1 {
		t.Fatalf("expected one block after three rewrites, got %d:\n%s", n, content)
	}
	if !strings.Contains(content, "A=1\nB=2\n") {
		t.Fatalf("lines missing:\n%s", content)
	}
}

// A block written by an older version listed other lines, and only a strip
// keyed on the markers can replace it.
func TestRewriteReplacesAnOlderBlockWhereverItSits(t *testing.T) {
	content := block.Rewrite("first\n", []string{"OLD=1"}) + "last\n"
	content = block.Rewrite(content, []string{"NEW=1"})
	if strings.Contains(content, "OLD=1") {
		t.Fatalf("the old block should be gone:\n%s", content)
	}
	for _, want := range []string{"first", "last", "NEW=1"} {
		if !strings.Contains(content, want) {
			t.Fatalf("%q should have survived:\n%s", want, content)
		}
	}
}

// Truncating from a lone opening marker to the end of the file would take
// lines the block never owned.
func TestStripLeavesAnUnterminatedBlockAlone(t *testing.T) {
	content := block.Start + "\nA=1\nmine\n"
	if got := block.Strip(content); got != content {
		t.Fatalf("content should be untouched, got:\n%s", got)
	}
}
