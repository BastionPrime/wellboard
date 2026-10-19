// Package api contains routes handlers.
package api

import (
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"net/http"
	"sort"
	"strings"
)

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
