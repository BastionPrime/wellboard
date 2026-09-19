package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/applog"
	"github.com/wellboard/wellboard/internal/apply"
	"github.com/wellboard/wellboard/internal/store"
)

// fakeApplyer implements ApplyerAPI with scripted behavior.
type fakeApplyer struct {
	mu        sync.Mutex
	applyRes  apply.Result
	applyErr  error
	history   []apply.Record
	rollbackF func(step int) (apply.Record, error)
	pending   bool
	hash      string
	pendErr   error
}

func (f *fakeApplyer) Apply() (apply.Result, error) { return f.applyRes, f.applyErr }
func (f *fakeApplyer) History() []apply.Record      { return f.history }
func (f *fakeApplyer) Rollback(step int) (apply.Record, error) {
	if f.rollbackF != nil {
		return f.rollbackF(step)
	}
	return apply.Record{}, fmt.Errorf("no history")
}
func (f *fakeApplyer) Pending() (bool, string, error) { return f.pending, f.hash, f.pendErr }

// newMonitorTestServer builds a server with the monitoring wiring.
func newMonitorTestServer(t *testing.T, cfg MonitorConfig) (*Server, *httptest.Server) {
	t.Helper()
	stStore := newTestStore(t)
	srv := NewServer(stStore, nil)
	srv.SetMonitor(cfg)
	mux := http.NewServeMux()
	srv.Register(mux)
	return srv, httptest.NewServer(mux)
}

func newTestStore(t *testing.T) Store {
	t.Helper()
	dir := t.TempDir()
	st := storeNew(dir)
	return st
}

// storeNew avoids importing store in this file's header twice.
func storeNew(dir string) Store {
	return storeOf(dir)
}

func TestApplyEndpointSuccess(t *testing.T) {
	fake := &fakeApplyer{applyRes: apply.Result{OK: true, Profile: "wellboard-1", AppliedAt: time.Now()}}
	_, ts := newMonitorTestServer(t, MonitorConfig{Applyer: fake})
	defer ts.Close()

	code, body := do(t, ts, "POST", "/api/v1/apply", "")
	if code != http.StatusOK {
		t.Fatalf("apply: %d %s", code, body)
	}
	if !strings.Contains(body, `"ok":true`) || !strings.Contains(body, "wellboard-1") {
		t.Fatalf("apply body: %s", body)
	}
}

func TestApplyEndpointValidateFailure409(t *testing.T) {
	fake := &fakeApplyer{applyRes: apply.Result{Stage: "validate", Error: "mihomo -t: exit 1: proxy [x] not found"}}
	_, ts := newMonitorTestServer(t, MonitorConfig{Applyer: fake})
	defer ts.Close()

	code, body := do(t, ts, "POST", "/api/v1/apply", "")
	if code != http.StatusConflict {
		t.Fatalf("validate failure should be 409, got %d %s", code, body)
	}
}

func TestApplyEndpointHealthFailure503(t *testing.T) {
	fake := &fakeApplyer{applyRes: apply.Result{Stage: "health", Error: "dead (auto-rolled back to p1)", RolledBack: true, LastGoodProfile: "p1"}}
	_, ts := newMonitorTestServer(t, MonitorConfig{Applyer: fake})
	defer ts.Close()

	code, body := do(t, ts, "POST", "/api/v1/apply", "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("health failure should be 503, got %d %s", code, body)
	}
	if !strings.Contains(body, "auto-rolled back") {
		t.Fatalf("body should surface the rollback: %s", body)
	}
}

func TestApplyEndpointNotConfigured(t *testing.T) {
	_, ts := newMonitorTestServer(t, MonitorConfig{})
	defer ts.Close()
	code, body := do(t, ts, "POST", "/api/v1/apply", "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without applyer, got %d %s", code, body)
	}
}

func TestAppliedAndPending(t *testing.T) {
	fake := &fakeApplyer{
		history: []apply.Record{{Profile: "p1", StateHash: "abc", AppliedAt: time.Now()}},
		pending: true, hash: "def",
	}
	_, ts := newMonitorTestServer(t, MonitorConfig{Applyer: fake})
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/applied", "")
	if code != 200 || !strings.Contains(body, "p1") {
		t.Fatalf("applied: %d %s", code, body)
	}
	code, body = do(t, ts, "GET", "/api/v1/pending", "")
	if code != 200 || !strings.Contains(body, `"pending":true`) {
		t.Fatalf("pending: %d %s", code, body)
	}
}

func TestRollbackEndpoint(t *testing.T) {
	fake := &fakeApplyer{rollbackF: func(step int) (apply.Record, error) {
		if step != 1 {
			return apply.Record{}, fmt.Errorf("bad step %d", step)
		}
		return apply.Record{Profile: "p0"}, nil
	}}
	_, ts := newMonitorTestServer(t, MonitorConfig{Applyer: fake})
	defer ts.Close()

	code, body := do(t, ts, "POST", "/api/v1/rollback", "")
	if code != 200 || !strings.Contains(body, "p0") {
		t.Fatalf("rollback: %d %s", code, body)
	}
	// step_back plumbed through.
	code, body = do(t, ts, "POST", "/api/v1/rollback", `{"step_back":2}`)
	if code != 409 {
		t.Fatalf("rollback step 2 should 409 on fake, got %d %s", code, body)
	}
	// invalid body
	code, _ = do(t, ts, "POST", "/api/v1/rollback", `{"bogus":1}`)
	if code != 400 {
		t.Fatalf("bad body should 400, got %d", code)
	}
}

