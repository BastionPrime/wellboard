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
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wellboard/wellboard/internal/convert"
	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/geodata"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
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

// Register mounts all Phase 3 routes on mux (method-patterned; net/http
// answers other methods with 405).
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/state", s.handleState)
	mux.HandleFunc("GET /api/v1/sources", s.handleSourcesList)
	mux.HandleFunc("POST /api/v1/sources", s.handleSourcesCreate)
	mux.HandleFunc("POST /api/v1/sources/import-nikki", s.handleSourcesImportNikki)
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
	// External nikki rules: read-only view of the active
	// mihomo config + explicit per-rule import into WellBoard state.
	mux.HandleFunc("GET /api/v1/external-rules", s.handleExternalRulesList)
	mux.HandleFunc("POST /api/v1/external-rules/import", s.handleExternalRuleImport)
	mux.HandleFunc("POST /api/v1/external-rules/import-all", s.handleExternalRulesImportAll)
	mux.HandleFunc("GET /api/v1/templates", s.handleTemplatesList)
	mux.HandleFunc("POST /api/v1/templates", s.handleTemplatesCreate)
	mux.HandleFunc("GET /api/v1/templates/{id}", s.handleTemplatesGet)
	mux.HandleFunc("PUT /api/v1/templates/{id}", s.handleTemplatesUpdate)
	mux.HandleFunc("DELETE /api/v1/templates/{id}", s.handleTemplatesDelete)
	mux.HandleFunc("POST /api/v1/templates/{id}/toggle", s.handleTemplatesToggle)
	mux.HandleFunc("POST /api/v1/templates/{id}/apply", s.handleTemplateApply)
	mux.HandleFunc("GET /api/v1/geodata/tags", s.handleGeodataTags)
	mux.HandleFunc("GET /api/v1/lan-devices", s.handleLANDevices)
	mux.HandleFunc("POST /api/v1/lan-devices/static", s.handleLANStatic)
	mux.HandleFunc("GET /api/v1/settings", s.handleSettingsGet)
	mux.HandleFunc("PATCH /api/v1/settings", s.handleSettingsPatch)
	mux.HandleFunc("GET /api/v1/profile", s.handleProfilePreview)
	mux.HandleFunc("GET /api/v1/export", s.handleExport)
	mux.HandleFunc("POST /api/v1/import", s.handleImport)
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

// ----------------------------------------------------------------------------
// Sources: nikki import (A3)
// ----------------------------------------------------------------------------

// importNikkiIn is the import request body. An empty object works;
// name optionally overrides the default "nikki N" source naming.
type importNikkiIn struct {
	Name string `json:"name"`
}

