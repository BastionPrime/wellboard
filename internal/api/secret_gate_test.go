// Secret gate (audit §5 / OPE-3741): the API handlers that touch
// secret-carrying payloads — subscription source PATCH (URL with a
// token), nikki import (discovered URLs), state import (proxy
// passwords) — must keep the diagnostics log (s.log → SetLog → the
// daemon's applog) free of those secrets. Uses the shared
// applog.RedactedLogger helper.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/applog"
	"github.com/wellboard/wellboard/internal/model"
)

// Gate-scoped stand-in secrets (never real values, never expected in
// any log text — any hit is a leak).
const (
	gateToken    = "ZmFrZVRva2VuNDU2"
	gatePassword = "ZmFrZVBhc3N3b3Jk"
)

const gateTokenedURL = "https://panel.example/api/v1/client/subscribe?token=" + gateToken

// gateServer is a test server with the RedactedLogger wired as the
// SetLog diagnostics sink (exactly how cmd/wellboard routes s.log into
// applog).
func gateServer(t *testing.T) (*Server, *httptest.Server, *applog.RedactedLogger) {
	t.Helper()
	srv, ts := newTestServer(t)
	r := &applog.RedactedLogger{}
	srv.SetLog(r.Log)
	return srv, ts, r
}

// addGateSubscription POSTs a tokened subscription source and returns
// the created row's ID.
func addGateSubscription(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	code, body := do(t, ts, "POST", "/api/v1/sources",
		`{"kind":"subscription","name":"Gate sub","url":"`+gateTokenedURL+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("create gate source: %d %s", code, body)
	}
	src := model.Source{}
	if err := json.Unmarshal([]byte(body), &src); err != nil || src.ID == "" {
		t.Fatalf("create gate source body: %s", body)
	}
	return src.ID
}

// assertNoURL asserts no URL fragment shows up in the capture: a line
// like "https://host/[url redacted]" still fails — any URL-shaped
// fragment in a log line is one edit away from carrying the token.
func assertNoURL(t *testing.T, r *applog.RedactedLogger) {
	t.Helper()
	r.HasSecret(t, "http://")
	r.HasSecret(t, "https://")
}

// TestGateSourcePatchWithURLToken: "subscription update with a token in
// the URL" — create a tokened source, PATCH it to a new tokened URL
// (the token-rotation flow), and assert the log carries only ids,
// never the token or a URL fragment.
func TestGateSourcePatchWithURLToken(t *testing.T) {
	srv, ts, r := gateServer(t)
	defer ts.Close()
	seedState(t, srv)

	id := addGateSubscription(t, ts)

	newURL := "https://panel.example/api/v1/client/subscribe?token=" + gateToken + "&refresh=1"
	code, body := do(t, ts, "PATCH", "/api/v1/sources/"+id,
		`{"url":"`+newURL+`"}`)
	if code != http.StatusOK {
		t.Fatalf("patch gate source: %d %s", code, body)
	}

	if len(r.Lines()) == 0 {
		t.Fatal("expected diagnostics for the create/patch calls")
	}
	r.HasSecret(t, gateToken)
	assertNoURL(t, r)
}

// TestGateImportNikkiDiscoveredURLs: nikki import discovers
// token-carrying URLs in the router config; its diagnostics must carry
// counts only (the secret rule documented on the handler).
func TestGateImportNikkiDiscoveredURLs(t *testing.T) {
	srv, ts, r := gateServer(t)
	defer ts.Close()

	// Fixture config carrying a tokened subscription URL.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	body := "proxy-providers:\n  gate:\n    type: http\n    url: " + gateTokenedURL + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.SetNikkiPaths([]string{cfg})

	code, body := do(t, ts, "POST", "/api/v1/sources/import-nikki", `{}`)
	if code != http.StatusOK {
		t.Fatalf("import-nikki: %d %s", code, body)
	}

	if len(r.Lines()) == 0 {
		t.Fatal("expected import diagnostics (counts)")
	}
	r.HasSecret(t, gateToken)
	assertNoURL(t, r)
}

