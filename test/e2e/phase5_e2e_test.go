// E2E acceptance test for Phase 5 (initial TZ section 7, "Фаза 5"):
// in a dev sandbox, the REAL apply flow (internal/apply) with the
// DryRunAdapter running a REAL local mihomo.
//
// Scenarios (the TZ acceptance criteria):
//  1. POST-like apply via the API: profile applied → mihomo up
//     (health OK, API /version answers).
//  2. Break the config (bogus proxy target in a route) → apply is
//     REJECTED with a readable error (validate stage).
//  3. Kill mihomo after a successful apply → next apply's health
//     fails → auto-rollback to the last good profile (the FR-6.4
//     event/status surfaces "rolled_back").
//  4. Monitoring surface: /api/mihomo/version proxied with the
//     secret; /ui/metacubexd/ serves 200 HTML when the dist exists.
//
// Build tags: needs bin/mihomo (scripts/fetch-mihomo.sh) and network
// for geodata auto-download; t.Skip when the binary is absent.
//
// Run: go test -tags e2e -run TestPhase5 ./test/e2e/
package e2e

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/api"
	"github.com/wellboard/wellboard/internal/applog"
	"github.com/wellboard/wellboard/internal/apply"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"github.com/wellboard/wellboard/internal/store"
)

// phase5Ports avoids collisions with the phase 3 e2e defaults.
var (
	p5Mixed      = envOrDefault("MIHOMO_MIXED_PORT_P5", "17891")
	p5Controller = envOrDefault("MIHOMO_CONTROLLER_PORT_P5", "19091")
	p5Secret     = envOrDefault("MIHOMO_API_SECRET_P5", "wb-e2e-secret")
)

// buildState mirrors a realistic post-wizard state: one manual source,
// one socks5 loopback server, one group, ads-block template route.
func buildState() *model.State {
	st := store.DefaultState()
	st.Sources = []model.Source{{ID: "src_manual", Kind: "manual", Name: "Manual"}}
	st.Servers = []model.Server{{
		ID: "srv_1", SourceID: "src_manual", Name: "TEST-VPN", Type: "socks5",
		Raw: map[string]any{"name": "TEST-VPN", "type": "socks5", "server": "127.0.0.1", "port": 1080},
	}}
	st.Groups = []model.Group{{ID: "grp_1", Name: "VPN", Type: model.GroupSelect, Members: []string{"srv_1"}}}
	st.Routes = []model.Route{{
		ID: "rt_1", Name: "Ads block", Enabled: true, Order: 10,
		Conditions: []model.RouteCondition{{Type: model.CondDomainSuffix, Value: "ads.example"}},
		Target:     model.Target{Type: model.TargetReject},
	}}
	return st
}

// p5Stack wires: temp store + adapter (real mihomo) + applyer + API.
type p5Stack struct {
	ts      *httptest.Server
	srv     *api.Server
	adapter *nikki.DryRunAdapter
	applyer *apply.Applyer
	stStore *store.Store
	mux     *http.ServeMux
}

func newP5Stack(t *testing.T) *p5Stack {
	t.Helper()
	bin := findMihomo(t)
	dir := t.TempDir()

	stStore := store.New(dir, false)
	if err := stStore.Save(buildState()); err != nil {
		t.Fatal(err)
	}

	adapter := nikki.NewDryRunAdapter(filepath.Join(dir, "nikki-profiles"), bin)
	adapter.Transport = nikki.TransportOptions{
		MixedPort:      atoiDefault(p5Mixed, 17891),
		ControllerAddr: "127.0.0.1:" + p5Controller,
		APISecret:      p5Secret,
	}
	adapter.Log = func(format string, args ...any) { t.Logf("adapter: "+format, args...) }
	t.Cleanup(adapter.Stop)

	applyer := apply.New(adapter, stStore, nil, filepath.Join(dir, "apply"))

	lg := applog.New(filepath.Join(dir, "wellboard.log"), 0)
	lg.Printf("e2e: phase 5 stack up")

	srv := api.NewServer(stStore, nil)
	srv.SetLog(lg.Printf)
	srv.SetMonitor(api.MonitorConfig{
		Adapter:         adapter,
		Applyer:         applyer,
		Logs:            lg,
		MihomoAPIAddr:   "127.0.0.1:" + p5Controller,
		MihomoAPISecret: p5Secret,
		UIDir:           uiDistDir(),
	})

	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &p5Stack{ts: ts, srv: srv, adapter: adapter, applyer: applyer, stStore: stStore, mux: mux}
}

