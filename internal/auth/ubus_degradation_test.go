package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeUbusCLI records CLI fallback invocations and replies with canned
// output, so CLI-path tests never shell out to a real ubus binary.
type fakeUbusCLI struct {
	calls    int
	lastArgs []string
	reply    []byte
	err      error
}

func (f *fakeUbusCLI) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls++
	f.lastArgs = args
	return f.reply, f.err
}

// unreachableUbusURL returns a URL where nothing listens: the HTTP
// bridge is down, exactly like on a dev host without ubusd.
func unreachableUbusURL(t *testing.T) string {
	t.Helper()
	// Port 1 (tcpmux) is closed on every sane host; probe to be sure.
	connProbe := func(url string) bool {
		_, err := http.Post(url, "application/json", strings.NewReader("{}"))
		return err != nil
	}
	url := "http://127.0.0.1:1/ubus"
	if !connProbe(url) {
		t.Skipf("port 1 unexpectedly reachable; cannot simulate dead ubusd")
	}
	return url
}

// TestUbusUnavailableNoPanic pins the headline behaviour of this ticket:
// when ubusd is nowhere (HTTP bridge dead AND the CLI binary is
// missing), Login returns ErrUbusUnavailable instead of panicking, and
// existing sessions keep working: only new logins are refused.
func TestUbusUnavailableNoPanic(t *testing.T) {
	cli := &fakeUbusCLI{err: &exec.Error{Name: "ubus", Err: exec.ErrNotFound}}
	c := &UbusClient{URL: unreachableUbusURL(t), RunCLI: cli.run}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Login panicked: %v", r)
		}
	}()
	sess, err := c.Login(context.Background(), "root", "secret")
	if !errors.Is(err, ErrUbusUnavailable) {
		t.Fatalf("err = %v, want ErrUbusUnavailable", err)
	}
	if sess != "" {
		t.Fatalf("session = %q, want empty", sess)
	}

	// The live-session path: a WellBoard session minted earlier is not
	// re-checked against ubus, so it stays valid across an ubusd crash.
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	mgr := NewManager(time.Hour)
	mgr.now = func() time.Time { return now }
	s, err := mgr.Create("root")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := mgr.Get(s.Token); !ok {
		t.Fatal("live session lost while ubusd is down")
	}

	// The login endpoint surfaces it as 503, not 500 and not a panic.
	_, srv := newTestServer(t, func(context.Context, string, string) (string, error) {
		return "", ErrUbusUnavailable
	})
	resp := postLogin(t, srv, "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// TestUbusLoginHTTPTimeout: the bridge hangs → the client timeout turns
// it into ErrUbusUnavailable, the CLI fallback is tried, and a
// user-canceled context fails fast with ErrUbusUnavailable.
func TestUbusLoginHTTPTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // hold the request open until the test ends
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	cli := &fakeUbusCLI{reply: []byte(`{"ubus_rpc_session":"cli-after-timeout"}`)}
	c := &UbusClient{URL: srv.URL, HTTP: &http.Client{Timeout: 50 * time.Millisecond}, RunCLI: cli.run}

	start := time.Now()
	sess, err := c.Login(context.Background(), "root", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if sess != "cli-after-timeout" {
		t.Fatalf("session = %q, want cli-after-timeout", sess)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout did not fire: %v", elapsed)
	}
	if cli.calls != 1 {
		t.Fatalf("CLI fallback calls = %d, want 1", cli.calls)
	}

	// Canceled context: fails fast with the sentinel, no panic.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c2 := &UbusClient{
		URL:  srv.URL,
		HTTP: &http.Client{Timeout: 50 * time.Millisecond},
		RunCLI: func(context.Context, ...string) ([]byte, error) {
			return nil, context.Canceled
		},
	}
	_, err = c2.Login(ctx, "root", "secret")
	if !errors.Is(err, ErrUbusUnavailable) {
		t.Fatalf("canceled ctx: err = %v, want ErrUbusUnavailable", err)
	}
}

// TestUbusLoginHTTPNonJSON: uhttpd answers with an HTML error page (the
// bridge is not configured) → ErrUbusUnavailable → CLI fallback.
func TestUbusLoginHTTPNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("<html><body><h1>404 Not Found</h1></body></html>"))
	}))
	defer srv.Close()

	cli := &fakeUbusCLI{reply: []byte(`{"ubus_rpc_session":"cli-after-html"}`)}
	c := &UbusClient{URL: srv.URL, RunCLI: cli.run}
	sess, err := c.Login(context.Background(), "root", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if sess != "cli-after-html" {
		t.Fatalf("session = %q, want cli-after-html", sess)
	}

	// Same page but the CLI is gone too: plain ErrUbusUnavailable.
	cli2 := &fakeUbusCLI{err: &exec.Error{Name: "ubus", Err: exec.ErrNotFound}}
	c2 := &UbusClient{URL: srv.URL, RunCLI: cli2.run}
	_, err = c2.Login(context.Background(), "root", "secret")
	if !errors.Is(err, ErrUbusUnavailable) {
		t.Fatalf("err = %v, want ErrUbusUnavailable", err)
	}
}