// handleSourcesImportNikki discovers subscription URLs in the nikki
// mihomo config (read-only scan; WellBoard never writes /etc/nikki)
// and creates a subscription source for every URL not already present
// (match by URL). Never prints the discovered URLs — logs carry
// counts only (secret rule).
func (s *Server) handleSourcesImportNikki(w http.ResponseWriter, r *http.Request) {
	var in importNikkiIn
	// Body may be empty (Content-Length 0) — decodeStrict on an empty
	// stream errors, so only decode when a body is present.
	if r.ContentLength > 0 {
		if err := decodeStrict(r, &in, 0); err != nil {
			writeError(w, err)
			return
		}
	}
	paths := s.nikkiPaths
	if paths == nil {
		paths = nikki.DefaultPathList()
	}
	urls, skipped := nikki.DiscoverSubscriptionURLs(paths)
	if skipped > 0 {
		s.log("nikki import: %d unreadable config files skipped", skipped)
	}

	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	existing := map[string]bool{}
	for _, src := range st.Sources {
		existing[src.URL] = true
	}
	imported := 0
	for i, u := range urls {
		if existing[u] {
			continue
		}
		name := in.Name
		if name == "" {
			name = fmt.Sprintf("nikki %d", i+1)
		} else if i > 0 {
			name = fmt.Sprintf("%s %d", name, i+1)
		}
		src := model.Source{
			ID: newID("sub", st), Kind: string(model.SourceSubscription),
			Name: name, URL: u, Enabled: true,
		}
		st.Sources = append(st.Sources, src)
		imported++
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("nikki import: %d sources created (%d urls found)", imported, len(urls))
	// The response carries MASKED rows only: a new API surface must not
	// leak the subscription URL (secret rule; review fix on A3
	// carried into).
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"found":    len(urls),
		"sources":  sourcesSummary(st.Sources),
	})
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
		// Rule-line safety: values land in comma-separated mihomo rule
		// lines; ',' ':' or newlines would inject segments/structure
		// (security audit Phase 7; the generator enforces the same).
		if strings.ContainsAny(c.Value, ",:\n\r") {
			return httpErr(http.StatusBadRequest,
				"condition %s value %q must not contain ',', ':' or newlines", c.Type, c.Value)
		}
		if c.Type == model.CondIPCIDR || c.Type == model.CondSrcDevice {
			if !isCIDR(c.Value) {
				return httpErr(http.StatusBadRequest,
					"condition %s value %q is not a valid ip/cidr", c.Type, c.Value)
			}
		}
		if c.Type == model.CondDomainSuffix || c.Type == model.CondDomain {
			if strings.ContainsAny(c.Value, " 	/") || strings.HasPrefix(c.Value, ".") {
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
// External nikki rules API (read-only view, explicit import)
// ----------------------------------------------------------------------------

// ExternalRule mirrors nikki.ExternalRule over the wire. Source is
// always /etc/nikki/run/config.yaml (the file mihomo actually reads).
type externalRulesView struct {
	Source     string            `json:"source"`
	Count      int               `json:"count"`
	Targets    []string          `json:"targets"`
	Rules      []nikkiExternal   `json:"rules"`
	Importable map[string]string `json:"importable_types"`
	Warning    string            `json:"warning,omitempty"`
}

type nikkiExternal struct {
	Index     int    `json:"index"`
	Type      string `json:"type"`
	Value     string `json:"value,omitempty"`
	Target    string `json:"target"`
	NoResolve bool   `json:"no_resolve,omitempty"`
	Raw       string `json:"raw"`
	// Importable reports whether this line's condition+target can be
	// expressed as a WellBoard route (see conditionFromRuleType).
	Importable bool `json:"importable"`
}

// importableRuleTypes maps mihomo rule types onto WellBoard condition
// types. Types outside this map (PROCESS-NAME, MATCH, logic rules) are
// shown read-only and marked not importable.
var importableRuleTypes = map[string]model.RouteConditionType{
	"DOMAIN":         model.CondDomain,
	"DOMAIN-SUFFIX":  model.CondDomainSuffix,
	"DOMAIN-KEYWORD": model.CondDomainKeyword,
	"GEOSITE":        model.CondGeosite,
	"GEOIP":          model.CondGeoIP,
	"IP-CIDR":        model.CondIPCIDR,
	"IP-CIDR6":       model.CondIPCIDR,
	"SRC-IP-CIDR":    model.CondSrcDevice,
	"DST-PORT":       model.CondDstPort,
}

// handleExternalRulesList serves the read-only external rule view.
// It never writes: the handler takes no lock on the store (nothing to
// save) and the source file is opened read-only (design invariant).
func (s *Server) handleExternalRulesList(w http.ResponseWriter, r *http.Request) {
	rules, targets, err := nikki.LoadExternalRules()
	if err != nil {
		// The tab must stay usable: report the failure as a warning
		// field, not a 500 that kills the whole Routes view.
		out := externalRulesView{
			Source: nikki.RunConfigPath, Warning: err.Error(),
			Importable: importableTypeNames(),
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	out := externalRulesView{
		Source:     nikki.RunConfigPath,
		Count:      len(rules),
		Targets:    targets,
		Rules:      make([]nikkiExternal, len(rules)),
		Importable: importableTypeNames(),
	}
	for i, ru := range rules {
		_, importable := importableRuleTypes[ru.Type]
		if ru.Type == "MATCH" {
			importable = false
		}
		out.Rules[i] = nikkiExternal{
			Index: ru.Index, Type: ru.Type, Value: ru.Value, Target: ru.Target,
			NoResolve: ru.NoResolve, Raw: ru.Raw, Importable: importable,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func importableTypeNames() map[string]string {
	m := make(map[string]string, len(importableRuleTypes))
	for k, v := range importableRuleTypes {
		m[k] = string(v)
	}
	return m
}

// ruleImportIn is the import request: the rule line to copy into a
// WellBoard route. The line is re-parsed from the source file — the
// client cannot smuggle a condition that is not actually live.
type ruleImportIn struct {
	// Rule is the raw rule line exactly as shown by GET /external-rules.
	Rule string `json:"rule"`
	// Name is the route name; default = "nikki: <TYPE> <value>".
	Name string `json:"name"`
	// Target maps the nikki policy segment onto a WellBoard target.
	// DIRECT -> direct, REJECT -> reject, otherwise a group/server
	// name to resolve in state (accepted by id in Target.ID).
	Target model.Target `json:"target"`
}

// handleExternalRuleImport copies ONE external rule into WellBoard
// state as a route (explicit, never silent,
// never automatic). Default disabled: enabled=false until the owner
// turns it on. The external source is never modified.
func (s *Server) handleExternalRuleImport(w http.ResponseWriter, r *http.Request) {
	var in ruleImportIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(in.Rule) == "" {
		writeError(w, httpErr(http.StatusBadRequest, "rule is required"))
		return
	}
	// The rule must exist in the live external set, verbatim.
	rules, _, err := nikki.LoadExternalRules()
	if err != nil {
		writeError(w, httpErr(http.StatusServiceUnavailable, "external rules unavailable: %v", err))
		return
	}
	var match *nikki.ExternalRule
	for i := range rules {
		if rules[i].Raw == strings.TrimSpace(in.Rule) {
			match = &rules[i]
			break
		}
	}
	if match == nil {
		writeError(w, httpErr(http.StatusConflict,
			"rule %q is not present in %s (stale view? re-fetch the tab)", in.Rule, nikki.RunConfigPath))
		return
	}
	condType, ok := importableRuleTypes[match.Type]
	if !ok {
		writeError(w, httpErr(http.StatusBadRequest,
			"rule type %s cannot be expressed as a WellBoard route (view only)", match.Type))
		return
	}
	cond, err := conditionFromRule(match, condType)
	if err != nil {
		writeError(w, err)
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	// Map the external policy onto a WellBoard target when the caller
	// did not override it: DIRECT/REJECT map to built-ins, named
	// policies resolve by display name against WellBoard groups/servers
	// in state; unknown names are rejected with a clear message.
	target := in.Target
	if target.Type == "" {
		target, _ = targetFromPolicy(match.Target)
	}
	if target.Type == "" || (target.ID == "" && (target.Type == model.TargetServer || target.Type == model.TargetGroup)) {
		if t2, ok := serverOrGroupByName(st, match.Target); ok {
			target = t2
		}
	}
	if target.Type == "" {
		writeError(w, httpErr(http.StatusBadRequest,
			"external policy %q is not a WellBoard server/group yet; create it first or pass an explicit target", match.Target))
		return
	}
	if err := validateTarget(st, target); err != nil {
		writeError(w, err)
		return
	}
	name := in.Name
	if name == "" {
		name = defaultImportName(match)
	}
	rt := model.Route{
		ID: newID("rt", st), Name: name, Enabled: false, Order: nextOrder(st),
		Conditions: []model.RouteCondition{cond}, Target: target, OnUnavailable: "block",
	}
	st.Routes = append(st.Routes, rt)
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("external rule imported as route %s (from %s)", rt.ID, nikki.RunConfigPath)
	writeJSON(w, http.StatusCreated, rt)
}

// importAllOut is the bulk-import result: how many of the live nikki
// rules became WellBoard routes, and why the rest did not.
type importAllOut struct {
	Imported int               `json:"imported"`
	Skipped  int               `json:"skipped"`
	Total    int               `json:"total"`
	SkippedD []skippedRuleNote `json:"skipped_rules,omitempty"`
}

// skippedRuleNote names one rule that could not be imported verbatim.
type skippedRuleNote struct {
	Index  int    `json:"index"`
	Rule   string `json:"raw"`
	Reason string `json:"reason"`
}

// handleExternalRulesImportAll copies EVERY importable rule of the live
// nikki config into WellBoard state as a DISABLED route (the owner
// enables them individually and presses Apply — nothing is applied
// automatically, and the running config is never touched).
//
// Read-only invariant (deliverable 2 of the ticket): this handler opens
// RunConfigPath for reading only; no file under /etc/nikki is written
// by any path in this package.
func (s *Server) handleExternalRulesImportAll(w http.ResponseWriter, r *http.Request) {
	rules, _, err := nikki.LoadExternalRules()
	if err != nil {
		writeError(w, httpErr(http.StatusServiceUnavailable, "external rules unavailable: %v", err))
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()

	// Existing conditions, so a repeated import is a no-op instead of
	// piling duplicates onto the route list.
	seen := map[string]bool{}
	for _, rt := range st.Routes {
		for _, c := range rt.Conditions {
			seen[string(c.Type)+"\x00"+c.Value] = true
		}
	}

	out := importAllOut{Total: len(rules)}
	for i := range rules {
		ru := &rules[i]
		condType, importable := importableRuleTypes[ru.Type]
		if !importable || ru.Type == "MATCH" {
			out.Skipped++
			out.SkippedD = append(out.SkippedD, skippedRuleNote{ru.Index, ru.Raw,
				fmt.Sprintf("rule type %s is shown read-only (not expressible as a route)", ru.Type)})
			continue
		}
		cond, err := conditionFromRule(ru, condType)
		if err != nil {
			out.Skipped++
			out.SkippedD = append(out.SkippedD, skippedRuleNote{ru.Index, ru.Raw, err.Error()})
			continue
		}
		if seen[string(cond.Type)+"\x00"+cond.Value] {
			out.Skipped++
			out.SkippedD = append(out.SkippedD, skippedRuleNote{ru.Index, ru.Raw, "already imported"})
			continue
		}
		target, _ := targetFromPolicy(ru.Target)
		if target.Type == "" {
			if t2, ok := serverOrGroupByName(st, ru.Target); ok {
				target = t2
			}
		}
		if target.Type == "" {
			out.Skipped++
			out.SkippedD = append(out.SkippedD, skippedRuleNote{ru.Index, ru.Raw,
				fmt.Sprintf("policy %q is not a WellBoard group/server", ru.Target)})
			continue
		}
		if err := validateTarget(st, target); err != nil {
			out.Skipped++
			out.SkippedD = append(out.SkippedD, skippedRuleNote{ru.Index, ru.Raw, err.Error()})
			continue
		}
		st.Routes = append(st.Routes, model.Route{
			ID: newID("rt", st), Name: defaultImportName(ru), Enabled: false,
			Order: nextOrder(st), Conditions: []model.RouteCondition{cond},
			Target: target, OnUnavailable: "block",
		})
		seen[string(cond.Type)+"\x00"+cond.Value] = true
		out.Imported++
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("external rules bulk import: %d routes created, %d skipped (%d rules read from %s)",
		out.Imported, out.Skipped, out.Total, nikki.RunConfigPath)
	writeJSON(w, http.StatusCreated, out)
}

// conditionFromRule builds the route condition, applying the same
// value validation as the routes API (commas/colons/newlines and CIDR
// checks; the imported value lands in generated rule lines,
// so the security rules from Phase 7 apply unchanged).
func conditionFromRule(ru *nikki.ExternalRule, t model.RouteConditionType) (model.RouteCondition, error) {
	value := strings.TrimSpace(ru.Value)
	if value == "" {
		return model.RouteCondition{}, httpErr(http.StatusBadRequest,
			"rule %q has no value segment", ru.Raw)
	}
	cond := model.RouteCondition{Type: t, Value: value}
	if strings.ContainsAny(value, ",:\n\r") {
		return model.RouteCondition{}, httpErr(http.StatusBadRequest,
			"condition %s value %q must not contain ',', ':' or newlines", t, value)
	}
	if t == model.CondIPCIDR || t == model.CondSrcDevice {
		if !isCIDR(value) {
			return model.RouteCondition{}, httpErr(http.StatusBadRequest,
				"condition %s value %q is not a valid ip/cidr", t, value)
		}
	}
	if t == model.CondDomainSuffix || t == model.CondDomain {
		if strings.ContainsAny(value, " \t/") || strings.HasPrefix(value, ".") {
			return model.RouteCondition{}, httpErr(http.StatusBadRequest, "bad domain %q", value)
		}
	}
	if t == model.CondDstPort {
		if !isPortOrRange(value) {
			return model.RouteCondition{}, httpErr(http.StatusBadRequest, "bad dst-port %q", value)
		}
	}
	return cond, nil
}

// targetFromPolicy maps a mihomo policy segment to a WellBoard target.
// DIRECT/REJECT map to the built-ins. Named policies return a zero
// target (ok=false): the caller resolves them by display name against
// WellBoard groups/servers in state (external groups are not WellBoard
// objects; a name match is the honest bridge — the response tells the
// caller which id was chosen).
func targetFromPolicy(policy string) (model.Target, error) {
	switch strings.TrimSpace(policy) {
	case "DIRECT":
		return model.Target{Type: model.TargetDirect}, nil
	case "REJECT", "REJECT-DROP":
		return model.Target{Type: model.TargetReject}, nil
	}
	return model.Target{}, nil // named policy: caller resolves by name
}

// serverOrGroupByName resolves an external policy name to a WellBoard
// target by display name (used when the caller asks for name mapping).
func serverOrGroupByName(st *model.State, name string) (model.Target, bool) {
	for _, g := range st.Groups {
		if g.Name == name {
			return model.Target{Type: model.TargetGroup, ID: g.ID}, true
		}
	}
	for _, srv := range st.Servers {
		if srv.Name == name {
			return model.Target{Type: model.TargetServer, ID: srv.ID}, true
		}
	}
	return model.Target{}, false
}

func defaultImportName(ru *nikki.ExternalRule) string {
	name := "nikki: " + ru.Type
	if ru.Value != "" {
		name += " " + ru.Value
	}
	if len(name) > 64 {
		name = name[:61] + "..."
	}
	return name
}

// ----------------------------------------------------------------------------
// Templates (FR-5)
// ----------------------------------------------------------------------------

// templateOut is the API view of a catalog entry: the Template fields
// plus the disabled flag (from settings, B1).
type templateOut struct {
	templates.Template
	Disabled bool `json:"disabled"`
}

// templatesOut is the list response: the merged catalog (origin/
// overridden on every entry) + overlay warnings + the disabled ids.
type templatesOut struct {
	Templates []templateOut `json:"templates"`
	Warnings  []string      `json:"warnings,omitempty"`
}

// templateOutList renders the merged catalog with per-entry disabled
// flags. Reads settings (shared lock scope with the handler).
func (s *Server) templateOutList(st *model.State) templatesOut {
	disabled := map[string]bool{}
	for _, id := range st.Settings.DisabledTemplates {
		disabled[id] = true
	}
	out := templatesOut{Warnings: s.Catalog.Warnings}
	for _, tpl := range s.Catalog.Templates {
		out.Templates = append(out.Templates, templateOut{Template: tpl, Disabled: disabled[tpl.ID]})
	}
	return out
}

func (s *Server) handleTemplatesList(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	writeJSON(w, http.StatusOK, s.templateOutList(st))
}

func (s *Server) handleTemplatesGet(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	t, ok := s.Catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
		return
	}
	disabled := false
	for _, id := range st.Settings.DisabledTemplates {
		if id == t.ID {
			disabled = true
		}
	}
	writeJSON(w, http.StatusOK, templateOut{Template: t, Disabled: disabled})
}

// templateIn is the create/update payload for a custom template
// (B1). Providers are optional and validated against the
// shipped payload files.
type templateIn struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	ListSource    string                 `json:"list_source"`
	Conditions    []model.RouteCondition `json:"conditions"`
	TypicalTarget string                 `json:"typical_target"`
	Providers     []string               `json:"providers"`
}

// validateTemplateIn checks the payload (id format, name, conditions,
// typical_target, provider payload references) and returns the
// templates.Template to persist. Mirrors the loader rules so a written
// file round-trips through LoadMerged cleanly.
func (s *Server) validateTemplateIn(in templateIn) (templates.Template, error) {
	if !templates.ValidID(in.ID) {
		return templates.Template{}, httpErr(http.StatusBadRequest, "id must match [a-z0-9-]{1,64}")
	}
	if in.Name == "" {
		return templates.Template{}, httpErr(http.StatusBadRequest, "name is required")
	}
	switch in.TypicalTarget {
	case "direct", "reject", "server", "group", "":
	default:
		return templates.Template{}, httpErr(http.StatusBadRequest,
			"typical_target must be one of direct, reject, server, group")
	}
	tpl := templates.Template{
		ID: in.ID, Name: in.Name, Description: in.Description,
		ListSource: in.ListSource, Conditions: in.Conditions,
		TypicalTarget: in.TypicalTarget, Providers: in.Providers,
	}
	// Condition rules: the route-validation subset that templates use
	// (types the generator can translate; value hygiene — no rule-line
	// injection, format checks per type).
	for _, c := range in.Conditions {
		switch c.Type {
		case model.CondDomain, model.CondDomainSuffix, model.CondDomainKeyword,
			model.CondGeosite, model.CondGeoIP, model.CondIPCIDR,
			model.CondSrcDevice, model.CondDstPort:
		default:
			return templates.Template{}, httpErr(http.StatusBadRequest, "unknown condition type %q", c.Type)
		}
		if c.Value == "" {
			return templates.Template{}, httpErr(http.StatusBadRequest, "condition %s has empty value", c.Type)
		}
		if strings.ContainsAny(c.Value, ",:\n\r") {
			return templates.Template{}, httpErr(http.StatusBadRequest,
				"condition %s value %q must not contain ',', ':' or newlines", c.Type, c.Value)
		}
		if c.Type == model.CondIPCIDR || c.Type == model.CondSrcDevice {
			if !isCIDR(c.Value) {
				return templates.Template{}, httpErr(http.StatusBadRequest, "condition %s value %q is not a valid ip/cidr", c.Type, c.Value)
			}
		}
		if c.Type == model.CondDomainSuffix || c.Type == model.CondDomain {
			if strings.ContainsAny(c.Value, " 	/") || strings.HasPrefix(c.Value, ".") {
				return templates.Template{}, httpErr(http.StatusBadRequest, "bad domain %q", c.Value)
			}
		}
		if c.Type == model.CondDstPort {
			if !isPortOrRange(c.Value) {
				return templates.Template{}, httpErr(http.StatusBadRequest, "bad dst-port %q", c.Value)
			}
		}
	}
	// Providers reference the SHIPPED payload files only (the overlay
	// cannot carry payload files).
	for _, p := range in.Providers {
		if _, err := os.Stat(filepath.Join(s.catalogDir(), "providers", p+".yaml")); err != nil {
			return templates.Template{}, httpErr(http.StatusBadRequest,
				"provider %q has no shipped payload file", p)
		}
	}
	return tpl, nil
}

// catalogDir returns the shipped dir behind the current catalog ("" —
// and a stat miss — when the catalog is unavailable).
func (s *Server) catalogDir() string {
	if s.Catalog == nil {
		return ""
	}
	return s.Catalog.Dir
}

// writeCustomTemplate validates + persists a custom/override template
// file and reloads the merged catalog (shared by POST and PUT).
func (s *Server) writeCustomTemplate(w http.ResponseWriter, in templateIn, created bool) {
	tpl, err := s.validateTemplateIn(in)
	if err != nil {
		writeError(w, err)
		return
	}
	if s.overlayDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template overlay directory not configured"})
		return
	}
	if err := templates.WriteOverlay(s.overlayDir, tpl); err != nil {
		writeError(w, err)
		return
	}
	if err := s.ReloadCatalog(); err != nil {
		writeError(w, err)
		return
	}
	s.log("template %s %s (overlay %s)", tpl.ID, map[bool]string{true: "created", false: "updated"}[created], s.overlayDir)
	out, ok := s.Catalog.Get(tpl.ID)
	if !ok {
		writeError(w, httpErr(http.StatusInternalServerError, "template %q missing after write", tpl.ID))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, templateOut{Template: out})
}

// handleTemplatesCreate writes a NEW custom template or an override of
// a builtin one (writing a builtin id IS "editing the shipped set" —
// the override file replaces it in the merged catalog).
func (s *Server) handleTemplatesCreate(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	var in templateIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	s.writeCustomTemplate(w, in, true)
}

// handleTemplatesUpdate edits an existing custom template or an
// override (builtin ids route to the same overlay write). The id in
// the path wins; a body id must match when present.
func (s *Server) handleTemplatesUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	var in templateIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	if _, ok := s.Catalog.Get(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
		return
	}
	if in.ID != "" && in.ID != id {
		writeError(w, httpErr(http.StatusBadRequest, "body id %q does not match path id %q", in.ID, id))
		return
	}
	in.ID = id
	s.writeCustomTemplate(w, in, false)
}

// handleTemplatesDelete removes the overlay file for id (never a
// shipped one). Deleting an override restores the builtin entry.
func (s *Server) handleTemplatesDelete(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	if s.overlayDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template overlay directory not configured"})
		return
	}
	id := r.PathValue("id")
	if _, ok := s.Catalog.Get(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
		return
	}
	if err := templates.DeleteOverlay(s.overlayDir, id); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no custom template file to delete (builtin templates are not deletable)"})
			return
		}
		writeError(w, err)
		return
	}
	if err := s.ReloadCatalog(); err != nil {
		writeError(w, err)
		return
	}
	s.log("template %s deleted (overlay %s)", id, s.overlayDir)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// toggleIn is the disable/enable request body (B1).
type toggleIn struct {
	Disabled *bool `json:"disabled"`
}

// handleTemplatesToggle adds/removes the template id in
// Settings.DisabledTemplates. Works for builtin ("disabling the
// shipped ones") and custom ids alike.
func (s *Server) handleTemplatesToggle(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "template catalog unavailable"})
		return
	}
	var in toggleIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	if in.Disabled == nil {
		writeError(w, httpErr(http.StatusBadRequest, "disabled is required"))
		return
	}
	id := r.PathValue("id")
	if _, ok := s.Catalog.Get(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
		return
	}
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	if *in.Disabled {
		if !containsString(st.Settings.DisabledTemplates, id) {
			st.Settings.DisabledTemplates = append(st.Settings.DisabledTemplates, id)
		}
	} else {
		st.Settings.DisabledTemplates = removeString(st.Settings.DisabledTemplates, id)
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("template %s disabled=%v", id, *in.Disabled)
	writeJSON(w, http.StatusOK, templateOut{Template: mustTemplate(s.Catalog, id), Disabled: *in.Disabled})
}

// mustTemplate fetches a template that was just existence-checked.
func mustTemplate(cat *templates.Catalog, id string) templates.Template {
	t, _ := cat.Get(id)
	return t
}

// containsString reports whether v is in list.
func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// applyIn is the template-apply request: the chosen target (FR-5.1).
type applyIn struct {
	Target model.Target `json:"target"`
}

// handleTemplateApply creates a route from the template with the user's
// target (FR-5.1/FR-5.2). The "all-vpn" template instead sets the
// default policy (it has no conditions). Disabled templates refuse
// with 409 (B1: disabled = not offered; apply is explicit).
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
	if containsString(st.Settings.DisabledTemplates, t.ID) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("template %q is disabled; enable it first", t.ID),
		})
		return
	}
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
// Geodata tags (B3)
// ----------------------------------------------------------------------------

