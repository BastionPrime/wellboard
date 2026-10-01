package nikki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunner records every command and answers according to a handler.
type fakeRunner struct {
	calls   [][]string
	handler func(name string, args []string) ([]byte, error)
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.handler != nil {
		return f.handler(name, args)
	}
	return nil, nil
}

func (f *fakeRunner) joined() string {
	var b strings.Builder
	for _, c := range f.calls {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}

// newTestRouter builds a RouterAdapter over temp dirs with a fake
// runner, mirroring the router layout without touching the host.
func newTestRouter(t *testing.T) (*RouterAdapter, *fakeRunner, string, string) {
	t.Helper()
	root := t.TempDir()
	profiles := filepath.Join(root, "profiles")
	runDir := filepath.Join(root, "run")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	a := &RouterAdapter{
		ProfilesDir: profiles,
		RunDir:      runDir,
		MihomoBin:   "/usr/bin/mihomo",
		UCIBin:      "uci",
		InitScript:  "/etc/init.d/nikki",
		HealthPoll:  time.Millisecond,
		Run:         fr.run,
	}
	return a, fr, profiles, runDir
}

func TestRouterDetectSeedsLastGoodFromUci(t *testing.T) {
	a, fr, profiles, runDir := newTestRouter(t)
	// The owner's profile exists on disk and UCI selects it.
	owner := "owner-working"
	if err := os.WriteFile(filepath.Join(profiles, owner+".yaml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Runtime config carries the API coordinates (read-only input).
	if err := os.WriteFile(filepath.Join(runDir, "config.yaml"),
		[]byte("external-controller: 127.0.0.1:9090\nsecret: s3cr3t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fr.handler = func(name string, args []string) ([]byte, error) {
		switch {
		case name == "uci" && len(args) == 3 && args[2] == "nikki.config.profile":
			return []byte(owner + "\n"), nil
		case name == a.MihomoBin:
			return []byte("Mihomo Meta v1.19.0 linux arm64\n"), nil
		case name == "opkg":
			return []byte("nikki - 1.10.0\n"), nil
		}
		return nil, nil
	}
	info, err := a.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.MihomoVersion != "Mihomo Meta v1.19.0 linux arm64" {
		t.Fatalf("mihomo version = %q", info.MihomoVersion)
	}
	if info.NikkiVersion != "1.10.0" {
		t.Fatalf("nikki version = %q", info.NikkiVersion)
	}
	if info.APIAddr != "127.0.0.1:9090" || info.APISecret != "s3cr3t" {
		t.Fatalf("api coords = %q / %q", info.APIAddr, info.APISecret)
	}
	lg, ok := a.LastGood()
	if !ok || lg != owner {
		t.Fatalf("LastGood = %q,%v; want %q,true (owner profile is the rollback target)", lg, ok, owner)
	}
}

func TestRouterDetectFailsWithoutProfilesDir(t *testing.T) {
	a, _, _, _ := newTestRouter(t)
	a.ProfilesDir = filepath.Join(t.TempDir(), "missing")
	if _, err := a.Detect(); err == nil {
		t.Fatal("Detect must fail when nikki is not installed")
	}
}

func TestRouterWriteProfileKeepsForeignProfiles(t *testing.T) {
	a, _, profiles, _ := newTestRouter(t)
	foreign := filepath.Join(profiles, "owner-working.yaml")
	if err := os.WriteFile(foreign, []byte("rules: [owner]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := a.WriteProfile("wellboard-20261001-000000-001", []byte("rules: [new]\n"))
	if err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	if base := filepath.Base(path); base != "wellboard-20261001-000000-001.yaml" {
		t.Fatalf("profile path = %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "rules: [new]\n" {
		t.Fatalf("profile content = %q err=%v", got, err)
	}
	// No temp file left behind, owner's profile untouched.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left: %v", err)
	}
	owner, err := os.ReadFile(foreign)
	if err != nil || string(owner) != "rules: [owner]\n" {
		t.Fatalf("foreign profile changed: %q err=%v", owner, err)
	}
}

func TestRouterActivateDrivesUciAndRestart(t *testing.T) {
	a, fr, profiles, _ := newTestRouter(t)
	name := "wellboard-20261001-000000-001"
	if err := os.WriteFile(filepath.Join(profiles, name+".yaml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Activate(name); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	j := fr.joined()
	for _, want := range []string{
		"uci set nikki.config.profile=" + name,
		"uci commit nikki",
		"/etc/init.d/nikki restart",
	} {
		if !strings.Contains(j, want) {
			t.Fatalf("missing %q in commands:\n%s", want, j)
		}
	}
	// The only UCI writes are the profile option: the mixin and the
	// other nikki sections are never touched.
	if strings.Contains(j, "nikki.mixin") || strings.Contains(j, "run/config.yaml") {
		t.Fatalf("coexistence violated by commands:\n%s", j)
	}
}

func TestRouterActivateRejectsUnknownProfile(t *testing.T) {
	a, fr, _, _ := newTestRouter(t)
	if err := a.Activate("nope"); err == nil {
		t.Fatal("Activate must fail for a profile that is not on disk")
	}
	if len(fr.calls) != 0 {
		t.Fatalf("no command may run for an unknown profile: %v", fr.calls)
	}
}

func TestRouterActivateUciFailure(t *testing.T) {
	a, fr, profiles, _ := newTestRouter(t)
	name := "wellboard-20261001-000000-002"
	if err := os.WriteFile(filepath.Join(profiles, name+".yaml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fr.handler = func(name string, args []string) ([]byte, error) {
		if name == "uci" {
			return []byte("uci: Entry not found"), fmt.Errorf("exit status 1")
		}
		return nil, nil
	}
	if err := a.Activate(name); err == nil {
		t.Fatal("Activate must surface a uci failure")
	}
}

func TestRouterHealthUsesMihomoAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer s3cr3t" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, _, _, _ := newTestRouter(t)
	a.ControllerAddr = strings.TrimPrefix(srv.URL, "http://")
	a.APISecret = "s3cr3t"
	if err := a.Health(2 * time.Second); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func TestRouterHealthFailsWithinBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a, _, _, _ := newTestRouter(t)
	a.ControllerAddr = strings.TrimPrefix(srv.URL, "http://")
	start := time.Now()
	err := a.Health(300 * time.Millisecond)
	if err == nil {
		t.Fatal("Health must fail when the API never turns healthy")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Health ignored its budget: %s", elapsed)
	}
}

func TestRouterHealthFallsBackToServiceStatus(t *testing.T) {
	a, fr, _, _ := newTestRouter(t)
	fr.handler = func(name string, args []string) ([]byte, error) {
		if name == a.InitScript && len(args) == 1 && args[0] == "status" {
			return []byte("running\n"), nil
		}
		return nil, nil
	}
	if err := a.Health(time.Second); err != nil {
		t.Fatalf("service fallback health: %v", err)
	}
}

func TestRouterValidateRunsMihomoTest(t *testing.T) {
	a, fr, profiles, _ := newTestRouter(t)
	p := filepath.Join(profiles, "wellboard-x.yaml")
	if err := a.Validate(p); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !strings.Contains(fr.joined(), "mihomo -t -d "+profiles+" -f "+p) {
		t.Fatalf("validate command = %s", fr.joined())
	}
	fr.handler = func(string, []string) ([]byte, error) { return []byte("parse error"), fmt.Errorf("exit status 1") }
	if err := a.Validate(p); err == nil {
		t.Fatal("Validate must surface mihomo -t failure")
	}
}

// TestRouterCoexistenceRunConfigUntouched is the coexistence invariant
// from the ticket: the running nikki config is byte-identical
// (sha256 before/after) across write + activate + health.
func TestRouterCoexistenceRunConfigUntouched(t *testing.T) {
	a, fr, profiles, runDir := newTestRouter(t)
	cfg := filepath.Join(runDir, "config.yaml")
	original := []byte("mixed-port: 7890\nrules:\n  - MATCH,DIRECT\n")
	if err := os.WriteFile(cfg, original, 0o644); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(original)

	name := "wellboard-20261001-000000-003"
	if _, err := a.WriteProfile(name, []byte("rules: []\n")); err != nil {
		t.Fatal(err)
	}
	fr.handler = func(n string, args []string) ([]byte, error) {
		if n == a.InitScript {
			return []byte("running\n"), nil
		}
		return nil, nil
	}
	if err := a.Activate(name); err != nil {
		t.Fatal(err)
	}
	if err := a.Health(time.Second); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(before[:]) != hex.EncodeToString(sha256Sum(after)) {
		t.Fatalf("run config changed:\nbefore %q\nafter  %q", original, after)
	}
	// Only the profile WellBoard minted appeared in the profiles dir.
	entries, _ := os.ReadDir(profiles)
	if len(entries) != 1 || entries[0].Name() != name+".yaml" {
		t.Fatalf("profiles dir gained unexpected entries: %v", entries)
	}
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}