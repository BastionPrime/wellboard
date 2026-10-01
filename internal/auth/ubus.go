// Package auth implements the WellBoard login flow.
//
// The owner asked to log in with the router's own root password instead
// of a second password database (initial TZ Q5). The password is
// verified through the ubus `session` service — the same backend LuCI
// uses — and the resulting WellBoard session is carried by an HttpOnly
// cookie plus a double-submit CSRF token for state-changing requests.
//
// The ubus session id returned by `session login` is deliberately NOT
// stored: it is used once as proof of the password and dropped, so a
// leaked WellBoard cookie never grants ubus/root rights to the router.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// DefaultUbusURL is the local ubus JSON-RPC bridge served by uhttpd
// (OpenWrt wiki: "Session management"). Talking to it over loopback
// keeps the password out of any process argument list.
const DefaultUbusURL = "http://127.0.0.1/ubus"

// Sentinel errors. Callers map ErrInvalidCredentials to 401 and
// ErrUbusUnavailable to 503.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUbusUnavailable    = errors.New("ubus unavailable")
)

// LoginFunc verifies a username/password pair and returns the ubus rpc
// session id ("" is never returned with a nil error).
type LoginFunc func(ctx context.Context, username, password string) (string, error)

// UbusClient logs in through ubus. Two transports are tried in order:
// the HTTP JSON-RPC bridge on loopback (preferred — the password never
// appears in a process argument list) and the `ubus` CLI, which keeps
// working on images where the HTTP bridge is disabled (the CLI sees the
// password in its own argv, which root can read from /proc; documented
// in docs/API.md).
type UbusClient struct {
	// URL is the JSON-RPC bridge; empty means DefaultUbusURL.
	URL string
	// HTTP is the client used for the bridge; empty means a 5 s client.
	HTTP *http.Client
	// UbusBin is the CLI used as fallback; empty means "ubus".
	UbusBin string
	// RunCLI is injected in tests; nil shells out to UbusBin.
	RunCLI func(ctx context.Context, args ...string) ([]byte, error)
}

// Login verifies the password. username is normally "root".
func (c *UbusClient) Login(ctx context.Context, username, password string) (string, error) {
	session, err := c.loginHTTP(ctx, username, password)
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, ErrUbusUnavailable) {
		// The bridge answered: the credentials are wrong. Trying the
		// CLI would only repeat the same question.
		return "", err
	}
	return c.loginCLI(ctx, username, password)
}

// ubusRequest is the JSON-RPC container documented on the OpenWrt wiki.
type ubusRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      int               `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

type ubusError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ubusResponse struct {
	Result []json.RawMessage `json:"result"`
	Error  *ubusError        `json:"error"`
}

type ubusSession struct {
	UbusRPCSession string `json:"ubus_rpc_session"`
}

// loginPayload is the ubus `session login` argument object.
type loginPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *UbusClient) loginHTTP(ctx context.Context, username, password string) (string, error) {
	url := c.URL
	if url == "" {
		url = DefaultUbusURL
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	args, err := json.Marshal(loginPayload{Username: username, Password: password})
	if err != nil {
		return "", fmt.Errorf("encode login payload: %w", err)
	}
	body, err := json.Marshal(ubusRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "call",
		Params: []json.RawMessage{
			json.RawMessage(`"00000000000000000000000000000000"`),
			json.RawMessage(`"session"`),
			json.RawMessage(`"login"`),
			args,
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode ubus request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUbusUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUbusUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUbusUnavailable, err)
	}
	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("%w: http %d", ErrUbusUnavailable, resp.StatusCode)
	}

	var out ubusResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		// A uhttpd error page (the bridge is not configured) is not a
		// credential verdict: fall back to the CLI.
		return "", fmt.Errorf("%w: %v", ErrUbusUnavailable, err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("%w: ubus code %d", ErrInvalidCredentials, out.Error.Code)
	}
	if len(out.Result) < 2 {
		return "", fmt.Errorf("%w: malformed ubus result", ErrInvalidCredentials)
	}
	var status int
	if err := json.Unmarshal(out.Result[0], &status); err != nil {
		return "", fmt.Errorf("%w: malformed ubus status", ErrInvalidCredentials)
	}
	if status != 0 {
		return "", fmt.Errorf("%w: ubus status %d", ErrInvalidCredentials, status)
	}
	var sess ubusSession
	if err := json.Unmarshal(out.Result[1], &sess); err != nil || sess.UbusRPCSession == "" {
		return "", fmt.Errorf("%w: no session in ubus reply", ErrInvalidCredentials)
	}
	return sess.UbusRPCSession, nil
}

func (c *UbusClient) loginCLI(ctx context.Context, username, password string) (string, error) {
	payload, err := json.Marshal(loginPayload{Username: username, Password: password})
	if err != nil {
		return "", fmt.Errorf("encode login payload: %w", err)
	}
	run := c.RunCLI
	if run == nil {
		bin := c.UbusBin
		if bin == "" {
			bin = "ubus"
		}
		run = func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, bin, args...).Output()
		}
	}
	raw, err := run(ctx, "call", "session", "login", string(payload))
	if err != nil {
		// The CLI prints "Command failed: ..." on a rejected login and
		// exits non-zero; a missing binary is the only unavailable case
		// we can distinguish cheaply, so treat an exec.ExitError as a
		// credential verdict and everything else as unavailable.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%w: %s", ErrInvalidCredentials, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%w: %v", ErrUbusUnavailable, err)
	}
	var sess ubusSession
	if err := json.Unmarshal(raw, &sess); err != nil || sess.UbusRPCSession == "" {
		return "", fmt.Errorf("%w: no session in ubus CLI output", ErrInvalidCredentials)
	}
	return sess.UbusRPCSession, nil
}
