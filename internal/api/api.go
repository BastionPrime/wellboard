// Package api hosts the WellBoard HTTP handlers. Phase 0/1 shipped the
// health endpoint; Phase 3 adds the REST CRUD surface over the store
// (FR-3.3, FR-4, FR-4.4, FR-9.1) and the template catalog (FR-5).
//
// Conventions:
//   - JSON bodies; unknown fields rejected (strict decoding) so typos
//     fail loudly instead of silently.
//   - Validation errors → 400 with a human-readable message.
//   - Referential conflicts (target lost, name collisions, unknown
//     references) → 409 with a message naming the object.
//   - Generator *Problems errors (lost targets, broken references,
//     FR-4.8) → 409 with the per-route problem list.
//   - IDs are minted server-side (srv_/grp_/rt_/sub_) — clients never
//     choose them (FR-3.4).
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"github.com/wellboard/wellboard/internal/templates"
)

// Store is the persistence contract (implemented by *store.Store).
type Store interface {
	Load() (*model.State, error)
	Save(st *model.State) error
}

// Server is the Phase 3/5 API: state CRUD + templates + profile
// preview + apply/rollback + logs + diagnostics + monitoring.
type Server struct {
	// Store persists the state.
	Store Store
	// Catalog is the loaded template catalog (templates/ dir).
	Catalog *templates.Catalog
	// overlayDir is the user template overlay dir (B1);
	// template write/delete handlers 503 when it is unset.
	overlayDir string
	// geositeDatPaths is the probed .dat file list for the geosite
	// tags endpoint (B3); nil = the default probes.
	geositeDatPaths []string
	// lanReader lists DHCP devices (FR-4.4); wired via SetLAN.
	lanReader LANReader
	// lanStatic applies static leases; wired via SetLAN.
	lanStatic SetStater
	// monitor carries the Phase 5 wiring (apply/logs/diagnostics/
	// metacubexd/mihomo proxy); nil fields disable the endpoints.
	monitor MonitorConfig
	// nikkiPaths is the file list scanned by the nikki subscription
	// import (A3); nil = nikki.DefaultPathList().
	nikkiPaths []string
	// mu serializes load-modify-save cycles (single-writer; the store
	// file is rewritten atomically but read-modify-write must not race).
	mu sync.Mutex

	// logf receives request-level diagnostics (never bodies).
	logf func(format string, args ...any)
}

// NewServer builds the API server. catalog may be nil (templates
// endpoints then return 503).
func NewServer(st Store, catalog *templates.Catalog) *Server {
	return &Server{Store: st, Catalog: catalog}
}

// SetOverlayDir wires the user template overlay directory (
// B1): template create/edit/delete write files there. Passing an
// empty string disables the write endpoints (503).
func (s *Server) SetOverlayDir(dir string) { s.overlayDir = dir }

// SetGeositeDatPaths overrides the .dat probe list of the geosite tags
// endpoint (B3; tests use fixture paths).
func (s *Server) SetGeositeDatPaths(paths []string) { s.geositeDatPaths = paths }

// ReloadCatalog re-runs templates.LoadMerged on the catalog's own dirs
// (shipped + overlay) and replaces s.Catalog. Called after every
// overlay write/delete so the merged view is always current.
func (s *Server) ReloadCatalog() error {
	if s.Catalog == nil {
		return nil
	}
	cat, err := templates.LoadMerged(s.Catalog.Dir, s.overlayDir)
	if err != nil {
		return err
	}
	s.Catalog = cat
	return nil
}

// SetLog sets the diagnostics logger.
func (s *Server) SetLog(f func(format string, args ...any)) { s.logf = f }

// SetNikkiPaths overrides the nikki config file list scanned by the
// subscription import (tests use fixture paths; production leaves it
// nil for nikki.DefaultPathList()).
func (s *Server) SetNikkiPaths(paths []string) { s.nikkiPaths = paths }

func (s *Server) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

// ----------------------------------------------------------------------------
// Shared plumbing
// ----------------------------------------------------------------------------

// errHTTP carries an HTTP status + message.
type errHTTP struct {
	status int
	msg    string
}

func (e *errHTTP) Error() string { return e.msg }

func httpErr(status int, format string, args ...any) error {
	return &errHTTP{status: status, msg: fmt.Sprintf(format, args...)}
}

