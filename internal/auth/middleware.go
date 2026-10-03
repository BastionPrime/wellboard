package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cookie and header names of the WellBoard session.
const (
	// SessionCookie carries the opaque session token.
	SessionCookie = "wb_session"
	// CSRFCookie carries the double-submit token (HttpOnly: the SPA
	// reads the token from /api/v1/auth/session, never from document.cookie).
	CSRFCookie = "wb_csrf"
	// CSRFHeader must repeat the CSRF cookie on every state-changing request.
	CSRFHeader = "X-CSRF-Token"
)

// DefaultUsername is the only account WellBoard knows: the router's
// own root account (initial TZ Q5 — no second password database).
const DefaultUsername = "root"

// loginWindow / loginMaxAttempts throttle password guessing. Five
// failures from one address inside ten minutes are answered with 429
// until the window expires (cooldown).
const (
	loginMaxAttempts = 5
	loginWindow      = 10 * time.Minute
)

// Config wires the auth handlers into the HTTP mux.
type Config struct {
	// Manager holds the live sessions.
	Manager *Manager
	// Login verifies the password through ubus.
	Login LoginFunc
	// Username is the ubus account to log in as; empty means "root".
	Username string
	// Secure marks the cookies Secure (set when the UI is served over
	// HTTPS; the default LAN deployment is plain HTTP).
	Secure bool
	// Disabled marks a deployment that runs without login (dev stand or
	// UCI wellboard.main.auth=0): the endpoints answer truthfully
	// instead of pretending a session exists.
	Disabled bool
	// Limiter throttles failed logins; nil uses the process-wide default.
	Limiter *Limiter
	// Logf receives login/login-failure diagnostics (never passwords).
	Logf func(format string, args ...any)
}

// handler helpers ------------------------------------------------------------

func (c *Config) username() string {
	if c.Username == "" {
		return DefaultUsername
	}
	return c.Username
}

func (c *Config) log(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// RegisterRoutes mounts the auth endpoints on mux. They must stay
// reachable without a session (RequireAuth exempts /api/v1/auth/).
func (c *Config) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", c.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", c.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/session", c.handleSession)
}

type loginRequest struct {
	Password string `json:"password"`
}

type sessionResponse struct {
	// Required reports whether this deployment asks for a login at all.
	Required      bool   `json:"required"`
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
	CSRF          string `json:"csrf,omitempty"`
	ExpiresInSec  int    `json:"expires_in_sec,omitempty"`
}

func (c *Config) handleLogin(w http.ResponseWriter, r *http.Request) {
	if c.Disabled {
		writeError(w, http.StatusBadRequest, "authentication is disabled on this deployment")
		return
	}
	var req loginRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	ip := clientIP(r)
	if !c.limiter().allow(ip) {
		c.log("auth: login throttled for %s", ip)
		if wait := c.limiter().retryAfter(ip); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
		}
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}

	username := c.username()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := c.Login(ctx, username, req.Password); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			c.limiter().fail(ip)
			c.log("auth: login failed for %s", username)
			writeError(w, http.StatusUnauthorized, "invalid password")
			return
		}
		c.log("auth: ubus unavailable: %v", err)
		writeError(w, http.StatusServiceUnavailable, "ubus is unavailable")
		return
	}

	sess, err := c.Manager.Create(username)
	if err != nil {
		c.log("auth: cannot mint session: %v", err)
		writeError(w, http.StatusInternalServerError, "cannot create session")
		return
	}
	c.limiter().reset(ip)
	c.setCookies(w, sess)
	c.log("auth: login ok for %s", username)
	writeJSON(w, http.StatusOK, sessionResponse{
		Required:      true,
		Authenticated: true,
		Username:      sess.Username,
		CSRF:          sess.CSRF,
		ExpiresInSec:  int(time.Until(sess.Expires).Seconds()),
	})
}

