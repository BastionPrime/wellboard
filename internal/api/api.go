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
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wellboard/wellboard/internal/convert"
	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/subscription"
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
	// lanReader lists DHCP devices (FR-4.4); wired via SetLAN.
	lanReader LANReader
	// lanStatic applies static leases; wired via SetLAN.
	lanStatic SetStater
	// monitor carries the Phase 5 wiring (apply/logs/diagnostics/
	// metacubexd/mihomo proxy); nil fields disable the endpoints.
	monitor MonitorConfig
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

// SetLog sets the diagnostics logger.
func (s *Server) SetLog(f func(format string, args ...any)) { s.logf = f }

func (s *Server) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

// Register mounts all Phase 3 routes on mux (method-patterned; net/http
// answers other methods with 405).
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/state", s.handleState)
	mux.HandleFunc("GET /api/v1/sources", s.handleSourcesList)
	mux.HandleFunc("POST /api/v1/sources", s.handleSourcesCreate)
	mux.HandleFunc("GET /api/v1/sources/{id}", s.handleSourceGet)
	mux.HandleFunc("PATCH /api/v1/sources/{id}", s.handleSourcePatch)
	mux.HandleFunc("DELETE /api/v1/sources/{id}", s.handleSourceDelete)
	mux.HandleFunc("GET /api/v1/servers", s.handleServersList)
	mux.HandleFunc("POST /api/v1/servers", s.handleServersCreate)
	mux.HandleFunc("GET /api/v1/servers/{id}", s.handleServerGet)
	mux.HandleFunc("DELETE /api/v1/servers/{id}", s.handleServerDelete)
	mux.HandleFunc("GET /api/v1/groups", s.handleGroupsList)
	mux.HandleFunc("POST /api/v1/groups", s.handleGroupsCreate)
	mux.HandleFunc("GET /api/v1/groups/{id}", s.handleGroupGet)
	mux.HandleFunc("PATCH /api/v1/groups/{id}", s.handleGroupPatch)
	mux.HandleFunc("DELETE /api/v1/groups/{id}", s.handleGroupDelete)
	mux.HandleFunc("GET /api/v1/routes", s.handleRoutesList)
	mux.HandleFunc("POST /api/v1/routes", s.handleRoutesCreate)
	mux.HandleFunc("GET /api/v1/routes/{id}", s.handleRouteGet)
	mux.HandleFunc("PATCH /api/v1/routes/{id}", s.handleRoutePatch)
	mux.HandleFunc("DELETE /api/v1/routes/{id}", s.handleRouteDelete)
	mux.HandleFunc("GET /api/v1/templates", s.handleTemplatesList)
	mux.HandleFunc("GET /api/v1/templates/{id}", s.handleTemplatesGet)
	mux.HandleFunc("POST /api/v1/templates/{id}/apply", s.handleTemplateApply)
	mux.HandleFunc("GET /api/v1/lan-devices", s.handleLANDevices)
	mux.HandleFunc("POST /api/v1/lan-devices/static", s.handleLANStatic)
	mux.HandleFunc("GET /api/v1/settings", s.handleSettingsGet)
	mux.HandleFunc("PATCH /api/v1/settings", s.handleSettingsPatch)
	mux.HandleFunc("GET /api/v1/profile", s.handleProfilePreview)
	s.registerMonitor(mux)
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
type sourceIn struct {
	Kind              string `json:"kind"`
	Name              string `json:"name"`
	URL               string `json:"url"`
	Enabled           *bool  `json:"enabled"`
	UpdateIntervalSec *int   `json:"update_interval_sec"`
}

func (s *Server) handleSourcesList(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	writeJSON(w, http.StatusOK, map[string]any{"sources": st.Sources})
}

func (s *Server) handleSourceGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	for i := range st.Sources {
		if st.Sources[i].ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, st.Sources[i])
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "source not found"})
}