// handleGeodataTags returns the known geosite category tags for the
// template editor datalist: parsed from a local .dat when found
// (nikki run dir / state-dir cache), else a curated static list.
func (s *Server) handleGeodataTags(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "geosite" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind must be \"geosite\""})
		return
	}
	paths := s.geositeDatPaths
	if paths == nil {
		paths = defaultGeositeDatPaths()
	}
	tags := geodata.KnownGeositeTags(paths)
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// defaultGeositeDatPaths probes the local geosite.dat copies: the
// nikki/mihomo run dir first, then the WellBoard state-dir cache.
// Missing files are skipped by KnownGeositeTags.
func defaultGeositeDatPaths() []string {
	return []string{
		"/etc/nikki/run/geosite.dat",
		"/etc/wellboard/geosite.dat",
	}
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
	UIPort               *int                 `json:"ui_port"`
	Lang                 *string              `json:"lang"`
	GeositeSource        *string              `json:"geosite_source"`
	GeoipSource          *string              `json:"geoip_source"`
	GeositeCustomURL     *string              `json:"geosite_custom_url"`
	GeoipCustomURL       *string              `json:"geoip_custom_url"`
	GeodataAdditions     *map[string][]string `json:"geodata_additions"`
	DefaultPolicy        *model.Target        `json:"default_policy"`
	DelayTestIntervalSec *int                 `json:"delay_test_interval_sec"`
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	// A3: the Settings PAGE shows sources with a masked URL
	// (secret rule: the full URL is not printed on the settings
	// screen). The shape of the settings object itself is unchanged —
	// sources_summary is an additional field.
	summary := sourcesSummary(st.Sources)
	writeJSON(w, http.StatusOK, settingsOut{
		Settings:       st.Settings,
		SourcesSummary: summary,
	})
}

