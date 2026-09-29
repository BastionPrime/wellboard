// Package nikki rules: read-only access to the active mihomo/nikki
// rule set (OPE-2982).
//
// The ONLY entry point is LoadExternalRules, which parses the nikki
// runtime config (/etc/nikki/run/config.yaml — the exact file mihomo
// reads) and returns its `rules:` lines as structured records. The
// package NEVER writes: no file in /etc/nikki is opened for writing
// anywhere in WellBoard (invariant "the owner's rules stay untouched",
// acceptance stop-rule: any write to mixin.yaml or run/config.yaml =
// acceptance failure).
package nikki

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RunConfigPath is the nikki runtime config location on the router.
// Overridable for tests; the default matches the nikki package layout.
var RunConfigPath = "/etc/nikki/run/config.yaml"

// ExternalRule is one rule line of the active nikki/mihomo config,
// surfaced read-only in the Routes tab ("External nikki rules").
type ExternalRule struct {
	// Index is the 1-based position of the line inside `rules:`
	// (mihomo evaluates rules top-down; the index is the honest way to
	// show order without importing ordering semantics).
	Index int `json:"index"`
	// Type is the mihomo rule type segment (DOMAIN-SUFFIX, GEOIP,
	// DST-PORT, MATCH, …), upper-cased as found in the file.
	Type string `json:"type"`
	// Value is the matcher segment (domain, cidr, port range, geosite
	// category, …). Empty for no-argument types (MATCH).
	Value string `json:"value,omitempty"`
	// Target is the policy segment: a group name, a proxy name or one
	// of the DIRECT/REJECT/REJECT-DROP/PASS built-ins.
	Target string `json:"target"`
	// NoResolve is the trailing no-resolve flag (GEOIP/IP rules).
	NoResolve bool `json:"no_resolve,omitempty"`
	// Raw is the full rule line as it stands in the file (provenance:
	// what you see is exactly what mihomo evaluates).
	Raw string `json:"raw"`
}

// LoadExternalRules reads RunConfigPath and returns the active rule
// lines. A missing file returns (nil, nil): the tab then shows the
// "nikki config not found" state instead of failing the whole UI.
// A present-but-unparseable file returns an error naming the file.
func LoadExternalRules() ([]ExternalRule, []string, error) {
	data, err := os.ReadFile(RunConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read %s: %w", RunConfigPath, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", RunConfigPath, err)
	}
	raw, ok := doc["rules"].([]any)
	if !ok {
		// config without a rules section: empty view, not an error
		return nil, nil, nil
	}
	rules := make([]ExternalRule, 0, len(raw))
	groups := map[string]bool{}
	for i, item := range raw {
		line, ok := item.(string)
		if !ok {
			continue // non-string entries are not mihomo rule lines
		}
		r, err := parseRuleLine(line)
		if err != nil {
			// Unparseable lines are surfaced as raw-only records so the
			// count still matches the file; nothing is silently dropped.
			rules = append(rules, ExternalRule{Index: i + 1, Raw: line, Target: "unknown"})
			continue
		}
		r.Index = i + 1
		rules = append(rules, r)
		if r.Target != "" && r.Target != "MATCH" {
			groups[r.Target] = true
		}
	}
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	return rules, names, nil
}

// parseRuleLine splits a mihomo rule line "TYPE,value,Policy[,no-resolve]"
// into an ExternalRule. Empty lines are invalid; MATCH has no value.
func parseRuleLine(line string) (ExternalRule, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ExternalRule{}, fmt.Errorf("empty rule line")
	}
	parts := strings.Split(trimmed, ",")
	r := ExternalRule{Raw: trimmed}
	r.Type = strings.ToUpper(strings.TrimSpace(parts[0]))
	switch len(parts) {
	case 2:
		// TYPE,Policy — MATCH and its kin
		r.Target = strings.TrimSpace(parts[1])
	case 3:
		r.Value = strings.TrimSpace(parts[1])
		r.Target = strings.TrimSpace(parts[2])
	case 4:
		r.Value = strings.TrimSpace(parts[1])
		r.Target = strings.TrimSpace(parts[2])
		if strings.TrimSpace(parts[3]) == "no-resolve" {
			r.NoResolve = true
		} else {
			// keep unknown 4th segments visible in Raw, do not invent
			r.Value = strings.TrimSpace(parts[1])
			r.Target = strings.TrimSpace(parts[2]) + "," + strings.TrimSpace(parts[3])
		}
	default:
		// long lines (e.g. logic rules with commas): record raw only
		return r, fmt.Errorf("unparsed rule shape")
	}
	return r, nil
}
