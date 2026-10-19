// Package api contains sources handlers.
package api

import (
	"fmt"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"net/http"
	"strings"
)

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
	// leak the subscription URL (secret rule).
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
