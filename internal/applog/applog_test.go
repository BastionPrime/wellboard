package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRingAndTail(t *testing.T) {
	l := New("", 10, false)
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
	l := New("", 100, false)
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

	l1 := New(path, 100, false)
	l1.Printf("alpha")
	l1.Printf("beta")
	l1.Close()

	// Fresh buffer, empty memory: Tail falls back to the file.
	l2 := New(path, 100, false)
	defer l2.Close()
	tail := l2.Tail(0)
	joined := strings.Join(tail, "\n")
	if !strings.Contains(joined, "alpha") || !strings.Contains(joined, "beta") {
		t.Fatalf("file fallback lost lines: %q", joined)
	}
}

func TestTruncation(t *testing.T) {
	l := New("", 5, false)
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

// TestProdPermissions: prod log creates the mirror file 0600 (NFR-2.3),
// mirroring internal/store TestProdPermissions.
func TestProdPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wellboard.log")
	l := New(path, 100, true)
	l.Printf("prod line")
	l.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", fi.Mode().Perm())
	}
}

// TestProdTightensExistingFile: a 0644 mirror file left by an older
// build is tightened to 0600 when a prod Log opens it for append.
func TestProdTightensExistingFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not apply")
	}
	path := filepath.Join(t.TempDir(), "wellboard.log")
	if err := os.WriteFile(path, []byte("old 0644 line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := New(path, 100, true)
	defer l.Close()
	l.Printf("new prod line")

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600 (existing file tightened)", fi.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "old 0644 line") || !strings.Contains(string(data), "new prod line") {
		t.Errorf("append semantics broken: %q", data)
	}
}

// TestDevPermissions: dev log keeps umask-governed modes (0644 with
// the test umask), like internal/store TestDevPermissions.
func TestDevPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wellboard.log")
	l := New(path, 100, false)
	l.Printf("dev line")
	l.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("file mode = %o, want 644 (umask-governed)", fi.Mode().Perm())
	}
}