func (s *Server) handleSourcesCreate(w http.ResponseWriter, r *http.Request) {
	var in sourceIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	if in.Name == "" {
		writeError(w, httpErr(http.StatusBadRequest, "name is required"))
		return
	}
	switch in.Kind {
	case "subscription":
		if in.URL == "" || !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
			writeError(w, httpErr(http.StatusBadRequest, "subscription source requires an http(s) url"))
			return
		}
	case "manual":
		in.URL = ""
	default:
		writeError(w, httpErr(http.StatusBadRequest, "kind must be \"subscription\" or \"manual\""))
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	src := model.Source{
		ID: newID("sub", st), Kind: in.Kind, Name: in.Name, URL: in.URL,
		Enabled: enabled,
	}
	if in.UpdateIntervalSec != nil {
		if *in.UpdateIntervalSec < 60 {
			writeError(w, httpErr(http.StatusBadRequest, "update_interval_sec must be ≥ 60"))
			return
		}
		src.UpdateIntervalSec = *in.UpdateIntervalSec
	}
	st.Sources = append(st.Sources, src)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("source %s created", src.ID)
	writeJSON(w, http.StatusCreated, src)
}

// sourcePatch is the mutable subset of a source.
type sourcePatch struct {
	Name              *string `json:"name"`
	URL               *string `json:"url"`
	Enabled           *bool   `json:"enabled"`
	UpdateIntervalSec *int    `json:"update_interval_sec"`
}

func (s *Server) handleSourcePatch(w http.ResponseWriter, r *http.Request) {
	var in sourcePatch
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	idx := sourceIndex(st, r.PathValue("id"))
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "source not found"})
		return
	}
	src := &st.Sources[idx]
	if in.Name != nil {
		if *in.Name == "" {
			writeError(w, httpErr(http.StatusBadRequest, "name must not be empty"))
			return
		}
		src.Name = *in.Name
	}
	if in.URL != nil {
		if src.Kind != string(model.SourceSubscription) {
			writeError(w, httpErr(http.StatusBadRequest, "manual sources have no url"))
			return
		}
		if *in.URL != "" && !strings.HasPrefix(*in.URL, "http://") && !strings.HasPrefix(*in.URL, "https://") {
			writeError(w, httpErr(http.StatusBadRequest, "url must be http(s)"))
			return
		}
		src.URL = *in.URL
	}
	if in.Enabled != nil {
		src.Enabled = *in.Enabled
	}
	if in.UpdateIntervalSec != nil {
		if *in.UpdateIntervalSec != 0 && *in.UpdateIntervalSec < 60 {
			writeError(w, httpErr(http.StatusBadRequest, "update_interval_sec must be 0 (auto) or ≥ 60"))
			return
		}
		src.UpdateIntervalSec = *in.UpdateIntervalSec
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st.Sources[idx])
}

