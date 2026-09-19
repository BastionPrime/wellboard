package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRingAndTail(t *testing.T) {
	l := New("", 10)
	for i := 0; i < 25; i++ {
		l.Printf("line %02d", i)
	}
	tail := l.Tail(0)
	if len(tail) != 10 {
		t.Fatalf("tail len = %d, want 10 (buffer cap)", len(tail))
	}
	if tail[0] != "line 15" || tail[9] != "line 24" {
		t.Fatalf("tail boundaries wrong: %q…%q", tail[0], tail[9])
	}
	// Explicit n < cap.
	if got := l.Tail(3); len(got) != 3 || got[0] != "line 22" {
		t.Fatalf("tail(3) = %v", got)
	}
}

func TestMultiLineWrite(t *testing.T) {
	l := New("", 100)
	// log.Printf sends newline-terminated chunks; a chunk may contain
	// embedded newlines (adapter dumps).
	_, _ = l.Write([]byte("a\nb\nc\n"))
	got := l.Tail(0)
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("multi-line split failed: %v", got)
	}
}

func TestMirrorFileAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wellboard.log")

	l1 := New(path, 100)
	l1.Printf("alpha")
	l1.Printf("beta")
	l1.Close()

	// Fresh buffer, empty memory: Tail falls back to the file.
	l2 := New(path, 100)
	defer l2.Close()
	tail := l2.Tail(0)
	joined := strings.Join(tail, "\n")
	if !strings.Contains(joined, "alpha") || !strings.Contains(joined, "beta") {
		t.Fatalf("file fallback lost lines: %q", joined)
	}
}

func TestTruncation(t *testing.T) {
	l := New("", 5)
	l.Printf("%s", strings.Repeat("x", 20000))
	got := l.Tail(1)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if len(got[0]) > 8196 { // 8 KiB + 3-byte ellipsis
		t.Fatalf("line not truncated: %d bytes", len(got[0]))
	}
	if _, err := os.Stat("no-such-file"); err == nil {
		t.Fatal("sanity")
	}
}
