package apply

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
)

// mockAdapter scripts Adapter behavior for the flow tests.
type mockAdapter struct {
	dir string

	validateErr error // returned by Validate
	activateErr error // returned by Activate
	healthErr   error // returned by Health (unless healthOf overrides)

	written   []string
	validated []string
	activated []string
	healthOf  map[string]error // profile name → health error (nil = ok)

	current     string // last activated profile
	lastGood    string
	lastGoodSet bool
}

func newMockAdapter(dir string) *mockAdapter {
	return &mockAdapter{dir: dir, healthOf: map[string]error{}}
}

func (m *mockAdapter) Detect() (nikki.Info, error) { return nikki.Info{}, nil }

func (m *mockAdapter) WriteProfile(name string, yamlData []byte) (string, error) {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(m.dir, name+".yaml")
	if err := os.WriteFile(path, yamlData, 0o644); err != nil {
		return "", err
	}
	m.written = append(m.written, name)
	return path, nil
}

func (m *mockAdapter) Validate(path string) error {
	m.validated = append(m.validated, filepath.Base(path))
	return m.validateErr
}

func (m *mockAdapter) Activate(name string) error {
	m.activated = append(m.activated, name)
	if m.activateErr != nil {
		return m.activateErr
	}
	m.current = name
	// Re-activating the last good profile (rollback path) restores
	// healthy service in this mock — a blanket healthErr applies only
	// to the profile that failed right after its activation.
	if name == m.lastGood {
		m.healthErr = nil
	}
	return nil
}

// Health answers per-profile when healthOf has an entry for the
// CURRENT profile, else the blanket healthErr.
func (m *mockAdapter) Health(timeout time.Duration) error {
	if err, ok := m.healthOf[m.current]; ok {
		return err
	}
	return m.healthErr
}

func (m *mockAdapter) LastGood() (string, bool) { return m.lastGood, m.lastGoodSet }

func (m *mockAdapter) MarkHealthy(name string) {
	m.lastGood = name
	m.lastGoodSet = true
}

// ------------------------------------------------------------------
// State fixtures
// ------------------------------------------------------------------

func testState() *model.State {
	return &model.State{
		Version: 2,
		Settings: model.Settings{
			UIPort: 8090, Lang: "ru", Geodata: "runetfreedom",
			DefaultPolicy: model.Target{Type: model.TargetDirect},
		},
	}
}

func brokenState() *model.State {
	// Route pointing at a nonexistent group → generator *Problems.
	st := testState()
	st.Routes = []model.Route{{
		ID: "rt_1", Name: "Broken", Enabled: true, Order: 10,
		Target: model.Target{Type: model.TargetGroup, ID: "grp_missing"},
	}}
	return st
}

// memStore is a trivial StateLoader.
type memStore struct{ st *model.State }

func (m *memStore) Load() (*model.State, error) { return m.st, nil }

// ------------------------------------------------------------------
// Flow tests
// ------------------------------------------------------------------

// TestApplySuccess: generate → validate → write → activate → health all
// pass; the history gets a record and the adapter's last-good is set.
func TestApplySuccess(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	a := New(adapter, &memStore{testState()}, nil, filepath.Join(dir, "apply"))

	res, err := a.Apply()
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !res.OK {
		t.Fatalf("apply should succeed, got stage=%q error=%q", res.Stage, res.Error)
	}
	if res.Profile == "" {
		t.Fatal("profile name empty")
	}
	if len(adapter.written) != 1 || len(adapter.validated) != 1 || len(adapter.activated) != 1 {
		t.Fatalf("adapter calls: written=%v validated=%v activated=%v", adapter.written, adapter.validated, adapter.activated)
	}
	if lg, ok := adapter.LastGood(); !ok || lg != res.Profile {
		t.Fatalf("LastGood = (%q,%v), want the applied profile %q", lg, ok, res.Profile)
	}
	hist := a.History()
	if len(hist) != 1 || hist[0].Profile != res.Profile {
		t.Fatalf("history = %+v", hist)
	}
}