func (s *Server) handleSourceDelete(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	id := r.PathValue("id")
	idx := sourceIndex(st, id)
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "source not found"})
		return
	}
	// FR-1.5: report which routes/groups lose their target BEFORE deleting.
	affected := routesTargeting(st, id)
	if len(affected) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "source has routes depending on it; delete or reassign them first",
			"routes": affected,
		})
		return
	}
	st.Sources = append(st.Sources[:idx], st.Sources[idx+1:]...)
	// Drop the source's servers from the pool.
	servers := st.Servers[:0]
	for _, srv := range st.Servers {
		if srv.SourceID != id {
			servers = append(servers, srv)
		}
	}
	st.Servers = servers
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("source %s deleted", id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ----------------------------------------------------------------------------
// Servers (read/delete; creation via POST /servers parses share links —
// FR-1.3 — into the manual source, or comes from subscriptions)
// ----------------------------------------------------------------------------

func (s *Server) handleServersList(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	writeJSON(w, http.StatusOK, map[string]any{"servers": st.Servers})
}

// serverIn is the manual-server creation payload (FR-1.3): one or more
// share links (vless://, trojan://, ss://, vmess://, hysteria2://,
// tuic://) pasted as text. The mihomo converter parses them (policy
// C1/C2: never a hand-written parser).
type serverIn struct {
	Links string `json:"links"`
}

// manualSourceID returns the manual source id, creating the source on
// first use (FR-1.3: manual servers live in the built-in bucket).
func manualSourceID(st *model.State) string {
	for _, src := range st.Sources {
		if src.Kind == string(model.SourceManual) {
			return src.ID
		}
	}
	id := newID("sub", st)
	st.Sources = append(st.Sources, model.Source{
		ID: id, Kind: string(model.SourceManual), Name: "Manual", Enabled: true,
	})
	return id
}

func (s *Server) handleServersCreate(w http.ResponseWriter, r *http.Request) {
	var in serverIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(in.Links) == "" {
		writeError(w, httpErr(http.StatusBadRequest, "links is required (paste vless://, trojan://, ss://, vmess://, hysteria2:// or tuic:// share links)"))
		return
	}
	proxies, err := convert.Links([]byte(in.Links))
	if err != nil {
		writeError(w, httpErr(http.StatusBadRequest, "could not parse links: %v", err))
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	srcID := manualSourceID(st)
	res := subscription.Merge(st, srcID, proxies)
	if res.Added+res.Updated == 0 {
		writeError(w, httpErr(http.StatusConflict, "no usable proxies parsed from links"))
		return
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	// Report the resulting manual-source servers (merge matched by
	// name/server/port — updated entries keep their IDs, FR-3.4).
	out := make([]model.Server, 0, res.Added+res.Updated)
	for _, srv := range st.Servers {
		if srv.SourceID == srcID {
			out = append(out, srv)
		}
	}
	s.log("servers created via manual links: %d added, %d updated (source %s)", res.Added, res.Updated, srcID)
	writeJSON(w, http.StatusCreated, map[string]any{"servers": out})
}

func (s *Server) handleServerGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	for i := range st.Servers {
		if st.Servers[i].ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, st.Servers[i])
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
}

func (s *Server) handleServerDelete(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	id := r.PathValue("id")
	idx := -1
	for i := range st.Servers {
		if st.Servers[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}
	if affected := routesTargeting(st, id); len(affected) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "server is a route target; delete or reassign those routes first",
			"routes": affected,
		})
		return
	}
	// Remove from groups too.
	for gi := range st.Groups {
		st.Groups[gi].Members = removeString(st.Groups[gi].Members, id)
	}
	st.Servers = append(st.Servers[:idx], st.Servers[idx+1:]...)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ----------------------------------------------------------------------------
// Groups (FR-3.3)
// ----------------------------------------------------------------------------

// groupIn is the create/update payload for a proxy group.
type groupIn struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Members []string `json:"members"`
}

func (s *Server) handleGroupsList(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	writeJSON(w, http.StatusOK, map[string]any{"groups": st.Groups})
}

func (s *Server) handleGroupGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	for i := range st.Groups {
		if st.Groups[i].ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, st.Groups[i])
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
}

func validateGroupIn(st *model.State, in groupIn, selfID string) (model.Group, error) {
	if in.Name == "" {
		return model.Group{}, httpErr(http.StatusBadRequest, "name is required")
	}
	if strings.HasPrefix(in.Name, generator.ServiceGroupPrefix) {
		return model.Group{}, httpErr(http.StatusBadRequest,
			"group names starting with %q are reserved", generator.ServiceGroupPrefix)
	}
	switch model.GroupType(in.Type) {
	case model.GroupSelect, model.GroupURLTest, model.GroupFallback, model.GroupLoadBalance:
	default:
		return model.Group{}, httpErr(http.StatusBadRequest,
			"type must be one of select, url-test, fallback, load-balance")
	}
	if len(in.Members) == 0 {
		return model.Group{}, httpErr(http.StatusBadRequest, "at least one member is required")
	}
	for _, m := range in.Members {
		if m == selfID {
			return model.Group{}, httpErr(http.StatusBadRequest, "group cannot contain itself")
		}
		if !existsServer(st, m) && !existsGroup(st, m) {
			return model.Group{}, httpErr(http.StatusBadRequest,
				"member %q is neither a known server nor group id", m)
		}
	}
	return model.Group{Name: in.Name, Type: model.GroupType(in.Type), Members: in.Members}, nil
}

