package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/apply"
	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"github.com/wellboard/wellboard/internal/store"
	"github.com/wellboard/wellboard/internal/templates"
)

// applyEdgeServer builds the standard templates test server (real
// catalog, overlay dir) and returns it with the underlying store, so
// apply-edge tests can inspect the persisted state directly.
func applyEdgeServer(t *testing.T) (*Server, *httptest.Server, *store.Store) {
	t.Helper()
	srv, ts, _ := newTemplatesTestServer(t)
	stStore, ok := srv.Store.(*store.Store)
	if !ok {
		t.Fatalf("server store is %T, want *store.Store", srv.Store)
	}
	return srv, ts, stStore
}

// mockAdapter is a scripted nikki.Adapter: profiles are written to a
// temp dir, validate always passes, activate/health are programmable.
type mockAdapter struct {
	mu       sync.Mutex
	dir      string
	written  map[string][]byte
	activate map[string]error // profile name -> error
	health   error            // global health result (nil = healthy)
	// healthOneShot, when set, makes the NEXT Health call return
	// health (if non-nil) and every following call succeed — modelling
	// a service that fails to come up on the new profile but is
	// healthy again when the rollback re-activates the previous good
	// profile (FR-6.3/6.4 auto-rollback scenario).
	healthOneShot bool
	lastGood      string
	goodSet       bool
}

func newMockAdapter(t *testing.T) *mockAdapter {
	t.Helper()
	return &mockAdapter{dir: t.TempDir(), written: map[string][]byte{}}
}

func (m *mockAdapter) Detect() (nikki.Info, error) {
	return nikki.Info{}, nil
}

func (m *mockAdapter) WriteProfile(name string, yamlData []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.written[name] = yamlData
	return filepath.Join(m.dir, name+".yaml"), nil
}

func (m *mockAdapter) Validate(path string) error { return nil }

func (m *mockAdapter) Activate(name string) error {
	m.mu.Lock()
	err := m.activate[name]
	m.mu.Unlock()
	return err
}

func (m *mockAdapter) Health(timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.healthOneShot {
		err := m.health
		m.health = nil
		return err
	}
	return m.health
}
func (m *mockAdapter) LastGood() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastGood, m.goodSet
}

func (m *mockAdapter) MarkHealthy(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastGood, m.goodSet = name, true
}

// TestTemplateApplyOnEmptyState: applying a condition template to a
// virgin DefaultState creates exactly one enabled route carrying the
// template's conditions/providers, and the generated profile contains
// both the GEOSITE rule and the RULE-SET line plus the rule-providers
// section pointing at the shipped local payload (FR-5.1/FR-5.2).
func TestTemplateApplyOnEmptyState(t *testing.T) {
	_, ts, stStore := applyEdgeServer(t)
	defer ts.Close()

	// 1. Apply ads-block to the empty state.
	code, body := do(t, ts, "POST", "/api/v1/templates/ads-block/apply", `{"target":{"type":"direct"}}`)
	if code != http.StatusCreated {
		t.Fatalf("apply on empty state: %d %s", code, body)
	}
	var rt model.Route
	if err := json.Unmarshal([]byte(body), &rt); err != nil {
		t.Fatalf("apply response body: %v", err)
	}
	if !rt.Enabled {
		t.Fatalf("applied route must be enabled, got %+v", rt)
	}
	if rt.OnUnavailable != "block" {
		t.Fatalf("applied route OnUnavailable must be \"block\" (template default), got %q", rt.OnUnavailable)
	}
	if rt.Order != 10 {
		t.Fatalf("first route order must be 10, got %d", rt.Order)
	}
	if len(rt.Conditions) != 1 || rt.Conditions[0].Type != model.CondGeosite || rt.Conditions[0].Value != "CATEGORY-ADS-ALL" {
		t.Fatalf("route conditions must mirror the template, got %+v", rt.Conditions)
	}
	if len(rt.Providers) != 1 || rt.Providers[0] != "ads" {
		t.Fatalf("route providers must mirror the template, got %v", rt.Providers)
	}

	// 2. Exactly one route in the persisted state, with a unique ID.
	st, err := stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Routes) != 1 {
		t.Fatalf("state must hold exactly 1 route after apply, got %d", len(st.Routes))
	}
	if st.Routes[0].ID != rt.ID {
		t.Fatalf("persisted route id %q != response id %q", st.Routes[0].ID, rt.ID)
	}

	// 3. Profile generation is clean and contains the expected rules.
	out, err := generator.GenerateWithProviders(st, nil)
	if err != nil {
		t.Fatalf("generation after apply must be clean: %v", err)
	}
	prof := string(out)
	svc := "rt:" + rt.ID
	for _, want := range []string{
		"GEOSITE,CATEGORY-ADS-ALL," + svc,
		"RULE-SET,ads," + svc,
	} {
		if !strings.Contains(prof, want) {
			t.Fatalf("profile must contain rule %q", want)
		}
	}
	if !strings.Contains(prof, "rule-providers:") {
		t.Fatalf("profile must emit a rule-providers section")
	}
	if !strings.Contains(prof, "path: ./providers/ads.yaml") {
		t.Fatalf("rule-provider ads must point at the local payload path")
	}
}

