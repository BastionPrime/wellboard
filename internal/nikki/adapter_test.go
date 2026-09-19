package nikki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// compile-time interface check: DryRunAdapter satisfies Adapter.
var _ Adapter = (*DryRunAdapter)(nil)

func newAdapter(t *testing.T) (*DryRunAdapter, *strings.Builder) {
	t.Helper()
	var logs strings.Builder
	d := NewDryRunAdapter(t.TempDir(), "")
	d.Log = func(format string, args ...any) {
		fmt.Fprintf(&logs, format+"\n", args...)
	}
	return d, &logs
}

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteProfileAndValidateSkip(t *testing.T) {
	d, logs := newAdapter(t)

	path, err := d.WriteProfile("default", []byte("rules:\n  - MATCH,DIRECT\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if filepath.Base(path) != "default.yaml" {
		t.Errorf("path base = %q, want default.yaml", filepath.Base(path))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("profile not written: %v", err)
	}

	// No binary configured → validation is skipped (nil error, logged).
	if err := d.Validate(path); err != nil {
		t.Fatalf("validate without binary should skip, got %v", err)
	}
	if !strings.Contains(logs.String(), "skipping validation") {
		t.Errorf("skip not logged: %q", logs.String())
	}
}

func TestWriteProfileSanitizesName(t *testing.T) {
	d, _ := newAdapter(t)
	path, err := d.WriteProfile("../../etc/passwd", []byte("x"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(filepath.Base(path), "_etc_passwd.yaml") {
		t.Errorf("name not sanitized: %s", path)
	}
	abs, _ := filepath.Abs(path)
	if !strings.HasPrefix(abs, d.Dir) {
		t.Errorf("profile escaped dir: %s not under %s", abs, d.Dir)
	}
}

func TestWriteProfileEmptyName(t *testing.T) {
	d, _ := newAdapter(t)
	path, err := d.WriteProfile("", []byte("x"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if filepath.Base(path) != "profile.yaml" {
		t.Errorf("empty name fallback = %q, want profile.yaml", filepath.Base(path))
	}
}

func TestWriteProfileInvalidDir(t *testing.T) {
	// Point the adapter at a path under a FILE: MkdirAll must fail.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewDryRunAdapter(filepath.Join(blocker, "sub"), "")
	if _, err := d.WriteProfile("p", []byte("x")); err == nil {
		t.Fatal("expected error for unwritable dir")
	}
}

func TestValidateRunsRealBinaryWhenPresent(t *testing.T) {
	// /bin/true as a stand-in "mihomo" that always succeeds.
	d := NewDryRunAdapter(t.TempDir(), "/bin/true")
	path, err := d.WriteProfile("ok", []byte("rules:\n  - MATCH,DIRECT\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(path); err != nil {
		t.Fatalf("validate with true binary: %v", err)
	}
}

func TestValidateFailingBinary(t *testing.T) {
	// /bin/false always exits non-zero → error must carry context.
	d := NewDryRunAdapter(t.TempDir(), "/bin/false")
	path, err := d.WriteProfile("bad", []byte("rules:\n  - MATCH,DIRECT\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = d.Validate(path)
	if err == nil {
		t.Fatal("expected validation error from failing binary")
	}
	if !strings.Contains(err.Error(), "mihomo -t") {
		t.Errorf("error should mention mihomo -t: %v", err)
	}
}

func TestValidateMissingBinarySkips(t *testing.T) {
	d := NewDryRunAdapter(t.TempDir(), "/nonexistent/mihomo")
	var logs strings.Builder
	d.Log = func(format string, args ...any) { fmt.Fprintf(&logs, format+"\n", args...) }
	path, err := d.WriteProfile("p", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(path); err != nil {
		t.Fatalf("missing binary should skip, got %v", err)
	}
	if !strings.Contains(logs.String(), "binary missing") {
		t.Errorf("missing-binary skip not logged: %q", logs.String())
	}
}

func TestActivateLastGood(t *testing.T) {
	d, _ := newAdapter(t)

	if _, ok := d.LastGood(); ok {
		t.Fatal("LastGood before any activate should be false")
	}
	if err := d.Activate("default"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	name, ok := d.LastGood()
	if !ok || name != "default" {
		t.Fatalf("LastGood = (%q,%v), want (default,true)", name, ok)
	}
}

func TestHealthSimulated(t *testing.T) {
	d, _ := newAdapter(t)
	if err := d.Health(5 * time.Second); err != nil {
		t.Fatalf("dry-run health should be ok: %v", err)
	}
}

func TestDetect(t *testing.T) {
	d, _ := newAdapter(t)
	info, err := d.Detect()
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if info.ProfilesDir != d.Dir {
		t.Errorf("ProfilesDir = %q, want %q", info.ProfilesDir, d.Dir)
	}
	if !strings.Contains(info.MihomoVersion, "unavailable") {
		t.Errorf("MihomoVersion = %q, want unavailable note", info.MihomoVersion)
	}
}

func TestDetectWithVersionBinary(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fake-mihomo",
		"#!/bin/sh\necho 'Mihomo Meta v1.19.31 linux amd64'\n")
	d := NewDryRunAdapter(dir, script)
	info, err := d.Detect()
	if err != nil {
		t.Fatal(err)
	}
	if info.MihomoVersion != "Mihomo Meta v1.19.31 linux amd64" {
		t.Errorf("MihomoVersion = %q", info.MihomoVersion)
	}
}

func TestDetectFailingVersionBinary(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fail-mihomo", "#!/bin/sh\nexit 3\n")
	d := NewDryRunAdapter(dir, script)
	info, err := d.Detect()
	if err != nil {
		t.Fatalf("detect should not fail hard: %v", err)
	}
	if !strings.Contains(info.MihomoVersion, "unavailable") {
		t.Errorf("MihomoVersion = %q, want unavailable note", info.MihomoVersion)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine([]byte("line1\nline2\n")); got != "line1" {
		t.Errorf("firstLine = %q, want line1", got)
	}
	if got := firstLine([]byte("single")); got != "single" {
		t.Errorf("firstLine = %q, want single", got)
	}
}