func (s *Server) handleGroupsCreate(w http.ResponseWriter, r *http.Request) {
	var in groupIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	g, err := validateGroupIn(st, in, "")
	if err != nil {
		writeError(w, err)
		return
	}
	g.ID = newID("grp", st)
	st.Groups = append(st.Groups, g)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("group %s created", g.ID)
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleGroupPatch(w http.ResponseWriter, r *http.Request) {
	var in groupIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	idx := -1
	for i := range st.Groups {
		if st.Groups[i].ID == r.PathValue("id") {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		return
	}
	if in.Name == "" {
		writeError(w, httpErr(http.StatusBadRequest, "name is required"))
		return
	}
	if strings.HasPrefix(in.Name, generator.ServiceGroupPrefix) {
		writeError(w, httpErr(http.StatusBadRequest, "reserved prefix"))
		return
	}
	st.Groups[idx].Name = in.Name
	st.Groups[idx].Type = model.GroupType(in.Type)
	st.Groups[idx].Members = in.Members
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st.Groups[idx])
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	id := r.PathValue("id")
	idx := -1
	for i := range st.Groups {
		if st.Groups[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		return
	}
	if affected := routesTargeting(st, id); len(affected) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "group is a route target; delete or reassign those routes first",
			"routes": affected,
		})
		return
	}
	// Remove from other groups' members.
	for gi := range st.Groups {
		if st.Groups[gi].ID != id {
			st.Groups[gi].Members = removeString(st.Groups[gi].Members, id)
		}
	}
	st.Groups = append(st.Groups[:idx], st.Groups[idx+1:]...)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ----------------------------------------------------------------------------
// Routes (FR-4)
// ----------------------------------------------------------------------------

// routeIn is the create/update payload for a route.
type routeIn struct {
	Name          string                 `json:"name"`
	Enabled       *bool                  `json:"enabled"`
	Order         *int                   `json:"order"`
	Conditions    []model.RouteCondition `json:"conditions"`
	Target        *model.Target          `json:"target"`
	OnUnavailable *string                `json:"on_unavailable"`
	Providers     []string               `json:"providers"`
}