// TestTemplateApplyConflictDuplicateConditions: a route with the same
// condition as the template plus a second apply of the same template
// are both accepted at the CRUD level (each apply is an independent
// route, FR-5.2), but the generator then reports the second route's
// rule-provider as already used — the conflict surfaces as
// *generator.Problems instead of a silently broken profile.
func TestTemplateApplyConflictDuplicateConditions(t *testing.T) {
	_, ts, stStore := applyEdgeServer(t)
	defer ts.Close()

	// 1. First apply — accepted.
	code, body := do(t, ts, "POST", "/api/v1/templates/ads-block/apply", `{"target":{"type":"direct"}}`)
	if code != http.StatusCreated {
		t.Fatalf("first apply: %d %s", code, body)
	}

	// 2. Second apply of the same template — also accepted at CRUD
	// level (independent route, FR-5.2).
	code, body = do(t, ts, "POST", "/api/v1/templates/ads-block/apply", `{"target":{"type":"reject"}}`)
	if code != http.StatusCreated {
		t.Fatalf("second apply must be accepted at CRUD level: %d %s", code, body)
	}

	st, err := stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Routes) != 2 {
		t.Fatalf("two applies must create two routes, got %d", len(st.Routes))
	}

	// 3. Generation now fails with Problems: the `ads` provider is
	// claimed by the earlier route. The profile preview endpoint maps
	// this to 409 — not a silently broken profile.
	code, body = do(t, ts, "GET", "/api/v1/profile", "")
	if code != http.StatusConflict {
		t.Fatalf("duplicate provider must surface as 409, got %d %s", code, body)
	}
	if !strings.Contains(body, "already used by an earlier route") {
		t.Fatalf("problem body must name the duplicate provider, got %s", body)
	}

	// 4. Same verdict straight from the generator (type pin).
	_, err = generator.GenerateWithProviders(st, nil)
	var prob *generator.Problems
	if err == nil {
		t.Fatal("generation must fail on duplicate provider")
	}
	if !errorsAs(err, &prob) {
		t.Fatalf("error must be *generator.Problems, got %T", err)
	}
	if !strings.Contains(err.Error(), "already used by an earlier route") {
		t.Fatalf("problem must name the duplicate provider, got %v", err)
	}
}

// TestTemplateApplyIdempotence pins the documented idempotence
// boundary: a repeated apply of a CONDITION template creates a NEW
// route with a unique ID/order (providers are not shared between
// routes), while the no-condition all-vpn template is strictly
// idempotent — a repeat only rewrites the default policy and the
// route list never grows.
func TestTemplateApplyIdempotence(t *testing.T) {
	_, ts, stStore := applyEdgeServer(t)
	defer ts.Close()

	// --- Condition template (ads-block): each apply = a new route.
	var ids []string
	for i := 0; i < 2; i++ {
		code, body := do(t, ts, "POST", "/api/v1/templates/ads-block/apply", `{"target":{"type":"direct"}}`)
		if code != http.StatusCreated {
			t.Fatalf("apply #%d: %d %s", i+1, code, body)
		}
		var rt model.Route
		if err := json.Unmarshal([]byte(body), &rt); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rt.ID)
	}
	if ids[0] == ids[1] {
		t.Fatalf("repeated apply must mint a unique route id, got %q twice", ids[0])
	}
	st, err := stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Routes) != 2 {
		t.Fatalf("two applies of a condition template must yield 2 routes, got %d", len(st.Routes))
	}
	if st.Routes[0].Order == st.Routes[1].Order {
		t.Fatalf("orders must differ, both %d", st.Routes[0].Order)
	}

	// --- No-condition template (all-vpn): strictly idempotent.
	for i := 0; i < 2; i++ {
		code, body := do(t, ts, "POST", "/api/v1/templates/all-vpn/apply", `{"target":{"type":"reject"}}`)
		if code != http.StatusOK {
			t.Fatalf("all-vpn apply #%d must be 200 (default-policy mode), got %d %s", i+1, code, body)
		}
		if !strings.Contains(body, `"mode":"default-policy"`) {
			t.Fatalf("all-vpn apply body must report default-policy mode: %s", body)
		}
	}
	st, err = stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Routes) != 2 {
		t.Fatalf("all-vpn applies must not add routes, still %d routes", len(st.Routes))
	}
	if st.Settings.DefaultPolicy.Type != model.TargetReject {
		t.Fatalf("default policy must be rewritten to reject, got %+v", st.Settings.DefaultPolicy)
	}
}