// sourceSummary is one masked sources-list row for the settings page.
type sourceSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MaskedURL string `json:"masked_url"`
	Enabled   bool   `json:"enabled"`
}

// settingsOut wraps the settings object with the masked sources
// summary (extra field, existing keys unchanged).
type settingsOut struct {
	model.Settings
	SourcesSummary []sourceSummary `json:"sources_summary"`
}

// maskURL renders a display-safe URL: the first 10 characters plus
// the total length when long enough; "****" for short/empty values
// (a real http(s) URL is always ≥ 10 chars, so short means garbage).
func maskURL(u string) string {
	if len(u) > 14 {
		return u[:10] + "…(" + strconv.Itoa(len(u)) + ")"
	}
	return "****"
}

// sourcesSummary maps sources onto the masked rows used by every
// non-/sources API surface (the raw URL stays in the existing /sources
// route only, as it always has).
func sourcesSummary(sources []model.Source) []sourceSummary {
	out := make([]sourceSummary, 0, len(sources))
	for _, src := range sources {
		out = append(out, sourceSummary{
			ID: src.ID, Name: src.Name, Kind: src.Kind,
			MaskedURL: maskURL(src.URL), Enabled: src.Enabled,
		})
	}
	return out
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
	if in.GeositeSource != nil {
		if !geodata.GeositeValid(*in.GeositeSource) {
			writeError(w, httpErr(http.StatusBadRequest, "geosite_source must be \"runetfreedom\", \"metacubex\" or \"custom\""))
			return
		}
		set.GeositeSource = *in.GeositeSource
	}
	if in.GeoipSource != nil {
		if !geodata.GeoipValid(*in.GeoipSource) {
			writeError(w, httpErr(http.StatusBadRequest, "geoip_source must be \"runetfreedom\", \"metacubex\" or \"custom\""))
			return
		}
		set.GeoipSource = *in.GeoipSource
	}
	if in.GeositeCustomURL != nil {
		if *in.GeositeCustomURL != "" && !strings.HasPrefix(*in.GeositeCustomURL, "http://") && !strings.HasPrefix(*in.GeositeCustomURL, "https://") {
			writeError(w, httpErr(http.StatusBadRequest, "geosite_custom_url must be http(s)"))
			return
		}
		set.GeositeCustomURL = *in.GeositeCustomURL
	}
	if in.GeoipCustomURL != nil {
		if *in.GeoipCustomURL != "" && !strings.HasPrefix(*in.GeoipCustomURL, "http://") && !strings.HasPrefix(*in.GeoipCustomURL, "https://") {
			writeError(w, httpErr(http.StatusBadRequest, "geoip_custom_url must be http(s)"))
			return
		}
		set.GeoipCustomURL = *in.GeoipCustomURL
	}
	// Full replace of the additions map (B2). Custom-source
	// URL presence is enforced at GENERATION time (the profile is
	// where a custom source without a URL becomes an error) — here we
	// validate what can be validated: source values and entry formats.
	if in.GeodataAdditions != nil {
		for key, entries := range *in.GeodataAdditions {
			kind, cat, ok := strings.Cut(key, ":")
			if !ok || cat == "" {
				writeError(w, httpErr(http.StatusBadRequest, "geodata_additions key %q must be \"geosite:<category>\" or \"geoip:<category>\"", key))
				return
			}
			if kind != "geosite" && kind != "geoip" {
				writeError(w, httpErr(http.StatusBadRequest, "geodata_additions key %q must start with \"geosite:\" or \"geoip:\"", key))
				return
			}
			for _, v := range entries {
				if err := validateGeodataAdditionEntry(kind, v); err != nil {
					writeError(w, httpErr(http.StatusBadRequest, "geodata_additions[%s]: %v", key, err))
					return
				}
			}
		}
		set.GeodataAdditions = *in.GeodataAdditions
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
