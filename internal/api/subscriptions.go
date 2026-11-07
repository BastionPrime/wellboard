// Package api contains subscriptions handlers.
package api

import (
	"fmt"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"net/http"
	"strings"
)

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
