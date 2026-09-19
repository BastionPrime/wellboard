package templates

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCatalog(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadShippedCatalog(t *testing.T) {
	cat, err := Load("../../templates")
	if err != nil {
		t.Fatalf("shipped catalog must load: %v", err)
	}
	if len(cat.Templates) != AllDefault {
		t.Fatalf("Appendix В ships 10 templates, got %d", len(cat.Templates))
	}
	want := map[string]bool{
		"ru-direct": false, "streaming": false, "social": false, "messengers": false,
		"ai": false, "dev": false, "games": false, "torrents": false,
		"ads-block": false, "all-vpn": false,
	}
	for _, tpl := range cat.Templates {
		if _, ok := want[tpl.ID]; !ok {
			t.Errorf("unexpected template %q", tpl.ID)
			continue
		}
		want[tpl.ID] = true
		if tpl.Name == "" || tpl.Description == "" {
			t.Errorf("template %q missing name/description", tpl.ID)
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("template %q missing from the catalog", id)
		}
	}
}

func TestLoadValidation(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"id mismatch", map[string]string{
			"a.yaml": "id: other\nname: A\n",
		}},
		{"empty name", map[string]string{
			"a.yaml": "id: a\nname: \"\"\n",
		}},
		{"unknown condition type", map[string]string{
			"a.yaml": "id: a\nname: A\nconditions:\n  - {type: bogus, value: x}\n",
		}},
		{"condition without value", map[string]string{
			"a.yaml": "id: a\nname: A\nconditions:\n  - {type: geosite, value: \"\"}\n",
		}},
		{"bad typical_target", map[string]string{
			"a.yaml": "id: a\nname: A\ntypical_target: sideways\n",
		}},
		{"bad provider name (path escape)", map[string]string{
			"a.yaml": "id: a\nname: A\nproviders: [\"../evil\"]\n",
		}},
		{"empty dir", map[string]string{
			"providers/keep.yaml": "payload: []\n",
		}},
	}
	for _, c := range cases {
		dir := writeCatalog(t, c.files)
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	// id defaults to the file stem when absent.
	dir := writeCatalog(t, map[string]string{"only.yaml": "name: Only\n"})
	cat, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cat.Templates[0].ID != "only" {
		t.Fatalf("id must default to file stem, got %q", cat.Templates[0].ID)
	}
}

func TestGetAndProviderNames(t *testing.T) {
	dir := writeCatalog(t, map[string]string{
		"one.yaml":   "id: one\nname: One\ntypical_target: group\nproviders: [a, b]\n",
		"two.yaml":   "id: two\nname: Two\nproviders: [b, c]\n",
		"three.yaml": "id: three\nname: Three\n",
	})
	cat, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Get("nope"); ok {
		t.Fatal("Get(nope) must miss")
	}
	got := cat.ProviderNames([]string{"one", "two", "missing"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("ProviderNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ProviderNames = %v, want %v", got, want)
		}
	}
}

func TestLoadMissingDir(t *testing.T) {
	if _, err := Load(t.TempDir() + "/nope"); err == nil {
		t.Fatal("missing dir must error")
	}
}
