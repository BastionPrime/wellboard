// Package templates loads the WellBoard rule-template catalog (FR-5.1,
// FR-5.3, initial TZ Appendix В) from templates/*.yaml.
//
// A template is a ready-made condition set; applying it creates a normal
// model.Route with those conditions and the target the user picked
// (FR-5.2: the resulting route is an ordinary, editable route). The
// "all-vpn" template is special: it has no conditions and applying it
// sets the default policy instead of creating a route.
//
// B1: the catalog is a merge of two directories — the
// read-only shipped dir (/usr/share/wellboard/templates) and the user
// overlay (/etc/wellboard/templates on the router, <state_dir>/templates
// in dev; survives sysupgrade via the existing keep.d which covers
// /etc/wellboard). An overlay file with the id of a shipped template
// REPLACES it (that is "editing the built-ins"); a new id is appended.
// Broken overlay files are skipped with a warning — a user file must
// never brick the app (LoadMerged).
package templates

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	// ListSource explains where the template's domain list comes from
	// (geodata categories, local providers/ fallback, or "no list" for
	// the MATCH-policy template). Optional for backwards compatibility.
	ListSource string `yaml:"list_source,omitempty" json:"list_source,omitempty"`
	// Conditions is the ready-made route condition set.
	Conditions []model.RouteCondition `yaml:"conditions" json:"conditions"`
	// TypicalTarget is the suggested target kind: direct|reject|server|group.
	TypicalTarget string `yaml:"typical_target" json:"typical_target"`
	// Providers names local fallback rule-provider lists (FR-5.4) in
	// templates/providers/<name>.yaml.
	Providers []string `yaml:"providers,omitempty" json:"providers,omitempty"`
	// Origin reports where the entry came from: "builtin" (shipped
	// dir) or "custom" (user overlay). Derived at load time — never
	// serialized into template files.
	Origin string `yaml:"-" json:"origin,omitempty"`
	// Overridden marks a custom entry that shadows a shipped template
	// with the same id: deleting the overlay file restores the shipped
	// one. Derived at load time.
	Overridden bool `yaml:"-" json:"overridden,omitempty"`
}

// Catalog is the loaded template set.
type Catalog struct {
	// Dir is the shipped templates directory (repo templates/ or
	// /usr/share/wellboard/templates).
	Dir string
	// OverlayDir is the user overlay directory ("" when the catalog
	// was built with Load directly — no overlay support).
	OverlayDir string
	// Templates sorted by ID.
	Templates []Template
	// Warnings collects non-fatal overlay problems (skipped broken
	// user files). Empty for a clean catalog.
	Warnings []string
}

// AllDefault is the total shipped template count (Appendix В: 10).
const AllDefault = 10

// ValidID reports whether id is a usable template identifier / overlay
// file stem: [a-z0-9-]{1,64}.
func ValidID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

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
		tpl, stem, err := parseFile(path)
		if err != nil {
			return nil, fmt.Errorf("templates: %s: %w", path, err)
		}
		if tpl.ID == "" {
			tpl.ID = stem
		}
		if tpl.ID != stem {
			return nil, fmt.Errorf("templates: %s: id %q does not match file name", path, tpl.ID)
		}
		if err := checkTemplate(tpl, path, []string{dir}); err != nil {
			return nil, err
		}
		tpl.Origin = "builtin"
		cat.Templates = append(cat.Templates, tpl)
	}
	sort.Slice(cat.Templates, func(i, j int) bool { return cat.Templates[i].ID < cat.Templates[j].ID })
	if len(cat.Templates) == 0 {
		return nil, fmt.Errorf("templates: no templates in %s", dir)
	}
	return cat, nil
}

// LoadMerged loads the shipped catalog (strict — the shipped set must
// be exact, same rules as Load) and then applies the user overlay:
//
//   - for every *.yaml in overlayDir, the file stem is the template id;
//   - an id that also exists in the shipped set REPLACES the shipped
//     entry (origin "custom", Overridden=true — deleting the file
//     brings the shipped template back);
//   - a new id is appended (origin "custom").
//
// A missing overlay dir is fine (first start before the daemon created
// it). An overlay file that fails the SAME validation rules as the
// shipped set is SKIPPED with a warning in Catalog.Warnings — a broken
// user file must never brick the app. Overlay templates may reference
// provider payload files from either the shipped or the overlay
// providers/ subdirectory.
func LoadMerged(shippedDir, overlayDir string) (*Catalog, error) {
	cat, err := Load(shippedDir)
	if err != nil {
		return nil, err
	}
	cat.OverlayDir = overlayDir
	if overlayDir == "" {
		return cat, nil
	}
	entries, err := os.ReadDir(overlayDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cat, nil
		}
		return nil, fmt.Errorf("templates: read overlay dir %s: %w", overlayDir, err)
	}
	providerDirs := []string{shippedDir, overlayDir}
	byID := make(map[string]int, len(cat.Templates))
	for i, t := range cat.Templates {
		byID[t.ID] = i
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(overlayDir, e.Name())
		tpl, stem, err := parseFile(path)
		if err != nil {
			cat.warnSkip(e.Name(), err)
			continue
		}
		if tpl.ID == "" {
			tpl.ID = stem
		}
		if tpl.ID != stem {
			cat.warnSkip(e.Name(), fmt.Errorf("id %q does not match file name", tpl.ID))
			continue
		}
		if err := checkTemplate(tpl, path, providerDirs); err != nil {
			cat.warnSkip(e.Name(), err)
			continue
		}
		tpl.Origin = "custom"
		if i, ok := byID[tpl.ID]; ok {
			tpl.Overridden = true
			cat.Templates[i] = tpl
		} else {
			byID[tpl.ID] = len(cat.Templates)
			cat.Templates = append(cat.Templates, tpl)
		}
	}
	sort.Slice(cat.Templates, func(i, j int) bool { return cat.Templates[i].ID < cat.Templates[j].ID })
	return cat, nil
}