// validateRouteIn checks the route payload before it is stored.
// Provider references are checked against the catalog's known set when
// available (unknown provider → 400 naming it, review follow-up);
// without a catalog the generator-time check still guards /profile.
func validateRouteIn(st *model.State, in routeIn, knownProviders map[string]bool) error {
	if in.Name == "" {
		return httpErr(http.StatusBadRequest, "name is required")
	}
	if len(in.Conditions) == 0 && len(in.Providers) == 0 {
		return httpErr(http.StatusBadRequest, "at least one condition (or provider) is required")
	}
	seenCond := map[string]bool{}
	for _, c := range in.Conditions {
		key := string(c.Type) + "\x00" + c.Value
		if seenCond[key] {
			return httpErr(http.StatusBadRequest, "duplicate condition %s %q", c.Type, c.Value)
		}
		seenCond[key] = true
		switch c.Type {
		case model.CondDomain, model.CondDomainSuffix, model.CondDomainKeyword,
			model.CondGeosite, model.CondGeoIP, model.CondIPCIDR,
			model.CondSrcDevice, model.CondDstPort:
			if c.Value == "" {
				return httpErr(http.StatusBadRequest, "condition %s has empty value", c.Type)
			}
		default:
			return httpErr(http.StatusBadRequest, "unknown condition type %q", c.Type)
		}
		if c.Type == model.CondIPCIDR || c.Type == model.CondSrcDevice {
			if !isCIDR(c.Value) {
				return httpErr(http.StatusBadRequest,
					"condition %s value %q is not a valid ip/cidr", c.Type, c.Value)
			}
		}
		if c.Type == model.CondDomainSuffix || c.Type == model.CondDomain {
			if strings.ContainsAny(c.Value, " \t/") || strings.HasPrefix(c.Value, ".") {
				return httpErr(http.StatusBadRequest, "bad domain %q", c.Value)
			}
		}
		if c.Type == model.CondDstPort {
			if !isPortOrRange(c.Value) {
				return httpErr(http.StatusBadRequest, "bad dst-port %q", c.Value)
			}
		}
	}
	for _, p := range in.Providers {
		for _, r := range p {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			default:
				return httpErr(http.StatusBadRequest, "bad provider name %q", p)
			}
		}
		if knownProviders != nil && !knownProviders[p] {
			return httpErr(http.StatusBadRequest,
				"provider %q is not in the template catalog (known: %s)", p, knownProviderList(knownProviders))
		}
	}
	if in.Target == nil {
		return httpErr(http.StatusBadRequest, "target is required")
	}
	return validateTarget(st, *in.Target)
}

// validateTarget checks that the target resolves in st (server/group
// must exist and be usable — not stale/disabled-source).
func validateTarget(st *model.State, t model.Target) error {
	switch t.Type {
	case model.TargetDirect, model.TargetReject:
		return nil
	case model.TargetServer:
		for i := range st.Servers {
			if st.Servers[i].ID == t.ID {
				if st.Servers[i].Stale {
					return httpErr(http.StatusConflict, "target server %s is stale (lost from its subscription)", t.ID)
				}
				return nil
			}
		}
		return httpErr(http.StatusConflict, "target server %q not found", t.ID)
	case model.TargetGroup:
		if existsGroup(st, t.ID) {
			return nil
		}
		return httpErr(http.StatusConflict, "target group %q not found", t.ID)
	}
	return httpErr(http.StatusBadRequest, "unknown target type %q", t.Type)
}

func (s *Server) handleRoutesList(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	writeJSON(w, http.StatusOK, map[string]any{"routes": st.Routes})
}

func (s *Server) handleRouteGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	for i := range st.Routes {
		if st.Routes[i].ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, st.Routes[i])
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "route not found"})
}

// knownProviderList renders the sorted known provider names for error
// messages.
func knownProviderList(m map[string]bool) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (s *Server) handleRoutesCreate(w http.ResponseWriter, r *http.Request) {
	var in routeIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	if err := validateRouteIn(st, in, s.knownProviders()); err != nil {
		writeError(w, err)
		return
	}
	rt := model.Route{
		ID: newID("rt", st), Name: in.Name, Enabled: true, Order: nextOrder(st),
		Conditions: in.Conditions, Target: *in.Target, OnUnavailable: "block",
		Providers: in.Providers,
	}
	if in.Enabled != nil {
		rt.Enabled = *in.Enabled
	}
	if in.Order != nil {
		rt.Order = *in.Order
	}
	if in.OnUnavailable != nil {
		if err := validateOnUnavailable(*in.OnUnavailable); err != nil {
			writeError(w, err)
			return
		}
		rt.OnUnavailable = *in.OnUnavailable
	}
	st.Routes = append(st.Routes, rt)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("route %s created", rt.ID)
	writeJSON(w, http.StatusCreated, rt)
}