// TestGateImportSensitiveField: "import with a sensitive field" — a
// state import whose server carries a proxy password and whose source
// carries a tokened URL; both the ACCEPTED import and the REJECTED
// import (broken refs → s.log of the generator error) must stay
// secret-free.
func TestGateImportSensitiveField(t *testing.T) {
	srv, ts, r := gateServer(t)
	defer ts.Close()
	seedState(t, srv)

	st, err := srv.Store.Load()
	if err != nil {
		t.Fatal(err)
	}

	// 1. Valid import: source with tokened URL + server with password.
	imp := *st
	imp.Sources = []model.Source{{
		ID: "sub_gate", Kind: "subscription", Name: "Gate", Enabled: true,
		URL: gateTokenedURL,
	}}
	imp.Servers = []model.Server{{
		ID: "srv_gate", SourceID: "sub_gate", Name: "GateNode", Type: "ss",
		Raw: map[string]any{
			"server": "203.0.113.10", "port": 8388,
			"cipher": "aes-256-gcm", "password": gatePassword,
		},
	}}
	// The seeded groups/routes reference the seeded servers; the import
	// replaces them, so drop the stale references too (referential
	// integrity is validated before the save).
	imp.Groups = nil
	imp.Routes = nil
	doc := map[string]any{
		"format": 1, "app": AppName, "version": st.Version, "state": imp,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	code, body := do(t, ts, "POST", "/api/v1/import", string(raw))
	if code != http.StatusOK {
		t.Fatalf("gate import: %d %s", code, body)
	}

	// 2. Broken-refs import: rejected by generator validation with the
	// error text going through s.log.
	broken := map[string]any{
		"format": 1, "app": AppName, "version": st.Version,
		"state": map[string]any{
			"version": st.Version,
			"routes": []map[string]any{{
				"id": "rt_gate", "name": "x", "enabled": true, "order": 10,
				"conditions":     []map[string]any{{"type": "domain", "value": "a.com"}},
				"target":         map[string]any{"type": "group", "id": "grp_missing"},
				"on_unavailable": "block",
			}},
		},
	}
	rawBroken, err := json.Marshal(broken)
	if err != nil {
		t.Fatal(err)
	}
	code, _ = do(t, ts, "POST", "/api/v1/import", string(rawBroken))
	if code != http.StatusConflict {
		t.Fatalf("broken import must 409, got %d", code)
	}

	if len(r.Lines()) == 0 {
		t.Fatal("expected import diagnostics")
	}
	r.HasSecret(t, gateToken)
	r.HasSecret(t, gatePassword)
	assertNoURL(t, r)
}

// TestGateApplogEndToEnd: the full daemon path — the SetLog callback
// writes into a real applog.Log ring (as cmd/wellboard wires
// log.Printf), and GET /api/v1/logs serves the captured lines. The
// served tail must never materialize a secret: this is the FR-9.2
// surface an operator actually reads.
func TestGateApplogEndToEnd(t *testing.T) {
	srv, ts, r := gateServer(t)
	defer ts.Close()
	seedState(t, srv)

	lg := applog.New("", 100)
	srv.SetLog(func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		_, _ = lg.Write([]byte(line))
		_, _ = r.Write([]byte(line))
	})
	srv.SetMonitor(MonitorConfig{Logs: lg})

	// Drive the secret-carrying flows: source create + token rotation.
	id := addGateSubscription(t, ts)
	newURL := "https://panel.example/api/v1/client/subscribe?token=" + gateToken + "&v=2"
	if code, body := do(t, ts, "PATCH", "/api/v1/sources/"+id, `{"url":"`+newURL+`"}`); code != http.StatusOK {
		t.Fatalf("patch: %d %s", code, body)
	}

	code, body := do(t, ts, "GET", "/api/v1/logs", "")
	if code != http.StatusOK {
		t.Fatalf("logs: %d %s", code, body)
	}

	r.HasSecret(t, gateToken)
	assertNoURL(t, r)
	if strings.Contains(body, gateToken) {
		t.Fatalf("GET /logs leaked the token: %s", body)
	}
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Fatalf("GET /logs leaked a URL fragment: %s", body)
	}
}
