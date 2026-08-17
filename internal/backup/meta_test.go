package backup

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestMetaRoundTrip(t *testing.T) {
	m := &Manager{Root: t.TempDir()}
	written := Meta{
		GeneratedAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		GeneratedBy: "someone",
		GitRev:      "abc123",
	}
	if err := m.writeMetaFile("myapp", written); err != nil {
		t.Fatalf("writeMetaFile: %v", err)
	}
	read, err := m.ReadMeta("myapp")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if read != written {
		t.Fatalf("read %+v, wrote %+v", read, written)
	}
}

// `backup list` calls this on whatever sits next to the dump, so a truncated
// or hand-edited file has to say which one it is.
func TestReadMetaNamesTheFileItCannotParse(t *testing.T) {
	m := &Manager{Root: t.TempDir()}
	if err := m.writeMetaFile("myapp", Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.MetaPath("myapp"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := m.ReadMeta("myapp")
	if err == nil {
		t.Fatal("unparseable metadata should be an error")
	}
	if !strings.Contains(err.Error(), m.MetaPath("myapp")) {
		t.Fatalf("the error should name the file, got %q", err)
	}
}
