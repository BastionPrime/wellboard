// Package api contains templates handlers.
package api

import (
	"errors"
	"fmt"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/templates"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

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
