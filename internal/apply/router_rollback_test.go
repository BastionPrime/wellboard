package apply

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/nikki"
)

// TestApplyRouterAdapterRollsBackToOwnerProfile drives the whole FR-6
// flow against the REAL RouterAdapter (deliverable 1 of the ticket): a
// generated profile that never becomes healthy must be rolled back to
// the profile the owner's router was already running.
func TestApplyRouterAdapterRollsBackToOwnerProfile(t *testing.T) {
	old := HealthTimeout
	HealthTimeout = 300 * time.Millisecond
	defer func() { HealthTimeout = old }()

	root := t.TempDir()
	profiles := filepath.Join(root, "profiles")
	runDir := filepath.Join(root, "run")
	for _, d := range []string{profiles, runDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const owner = "owner-working"
	if err := os.WriteFile(filepath.Join(profiles, owner+".yaml"), []byte("rules: [MATCH,DIRECT]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The mihomo API is up only while the OWNER profile is selected:
	// the freshly generated one is the broken case.
	var healthy bool
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ok := healthy
		mu.Unlock()
		if ok {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := filepath.Join(runDir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("external-controller: "+strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgBefore, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}

	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "uci" && len(args) >= 2 && args[0] == "-q" && args[1] == "get":
			return []byte(owner + "\n"), nil
		case name == "uci" && len(args) == 2 && args[0] == "set":
			selected := strings.TrimPrefix(args[1], "nikki.config.profile=")
			mu.Lock()
			healthy = selected == owner
			mu.Unlock()
			return nil, nil
		case name == "uci" && len(args) >= 1 && args[0] == "commit":
			return nil, nil
		case name == "/etc/init.d/nikki":
			return []byte("running\n"), nil
		}
		return nil, nil
	}

	a := &nikki.RouterAdapter{
		ProfilesDir: profiles,
		RunDir:      runDir,
		MihomoBin:   "/usr/bin/mihomo",
		UCIBin:      "uci",
		InitScript:  "/etc/init.d/nikki",
		HealthPoll:  10 * time.Millisecond,
		Run:         run,
	}
	if _, err := a.Detect(); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if lg, ok := a.LastGood(); !ok || lg != owner {
		t.Fatalf("rollback target not seeded from the running config: %q,%v", lg, ok)
	}

	ap := New(a, &memStore{testState()}, nil, filepath.Join(root, "apply"))
	res, err := ap.Apply()
	if err != nil {
		t.Fatalf("Apply infrastructure error: %v", err)
	}
	if res.OK {
		t.Fatal("apply of a profile that never becomes healthy must fail")
	}
	if !res.RolledBack || res.LastGoodProfile != owner {
		t.Fatalf("expected rollback to %q, got rolled_back=%v last_good=%q err=%s",
			owner, res.RolledBack, res.LastGoodProfile, res.Error)
	}

	// The owner's running config is byte-identical after the whole flow.
	cfgAfter, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(cfgBefore) != string(cfgAfter) {
		t.Fatalf("running config changed:\nbefore %q\nafter  %q", cfgBefore, cfgAfter)
	}
	// The owner's own profile file is untouched.
	if data, err := os.ReadFile(filepath.Join(profiles, owner+".yaml")); err != nil ||
		string(data) != "rules: [MATCH,DIRECT]\n" {
		t.Fatalf("owner profile modified: %q err=%v", data, err)
	}
}