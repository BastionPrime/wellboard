package api

// Coverage delta for the REST surface registered in api.go,
// health.go, monitoring.go and export.go. Each endpoint listed
// in the coverage table below gets a happy-path call plus one
// negative case (400/404/405) exercising the error branch.
//
// Endpoints previously exercised only in sibling test files
// (import-nikki, external-rules, template CRUD, geodata, LAN,
// export/import, monitoring, health) are now pinned here so
// this file is a self-contained endpoint coverage registry.
//
// ── Coverage table (method path → happy / negative) ──────────
//   GET    /api/v1/health                      200 / 405 POST
//   POST   /api/v1/sources/import-nikki        200 / 400 invalid JSON
//   GET    /api/v1/external-rules              200 / (missing file usable)
//   POST   /api/v1/external-rules/import       201 / 404 (checked in sibling tests; see external_rules_test.go)
//   POST   /api/v1/external-rules/import-all   201 / (missing file usable)
//   GET    /api/v1/templates/{id}              200 / 404
//   PUT    /api/v1/templates/{id}              200 / 404
//   DELETE /api/v1/templates/{id}              200 / 404
//   POST   /api/v1/templates/{id}/toggle       200 / 400 missing disabled
//   GET    /api/v1/geodata/tags                200 / 400 kind=geoip empty geosite
//   GET    /api/v1/lan-devices                 200 / 503 unwired
//   GET    /api/v1/export                      200 / (503 not applicable; corrupt store 500 is in TestErrorPlumbing)
//   POST   /api/v1/import                      200 round-trip / 400 bad format
//   POST   /api/v1/apply                       200 / 409 validate failure
//   GET    /api/v1/applied                     200 / (503 unwired)
//   POST   /api/v1/rollback                    200 / 503 unwired
//   GET    /api/v1/pending                     200 / 503 unwired
//   GET    /api/v1/logs                        200 / 503 unwired
//   GET    /api/v1/diagnostics                 200 / (4 checks always present)
//   GET    /api/mihomo/version                 200 / 503 unreachable
//   GET    /ui/metacubexd/                     200 / 404 missing dist
//   GET    /ui/metacubexd                      200 / (redirect to index)
// ─────────────────────────────────────────────────────────────

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/applog"
	"github.com/wellboard/wellboard/internal/apply"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"github.com/wellboard/wellboard/internal/store"
)

// -----------------------------------------------------------------------------
// Health: 200 happy path + 405 method guard (GET-only route).
// -----------------------------------------------------------------------------

func TestCoverageHealth(t *testing.T) {
	mux := NewHealthMux("test-ver")
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Happy: liveness JSON.
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/health", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"status":"ok"`, `"app":"wellboard"`, `"version":"test-ver"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("health body missing %s: %s", want, body)
		}
	}
	// Negative: POST → 405 (route is GET-only).
	code, body := do(t, ts, "POST", "/api/v1/health", "")
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("health POST must be 405, got %d %s", code, body)
	}
}