// TestApplyGenerateFails: a broken state (lost target) aborts at the
// generate stage with a Problems-flavored message and NOTHING is
// written/validated/activated.
func TestApplyGenerateFails(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	a := New(adapter, &memStore{brokenState()}, nil, filepath.Join(dir, "apply"))

	res, err := a.Apply()
	if err != nil {
		t.Fatalf("flow failures are data, not errors: %v", err)
	}
	if res.OK {
		t.Fatal("broken state must not apply")
	}
	if res.Stage != "generate" {
		t.Fatalf("stage = %q, want generate", res.Stage)
	}
	if !strings.Contains(res.Error, "unresolved references") {
		t.Fatalf("error should be human-readable, got %q", res.Error)
	}
	if len(adapter.written) != 0 || len(adapter.activated) != 0 {
		t.Fatalf("no adapter calls expected, got written=%v activated=%v", adapter.written, adapter.activated)
	}
}

// TestApplyValidateFails: mihomo -t rejection aborts at validate; the
// failure names the validator.
func TestApplyValidateFails(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	adapter.validateErr = errors.New("mihomo -t /x.yaml: exit 1: proxy [nope] not found")
	a := New(adapter, &memStore{testState()}, nil, filepath.Join(dir, "apply"))

	res, _ := a.Apply()
	if res.OK || res.Stage != "validate" {
		t.Fatalf("expected validate failure, got %+v", res)
	}
	if !strings.Contains(res.Error, "mihomo -t") {
		t.Fatalf("error should mention mihomo -t: %q", res.Error)
	}
	if len(adapter.activated) != 0 {
		t.Fatalf("activate must not run after failed validate, got %v", adapter.activated)
	}
}

// TestApplyHealthFailsRollsBack: after a successful first apply, a
// second apply whose health fails must auto-activate the last good
// profile and report the rollback (FR-6.4).
func TestApplyHealthFailsRollsBack(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	a := New(adapter, &memStore{testState()}, nil, filepath.Join(dir, "apply"))

	// First apply succeeds and becomes last-good.
	first, err := a.Apply()
	if err != nil || !first.OK {
		t.Fatalf("first apply: %+v err=%v", first, err)
	}

	// Second apply fails at health (blanket failure, like a dead API).
	adapter.healthErr = errors.New("mihomo API health check failed within 15s: dead")
	second, _ := a.Apply()
	if second.OK || second.Stage != "health" {
		t.Fatalf("expected health failure, got %+v", second)
	}
	if !second.RolledBack {
		t.Fatalf("expected auto-rollback, got %+v", second)
	}
	if second.LastGoodProfile != first.Profile {
		t.Fatalf("rollback target = %q, want first profile %q", second.LastGoodProfile, first.Profile)
	}
	// Rollback re-activated the first profile: activated = [first, second, first].
	if len(adapter.activated) != 3 || adapter.activated[2] != first.Profile {
		t.Fatalf("rollback did not re-activate last good: %v", adapter.activated)
	}
	// The failed apply must NOT be in the history.
	if hist := a.History(); len(hist) != 1 {
		t.Fatalf("history should hold only the successful apply, got %d", len(hist))
	}
}

// TestApplyHealthFailsNoLastGood: the very first apply failing health
// reports "no previous good profile" instead of a rollback.
func TestApplyHealthFailsNoLastGood(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	adapter.healthErr = errors.New("dead")
	a := New(adapter, &memStore{testState()}, nil, filepath.Join(dir, "apply"))

	res, _ := a.Apply()
	if res.OK || res.Stage != "health" {
		t.Fatalf("expected health failure, got %+v", res)
	}
	if res.RolledBack {
		t.Fatalf("no last good exists — must not claim a rollback: %+v", res)
	}
	if !strings.Contains(res.Error, "no previous good profile") {
		t.Fatalf("error should explain the missing rollback target: %q", res.Error)
	}
}