func (s *Server) handleRoutePatch(w http.ResponseWriter, r *http.Request) {
	var in routeIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	idx := -1
	for i := range st.Routes {
		if st.Routes[i].ID == r.PathValue("id") {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "route not found"})
		return
	}
	if in.Name == "" {
		writeError(w, httpErr(http.StatusBadRequest, "name is required"))
		return
	}
	if in.Conditions == nil && in.Providers == nil && in.Target == nil {
		writeError(w, httpErr(http.StatusBadRequest, "nothing to update"))
		return
	}
	rt := &st.Routes[idx]
	rt.Name = in.Name
	if in.Conditions != nil {
		rt.Conditions = in.Conditions
	}
	if in.Providers != nil {
		rt.Providers = in.Providers
	}
	if in.Target != nil {
		rt.Target = *in.Target
	}
	if in.Enabled != nil {
		rt.Enabled = *in.Enabled
	}
	if in.Order != nil {
		rt.Order = *in.Order
	}
	if in.OnUnavailable != nil {
		if err := validateOnUnavailable(*in.OnUnavailable); err != nil {
			writeError(w, err)
			return
		}
		rt.OnUnavailable = *in.OnUnavailable
	}
	// Full re-validation of the merged route (FR-4.8 at write time).
	merged := routeIn{
		Name: rt.Name, Conditions: rt.Conditions, Target: &rt.Target,
		Providers: rt.Providers, OnUnavailable: &rt.OnUnavailable,
	}
	if err := validateRouteIn(st, merged, s.knownProviders()); err != nil {
		writeError(w, err)
		return
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st.Routes[idx])
}

func (s *Server) handleRouteDelete(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	id := r.PathValue("id")
	idx := -1
	for i := range st.Routes {
		if st.Routes[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "route not found"})
		return
	}
	st.Routes = append(st.Routes[:idx], st.Routes[idx+1:]...)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ----------------------------------------------------------------------------
// Templates (FR-5)
// ----------------------------------------------------------------------------

func (s *Server) handleTemplatesList(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": s.Catalog.Templates})
}

func (s *Server) handleTemplatesGet(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	t, ok := s.Catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// applyIn is the template-apply request: the chosen target (FR-5.1).
type applyIn struct {
	Target model.Target `json:"target"`
}

// handleTemplateApply creates a route from the template with the user's
// target (FR-5.1/FR-5.2). The "all-vpn" template instead sets the
// default policy (it has no conditions).
func (s *Server) handleTemplateApply(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	t, ok := s.Catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
		return
	}
	var in applyIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	if err := validateTarget(st, in.Target); err != nil {
		writeError(w, err)
		return
	}
	if t.ID == "all-vpn" {
		// No-condition template: it changes the default policy instead
		// of creating a route (Appendix В).
		st.Settings.DefaultPolicy = in.Target
		if err := s.save(st); err != nil {
			writeError(w, err)
			return
		}
		s.log("template %s applied (default policy)", t.ID)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "applied", "mode": "default-policy", "template": t.ID,
		})
		return
	}
	rt := model.Route{
		ID: newID("rt", st), Name: t.Name, Enabled: true, Order: nextOrder(st),
		Conditions: t.Conditions, Target: in.Target, OnUnavailable: "block",
		Providers: t.Providers,
	}
	st.Routes = append(st.Routes, rt)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("template %s applied as route %s", t.ID, rt.ID)
	writeJSON(w, http.StatusCreated, rt)
}

// ----------------------------------------------------------------------------
// LAN devices (FR-4.4)
// ----------------------------------------------------------------------------

// LANReader abstracts the lease source list.
type LANReader interface {
	Devices() ([]model.LANDevice, error)
}

// SetStater applies a static lease (UCI on the router; dev stub).
type SetStater interface {
	SetStatic(dev model.LANDevice) error
}

// LAN wires the reader + static-lease writer into the API.
func (s *Server) SetLAN(reader LANReader, static SetStater) {
	s.lanReader = reader
	s.lanStatic = static
}

func (s *Server) handleLANDevices(w http.ResponseWriter, r *http.Request) {
	if s.lanReader == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LAN device listing unavailable"})
		return
	}
	devs, err := s.lanReader.Devices()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

