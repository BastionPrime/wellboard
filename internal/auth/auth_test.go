package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagerLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m := NewManager(time.Hour)
	m.now = func() time.Time { return now }

	sess, err := m.Create("root")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Token == "" || sess.CSRF == "" || sess.Token == sess.CSRF {
		t.Fatalf("token/csrf not minted: %+v", sess)
	}
	if got, ok := m.Get(sess.Token); !ok || got.CSRF != sess.CSRF {
		t.Fatalf("Get after Create: ok=%v sess=%+v", ok, got)
	}

	now = now.Add(2 * time.Hour)
	if _, ok := m.Get(sess.Token); ok {
		t.Fatal("expired session still valid")
	}
	if n := m.Count(); n != 0 {
		t.Fatalf("expired session not swept: count=%d", n)
	}

	live, _ := m.Create("root")
	m.Delete(live.Token)
	if _, ok := m.Get(live.Token); ok {
		t.Fatal("deleted session still valid")
	}
	if _, ok := m.Get(""); ok {
		t.Fatal("empty token accepted")
	}
}

func TestUbusHTTPLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ubusRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(req.Params) != 4 {
			t.Errorf("params = %d, want 4", len(req.Params))
		}
		var payload loginPayload
		if err := json.Unmarshal(req.Params[3], &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.Password != "secret" {
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32002,"message":"Access denied"}}`))
			return
		}
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[0,{"ubus_rpc_session":"abc123","timeout":300}]}`))
	}))
	defer srv.Close()

	c := &UbusClient{URL: srv.URL}
	sess, err := c.Login(context.Background(), "root", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if sess != "abc123" {
		t.Fatalf("session = %q, want abc123", sess)
	}

	if _, err := c.Login(context.Background(), "root", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: err = %v, want ErrInvalidCredentials", err)
	}
}

func TestUbusHTTPUnavailableFallsBackToCLI(t *testing.T) {
	// Nothing listens on this port: the bridge is unreachable.
	c := &UbusClient{
		URL: "http://127.0.0.1:1/ubus",
		RunCLI: func(_ context.Context, args ...string) ([]byte, error) {
			if len(args) != 4 || args[0] != "call" || args[1] != "session" || args[2] != "login" {
				t.Fatalf("unexpected CLI args: %v", args)
			}
			if !strings.Contains(args[3], `"username":"root"`) {
				t.Fatalf("payload missing username: %s", args[3])
			}
			return []byte(`{"ubus_rpc_session":"cli-session"}`), nil
		},
	}
	sess, err := c.Login(context.Background(), "root", "secret")
	if err != nil {
		t.Fatalf("Login via CLI: %v", err)
	}
	if sess != "cli-session" {
		t.Fatalf("session = %q, want cli-session", sess)
	}
}

func TestUbusHTTPErrorCodeIsCredentialVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[6,{}]}`))
	}))
	defer srv.Close()

	calledCLI := false
	c := &UbusClient{URL: srv.URL, RunCLI: func(context.Context, ...string) ([]byte, error) {
		calledCLI = true
		return nil, errors.New("must not run")
	}}
	if _, err := c.Login(context.Background(), "root", "x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
	if calledCLI {
		t.Fatal("CLI fallback used after a definite credential verdict")
	}
}

func newTestConfig(t *testing.T, login LoginFunc) (*Config, http.Handler) {
	t.Helper()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	mgr := NewManager(time.Hour)
	mgr.now = func() time.Time { return now }
	cfg := &Config{Manager: mgr, Login: login, Limiter: NewLimiter(func() time.Time { return now })}

	mux := http.NewServeMux()
	cfg.RegisterRoutes(mux)
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /api/v1/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"state":true}`))
	})
	mux.HandleFunc("POST /api/v1/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"saved":true}`))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("spa"))
	})
	return cfg, cfg.RequireAuth(mux)
}

func newTestServer(t *testing.T, login LoginFunc) (*Config, *httptest.Server) {
	t.Helper()
	cfg, h := newTestConfig(t, login)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return cfg, srv
}

func okLogin(context.Context, string, string) (string, error) { return "ubus-session", nil }

func TestRequireAuth(t *testing.T) {
	_, srv := newTestServer(t, okLogin)

	// The health probe and the SPA stay public.
	for _, path := range []string{"/api/v1/health", "/"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}

	// Data endpoints need a session.
	resp, err := http.Get(srv.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /state = %d, want 401", resp.StatusCode)
	}

	login := postLogin(t, srv, "secret")
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", login.StatusCode)
	}
	var body sessionResponse
	if err := json.NewDecoder(login.Body).Decode(&body); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	login.Body.Close()
	if !body.Authenticated || body.CSRF == "" {
		t.Fatalf("login body = %+v", body)
	}
	cookies := login.Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies = %d, want 2", len(cookies))
	}
	for _, ck := range cookies {
		if !ck.HttpOnly {
			t.Fatalf("cookie %s is not HttpOnly", ck.Name)
		}
	}

	// Authorized read.
	resp = doWithCookies(t, srv, http.MethodGet, "/api/v1/state", cookies, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized GET = %d, want 200", resp.StatusCode)
	}

	// State-changing request without the CSRF header is refused.
	resp = doWithCookies(t, srv, http.MethodPost, "/api/v1/state", cookies, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d, want 403", resp.StatusCode)
	}

	// ...with the header it goes through.
	resp = doWithCookies(t, srv, http.MethodPost, "/api/v1/state", cookies, body.CSRF)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST with CSRF = %d, want 200", resp.StatusCode)
	}

	// A cross-site origin is rejected even with a valid CSRF header.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/state", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set(CSRFHeader, body.CSRF)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", resp.StatusCode)
	}
}

func TestLoginRejectsWrongPasswordAndThrottles(t *testing.T) {
	login := func(_ context.Context, _, password string) (string, error) {
		if password != "secret" {
			return "", ErrInvalidCredentials
		}
		return "ubus-session", nil
	}
	_, handler := newTestConfig(t, login)

	attempt := func(password, remote string) int {
		body, _ := json.Marshal(loginRequest{Password: password})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < loginMaxAttempts; i++ {
		if got := attempt("wrong", "192.0.2.1:1000"); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, got)
		}
	}
	if got := attempt("wrong", "192.0.2.1:1001"); got != http.StatusTooManyRequests {
		t.Fatalf("throttled attempt = %d, want 429", got)
	}
	// The throttle is per client address: another LAN host can still log in.
	if got := attempt("secret", "192.0.2.2:1000"); got != http.StatusOK {
		t.Fatalf("login from another address = %d, want 200", got)
	}
}

func TestLoginUnavailableUbus(t *testing.T) {
	_, srv := newTestServer(t, func(context.Context, string, string) (string, error) {
		return "", ErrUbusUnavailable
	})
	resp := postLogin(t, srv, "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestSessionEndpointWithoutCookie(t *testing.T) {
	_, srv := newTestServer(t, okLogin)
	resp, err := http.Get(srv.URL + "/api/v1/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body sessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Authenticated {
		t.Fatalf("unauthenticated session probe = %+v", body)
	}
}

func postLogin(t *testing.T, srv *httptest.Server, password string) *http.Response {
	t.Helper()
	return postLoginFrom(t, srv, password, "192.0.2.1:1234")
}

func postLoginFrom(t *testing.T, srv *httptest.Server, password, remote string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(loginRequest{Password: password})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func doWithCookies(t *testing.T, srv *httptest.Server, method, path string, cookies []*http.Cookie, csrf string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if csrf != "" {
		req.Header.Set(CSRFHeader, csrf)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