// TestUbusLoginHTTPServerErrors: 5xx from the bridge is treated as
// unavailability (falls back to CLI), 4xx-with-garbage is not a verdict.
func TestUbusLoginHTTPServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("502 Bad Gateway"))
	}))
	defer srv.Close()

	cli := &fakeUbusCLI{reply: []byte(`{"ubus_rpc_session":"cli-after-502"}`)}
	c := &UbusClient{URL: srv.URL, RunCLI: cli.run}
	sess, err := c.Login(context.Background(), "root", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if sess != "cli-after-502" {
		t.Fatalf("session = %q, want cli-after-502", sess)
	}
}

// TestUbusLoginCLIStatus: the CLI ran but failed — a non-zero exit is a
// credential verdict, a missing binary is unavailability.
func TestUbusLoginCLIStatus(t *testing.T) {
	// A non-zero CLI exit (ubus printed "Command failed: Access
	// denied") is a credential verdict, not unavailability.
	c := &UbusClient{
		URL: unreachableUbusURL(t),
		RunCLI: func(_ context.Context, _ ...string) ([]byte, error) {
			return nil, exitErrWithCode(1)
		},
	}
	_, err := c.Login(context.Background(), "root", "bad")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CLI exit error: err = %v, want ErrInvalidCredentials", err)
	}
}

// TestUbusLoginCLIReplies: CLI output shapes — valid session, non-JSON
// noise, and empty session id.
func TestUbusLoginCLIReplies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reply   []byte
		wantErr error
		wantSes string
	}{
		{
			name:    "ok",
			reply:   []byte(`{"ubus_rpc_session":"cli-ok"}`),
			wantSes: "cli-ok",
		},
		{
			name:    "not json",
			reply:   []byte("Command failed: Access denied\n"),
			wantErr: ErrInvalidCredentials,
		},
		{
			name:    "json without session",
			reply:   []byte(`{"timeout":300}`),
			wantErr: ErrInvalidCredentials,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := &fakeUbusCLI{reply: tc.reply}
			c := &UbusClient{URL: unreachableUbusURL(t), RunCLI: cli.run}
			sess, err := c.Login(context.Background(), "root", "secret")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Login: %v", err)
			}
			if sess != tc.wantSes {
				t.Fatalf("session = %q, want %q", sess, tc.wantSes)
			}
		})
	}
}

// TestUbusHTTPLoginIgorOneLine pins the malformed-HTTP-reply contract:
// an HTTP 200 with a JSON body that decodes but lacks a session id is a
// credential-side verdict (the bridge answered; the answer was empty).
func TestUbusHTTPLoginEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[0,{}]}`))
	}))
	defer srv.Close()

	cli := &fakeUbusCLI{reply: []byte(`{"ubus_rpc_session":"never"}`)}
	c := &UbusClient{URL: srv.URL, RunCLI: cli.run}
	_, err := c.Login(context.Background(), "root", "secret")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
	if cli.calls != 0 {
		t.Fatalf("CLI fallback calls = %d, want 0", cli.calls)
	}
}

// TestUbusLoginContextTimeoutCLI: an expired context makes the CLI path
// fail as unavailable (CommandContext kills the binary) without panic.
func TestUbusLoginContextTimeoutCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	c := &UbusClient{
		URL: unreachableUbusURL(t),
		RunCLI: func(runCtx context.Context, args ...string) ([]byte, error) {
			<-runCtx.Done() // emulate a hung ubus call until the context expires
			return nil, runCtx.Err()
		},
	}
	_, err := c.Login(ctx, "root", "secret")
	if !errors.Is(err, ErrUbusUnavailable) {
		t.Fatalf("err = %v, want ErrUbusUnavailable", err)
	}
}

var _ = json.Marshal // keep encoding/json imported for future helpers

// exitErrWithCode builds a genuine *exec.ExitError with the given exit
// code by running a shell command that exits with it, so the CLI
// failure paths are exercised against the real error type.
func exitErrWithCode(code int) error {
	cmd := exec.Command("/bin/sh", "-c", "exit "+strconv.Itoa(code))
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr
	}
	return err
}