// writeJSON writes v as a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError maps err onto a status code + JSON body. Generator
// *Problems (FR-4.8 lost targets) map to 409; validation to 400.
func writeError(w http.ResponseWriter, err error) {
	var eh *errHTTP
	if errors.As(err, &eh) {
		writeJSON(w, eh.status, map[string]any{"error": eh.msg})
		return
	}
	var prob *generator.Problems
	if errors.As(err, &prob) {
		var items []string
		items = append(items, prob.LostTargets...)
		items = append(items, prob.Invalid...)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    "state has unresolved references (routes with lost targets or broken objects)",
			"problems": items,
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

// decodeStrict decodes a JSON body into v, rejecting unknown fields.
func decodeStrict(r *http.Request, v any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1 MiB default cap (NFR hardening)
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return httpErr(http.StatusBadRequest, "invalid JSON body: %v", err)
	}
	return nil
}

// load locks the store mutex and returns the state. Callers must call
// Server.mu.Unlock (defer) — save re-uses the same lock.
func (s *Server) load() (*model.State, error) {
	s.mu.Lock()
	st, err := s.Store.Load()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	return st, nil
}

// save persists st; the caller must hold s.mu (defer s.unlock()).
func (s *Server) save(st *model.State) error {
	return s.Store.Save(st)
}

// unlock releases after a read-only handler.
func (s *Server) unlock() { s.mu.Unlock() }

// ----------------------------------------------------------------------------
// GET /api/v1/state — full snapshot (debug/UI bootstrap)
// ----------------------------------------------------------------------------

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	writeJSON(w, http.StatusOK, st)
}

// ----------------------------------------------------------------------------
// Sources
// ----------------------------------------------------------------------------

// sourceIn is the creation payload for a source.
func sourceIndex(st *model.State, id string) int {
	for i := range st.Sources {
		if st.Sources[i].ID == id {
			return i
		}
	}
	return -1
}

func existsServer(st *model.State, id string) bool {
	for i := range st.Servers {
		if st.Servers[i].ID == id {
			return true
		}
	}
	return false
}

func existsGroup(st *model.State, id string) bool {
	for i := range st.Groups {
		if st.Groups[i].ID == id {
			return true
		}
	}
	return false
}

// routesTargeting returns route ids+names whose target id equals targetID.
// Used for FR-1.5 delete guards.
func routesTargeting(st *model.State, targetID string) []string {
	var out []string
	for _, rt := range st.Routes {
		if rt.Target.ID == targetID && rt.Target.ID != "" {
			out = append(out, rt.ID+" ("+rt.Name+")")
		}
	}
	return out
}

func removeString(list []string, v string) []string {
	out := list[:0]
	for _, s := range list {
		if s != v {
			out = append(out, s)
		}
	}
	return out
}

// newID mints a collision-free id: prefix + "_" + <count+1>, falling
// back to suffix increments.
func newID(prefix string, st *model.State) string {
	n := 1
	for {
		id := fmt.Sprintf("%s_%d", prefix, n)
		taken := false
		for _, src := range st.Sources {
			if src.ID == id {
				taken = true
			}
		}
		for i := range st.Servers {
			if st.Servers[i].ID == id {
				taken = true
			}
		}
		for _, g := range st.Groups {
			if g.ID == id {
				taken = true
			}
		}
		for _, rt := range st.Routes {
			if rt.ID == id {
				taken = true
			}
		}
		if !taken {
			return id
		}
		n++
	}
}

// nextOrder returns max(route order)+10.
func nextOrder(st *model.State) int {
	max := 0
	for _, rt := range st.Routes {
		if rt.Order > max {
			max = rt.Order
		}
	}
	return max + 10
}

func validateOnUnavailable(v string) error {
	switch v {
	case "block", "direct":
		return nil
	}
	return httpErr(http.StatusBadRequest, "on_unavailable must be \"block\" or \"direct\"")
}

// isCIDR validates "ip[/mask]": an IP with an optional prefix length,
// which must fit the address family (≤32 for v4, ≤128 for v6). A bare
// IP is accepted (SRC-IP-CIDR /32 semantics).
func isCIDR(v string) bool {
	parts := strings.SplitN(v, "/", 2)
	addr, err := netip.ParseAddr(parts[0])
	if err != nil {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	bits, err := strconv.Atoi(parts[1])
	if err != nil || bits < 0 {
		return false
	}
	if addr.Is4() {
		return bits <= 32
	}
	return bits <= 128
}

// isPortOrRange validates "80", "443" or "100-200".
func isPortOrRange(v string) bool {
	parts := strings.SplitN(v, "-", 2)
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	if len(parts) == 2 {
		a, _ := strconv.Atoi(parts[0])
		b, _ := strconv.Atoi(parts[1])
		return a <= b
	}
	return true
}

// validateGeodataAdditionEntry checks one additions value by kind
// (B2): "geosite" entries must look like domain suffixes,
// "geoip" entries must be valid CIDRs (net.ParseCIDR — stricter than
// isCIDR because the additions are always explicit CIDR lists).
func validateGeodataAdditionEntry(kind, v string) error {
	if strings.ContainsAny(v, ",:\n\r") {
		return fmt.Errorf("value %q must not contain ',', ':' or newlines", v)
	}
	switch kind {
	case "geosite":
		if v == "" || strings.ContainsAny(v, " 	/") || strings.HasPrefix(v, ".") {
			return fmt.Errorf("value %q is not a valid domain suffix", v)
		}
		for _, r := range v {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			default:
				return fmt.Errorf("value %q is not a valid domain suffix", v)
			}
		}
		return nil
	case "geoip":
		if _, _, err := net.ParseCIDR(v); err != nil {
			return fmt.Errorf("value %q is not a valid CIDR", v)
		}
		return nil
	}
	return fmt.Errorf("unknown kind %q", kind)
}