// warnSkip records a skipped broken overlay file.
func (c *Catalog) warnSkip(name string, err error) {
	c.Warnings = append(c.Warnings, fmt.Sprintf("overlay %s skipped: %v", name, err))
}

// parseFile reads and unmarshals one template file; the file stem is
// returned alongside the template.
func parseFile(path string) (tpl Template, stem string, err error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from walking the builtin templates dir or the state-dir overlay (ValidID-checked ids), not from user input
	if err != nil {
		return Template{}, "", fmt.Errorf("read: %w", err)
	}
	if err := yaml.Unmarshal(data, &tpl); err != nil {
		return Template{}, "", fmt.Errorf("parse: %w", err)
	}
	return tpl, strings.TrimSuffix(filepath.Base(path), ".yaml"), nil
}

// checkTemplate applies the shared catalog validation rules. The
// message carries the full path (shipped loader errors, overlay
// warnings). providerDirs are the directories whose providers/
// subdirectory may hold the payload files for tpl.Providers.
func checkTemplate(tpl Template, path string, providerDirs []string) error {
	if tpl.Name == "" {
		return fmt.Errorf("templates: %s: empty name", path)
	}
	if err := validateConditions(tpl.Conditions); err != nil {
		return fmt.Errorf("templates: %s: %w", path, err)
	}
	switch tpl.TypicalTarget {
	case "direct", "reject", "server", "group", "":
		// ok; empty means "any target works"
	default:
		return fmt.Errorf("templates: %s: bad typical_target %q", path, tpl.TypicalTarget)
	}
	for _, p := range tpl.Providers {
		if err := validProviderName(p); err != nil {
			return fmt.Errorf("templates: %s: provider %q: %w", path, p, err)
		}
		if !providerPayloadExists(providerDirs, p) {
			return fmt.Errorf("templates: %s: provider %q: no payload file %s", path, p, filepath.Join("providers", p+".yaml"))
		}
	}
	return nil
}

// providerPayloadExists reports whether any of the dirs has a
// providers/<name>.yaml payload file.
func providerPayloadExists(dirs []string, name string) bool {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(d, "providers", name+".yaml")); err == nil {
			return true
		}
	}
	return false
}

// OverlayPath returns the overlay file location for id.
func OverlayPath(dir, id string) string {
	return filepath.Join(dir, id+".yaml")
}

// OverlayExists reports whether an overlay file exists for id.
func OverlayExists(dir, id string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(OverlayPath(dir, id))
	return err == nil
}

// WriteOverlay atomically writes tpl into the user overlay directory
// as <id>.yaml (marshal → tmp file → rename; the file mode is 0644 so
// the file stays readable after a mode-0700 state dir on the router).
// Origin/Overridden are load-time derived and never serialized.
func WriteOverlay(dir string, tpl Template) error {
	if dir == "" {
		return fmt.Errorf("templates: no overlay directory configured")
	}
	if !ValidID(tpl.ID) {
		return fmt.Errorf("templates: bad template id %q", tpl.ID)
	}
	// #nosec G301 -- overlay dir holds non-secret template yamls; 0755 so they stay readable under a mode-0700 state dir, matching the templates convention
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("templates: create overlay dir: %w", err)
	}
	data, err := yaml.Marshal(tpl)
	if err != nil {
		return fmt.Errorf("templates: marshal template: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+tpl.ID+"-*.tmp")
	if err != nil {
		return fmt.Errorf("templates: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Best effort: no leftover temp files on failure paths.
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("templates: write temp file: %w", err)
	}
	if err = tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("templates: chmod temp file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("templates: sync temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("templates: close temp file: %w", err)
	}
	if err = os.Rename(tmpName, OverlayPath(dir, tpl.ID)); err != nil {
		return fmt.Errorf("templates: rename template into place: %w", err)
	}
	return nil
}

// DeleteOverlay removes the user overlay file for id; the shipped
// template with the same id (if any) re-appears after a reload.
// fs.ErrNotExist is returned when there is no overlay file; shipped
// files are never touched.
func DeleteOverlay(dir, id string) error {
	if dir == "" {
		return fmt.Errorf("templates: no overlay directory configured")
	}
	if !ValidID(id) {
		return fmt.Errorf("templates: bad template id %q", id)
	}
	return os.Remove(OverlayPath(dir, id))
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
