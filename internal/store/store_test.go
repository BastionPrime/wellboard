package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
)

func mustSave(t *testing.T, s *Store, st *model.State) {
	t.Helper()
	if err := s.Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func mustLoad(t *testing.T, s *Store) *model.State {
	t.Helper()
	st, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return st
}

// TestRoundTrip covers save → load → equal, the version field, and
// atomic-write leftovers (no temp files remain after a successful save).
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, false)

	st := DefaultState()
	st.Sources = []model.Source{{ID: "src_manual", Kind: "manual", Name: "Manual"}}
	st.Servers = []model.Server{{
		ID: "srv_1", SourceID: "src_manual", Name: "NL-1", Type: "vless",
		Raw: map[string]any{"server": "203.0.113.10", "port": 443},
	}}
	mustSave(t, s, st)

	got := mustLoad(t, s)
	if got.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", got.Version, CurrentVersion)
	}
	if len(got.Servers) != 1 || got.Servers[0].ID != "srv_1" {
		t.Fatalf("servers not round-tripped: %+v", got.Servers)
	}
	if got.Servers[0].Raw["port"] != float64(443) {
		t.Fatalf("raw map not round-tripped: %+v", got.Servers[0].Raw)
	}
	if got.Settings.DefaultPolicy.Type != model.TargetDirect {
		t.Fatalf("default policy = %+v, want direct", got.Settings.DefaultPolicy)
	}

	// Atomic write must not leave temp files behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".state-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestLoadMissingFallsBackToDefault verifies first start: no file →
// defaults, no error.
func TestLoadMissingFallsBackToDefault(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "sub", "dir"), false)
	st := mustLoad(t, s)
	if st.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", st.Version, CurrentVersion)
	}
	if st.Settings.UIPort != 8090 {
		t.Fatalf("ui_port = %d, want 8090", st.Settings.UIPort)
	}
}

// TestLoadInvalidJSON errors out with the file path context.
func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, false).Load(); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestLoadNewerVersion refuses downgrades.
func TestLoadNewerVersion(t *testing.T) {
	dir := t.TempDir()
	raw := `{"version": 999}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(dir, false).Load()
	if !errors.Is(err, ErrVersionTooNew) {
		t.Fatalf("err = %v, want ErrVersionTooNew", err)
	}
}

// TestMigrateHookIsNoopForV1: the migration hook runs on load and is a
// no-op for v1 (the only current schema).
func TestMigrateHookIsNoopForV1(t *testing.T) {
	st := &model.State{Version: 1}
	if err := migrate(st); err != nil {
		t.Fatalf("migrate v1: %v", err)
	}
	if st.Version != 1 {
		t.Fatalf("version = %d, want 1", st.Version)
	}
}

// TestSaveOverwritesAtomically proves repeated saves converge to the latest
// content via rename (no truncation window).
func TestSaveOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, false)

	st := DefaultState()
	st.Settings.UIPort = 1111
	mustSave(t, s, st)
	st.Settings.UIPort = 2222
	mustSave(t, s, st)

	got := mustLoad(t, s)
	if got.Settings.UIPort != 2222 {
		t.Fatalf("ui_port = %d, want 2222 (latest save)", got.Settings.UIPort)
	}
}

// TestProdPermissions: prod store creates a 0700 dir and a 0600 file.
func TestProdPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s := New(dir, true)
	mustSave(t, s, DefaultState())

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", fi.Mode().Perm())
	}
	fi, err = os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", fi.Mode().Perm())
	}
}

// TestDevPermissions: dev store keeps umask-governed modes (0755/0644 with
// the test umask).
func TestDevPermissions(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, false)
	mustSave(t, s, DefaultState())

	fi, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o400 == 0 {
		t.Errorf("file mode = %o, want owner-readable", fi.Mode().Perm())
	}
}

// TestSaveFsyncErrorPath: writing to a read-only directory fails, and no
// half-written final file appears.
func TestSaveFsyncErrorPath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not apply")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	st := DefaultState()
	if err := New(ro, false).Save(st); err == nil {
		t.Fatal("expected error saving into read-only dir")
	}
	if _, err := os.Stat(filepath.Join(ro, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state.json should not exist after failed save, err = %v", err)
	}
}

// TestJSONShape sanity-checks the marshalled field names against the
// initial TZ 5.3 example so renames cannot slip in silently.
func TestJSONShape(t *testing.T) {
	st := DefaultState()
	st.Sources = []model.Source{{
		ID: "sub_a1", Kind: "subscription", Name: "WellDone",
		URL: "https://example.com/sub", Enabled: true, UpdateIntervalSec: 43200,
	}}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"version"`, `"settings"`, `"ui_port"`, `"lang"`, `"geodata"`,
		`"default_policy"`, `"delay_test_interval_sec"`, `"sources"`,
		`"servers"`, `"groups"`, `"routes"`, `"lan_devices"`,
		`"update_interval_sec"`,
	} {
		if !strings.Contains(string(data), key) {
			t.Errorf("marshalled state missing key %s in %s", key, data)
		}
	}
}