// applyerFixture wires a real *apply.Applyer (real generator, mock
// adapter) into the API monitoring endpoints.
func applyerFixture(t *testing.T, mock *mockAdapter) (*Server, *httptest.Server, *store.Store, *apply.Applyer) {
	t.Helper()
	srv, ts, stStore := applyEdgeServer(t)
	catalog, err := templates.Load("../../templates")
	if err != nil {
		t.Fatalf("shipped catalog: %v", err)
	}
	known := map[string]bool{}
	for _, tpl := range catalog.Templates {
		for _, p := range tpl.Providers {
			known[p] = true
		}
	}
	dir := t.TempDir()
	ap := apply.New(mock, stStore, known, dir)
	srv.SetMonitor(MonitorConfig{Applyer: ap})
	return srv, ts, stStore, ap
}

// TestTemplateApplyRollbackAfterFailedApply: apply a template, apply
// again with the second profile failing at the health stage — the API
// answers 503 with rolled_back=true, the last good profile is
// reactivated, and the state edit that caused the failed apply
// survives the rollback (still visible, still pending).
func TestTemplateApplyRollbackAfterFailedApply(t *testing.T) {
	mock := newMockAdapter(t)
	_, ts, stStore, ap := applyerFixture(t, mock)
	defer ts.Close()

	// 1. Apply a template — success.
	code, body := do(t, ts, "POST", "/api/v1/templates/ads-block/apply", `{"target":{"type":"direct"}}`)
	if code != http.StatusCreated {
		t.Fatalf("template apply: %d %s", code, body)
	}

	// 2. First profile apply — success, becomes last-good.
	code, body = do(t, ts, "POST", "/api/v1/apply", "")
	if code != http.StatusOK {
		t.Fatalf("first apply: %d %s", code, body)
	}
	first, ok := ap.LastApplied()
	if !ok {
		t.Fatal("first apply must be recorded in history")
	}

	// 3. Second state change + apply whose profile fails at the health
	// stage: the adapter reports the service unhealthy for this apply
	// only (one-shot) — when the flow auto-rolls back and re-activates
	// the last good profile, health passes again.
	mock.healthOneShot = true
	mock.health = fmt.Errorf("service did not become healthy")
	st, err := stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	st.Routes[0].Name = "renamed-after-apply"
	if err := stStore.Save(st); err != nil {
		t.Fatal(err)
	}
	code, body = do(t, ts, "POST", "/api/v1/apply", "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("failed health apply must be 503, got %d %s", code, body)
	}
	if !strings.Contains(body, `"rolled_back":true`) {
		t.Fatalf("body must report rolled_back=true: %s", body)
	}
	if !strings.Contains(body, first.Profile) {
		t.Fatalf("body must name the restored profile %q: %s", first.Profile, body)
	}

	// 4. The state edit survives the rollback: the route rename is
	// still there and the pending indicator is on.
	st2, err := stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st2.Routes[0].Name != "renamed-after-apply" {
		t.Fatalf("state edit must survive rollback, route name %q", st2.Routes[0].Name)
	}
	pending, _, err := ap.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("state must be pending after the post-apply edit")
	}

	// 5. Manual rollback with only ONE applied profile in history is
	// refused: nothing earlier to step back to.
	code, body = do(t, ts, "POST", "/api/v1/rollback", "")
	if code != http.StatusConflict {
		t.Fatalf("manual rollback with a single history entry must 409, got %d %s", code, body)
	}
	if !strings.Contains(body, "only 1 applied profiles") {
		t.Fatalf("error must explain the single-entry history, got %s", body)
	}
}

// TestTemplateApplyRollbackNoHistory: the FIRST apply ever fails at
// the health stage — there is no previous good profile, so no
// auto-rollback happens (the body says so) and a manual rollback is
// refused with 409.
func TestTemplateApplyRollbackNoHistory(t *testing.T) {
	mock := newMockAdapter(t)
	mock.health = fmt.Errorf("service dead from the start")
	_, ts, _, _ := applyerFixture(t, mock)
	defer ts.Close()

	// 1. First apply ever fails at health.
	code, body := do(t, ts, "POST", "/api/v1/apply", "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("first failed apply must be 503, got %d %s", code, body)
	}
	if strings.Contains(body, `"rolled_back":true`) {
		t.Fatalf("no rollback must happen without history: %s", body)
	}
	if !strings.Contains(body, "no previous good profile") {
		t.Fatalf("body must explain the missing rollback target: %s", body)
	}

	// 2. Manual rollback without history — 409.
	code, body = do(t, ts, "POST", "/api/v1/rollback", "")
	if code != http.StatusConflict {
		t.Fatalf("manual rollback without history must 409, got %d %s", code, body)
	}
	if !strings.Contains(body, "no applied profiles") {
		t.Fatalf("error must name the empty history, got %s", body)
	}
}

// errorsAs is a tiny wrapper so the test file does not import errors
// for a single call site.
func errorsAs(err error, target **generator.Problems) bool {
	if p, ok := err.(*generator.Problems); ok {
		*target = p
		return true
	}
	return false
}
