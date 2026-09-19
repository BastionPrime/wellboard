// Package templates loads the WellBoard rule-template catalog (FR-5.1,
// FR-5.3, initial TZ Appendix В) from templates/*.yaml.
//
// A template is a ready-made condition set; applying it creates a normal
// model.Route with those conditions and the target the user picked
// (FR-5.2: the resulting route is an ordinary, editable route). The
// "all-vpn" template is special: it has no conditions and applying it
// sets the default policy instead of creating a route.
package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/wellboard/wellboard/internal/model"
	"gopkg.in/yaml.v3"
)

// Template is one catalog entry (templates/<id>.yaml).
type Template struct {
	// ID is the stable template identifier (file stem).
	ID string `yaml:"id" json:"id"`
	// Name is the display name (RU).
	Name string `yaml:"name" json:"name"`
	// Description is a one-line UI explanation.
	Description string `yaml:"description" json:"description"`
	// Conditions is the ready-made route condition set.
	Conditions []model.RouteCondition `yaml:"conditions" json:"conditions"`
	// TypicalTarget is the suggested target kind: direct|reject|server|group.
	TypicalTarget string `yaml:"typical_target" json:"typical_target"`
	// Providers names local fallback rule-provider lists (FR-5.4) in
	// templates/providers/<name>.yaml.
	Providers []string `yaml:"providers,omitempty" json:"providers,omitempty"`
}

// Catalog is the loaded template set.
type Catalog struct {
	// Dir is the templates directory (repo templates/ or /usr/share/wellboard/templates).
	Dir string
	// Templates sorted by ID.
	Templates []Template
}

// AllDefault is the total shipped template count (Appendix В: 10).
const AllDefault = 10

// Load reads every *.yaml file in dir (non-recursive; providers/ is a
// subdirectory and is skipped) and validates each template: id matches
// the file stem, name non-empty, condition types known, typical_target
// in the allowed set. Unknown files are an error — the catalog ships
// with the app and must be exact.
func Load(dir string) (*Catalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("templates: read dir %s: %w", dir, err)
	}
	cat := &Catalog{Dir: dir}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("templates: read %s: %w", path, err)
		}
		tpl := Template{}
		if err := yaml.Unmarshal(data, &tpl); err != nil {
			return nil, fmt.Errorf("templates: parse %s: %w", path, err)
		}
		stem := e.Name()[:len(e.Name())-len(".yaml")]
		if tpl.ID == "" {
			tpl.ID = stem
		}
		if tpl.ID != stem {
			return nil, fmt.Errorf("templates: %s: id %q does not match file name", path, tpl.ID)
		}
		if tpl.Name == "" {
			return nil, fmt.Errorf("templates: %s: empty name", path)
		}
		if err := validateConditions(tpl.Conditions); err != nil {
			return nil, fmt.Errorf("templates: %s: %w", path, err)
		}
		switch tpl.TypicalTarget {
		case "direct", "reject", "server", "group", "":
			// ok; empty means "any target works"
		default:
			return nil, fmt.Errorf("templates: %s: bad typical_target %q", path, tpl.TypicalTarget)
		}
		for _, p := range tpl.Providers {
			if err := validProviderName(p); err != nil {
				return nil, fmt.Errorf("templates: %s: provider %q: %w", path, p, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "providers", p+".yaml")); err != nil {
				return nil, fmt.Errorf("templates: %s: provider %q: no payload file %s", path, p, filepath.Join("providers", p+".yaml"))
			}
		}
		cat.Templates = append(cat.Templates, tpl)
	}
	sort.Slice(cat.Templates, func(i, j int) bool { return cat.Templates[i].ID < cat.Templates[j].ID })
	if len(cat.Templates) == 0 {
		return nil, fmt.Errorf("templates: no templates in %s", dir)
	}
	return cat, nil
}

// Get returns the template by ID.
func (c *Catalog) Get(id string) (Template, bool) {
	for _, t := range c.Templates {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

// ProviderNames returns the deduplicated set of provider names
// referenced by the given template IDs (for the generator).
func (c *Catalog) ProviderNames(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		t, ok := c.Get(id)
		if !ok {
			continue
		}
		for _, p := range t.Providers {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// validProviderName rejects path escapes in provider names.
func validProviderName(p string) error {
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Errorf("only [a-zA-Z0-9-] allowed")
		}
	}
	if p == "" {
		return fmt.Errorf("empty provider name")
	}
	return nil
}

// validateConditions checks that every condition type is one the
// generator can translate (FR-4.2).
func validateConditions(conds []model.RouteCondition) error {
	for _, c := range conds {
		switch c.Type {
		case model.CondDomain, model.CondDomainSuffix, model.CondDomainKeyword,
			model.CondGeosite, model.CondGeoIP, model.CondIPCIDR,
			model.CondSrcDevice, model.CondDstPort:
			if c.Value == "" {
				return fmt.Errorf("condition %q without value", c.Type)
			}
		default:
			return fmt.Errorf("unknown condition type %q", c.Type)
		}
	}
	return nil
}