// staticIn requests a static lease for one device.
type staticIn struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}

func (s *Server) handleLANStatic(w http.ResponseWriter, r *http.Request) {
	if s.lanStatic == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "static leases unavailable"})
		return
	}
	var in staticIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	dev := model.LANDevice{MAC: in.MAC, IP: in.IP, Hostname: in.Hostname}
	err := s.lanStatic.SetStatic(dev)
	if err != nil && errors.Is(err, errDevStubSentinel) {
		// Dev mode: documented stub — success-but-simulated.
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "simulated", "detail": err.Error(),
		})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

// ----------------------------------------------------------------------------
// Settings (FR-9.1)
// ----------------------------------------------------------------------------

// settingsPatch is the mutable settings subset.
type settingsPatch struct {
	UIPort               *int          `json:"ui_port"`
	Lang                 *string       `json:"lang"`
	Geodata              *string       `json:"geodata"`
	DefaultPolicy        *model.Target `json:"default_policy"`
	DelayTestIntervalSec *int          `json:"delay_test_interval_sec"`
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	writeJSON(w, http.StatusOK, st.Settings)
}

func (s *Server) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var in settingsPatch
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	set := &st.Settings
	if in.UIPort != nil {
		if *in.UIPort < 1 || *in.UIPort > 65535 {
			writeError(w, httpErr(http.StatusBadRequest, "ui_port must be 1-65535"))
			return
		}
		set.UIPort = *in.UIPort
	}
	if in.Lang != nil {
		switch *in.Lang {
		case "ru", "en":
			set.Lang = *in.Lang
		default:
			writeError(w, httpErr(http.StatusBadRequest, "lang must be \"ru\" or \"en\""))
			return
		}
	}
	if in.Geodata != nil {
		switch *in.Geodata {
		case "runetfreedom", "metacubex":
			set.Geodata = *in.Geodata
		default:
			writeError(w, httpErr(http.StatusBadRequest, "geodata must be \"runetfreedom\" or \"metacubex\""))
			return
		}
	}
	if in.DefaultPolicy != nil {
		if err := validateTarget(st, *in.DefaultPolicy); err != nil {
			writeError(w, err)
			return
		}
		set.DefaultPolicy = *in.DefaultPolicy
	}
	if in.DelayTestIntervalSec != nil {
		if *in.DelayTestIntervalSec < 0 {
			writeError(w, httpErr(http.StatusBadRequest, "delay_test_interval_sec must be ≥ 0"))
			return
		}
		set.DelayTestIntervalSec = *in.DelayTestIntervalSec
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

// ----------------------------------------------------------------------------
// Profile preview (FR-4.9)
// ----------------------------------------------------------------------------

// handleProfilePreview renders the current state as the generated
// mihomo profile (read-only, for the UI debug view). Generator
// *Problems (lost targets, unknown providers) surface as 409 with the
// problem list.
func (s *Server) handleProfilePreview(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	out, err := generator.GenerateWithProviders(st, s.knownProviders())
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// knownProviders returns the set of provider names with a local payload
// file in the loaded template catalog (nil = check disabled, catalog
// unavailable in dev mode). Route provider references are validated
// against this set during generation (review follow-up: unknown
// provider must surface, not silently 200).
func (s *Server) knownProviders() map[string]bool {
	if s.Catalog == nil {
		return nil
	}
	m := make(map[string]bool, len(s.Catalog.Templates))
	for _, tpl := range s.Catalog.Templates {
		for _, p := range tpl.Providers {
			m[p] = true
		}
	}
	return m
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// errDevStubSentinel matches the lan dev-stub error without importing
// the lan package here (avoided a dependency cycle: lan imports nothing
// from api; matching by variable identity is enough — set by SetLAN).
var errDevStubSentinel error

// SetDevStubSentinel wires the lan.ErrDevStub identity into the server.
func SetDevStubSentinel(err error) { errDevStubSentinel = err }

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
