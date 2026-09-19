// Phase 5 API surface (FR-6 apply/rollback, FR-9.2 logs, FR-9.4
// diagnostics, FR-8 metacubexd + mihomo API proxy). See apply.go in
// this package for the apply/rollback/pending handlers.
package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/wellboard/wellboard/internal/applog"
	"github.com/wellboard/wellboard/internal/apply"
	"github.com/wellboard/wellboard/internal/geodata"
	"github.com/wellboard/wellboard/internal/nikki"
)

// MonitorConfig wires the Phase 5 monitoring endpoints.
type MonitorConfig struct {
	// Adapter is the nikki integration (DryRun on the dev stand).
	Adapter nikki.Adapter
	// Applyer runs the apply flow (nil → apply endpoints 503).
	Applyer ApplyerAPI
	// Logs is the app log buffer (nil → /logs 503).
	Logs *applog.Log
	// MihomoAPIAddr is the external-controller address to proxy to
	// (e.g. "127.0.0.1:19090"; "" → /api/mihomo/* 503).
	MihomoAPIAddr string
	// MihomoAPISecret is the external-controller bearer secret.
	MihomoAPISecret string
	// UIDir serves the metacubexd dist at /ui/metacubexd/ ("" → 404
	// with a fetch hint, DECISIONS D18).
	UIDir string
	// Auth gates /ui/metacubexd/ and /api/mihomo/* (nil = open, dev).
	Auth func(r *http.Request) bool
}

// ApplyerAPI is the apply-flow contract the API needs (satisfied by
// *apply.Applyer).
type ApplyerAPI interface {
	Apply() (apply.Result, error)
	History() []apply.Record
	Rollback(stepBack int) (apply.Record, error)
	Pending() (bool, string, error)
}

// SetMonitor wires the Phase 5 monitoring endpoints.
func (s *Server) SetMonitor(cfg MonitorConfig) { s.monitor = cfg }

func (s *Server) authed(r *http.Request) bool {
	if s.monitor.Auth == nil {
		return true // dev mode: no auth (documented in DECISIONS)
	}
	return s.monitor.Auth(r)
}

// registerMonitor mounts the Phase 5 routes (called from Register).
func (s *Server) registerMonitor(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/apply", s.handleApply)
	mux.HandleFunc("GET /api/v1/applied", s.handleApplied)
	mux.HandleFunc("POST /api/v1/rollback", s.handleRollback)
	mux.HandleFunc("GET /api/v1/pending", s.handlePending)
	mux.HandleFunc("GET /api/v1/logs", s.handleLogs)
	mux.HandleFunc("GET /api/v1/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("GET /ui/metacubexd/", s.handleMetaCubeXD)
	mux.HandleFunc("GET /ui/metacubexd", s.handleMetaCubeXDRoot)
	mux.HandleFunc("/api/mihomo/", s.handleMihomoProxy)
}

// -----------------------------------------------------------------------------
// FR-6: apply / applied / rollback / pending
// -----------------------------------------------------------------------------

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if s.monitor.Applyer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "apply flow not configured (dry-run stand without adapter)"})
		return
	}
	res, err := s.monitor.Applyer.Apply()
	if err != nil {
		// Infrastructure failure (state/history I/O).
		s.log("apply: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.log("apply: profile %s ok=%v stage=%q error=%q rolledBack=%v", res.Profile, res.OK, res.Stage, res.Error, res.RolledBack)
	status := http.StatusOK
	if !res.OK {
		// Flow failure: validation/generation problems are the
		// client's state (409); runtime failures (activate/health)
		// are 503 — the state was fine, the runtime was not.
		switch res.Stage {
		case "generate", "validate":
			status = http.StatusConflict
		default:
			status = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, status, res)
}

func (s *Server) handleApplied(w http.ResponseWriter, r *http.Request) {
	if s.monitor.Applyer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "apply flow not configured"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": s.monitor.Applyer.History()})
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	if s.monitor.Applyer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "apply flow not configured"})
		return
	}
	var in struct {
		StepBack *int `json:"step_back"`
	}
	// Body is optional; empty body = rollback one step.
	if r.ContentLength > 0 {
		if err := decodeStrict(r, &in, 0); err != nil {
			writeError(w, err)
			return
		}
	}
	step := 1
	if in.StepBack != nil && *in.StepBack >= 1 {
		step = *in.StepBack
	}
	rec, err := s.monitor.Applyer.Rollback(step)
	if err != nil {
		s.log("rollback: %v", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.log("rollback: restored profile %s", rec.Profile)
	writeJSON(w, http.StatusOK, map[string]any{"status": "rolled back", "profile": rec.Profile})
}

func (s *Server) handlePending(w http.ResponseWriter, r *http.Request) {
	if s.monitor.Applyer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "apply flow not configured"})
		return
	}
	pending, hash, err := s.monitor.Applyer.Pending()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pending": pending,
		"hash":    hash,
	})
}