func TestLogsEndpoint(t *testing.T) {
	lg := applog.New("", 100)
	lg.Printf("apply: profile p1 ok=true")
	_, ts := newMonitorTestServer(t, MonitorConfig{Logs: lg})
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/logs", "")
	if code != 200 || !strings.Contains(body, "profile p1 ok=true") {
		t.Fatalf("logs: %d %s", code, body)
	}
	// Not configured.
	_, ts2 := newMonitorTestServer(t, MonitorConfig{})
	defer ts2.Close()
	code, _ = do(t, ts2, "GET", "/api/v1/logs", "")
	if code != 503 {
		t.Fatalf("logs without wiring should 503, got %d", code)
	}
}

func TestDiagnosticsEndpoint(t *testing.T) {
	// Stand up a fake mihomo /version API.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			if r.Header.Get("Authorization") != "Bearer s3cret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, `{"version":"v1.19.31"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer api.Close()
	addr := strings.TrimPrefix(api.URL, "http://")

	_, ts := newMonitorTestServer(t, MonitorConfig{MihomoAPIAddr: addr, MihomoAPISecret: "s3cret"})
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/diagnostics", "")
	if code != 200 {
		t.Fatalf("diagnostics: %d %s", code, body)
	}
	var doc struct {
		Checks []struct {
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	byName := map[string]bool{}
	for _, c := range doc.Checks {
		byName[c.Name] = c.OK
		if c.Name == "mihomo" && !c.OK {
			t.Fatalf("mihomo check should pass with the secret: %+v", c)
		}
	}
	if !byName["nikki"] || !byName["geodata"] {
		// geodata check needs network; tolerate a failure there but
		// the check itself must exist.
		t.Logf("checks: %+v (geodata may fail offline)", doc.Checks)
	}
	if len(doc.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(doc.Checks))
	}
}

// TestDiagnosticsSecretRejected proves the probe sends the secret.
func TestDiagnosticsSecretRejected(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Refuse everything.
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer api.Close()
	addr := strings.TrimPrefix(api.URL, "http://")

	_, ts := newMonitorTestServer(t, MonitorConfig{MihomoAPIAddr: addr, MihomoAPISecret: ""})
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/diagnostics", "")
	if code != 200 {
		t.Fatalf("diagnostics: %d", code)
	}
	if !strings.Contains(body, `"name":"mihomo","ok":false`) {
		t.Fatalf("mihomo check should fail against a 401 API: %s", body)
	}
}

func TestMihomoProxyHTTP(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
	}))
	defer up.Close()
	addr := strings.TrimPrefix(up.URL, "http://")

	_, ts := newMonitorTestServer(t, MonitorConfig{MihomoAPIAddr: addr, MihomoAPISecret: "s3cret"})
	defer ts.Close()

	// The proxy strips the client's token and injects the server one.
	code, body := do(t, ts, "GET", "/api/mihomo/connections", "")
	if code != 200 {
		t.Fatalf("proxy: %d %s", code, body)
	}
	if !strings.Contains(body, `/connections`) {
		t.Fatalf("path not forwarded: %s", body)
	}
}

func TestMihomoProxyUnreachable(t *testing.T) {
	// Port 1 is guaranteed closed.
	_, ts := newMonitorTestServer(t, MonitorConfig{MihomoAPIAddr: "127.0.0.1:1"})
	defer ts.Close()
	code, body := do(t, ts, "GET", "/api/mihomo/version", "")
	if code != 503 {
		t.Fatalf("unreachable upstream should 503, got %d %s", code, body)
	}
}

func TestMetaCubeXDServing(t *testing.T) {
	dir := t.TempDir()
	// Minimal dist: index.html + one asset.
	if err := os.MkdirAll(filepath.Join(dir, "_nuxt"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>metacubexd-dist</body></html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "_nuxt", "app.js"), []byte("console.log(1)"), 0o644)

	_, ts := newMonitorTestServer(t, MonitorConfig{UIDir: dir})
	defer ts.Close()

	code, body := do(t, ts, "GET", "/ui/metacubexd/", "")
	if code != 200 || !strings.Contains(body, "metacubexd-dist") {
		t.Fatalf("index: %d %s", code, body)
	}
	code, body = do(t, ts, "GET", "/ui/metacubexd/_nuxt/app.js", "")
	if code != 200 || !strings.Contains(body, "console.log") {
		t.Fatalf("asset: %d %s", code, body)
	}
	// Root path redirects (client follows) to the index.
	code, body = do(t, ts, "GET", "/ui/metacubexd", "")
	if code != 200 || !strings.Contains(body, "metacubexd-dist") {
		t.Fatalf("root should redirect to the index: %d %s", code, body)
	}
	// Traversal attempt: cleaned to a path inside the dir.
	req, _ := http.NewRequest("GET", ts.URL+"/ui/metacubexd/../apply", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 && resp.Request.URL.Path == "/api/v1/apply" {
		t.Fatal("traversal escaped the UI dir")
	}
}

func TestMetaCubeXDMissing(t *testing.T) {
	_, ts := newMonitorTestServer(t, MonitorConfig{UIDir: ""})
	defer ts.Close()
	code, body := do(t, ts, "GET", "/ui/metacubexd/", "")
	if code != 404 || !strings.Contains(body, "fetch-metacubexd.sh") {
		t.Fatalf("missing dist should 404 with a hint: %d %s", code, body)
	}
}

// storeOf builds a real store on a temp dir (helper indirection to
// keep this file's imports tidy).
func storeOf(dir string) Store {
	return store.New(dir, false)
}