func (c *Config) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := c.sessionToken(r)
	// Logout is CSRF-protected when a session exists; without one it is
	// a no-op so a stale cookie can always be cleared.
	if sess, ok := c.Manager.Get(token); ok {
		if !c.checkCSRF(r, sess) {
			writeError(w, http.StatusForbidden, "csrf token mismatch")
			return
		}
		c.Manager.Delete(token)
	}
	c.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (c *Config) handleSession(w http.ResponseWriter, r *http.Request) {
	if c.Disabled {
		// The deployment runs without a login: report that so the SPA
		// opens the board instead of an unreachable login screen.
		writeJSON(w, http.StatusOK, sessionResponse{Required: false, Authenticated: true})
		return
	}
	sess, ok := c.Manager.Get(c.sessionToken(r))
	if !ok {
		writeJSON(w, http.StatusOK, sessionResponse{Required: true, Authenticated: false})
		return
	}
	// A session cookie alone must not hand the CSRF token to a
	// cross-site reader: the caller proves ownership with the token it
	// already has, or with a same-origin request (no Origin header).
	if r.Header.Get("Origin") != "" && !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		Required:      true,
		Authenticated: true,
		Username:      sess.Username,
		CSRF:          sess.CSRF,
		ExpiresInSec:  int(time.Until(sess.Expires).Seconds()),
	})
}

func (c *Config) setCookies(w http.ResponseWriter, sess *Session) {
	maxAge := int(time.Until(sess.Expires).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: sess.Token, Path: "/",
		MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Secure,
	})
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookie, Value: sess.CSRF, Path: "/",
		MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Secure,
	})
}

func (c *Config) clearCookies(w http.ResponseWriter) {
	for _, name := range []string{SessionCookie, CSRFCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Secure,
		})
	}
}

func (c *Config) sessionToken(r *http.Request) string {
	ck, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return ck.Value
}

// contextKey is unexported so no other package can forge a session.
type contextKey struct{}

// SessionFrom returns the session attached by RequireAuth.
func SessionFrom(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(contextKey{}).(*Session)
	return s, ok
}

// RequireAuth wraps the application mux. Guarded: every /api/ path
// except the health probe and the auth endpoints themselves. Static UI
// files stay public; the data behind them does not.
func (c *Config) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := c.Manager.Get(c.sessionToken(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !c.checkCSRF(r, sess) {
			writeError(w, http.StatusForbidden, "csrf token mismatch")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, sess)))
	})
}

func isPublicPath(path string) bool {
	switch path {
	case "/api/v1/health", "/api/v1/auth/login", "/api/v1/auth/logout", "/api/v1/auth/session":
		return true
	}
	return false
}

// checkCSRF enforces the double-submit token on state-changing methods
// and rejects cross-origin callers outright.
func (c *Config) checkCSRF(r *http.Request, sess *Session) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if r.Header.Get("Origin") != "" && !sameOrigin(r) {
		return false
	}
	return secureEqual(r.Header.Get(CSRFHeader), sess.CSRF)
}

// sameOrigin reports whether the request's Origin header matches its
// own Host (a browser on another site sends its own origin here).
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	stripped := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	return strings.EqualFold(stripped, r.Host)
}

// clientIP is the direct peer address. WellBoard is reached directly on
// the LAN (no proxy in front), so X-Forwarded-For is not trusted.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiterEntry is one address's failure record.
type limiterEntry struct {
	failures int
	until    time.Time
}

// NewLimiter builds a throttle with an injectable clock (tests).
func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{entries: map[string]*limiterEntry{}, now: now}
}

// Limiter counts failed logins per client address.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*limiterEntry
	now     func() time.Time
}

func (l *Limiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		return true
	}
	if l.now().After(e.until) {
		delete(l.entries, ip)
		return true
	}
	return e.failures < loginMaxAttempts
}

// retryAfter reports how long the address must still wait before the
// window ends; 0 when the address is not throttled. It feeds the
// Retry-After header of the 429 response.
func (l *Limiter) retryAfter(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		return 0
	}
	if wait := l.now().Sub(e.until); wait > 0 {
		return 0
	}
	return e.until.Sub(l.now())
}

func (l *Limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok || l.now().After(e.until) {
		e = &limiterEntry{}
		l.entries[ip] = e
	}
	e.failures++
	e.until = l.now().Add(loginWindow)
}

func (l *Limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

var (
	defaultLimiterOnce sync.Once
	defaultLimiter     *Limiter
)

// limiter returns the configured throttle or the process-wide default
// (a fresh Config copy must not reset the counter).
func (c *Config) limiter() *Limiter {
	if c.Limiter != nil {
		return c.Limiter
	}
	defaultLimiterOnce.Do(func() { defaultLimiter = NewLimiter(time.Now) })
	return defaultLimiter
}
