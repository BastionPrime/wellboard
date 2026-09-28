package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
)

// shippedFixture builds a minimal shipped dir (2 templates + provider).
func shippedFixture(t *testing.T) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"one.yaml":         "id: one\nname: One\ntypical_target: group\n",
		"two.yaml":         "id: two\nname: Two\nconditions:\n  - {type: geosite, value: YOUTUBE}\n",
		"providers/a.yaml": "payload: []\n",
	})
}

func TestLoadMergedAddsCustom(t *testing.T) {
	shipped := shippedFixture(t)
	overlay := writeCatalog(t, map[string]string{
		"mine.yaml": "id: mine\nname: Mine\nconditions:\n  - {type: domain, value: x.com}\n",
	})
	cat, err := LoadMerged(shipped, overlay)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if len(cat.Templates) != 3 {
		t.Fatalf("merged catalog must have 3 entries, got %d", len(cat.Templates))
	}
	got, ok := cat.Get("mine")
	if !ok || got.Origin != "custom" || got.Overridden {
		t.Fatalf("custom entry wrong: %+v ok=%v", got, ok)
	}
	if _, ok := cat.Get("one"); !ok {
		t.Fatal("shipped entry must survive")
	}
	one, _ := cat.Get("one")
	if one.Origin != "builtin" || one.Overridden {
		t.Fatalf("unshadowed builtin wrong: %+v", one)
	}
}

func TestLoadMergedOverride(t *testing.T) {
	shipped := shippedFixture(t)
	overlay := writeCatalog(t, map[string]string{
		"one.yaml": "id: one\nname: One CUSTOM\n",
	})
	cat, err := LoadMerged(shipped, overlay)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	got, ok := cat.Get("one")
	if !ok || got.Name != "One CUSTOM" {
		t.Fatalf("override must replace the builtin: %+v", got)
	}
	if got.Origin != "custom" || !got.Overridden {
		t.Fatalf("override entry wrong: %+v", got)
	}
	if len(cat.Templates) != 2 {
		t.Fatalf("override must not add a second entry, got %d", len(cat.Templates))
	}
}

func TestLoadMergedInvalidOverlaySkipped(t *testing.T) {
	shipped := shippedFixture(t)
	overlay := writeCatalog(t, map[string]string{
		"broken.yaml": "id: broken\nname: Broken\ntypical_target: sideways\n",
		"good.yaml":   "id: good\nname: Good\n",
	})
	cat, err := LoadMerged(shipped, overlay)
	if err != nil {
		t.Fatalf("a broken overlay file must not brick the app: %v", err)
	}
	if _, ok := cat.Get("broken"); ok {
		t.Fatal("broken overlay file must be skipped")
	}
	if _, ok := cat.Get("good"); !ok {
		t.Fatal("valid overlay file must load")
	}
	if len(cat.Warnings) != 1 || !strings.Contains(cat.Warnings[0], "broken.yaml") {
		t.Fatalf("warnings = %v", cat.Warnings)
	}
}

func TestLoadMergedMissingOverlayDir(t *testing.T) {
	shipped := shippedFixture(t)
	cat, err := LoadMerged(shipped, shipped+"/nope-overlay")
	if err != nil {
		t.Fatalf("missing overlay dir must be fine: %v", err)
	}
	if len(cat.Templates) != 2 || len(cat.Warnings) != 0 {
		t.Fatalf("empty overlay: %+v warnings=%v", cat.Templates, cat.Warnings)
	}
}

// TestWriteOverlayRoundTrip: WriteOverlay + LoadMerged must round-trip
// the template (the overlay YAML parses with the same validation).
func TestWriteOverlayRoundTrip(t *testing.T) {
	shipped := shippedFixture(t)
	overlay := t.TempDir()
	tpl := Template{
		ID: "mine", Name: "Mine",
		Description:   "desc",
		ListSource:    "user list",
		TypicalTarget: "direct",
		Conditions: []model.RouteCondition{
			{Type: model.CondGeosite, Value: "youtube"},
			{Type: model.CondDomainSuffix, Value: "x.com"},
		},
	}
	if err := WriteOverlay(overlay, tpl); err != nil {
		t.Fatalf("WriteOverlay: %v", err)
	}
	// No leftover temp files.
	entries, _ := os.ReadDir(overlay)
	if len(entries) != 1 || entries[0].Name() != "mine.yaml" {
		t.Fatalf("overlay must contain exactly mine.yaml, got %d entries", len(entries))
	}
	cat, err := LoadMerged(shipped, overlay)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	got, ok := cat.Get("mine")
	if !ok || got.Name != "Mine" || got.Origin != "custom" {
		t.Fatalf("round-trip failed: %+v", got)
	}
}

func TestDeleteOverlay(t *testing.T) {
	overlay := t.TempDir()
	tpl := Template{ID: "gone", Name: "Gone"}
	if err := WriteOverlay(overlay, tpl); err != nil {
		t.Fatal(err)
	}
	if err := DeleteOverlay(overlay, "gone"); err != nil {
		t.Fatalf("DeleteOverlay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(overlay, "gone.yaml")); !os.IsNotExist(err) {
		t.Fatal("file must be gone")
	}
	if err := DeleteOverlay(overlay, "gone"); err == nil {
		t.Fatal("deleting a missing overlay file must error")
	}
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"a", "abc-123", "ru-direct", "x"} {
		if !ValidID(ok) {
			t.Errorf("ValidID(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "A", "a_b", "a b", "привет", strings.Repeat("a", 65)} {
		if ValidID(bad) {
			t.Errorf("ValidID(%q) = true, want false", bad)
		}
	}
}

// TestOverlayExists: the overlay file probe used by the API delete
// guard (404 when there is nothing to delete).
func TestOverlayExists(t *testing.T) {
	overlay := t.TempDir()
	if OverlayExists(overlay, "nope") {
		t.Fatal("missing overlay must not exist")
	}
	if err := WriteOverlay(overlay, Template{ID: "yes", Name: "Y"}); err != nil {
		t.Fatal(err)
	}
	if !OverlayExists(overlay, "yes") {
		t.Fatal("written overlay must exist")
	}
	if OverlayExists("", "yes") {
		t.Fatal("empty dir must be false")
	}
}