// TestHistoryRotation: more than HistorySize applies keep only the
// newest 5 (FR-6.3).
func TestHistoryRotation(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	a := New(adapter, &memStore{testState()}, nil, filepath.Join(dir, "apply"))

	for i := 0; i < HistorySize+2; i++ {
		if _, err := a.Apply(); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	hist := a.History()
	if len(hist) != HistorySize {
		t.Fatalf("history len = %d, want %d", len(hist), HistorySize)
	}
	// Newest first ordering.
	for i := 1; i < len(hist); i++ {
		if !hist[i-1].AppliedAt.After(hist[i].AppliedAt) {
			t.Fatalf("history not newest-first: [%d]%v <= [%d]%v", i-1, hist[i-1].AppliedAt, i, hist[i].AppliedAt)
		}
	}
}

// TestPendingIndicator: modifying the state after an apply flips
// pending to true; identical state stays false (FR-6.1).
func TestPendingIndicator(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	st := testState()
	a := New(adapter, &memStore{st}, nil, filepath.Join(dir, "apply"))

	// Virgin empty state: not pending.
	if p, _, err := a.Pending(); err != nil || p {
		t.Fatalf("empty state should not be pending: %v %v", p, err)
	}
	// Contentful state, nothing applied: pending.
	st.Routes = []model.Route{{
		ID: "rt_1", Name: "Ads", Enabled: true, Order: 10,
		Conditions: []model.RouteCondition{{Type: model.CondDomainSuffix, Value: "ads.example"}},
		Target:     model.Target{Type: model.TargetReject},
	}}
	if p, _, _ := a.Pending(); !p {
		t.Fatal("unapplied routes must be pending")
	}
	// Apply → pending clears.
	if _, err := a.Apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if p, _, _ := a.Pending(); p {
		t.Fatal("freshly applied state must not be pending")
	}
	// Change the state → pending again.
	st.Routes[0].Enabled = false
	if p, _, _ := a.Pending(); !p {
		t.Fatal("state change after apply must be pending")
	}
}

// TestRollback: after two applies, Rollback(1) re-activates the first
// profile and the pending indicator compares against the RESTORED
// state hash.
func TestRollback(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	st := testState()
	a := New(adapter, &memStore{st}, nil, filepath.Join(dir, "apply"))

	first, _ := a.Apply()
	st.Routes = []model.Route{{
		ID: "rt_2", Name: "New", Enabled: true, Order: 20,
		Conditions: []model.RouteCondition{{Type: model.CondDomainSuffix, Value: "new.example"}},
		Target:     model.Target{Type: model.TargetReject},
	}}
	second, _ := a.Apply()
	if p, _, _ := a.Pending(); p {
		t.Fatal("setup: second apply should clear pending")
	}

	rec, err := a.Rollback(1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rec.Profile != first.Profile {
		t.Fatalf("rollback profile = %q, want %q", rec.Profile, first.Profile)
	}
	// The rollback record carries the FIRST state's hash → the state
	// (which is still the second state) is pending again.
	if p, _, _ := a.Pending(); !p {
		t.Fatal("after rollback the current state differs from restored state — pending expected")
	}
	// History grew by the rollback record (3 total, newest = rollback).
	hist := a.History()
	if len(hist) != 3 {
		t.Fatalf("history after rollback = %d records, want 3", len(hist))
	}
	if hist[0].Profile != first.Profile {
		t.Fatalf("newest history record should be the rollback, got %q", hist[0].Profile)
	}
	_ = second
}

// TestRollbackTooFar: stepping back beyond the history errors.
func TestRollbackTooFar(t *testing.T) {
	dir := t.TempDir()
	adapter := newMockAdapter(filepath.Join(dir, "profiles"))
	a := New(adapter, &memStore{testState()}, nil, filepath.Join(dir, "apply"))
	if _, err := a.Rollback(1); err == nil {
		t.Fatal("rollback with empty history must fail")
	}
	a.Apply()
	if _, err := a.Rollback(1); err == nil {
		t.Fatal("rollback deeper than history must fail")
	}
}

// TestProfileNameFormat: names are sortable timestamps with a
// per-second sequence disambiguator.
func TestProfileNameFormat(t *testing.T) {
	now := time.Date(2026, 9, 20, 15, 30, 45, 0, time.UTC)
	name := profileName(now)
	if !strings.HasPrefix(name, "wellboard-20260920-153045-") {
		t.Fatalf("profileName = %q", name)
	}
	// Consecutive names differ even within the same second.
	if name == profileName(now) {
		t.Fatalf("profileName must be unique per call, got %q twice", name)
	}
}