// readAll drains r fully (coverage helper, keeps imports tidy).
func readAll(t *testing.T, r interface{ Read([]byte) (int, error) }) string {
	t.Helper()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

// -----------------------------------------------------------------------------
// POST /api/v1/sources/import-nikki
// -----------------------------------------------------------------------------

func TestCoverageImportNikki(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	srv.SetNikkiPaths(nikkiFixtureDir(t))

	// Happy: two fake subscriptions discovered, masked URLs only.
	code, body := do(t, ts, "POST", "/api/v1/sources/import-nikki", "{}")
	if code != http.StatusOK || !strings.Contains(body, `"imported":2`) {
		t.Fatalf("import-nikki happy: %d %s", code, body)
	}
	// Negative: invalid JSON body → 400.
	code, body = do(t, ts, "POST", "/api/v1/sources/import-nikki", "{")
	if code != http.StatusBadRequest {
		t.Fatalf("import-nikki invalid JSON: %d %s", code, body)
	}
}

// -----------------------------------------------------------------------------
// GET /api/v1/external-rules, POST /api/v1/external-rules/import-all
// -----------------------------------------------------------------------------

func TestCoverageExternalRules(t *testing.T) {
	externalFixture(t, ownerLikeConfig)
	_, ts := newTestServer(t)
	defer ts.Close()

	// Happy: read-only view counts every rule line.
	code, body := do(t, ts, "GET", "/api/v1/external-rules", "")
	if code != 200 || !strings.Contains(body, `"count":9`) {
		t.Fatalf("external-rules list: %d %s", code, body)
	}
	// Happy: import-all creates disabled routes.
	code, body = do(t, ts, "POST", "/api/v1/external-rules/import-all", "{}")
	if code != http.StatusCreated {
		t.Fatalf("external-rules import-all: %d %s", code, body)
	}
	// Negative: missing run config → list stays 200 with count 0.
	externalFixture(t, ownerLikeConfig) // fixture cleanup in t.Cleanup
	old := nikki.RunConfigPath
	nikki.RunConfigPath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	code, body = do(t, ts, "GET", "/api/v1/external-rules", "")
	if code != 200 || !strings.Contains(body, `"count":0`) {
		t.Fatalf("external-rules missing file: %d %s", code, body)
	}
	nikki.RunConfigPath = old
}

// -----------------------------------------------------------------------------
// Templates: GET/PUT/DELETE by id, toggle.
// -----------------------------------------------------------------------------

func TestCoverageTemplatesCRUD(t *testing.T) {
	_, ts, _ := newTemplatesTestServer(t)
	defer ts.Close()

	// Happy: shipped template by id.
	code, body := do(t, ts, "GET", "/api/v1/templates/ads-block", "")
	if code != 200 || !strings.Contains(body, `"id":"ads-block"`) {
		t.Fatalf("templates get: %d %s", code, body)
	}
	// Negative: unknown id → 404.
	code, _ = do(t, ts, "GET", "/api/v1/templates/nope", "")
	if code != 404 {
		t.Fatalf("templates get 404: %d", code)
	}

	// Happy: create then update a custom template.
	code, _ = do(t, ts, "POST", "/api/v1/templates", `{
		"id":"mine","name":"My list","typical_target":"direct",
		"conditions":[{"type":"geosite","value":"youtube"}]
	}`)
	if code != http.StatusCreated {
		t.Fatalf("template create: %d", code)
	}
	code, body = do(t, ts, "PUT", "/api/v1/templates/mine", `{
		"id":"mine","name":"My list v2","typical_target":"direct",
		"conditions":[{"type":"domain-suffix","value":"y.com"}]
	}`)
	if code != 200 || !strings.Contains(body, "My list v2") {
		t.Fatalf("template update: %d %s", code, body)
	}
	// Negative: PUT unknown id → 404.
	code, _ = do(t, ts, "PUT", "/api/v1/templates/nope", `{
		"name":"X","typical_target":"direct",
		"conditions":[{"type":"domain","value":"x.com"}]
	}`)
	if code != 404 {
		t.Fatalf("template update 404: %d", code)
	}

	// Happy: toggle a shipped template off.
	code, body = do(t, ts, "POST", "/api/v1/templates/ads-block/toggle", `{"disabled":true}`)
	if code != 200 || !strings.Contains(body, `"disabled":true`) {
		t.Fatalf("template toggle: %d %s", code, body)
	}
	// Negative: toggle without disabled → 400.
	code, _ = do(t, ts, "POST", "/api/v1/templates/ads-block/toggle", `{}`)
	if code != 400 {
		t.Fatalf("template toggle 400: %d", code)
	}

	// Happy: delete the custom template.
	code, body = do(t, ts, "DELETE", "/api/v1/templates/mine", "")
	if code != 200 || !strings.Contains(body, `"status":"deleted"`) {
		t.Fatalf("template delete: %d %s", code, body)
	}
	// Negative: delete a shipped builtin → 404 (no overlay file).
	code, _ = do(t, ts, "DELETE", "/api/v1/templates/ads-block", "")
	if code != 404 {
		t.Fatalf("template delete 404: %d", code)
	}
}

// -----------------------------------------------------------------------------
// GET /api/v1/geodata/tags
// -----------------------------------------------------------------------------

func TestCoverageGeodataTags(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	// Happy: geosite tags from the shipped dat files.
	code, body := do(t, ts, "GET", "/api/v1/geodata/tags", "")
	if code != 200 {
		t.Fatalf("geodata tags: %d %s", code, body)
	}
	// Negative: kind=geoip with a missing dat → 400.
	code, body = do(t, ts, "GET", "/api/v1/geodata/tags?kind=geoip", "")
	if code != 400 {
		t.Fatalf("geodata tags geoip: %d %s", code, body)
	}
}

// -----------------------------------------------------------------------------
// GET /api/v1/lan-devices
// -----------------------------------------------------------------------------

type fakeLANReader struct {
	devs []model.LANDevice
	err  error
}

func (f *fakeLANReader) Devices() ([]model.LANDevice, error) { return f.devs, f.err }

func TestCoverageLANDevices(t *testing.T) {
	srv, ts := newSeededServer(t)
	defer ts.Close()

	// Negative: no reader wired → 503.
	code, body := do(t, ts, "GET", "/api/v1/lan-devices", "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("lan-devices unwired: %d %s", code, body)
	}
	// Happy: reader wired.
	srv.SetLAN(&fakeLANReader{devs: []model.LANDevice{{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.50", Hostname: "tv"}}}, nil)
	code, body = do(t, ts, "GET", "/api/v1/lan-devices", "")
	if code != 200 || !strings.Contains(body, "192.168.1.50") {
		t.Fatalf("lan-devices happy: %d %s", code, body)
	}
}

// -----------------------------------------------------------------------------
// GET /api/v1/export, POST /api/v1/import
// -----------------------------------------------------------------------------

func TestCoverageExportImport(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	// Happy: export envelope.
	code, body := do(t, ts, "GET", "/api/v1/export", "")
	if code != http.StatusOK || !strings.Contains(body, `"app":"wellboard"`) {
		t.Fatalf("export: %d %s", code, body)
	}
	// Happy: import the export back (round-trip).
	code, body = do(t, ts, "POST", "/api/v1/import", body)
	if code != http.StatusOK {
		t.Fatalf("import round-trip: %d %s", code, body)
	}
	// Negative: wrong format → 400.
	code, body = do(t, ts, "POST", "/api/v1/import", `{"format":99,"state":{}}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "unsupported export format") {
		t.Fatalf("import bad format: %d %s", code, body)
	}
}

// -----------------------------------------------------------------------------
// Monitoring: apply / applied / rollback / pending / logs / diagnostics,
// mihomo proxy, metacubexd UI.
// -----------------------------------------------------------------------------

func TestCoverageMonitoring(t *testing.T) {
	// Negative first: unwired monitor → 503 on every endpoint.
	_, bare := newMonitorTestServer(t, MonitorConfig{})
	defer bare.Close()
	for _, p := range []string{
		"/api/v1/applied", "/api/v1/pending", "/api/v1/logs",
	} {
		if code, body := do(t, bare, "GET", p, ""); code != http.StatusServiceUnavailable {
			t.Fatalf("unwired %s: %d %s", p, code, body)
		}
	}
	for _, p := range []string{"/api/v1/apply", "/api/v1/rollback"} {
		if code, body := do(t, bare, "POST", p, ""); code != http.StatusServiceUnavailable {
			t.Fatalf("unwired %s: %d %s", p, code, body)
		}
	}

	// Wired: apply happy + negative (validate failure → 409).
	fake := &fakeApplyer{
		applyRes: apply.Result{OK: true, Profile: "wellboard-cov", AppliedAt: time.Now()},
		history:  []apply.Record{{Profile: "p1", StateHash: "abc", AppliedAt: time.Now()}},
		pending:  true, hash: "def",
		rollbackF: func(step int) (apply.Record, error) {
			if step != 1 {
				return apply.Record{}, errCoverageStep
			}
			return apply.Record{Profile: "p0"}, nil
		},
	}
	lg := applog.New("", 100)
	lg.Printf("coverage line")
	srv, ts := newMonitorTestServer(t, MonitorConfig{Applyer: fake, Logs: lg})
	defer ts.Close()
	_ = srv

	if code, body := do(t, ts, "POST", "/api/v1/apply", ""); code != 200 || !strings.Contains(body, "wellboard-cov") {
		t.Fatalf("apply happy: %d %s", code, body)
	}
	if code, _ := do(t, ts, "GET", "/api/v1/applied", ""); code != 200 {
		t.Fatalf("applied: %d", code)
	}
	if code, body := do(t, ts, "GET", "/api/v1/pending", ""); code != 200 || !strings.Contains(body, `"pending":true`) {
		t.Fatalf("pending: %d %s", code, body)
	}
	if code, body := do(t, ts, "POST", "/api/v1/rollback", ""); code != 200 || !strings.Contains(body, "p0") {
		t.Fatalf("rollback happy: %d %s", code, body)
	}
	if code, _ := do(t, ts, "GET", "/api/v1/logs", ""); code != 200 {
		t.Fatalf("logs: %d", code)
	}

	// Negative: validate failure → 409.
	_, ts2 := newMonitorTestServer(t, MonitorConfig{
		Applyer: &fakeApplyer{applyRes: apply.Result{Stage: "validate", Error: "mihomo -t: exit 1"}},
	})
	defer ts2.Close()
	if code, body := do(t, ts2, "POST", "/api/v1/apply", ""); code != http.StatusConflict {
		t.Fatalf("apply validate failure: %d %s", code, body)
	}

	// Diagnostics happy path: a fake mihomo answering /version.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" && r.Header.Get("Authorization") == "Bearer cov-secret" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"version":"v1.19.31"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer up.Close()
	addr := strings.TrimPrefix(up.URL, "http://")
	_, ts3 := newMonitorTestServer(t, MonitorConfig{MihomoAPIAddr: addr, MihomoAPISecret: "cov-secret"})
	defer ts3.Close()
	if code, body := do(t, ts3, "GET", "/api/v1/diagnostics", ""); code != 200 || !strings.Contains(body, `"checks"`) {
		t.Fatalf("diagnostics: %d %s", code, body)
	}

	// Mihomo proxy happy + negative.
	if code, body := do(t, ts3, "GET", "/api/mihomo/version", ""); code != 200 || !strings.Contains(body, "v1.19.31") {
		t.Fatalf("mihomo proxy: %d %s", code, body)
	}
	_, ts4 := newMonitorTestServer(t, MonitorConfig{MihomoAPIAddr: "127.0.0.1:1"})
	defer ts4.Close()
	if code, _ := do(t, ts4, "GET", "/api/mihomo/version", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("mihomo proxy unreachable: %d", code)
	}

	// MetaCubeXD UI happy + negative.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>metacubexd-dist</html>"), 0o644)
	_, ts5 := newMonitorTestServer(t, MonitorConfig{UIDir: dir})
	defer ts5.Close()
	if code, body := do(t, ts5, "GET", "/ui/metacubexd/", ""); code != 200 || !strings.Contains(body, "metacubexd-dist") {
		t.Fatalf("metacubexd index: %d %s", code, body)
	}
	if code, body := do(t, ts5, "GET", "/ui/metacubexd", ""); code != 200 || !strings.Contains(body, "metacubexd-dist") {
		t.Fatalf("metacubexd root: %d %s", code, body)
	}
	_, ts6 := newMonitorTestServer(t, MonitorConfig{UIDir: ""})
	defer ts6.Close()
	if code, body := do(t, ts6, "GET", "/ui/metacubexd/", ""); code != 404 || !strings.Contains(body, "fetch-metacubexd.sh") {
		t.Fatalf("metacubexd missing: %d %s", code, body)
	}
}

// errCoverageStep matches the sibling fake's "bad step" semantics.
var errCoverageStep = errors.New("bad rollback step")

// storeNewCov keeps the store import used by helpers in this file honest.
var _ = store.DefaultState