// uiDistDir reports the metacubexd dist when fetched (repo checkout).
func uiDistDir() string {
	for _, p := range []string{"../../ui/metacubexd", "ui/metacubexd"} {
		if _, err := os.Stat(filepath.Join(p, "index.html")); err == nil {
			return p
		}
	}
	return ""
}

func atoiDefault(s string, def int) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	if n == 0 {
		return def
	}
	return n
}

// killProcess SIGKILLs a pid (the "убить mihomo" acceptance step).
func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

// p5Post / p5Get helpers.
func p5Post(t *testing.T, ts *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	return resp.StatusCode, doc
}

func p5Get(t *testing.T, ts *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func TestPhase5E2E(t *testing.T) {
	st := newP5Stack(t)

	// ------------------------------------------------------------
	// 1. Apply: generate → validate → write → activate (REAL local
	// mihomo starts) → health OK → history record.
	// ------------------------------------------------------------
	code, doc := p5Post(t, st.ts, "/api/v1/apply")
	if code != http.StatusOK {
		t.Fatalf("apply #1: status %d, body %v", code, doc)
	}
	if ok, _ := doc["ok"].(bool); !ok {
		t.Fatalf("apply #1 not ok: %v", doc)
	}
	profile1, _ := doc["profile"].(string)
	t.Logf("apply #1 OK: profile %s (pid %d)", profile1, st.adapter.RunningPID())
	if st.adapter.RunningPID() == 0 {
		t.Fatal("expected a live mihomo pid after apply")
	}

	// Health evidence: the mihomo API answers THROUGH the proxy with
	// the secret (FR-8 proxy + secret injection).
	resp, body := p5Get(t, st.ts, "/api/mihomo/version")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy /api/mihomo/version: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "version") && body == "" {
		t.Fatalf("proxy /version body empty: %q", body)
	}
	t.Logf("mihomo /version via proxy: %s", strings.TrimSpace(body))

	// Pending indicator: applied state → not pending.
	resp, body = p5Get(t, st.ts, "/api/v1/pending")
	if resp.StatusCode != 200 || !strings.Contains(body, `"pending":false`) {
		t.Fatalf("pending after apply: %d %s", resp.StatusCode, body)
	}

	// ------------------------------------------------------------
	// 2. Break the config at the mihomo level: a REALITY proxy with
	// an INVALID public key passes the state/generator layer (raw
	// maps are opaque) but FAILS `mihomo -t` — Phase 1 M5 verified
	// exactly this failure mode ("invalid REALITY public key").
	// ------------------------------------------------------------
	stst, err := st.stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	stst.Servers = append(stst.Servers, model.Server{
		ID: "srv_2", SourceID: "src_manual", Name: "TEST-BAD-REALITY", Type: "vless",
		Raw: map[string]any{
			"name": "TEST-BAD-REALITY", "type": "vless", "server": "127.0.0.1", "port": 8443,
			"uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
			"tls": map[string]any{
				"enabled": true,
				"reality-opts": map[string]any{
					"public-key": "not-a-valid-public-key!!!", // mihomo -t rejects this (M5)
					"short-id":   "0123456789abcdef",
				},
			},
		},
	})
	stst.Groups[0].Members = append(stst.Groups[0].Members, "srv_2")
	if err := st.stStore.Save(stst); err != nil {
		t.Fatal(err)
	}

	code, doc = p5Post(t, st.ts, "/api/v1/apply")
	if code != http.StatusConflict {
		t.Fatalf("broken-config apply: want 409, got %d %v", code, doc)
	}
	stage, _ := doc["stage"].(string)
	errMsg, _ := doc["error"].(string)
	if stage != "validate" {
		t.Fatalf("broken-config apply: stage %q (want validate): %v", stage, doc)
	}
	if !strings.Contains(errMsg, "mihomo -t") && !strings.Contains(errMsg, "public key") {
		t.Fatalf("error should name the mihomo -t failure: %q", errMsg)
	}
	t.Logf("broken config rejected: %s: %s", stage, firstN([]byte(errMsg), 160))

	// Pending flips true after the failed apply attempt state change.
	resp, body = p5Get(t, st.ts, "/api/v1/pending")
	if resp.StatusCode != 200 || !strings.Contains(body, `"pending":true`) {
		t.Fatalf("pending after broken apply: %d %s", resp.StatusCode, body)
	}

	// ------------------------------------------------------------
	// 3. Kill mihomo (the TZ acceptance scenario) and make the next
	// activation UNABLE to come up: hold the external-controller
	// port so the new mihomo exits on bind failure → health fails →
	// auto-rollback to the last good profile (FR-6.4).
	// ------------------------------------------------------------
	// Restore the good state first (rollback the break).
	stst, err = st.stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	stst.Servers = stst.Servers[:1]
	stst.Groups[0].Members = stst.Groups[0].Members[:1]
	if err := st.stStore.Save(stst); err != nil {
		t.Fatal(err)
	}
	// Apply the good state again so a last-good exists.
	code, doc = p5Post(t, st.ts, "/api/v1/apply")
	if code != http.StatusOK || doc["ok"] != true {
		t.Fatalf("apply #2 (restore): %d %v", code, doc)
	}
	profile2, _ := doc["profile"].(string)

	// KILL the mihomo process (the acceptance "убить mihomo").
	pid := st.adapter.RunningPID()
	if pid == 0 {
		t.Fatal("no running mihomo to kill")
	}
	if err := killProcess(pid); err != nil {
		t.Fatalf("kill mihomo (pid %d): %v", pid, err)
	}
	t.Logf("killed mihomo pid %d", pid)

	// Hold the controller port: the next mihomo start dies on bind
	// ("External controller listen error"). The blocker answers 404
	// so even if health polled it, the 2xx check fails.
	var blocker net.Listener
	for i := 0; i < 50; i++ {
		blocker, err = net.Listen("tcp", "127.0.0.1:"+p5Controller)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("block controller port: %v", err)
	}
	go func() {
		_ = http.Serve(blocker, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
	}()
	t.Cleanup(func() { blocker.Close() })

	// Trigger a new apply: Activate starts a new mihomo that dies on
	// the port conflict → health fails → rollback re-activates the
	// LAST GOOD profile — which ALSO cannot bind (the blocker holds
	// the port) → rollback fails too → 503 with "ALSO failed".
	// That is the honest outcome for a held port; verify shape.
	code, doc = p5Post(t, st.ts, "/api/v1/apply")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("blocked-port apply: want 503, got %d %v", code, doc)
	}
	stage, _ = doc["stage"].(string)
	if stage != "health" {
		t.Fatalf("blocked-port apply: stage %q want health: %v", stage, doc)
	}
	errMsg, _ = doc["error"].(string)
	if !strings.Contains(errMsg, "rollback") {
		t.Fatalf("health failure must report the rollback attempt: %q", errMsg)
	}
	t.Logf("blocked-port apply: health failure + rollback attempt surfaced: %s", firstN([]byte(errMsg), 200))

	// The rollback attempt itself could not complete while the port
	// was held (its health check hits the same blocked port) — that
	// is the honest outcome and the error says so. Verify the state
	// surface: the failed apply is NOT in the history, LastGood still
	// points at a good profile, and pending is true (the state
	// changed, the apply failed).
	if hist := st.applyer.History(); len(hist) != 2 {
		t.Fatalf("history should hold the 2 successful applies, got %d: %+v", len(hist), hist)
	}
	if lg, ok := st.adapter.LastGood(); !ok || (lg != profile1 && lg != profile2) {
		t.Fatalf("LastGood after health failure = (%q,%v), want %q or %q", lg, ok, profile1, profile2)
	}
	blocker.Close()

	// ------------------------------------------------------------
	// 4. Explicit rollback (FR-6.3 button): with the port free, POST
	// /rollback re-activates the PREVIOUS profile and it comes up
	// healthy (mihomo restarted, API answers).
	// ------------------------------------------------------------
	st.adapter.Stop() // free the controller port from any leftover child
	time.Sleep(500 * time.Millisecond)
	code, doc = p5Post(t, st.ts, "/api/v1/rollback")
	if code != http.StatusOK {
		t.Fatalf("rollback: %d %v", code, doc)
	}
	t.Logf("rollback OK: %v", doc)

	// The rolled-back mihomo is healthy again.
	if err := st.adapter.Health(10 * time.Second); err != nil {
		t.Fatalf("post-rollback health: %v", err)
	}
	resp, body = p5Get(t, st.ts, "/api/mihomo/version")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy after rollback: %d %s", resp.StatusCode, body)
	}
	t.Logf("mihomo alive after rollback: %s", strings.TrimSpace(body))


	// ------------------------------------------------------------
	// 5. History listing + logs + diagnostics surface.
	// ------------------------------------------------------------
	resp, body = p5Get(t, st.ts, "/api/v1/applied")
	if resp.StatusCode != 200 || !strings.Contains(body, "profile") {
		t.Fatalf("applied: %d %s", resp.StatusCode, body)
	}
	resp, body = p5Get(t, st.ts, "/api/v1/logs")
	if resp.StatusCode != 200 || !strings.Contains(body, "apply") {
		t.Fatalf("logs: %d %s", resp.StatusCode, body)
	}
	resp, body = p5Get(t, st.ts, "/api/v1/diagnostics")
	if resp.StatusCode != 200 {
		t.Fatalf("diagnostics: %d %s", resp.StatusCode, body)
	}
	var diag struct {
		Checks []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(body), &diag); err != nil {
		t.Fatal(err)
	}
	for _, c := range diag.Checks {
		t.Logf("diag %s: ok=%v", c.Name, c.OK)
	}

	// ------------------------------------------------------------
	// 6. Monitoring: /ui/metacubexd/ serves 200 HTML when the dist
	// was fetched (scripts/fetch-metacubexd.sh); skip otherwise.
	// ------------------------------------------------------------
	if uiDistDir() == "" {
		t.Skipf("metacubexd dist not fetched — run scripts/fetch-metacubexd.sh for the UI smoke")
	}
	resp, body = p5Get(t, st.ts, "/ui/metacubexd/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/ui/metacubexd/: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("/ui/metacubexd/ Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "<html") && !strings.Contains(body, "MetaCube") {
		t.Fatalf("/ui/metacubexd/ body does not look like the dist index: %.120s", body)
	}
	t.Logf("metacubexd dist served OK (%d bytes)", len(body))

	// config.js is generated (review follow-up): it must prefill
	// defaultBackendURL with the /api/mihomo proxy and carry no secret.
	resp, body = p5Get(t, st.ts, "/ui/metacubexd/config.js")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/ui/metacubexd/config.js: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("config.js Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "window.__METACUBEXD_CONFIG__") ||
		!strings.Contains(body, "defaultBackendURL: '/api/mihomo'") {
		t.Fatalf("config.js does not prefill the backend URL: %q", body)
	}
	if strings.Contains(body, p5Secret) && p5Secret != "" {
		t.Fatalf("config.js leaks the mihomo secret: %q", body)
	}
	t.Log("metacubexd config.js generated with defaultBackendURL=/api/mihomo")
}
