package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/store"
)

// Per-server delay test (OPE-4115): GET /api/v1/servers/{id}/delay
// proxies mihomo /proxies/{name}/delay. Cases: success (delay relayed,
// default URL applied), upstream timeout (502, server survives), unknown
// server (404), mihomo not configured (502).

// newDelayTestServer builds an API server with one server in the pool
// and the monitor wiring pointed at the given mihomo API address
// ("" = not configured).
func newDelayTestServer(t *testing.T, mihomoAddr, mihomoSecret string) (*Server, *httptest.Server) {
	t.Helper()
	stStore := store.New(t.TempDir(), false)
	st := store.DefaultState()
	st.Servers = []model.Server{
		{ID: "srv_1", SourceID: "src_manual", Name: "relay-one", Type: "vless", Raw: map[string]any{}},
	}
	if err := stStore.Save(st); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(stStore, nil)
	srv.SetMonitor(MonitorConfig{MihomoAPIAddr: mihomoAddr, MihomoAPISecret: mihomoSecret})
	mux := http.NewServeMux()
	srv.Register(mux)
	return srv, httptest.NewServer(mux)
}

// fakeMihomoDelay spins a stub mihomo external-controller answering
// /proxies/{name}/delay. handler sees the full request (url/timeout
// query + auth header).
func fakeMihomoDelay(t *testing.T, handler http.HandlerFunc) (addr string, closeFn func()) {
	t.Helper()
	up := httptest.NewServer(handler)
	t.Cleanup(up.Close)
	return strings.TrimPrefix(up.URL, "http://"), func() { up.Close() }
}

func TestServerDelaySuccess(t *testing.T) {
	var gotPath, gotURL, gotTimeout, gotAuth string
	addr, _ := fakeMihomoDelay(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotURL = r.URL.Query().Get("url")
		gotTimeout = r.URL.Query().Get("timeout")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"delay":123}`))
	})

	_, ts := newDelayTestServer(t, addr, "sekrit")
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/servers/srv_1/delay", "")
	if code != http.StatusOK {
		t.Fatalf("delay: %d %s", code, body)
	}
	var out serverDelayOut
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("parse: %v (%s)", err, body)
	}
	if out.DelayMS != 123 || out.ID != "srv_1" || out.Name != "relay-one" {
		t.Fatalf("unexpected body: %+v", out)
	}
	// Default target URL from settings (empty → model.DefaultDelayTestURL).
	if out.URL != model.DefaultDelayTestURL {
		t.Fatalf("expected default url %q, got %q", model.DefaultDelayTestURL, out.URL)
	}
	if gotPath != "/proxies/relay-one/delay" {
		t.Fatalf("upstream path: %q", gotPath)
	}
	if gotURL != model.DefaultDelayTestURL {
		t.Fatalf("upstream url param: %q", gotURL)
	}
	if gotTimeout != "5000" {
		t.Fatalf("upstream timeout param: %q", gotTimeout)
	}
	if gotAuth != "Bearer sekrit" {
		t.Fatalf("upstream auth header not forwarded: %q", gotAuth)
	}
}

func TestServerDelayCustomURLFromSettings(t *testing.T) {
	var gotURL string
	addr, _ := fakeMihomoDelay(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Query().Get("url")
		_, _ = w.Write([]byte(`{"delay":7}`))
	})

	srv, ts := newDelayTestServer(t, addr, "")
	defer ts.Close()

	// Point the delay test at a custom target via the settings PATCH.
	code, body := do(t, ts, "PATCH", "/api/v1/settings", `{"delay_test_url":"https://example.com/204"}`)
	if code != http.StatusOK {
		t.Fatalf("settings patch: %d %s", code, body)
	}
	_ = srv

	code, body = do(t, ts, "GET", "/api/v1/servers/srv_1/delay", "")
	if code != http.StatusOK {
		t.Fatalf("delay: %d %s", code, body)
	}
	if gotURL != "https://example.com/204" {
		t.Fatalf("upstream url param: %q", gotURL)
	}
	if !strings.Contains(body, `"url":"https://example.com/204"`) {
		t.Fatalf("response should echo the used url: %s", body)
	}
}

func TestServerDelayUpstreamTimeout(t *testing.T) {
	// Upstream hangs well past the 5 s probe timeout; the handler must
	// answer 502 and the API server must stay responsive afterwards.
	addr, _ := fakeMihomoDelay(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	})

	_, ts := newDelayTestServer(t, addr, "")
	defer ts.Close()

	start := time.Now()
	code, body := do(t, ts, "GET", "/api/v1/servers/srv_1/delay", "")
	if code != http.StatusBadGateway {
		t.Fatalf("timeout should be 502, got %d %s", code, body)
	}
	if !strings.Contains(body, "error") {
		t.Fatalf("502 body should carry an error text: %s", body)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("probe should be bounded by the 5s timeout, took %s", elapsed)
	}

	// The server survives a failed probe and keeps serving.
	code, _ = do(t, ts, "GET", "/api/v1/servers", "")
	if code != http.StatusOK {
		t.Fatalf("server list after failed probe: %d", code)
	}
}

func TestServerDelayUpstreamError502(t *testing.T) {
	addr, _ := fakeMihomoDelay(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // mihomo's own "cannot dial"
		_, _ = w.Write([]byte(`{"message":"dial tcp: connection refused"}`))
	})

	_, ts := newDelayTestServer(t, addr, "")
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/servers/srv_1/delay", "")
	if code != http.StatusBadGateway {
		t.Fatalf("upstream failure should be 502, got %d %s", code, body)
	}
}

func TestServerDelayUnknownServer404(t *testing.T) {
	addr, _ := fakeMihomoDelay(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream must not be called for an unknown server")
	})

	_, ts := newDelayTestServer(t, addr, "")
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/servers/srv_nope/delay", "")
	if code != http.StatusNotFound {
		t.Fatalf("unknown server should be 404, got %d %s", code, body)
	}
}

func TestServerDelayMihomoNotConfigured(t *testing.T) {
	_, ts := newDelayTestServer(t, "", "")
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/servers/srv_1/delay", "")
	if code != http.StatusBadGateway {
		t.Fatalf("unconfigured mihomo should be 502, got %d %s", code, body)
	}
	if !strings.Contains(body, "not configured") {
		t.Fatalf("body should explain the degradation: %s", body)
	}
}

func TestServerDelayUnreachableDoesNotCrashServer(t *testing.T) {
	// A dead mihomo address (closed port on loopback) → 502, server up.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close() // nothing listens here anymore

	_, ts := newDelayTestServer(t, deadAddr, "")
	defer ts.Close()

	code, _ := do(t, ts, "GET", "/api/v1/servers/srv_1/delay", "")
	if code != http.StatusBadGateway {
		t.Fatalf("unreachable mihomo should be 502, got %d", code)
	}
	code, _ = do(t, ts, "GET", "/api/v1/servers", "")
	if code != http.StatusOK {
		t.Fatalf("server must stay up after an unreachable mihomo: %d", code)
	}
}
