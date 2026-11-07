// Package api contains servers handlers.
package api

import (
	"github.com/wellboard/wellboard/internal/convert"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/subscription"
	"net/http"
	"strings"
)

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

// serverUpdateIn is the edit payload for PUT /api/v1/servers/{id}.
// Only name, address (Raw.server) and Raw parameters change; the id,
// source and route/group membership survive the edit.
type serverUpdateIn struct {
	Name     string         `json:"name"`
	Address  string         `json:"address"`
	RawPatch map[string]any `json:"raw_patch"`
}

// handleServerUpdate edits a manual server in place. The id and the
// source are stable, so routes and groups keep their target.
func (s *Server) handleServerUpdate(w http.ResponseWriter, r *http.Request) {
	var in serverUpdateIn
	if err := decodeStrict(r, &in, 0); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, httpErr(http.StatusBadRequest, "name is required"))
		return
	}
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
	// Renaming onto an existing server name conflicts (same rule the
	// subscription merge applies by name/server/port).
	for _, other := range st.Servers {
		if other.ID != id && other.Name == strings.TrimSpace(in.Name) {
			writeError(w, httpErr(http.StatusConflict, "server name %q is already taken", strings.TrimSpace(in.Name)))
			return
		}
	}
	srv := &st.Servers[idx]
	if srv.Raw == nil {
		srv.Raw = map[string]any{}
	}
	srv.Name = strings.TrimSpace(in.Name)
	if addr := strings.TrimSpace(in.Address); addr != "" {
		srv.Raw["server"] = addr
	}
	for k, v := range in.RawPatch {
		if k == "server" || k == "name" {
			continue // owned by the explicit fields above
		}
		srv.Raw[k] = v
	}
	if err := s.save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("server %s updated (name %q)", id, srv.Name)
	writeJSON(w, http.StatusOK, srv)
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