// -----------------------------------------------------------------------------
// FR-9.2: logs
// -----------------------------------------------------------------------------

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.monitor.Logs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "log capture not configured"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": s.monitor.Logs.Tail(applog.ServeLines)})
}

// -----------------------------------------------------------------------------
// FR-9.4: diagnostics — nikki available, mihomo API answers, geodata
// endpoints reachable (HEAD probe).
// -----------------------------------------------------------------------------

// diagCheck is one FR-9.4 check outcome.
type diagCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
	Measure string `json:"measure,omitempty"`
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var checks []diagCheck

	// 1. nikki (adapter) available.
	if s.monitor.Adapter == nil {
		checks = append(checks, diagCheck{Name: "nikki", OK: false, Detail: "adapter not configured"})
	} else if info, err := s.monitor.Adapter.Detect(); err != nil {
		checks = append(checks, diagCheck{Name: "nikki", OK: false, Detail: err.Error()})
	} else {
		detail := "mihomo " + info.MihomoVersion
		if info.APIAddr != "" {
			detail += ", API " + info.APIAddr
		}
		checks = append(checks, diagCheck{Name: "nikki", OK: true, Detail: detail})
	}

	// 2. mihomo API answers (version probe with the secret).
	if s.monitor.MihomoAPIAddr == "" {
		checks = append(checks, diagCheck{Name: "mihomo", OK: false, Detail: "external-controller address unknown"})
	} else {
		checks = append(checks, probeMihomoAPI(ctx, s.monitor.MihomoAPIAddr, s.monitor.MihomoAPISecret))
	}

	// 3. geodata available (HEAD probe of the configured source).
	st, err := s.load()
	if err != nil {
		s.unlock()
		writeError(w, err)
		return
	}
	src := geodata.SourceKind(st.Settings.Geodata)
	s.unlock()
	if !geodata.Valid(string(src)) {
		src = geodata.Runetfreedom
	}
	checker := &geodata.Checker{}
	avail := checker.Check(ctx, src)
	gd := diagCheck{Name: "geodata", OK: avail.GeositeOK && avail.GeoipOK, Detail: string(src)}
	if avail.Error != "" {
		gd.Detail += ": " + avail.Error
	}
	checks = append(checks, gd)

	writeJSON(w, http.StatusOK, map[string]any{"checks": checks})
}

// probeMihomoAPI GETs /version on the external-controller with the
// bearer secret and reports the core version from the response.
func probeMihomoAPI(ctx context.Context, addr, secret string) diagCheck {
	start := time.Now()
	url := "http://" + addr + "/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return diagCheck{Name: "mihomo", OK: false, Detail: err.Error()}
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return diagCheck{Name: "mihomo", OK: false, Detail: fmt.Sprintf("API at %s: %v", addr, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return diagCheck{Name: "mihomo", OK: false, Detail: fmt.Sprintf("API at %s: status %d", addr, resp.StatusCode)}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	ver := strings.TrimSpace(string(body))
	return diagCheck{
		Name: "mihomo", OK: true, Detail: fmt.Sprintf("API at %s answered: %s", addr, ver),
		Measure: time.Since(start).Round(time.Millisecond).String(),
	}
}

// -----------------------------------------------------------------------------
// FR-8: metacubexd dist + mihomo API proxy
// -----------------------------------------------------------------------------

// handleMetaCubeXDRoot redirects /ui/metacubexd → the trailing-slash
// form so relative assets resolve.
func (s *Server) handleMetaCubeXDRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui/metacubexd/", http.StatusMovedPermanently)
}

// handleMetaCubeXD serves the metacubexd dist (FR-8.2) for
// authenticated users only (dev mode: no auth, DECISIONS D18/D19).
// Without the dist (not fetched) it answers 404 with a hint.
func (s *Server) handleMetaCubeXD(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	dir := s.monitor.UIDir
	if dir == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "metacubexd dist not present — run scripts/fetch-metacubexd.sh (DECISIONS D18: not vendored)",
		})
		return
	}
	if _, err := os.Stat(dir); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "metacubexd dist not present — run scripts/fetch-metacubexd.sh (DECISIONS D18: not vendored)",
		})
		return
	}

	// Serve like a static dir: strip the /ui/metacubexd/ prefix.
	// Directory traversal is prevented by path.Clean + the prefix
	// check (http.ServeFile rejects ".." segments on its own as well).
	rel := strings.TrimPrefix(r.URL.Path, "/ui/metacubexd/")
	rel = path.Clean("/" + rel) // absolute, ".." neutralized
	full := dir + rel
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		// Directory: index.html (matches the dist's own layout).
		full = full + "/index.html"
	}
	if strings.HasSuffix(full, ".html") {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, full)
}

// handleMihomoProxy forwards /api/mihomo/* to the mihomo
// external-controller (FR-8: the TZ 5.9 model — mihomo listens on
// loopback, the SPA cannot reach it directly, so WellBoard proxies;
// the Bearer secret is added server-side and NEVER sent to the
// browser). WebSocket upgrades (/traffic, /logs, /memory streams)
// are hijacked and piped bidirectionally.
func (s *Server) handleMihomoProxy(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if s.monitor.MihomoAPIAddr == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "mihomo external-controller not configured"})
		return
	}

	// The browser must NOT carry the secret: replace any client token
	// with the server-side one.
	r.Header.Del("Authorization")
	if s.monitor.MihomoAPISecret != "" {
		r.Header.Set("Authorization", "Bearer "+s.monitor.MihomoAPISecret)
	}

	target := &url.URL{Scheme: "http", Host: s.monitor.MihomoAPIAddr}

	// WebSocket: hijack and pipe both directions.
	if isUpgrade(r) {
		s.proxyWebSocket(w, r, target)
		return
	}

	// Plain HTTP: reverse proxy with the path rewritten.
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// Strip the /api/mihomo prefix: the upstream is the
			// external-controller ROOT (e.g. /version, /connections).
			rest := strings.TrimPrefix(r.URL.Path, "/api/mihomo")
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = singleJoiningSlash("", rest)
			req.Host = target.Host
		},
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": fmt.Sprintf("mihomo API unreachable: %v", err)})
	}
	proxy.ServeHTTP(w, r)
}

// isUpgrade reports a WebSocket handshake request.
func isUpgrade(r *http.Request) bool {
	for _, v := range r.Header.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
				for _, u := range r.Header.Values("Upgrade") {
					if strings.EqualFold(strings.TrimSpace(u), "websocket") {
						return true
					}
				}
			}
		}
	}
	return false
}

// proxyWebSocket dials the target with the WS handshake and pipes the
// raw TCP streams both ways (no stdlib websocket hijacking helpers
// needed — the handshake headers pass through as-is apart from Host).
func (s *Server) proxyWebSocket(w http.ResponseWriter, r *http.Request, target *url.URL) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server does not support connection hijacking"})
		return
	}
	// Dial the upstream as a raw TCP conn and replay the handshake.
	backend, err := net.DialTimeout("tcp", target.Host, 5*time.Second)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": fmt.Sprintf("mihomo API unreachable: %v", err)})
		return
	}
	// Rewrite the request line + Host for the upstream.
	up := r.Clone(r.Context())
	up.URL.Scheme = "ws"
	up.URL.Host = target.Host
	up.Host = target.Host
	// Absolute-form request URI for the handshake.
	if err := up.Write(backend); err != nil {
		backend.Close()
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": fmt.Sprintf("write ws handshake: %v", err)})
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		backend.Close()
		return
	}
	// The hijacked client conn may carry buffered bytes (rare for WS).
	go func() {
		if br := buf.Reader; br != nil && br.Buffered() > 0 {
			b := make([]byte, br.Buffered())
			_, _ = br.Read(b)
			_, _ = backend.Write(b)
		}
		_, _ = io.Copy(backend, client)
		backend.Close()
	}()
	go func() {
		_, _ = io.Copy(client, backend)
		client.Close()
	}()
}

// singleJoiningSlash joins two path fragments with exactly one slash
// ("" + "/version" → "/version").
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash && a != "" && b != "":
		return a + "/" + b
	default:
		return a + b
	}
}

// basicAuthChallenge writes a WWW-Authenticate header (unused in dev;
// kept for the phase 6 auth wiring).
func basicAuthChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="wellboard"`)
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
}
