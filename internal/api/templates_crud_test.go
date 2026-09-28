package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/store"
	"github.com/wellboard/wellboard/internal/templates"
)

// newTemplatesTestServer is newTestServer plus the overlay dir wiring
// (OPE-3045 B1). Returns the server (for store inspection), the test
// server and the overlay dir.
func newTemplatesTestServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	stStore := store.New(t.TempDir(), false)
	if err := stStore.Save(store.DefaultState()); err != nil {
		t.Fatal(err)
	}
	catalog, err := templates.Load("../../templates")
	if err != nil {
		t.Fatalf("shipped catalog: %v", err)
	}
	overlay := t.TempDir()
	srv := NewServer(stStore, catalog)
	srv.SetOverlayDir(overlay)
	mux := http.NewServeMux()
	srv.Register(mux)
	return srv, httptest.NewServer(mux), overlay
}

func TestTemplatesCustomCRUD(t *testing.T) {
	srv, ts, overlay := newTemplatesTestServer(t)
	defer ts.Close()

	// 1. Create a custom template.
	code, body := do(t, ts, "POST", "/api/v1/templates", `{
		"id":"mine","name":"My list",
		"description":"my custom template",
		"list_source":"custom geosite categories",
		"typical_target":"direct",
		"conditions":[
			{"type":"geosite","value":"youtube"},
			{"type":"domain-suffix","value":"x.com"}
		]
	}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var tpl struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Origin   string `json:"origin"`
		Disabled bool   `json:"disabled"`
	}
	if err := json.Unmarshal([]byte(body), &tpl); err != nil {
		t.Fatal(err)
	}
	if tpl.Origin != "custom" || tpl.Disabled {
		t.Fatalf("created entry: %+v", tpl)
	}

	// The overlay file exists and carries the yaml fields.
	data, err := os.ReadFile(filepath.Join(overlay, "mine.yaml"))
	if err != nil {
		t.Fatalf("overlay file: %v", err)
	}
	if !strings.Contains(string(data), "name: My list") ||
		!strings.Contains(string(data), "value: youtube") {
		t.Fatalf("overlay yaml wrong:\n%s", data)
	}

	// 2. List shows origin flags.
	code, body = do(t, ts, "GET", "/api/v1/templates", "")
	if code != 200 || !strings.Contains(body, `"origin":"custom"`) || !strings.Contains(body, `"origin":"builtin"`) {
		t.Fatalf("list: %d %.200s", code, body)
	}

	// 3. Update (PUT).
	code, body = do(t, ts, "PUT", "/api/v1/templates/mine", `{
		"name":"My list v2",
		"description":"edited",
		"typical_target":"reject",
		"conditions":[{"type":"domain","value":"y.com"}]
	}`)
	if code != 200 || !strings.Contains(body, "My list v2") {
		t.Fatalf("update: %d %s", code, body)
	}

	// Body id mismatch → 400.
	if code, _ := do(t, ts, "PUT", "/api/v1/templates/mine", `{"id":"other","name":"x"}`); code != 400 {
		t.Fatalf("id mismatch must 400, got %d", code)
	}
	// PUT unknown id → 404.
	if code, _ := do(t, ts, "PUT", "/api/v1/templates/nope", `{"name":"x"}`); code != 404 {
		t.Fatalf("unknown PUT must 404, got %d", code)
	}

	// 4. Toggle disable (custom id) — persists in settings.
	code, body = do(t, ts, "POST", "/api/v1/templates/mine/toggle", `{"disabled":true}`)
	if code != 200 || !strings.Contains(body, `"disabled":true`) {
		t.Fatalf("toggle: %d %s", code, body)
	}
	st, _ := srv.Store.Load()
	if len(st.Settings.DisabledTemplates) != 1 || st.Settings.DisabledTemplates[0] != "mine" {
		t.Fatalf("disabled_templates must persist: %v", st.Settings.DisabledTemplates)
	}

	// 5. Apply of a disabled template → 409.
	if code, _ := do(t, ts, "POST", "/api/v1/templates/mine/apply", `{"target":{"type":"direct"}}`); code != 409 {
		t.Fatalf("apply disabled must 409, got %d", code)
	}

	// 6. Re-enable → apply works.
	if code, _ := do(t, ts, "POST", "/api/v1/templates/mine/toggle", `{"disabled":false}`); code != 200 {
		t.Fatalf("re-enable: %d", code)
	}
	if code, _ := do(t, ts, "POST", "/api/v1/templates/mine/apply", `{"target":{"type":"direct"}}`); code != 201 {
		t.Fatalf("apply after re-enable must 201, got %d", code)
	}

	// 7. Validation failures → 400.
	badBodies := []string{
		`{"id":"BAD","name":"x"}`,                       // id charset
		`{"id":"ok1","name":""}`,                        // empty name
		`{"id":"ok1","name":"x","typical_target":"zz"}`, // bad target
		`{"id":"ok1","name":"x","conditions":[{"type":"weird","value":"a"}]}`,
		`{"id":"ok1","name":"x","conditions":[{"type":"ip-cidr","value":"nope"}]}`,
		`{"id":"ok1","name":"x","conditions":[{"type":"domain","value":"a,b"}]}`,
		`{"id":"ok1","name":"x","providers":["ghost"]}`,
	}
	for _, b := range badBodies {
		if code, _ := do(t, ts, "POST", "/api/v1/templates", b); code != 400 {
			t.Fatalf("bad body must 400: %s → %d", b, code)
		}
	}

	// 8. Override a builtin (writing a builtin id IS editing it).
	code, body = do(t, ts, "POST", "/api/v1/templates", `{
		"id":"ads-block","name":"Ads CUSTOM","typical_target":"reject",
		"conditions":[{"type":"geosite","value":"category-ads-all"}]
	}`)
	if code != 201 {
		t.Fatalf("override create: %d %s", code, body)
	}
	if !strings.Contains(body, `"overridden":true`) {
		t.Fatalf("override must be marked: %.200s", body)
	}
	// Deleting the override restores the builtin.
	if code, _ := do(t, ts, "DELETE", "/api/v1/templates/ads-block", ""); code != 200 {
		t.Fatalf("delete override: %d", code)
	}
	code, body = do(t, ts, "GET", "/api/v1/templates/ads-block", "")
	if code != 200 || !strings.Contains(body, `"origin":"builtin"`) {
		t.Fatalf("builtin must re-appear after override delete: %d %.200s", code, body)
	}

	// 9. Delete the custom template.
	if code, _ := do(t, ts, "DELETE", "/api/v1/templates/mine", ""); code != 200 {
		t.Fatalf("delete custom: %d", code)
	}
	if code, _ := do(t, ts, "GET", "/api/v1/templates/mine", ""); code != 404 {
		t.Fatalf("deleted custom must 404, got %d", code)
	}

	// 10. Deleting a plain builtin (no overlay file) → 404.
	if code, _ := do(t, ts, "DELETE", "/api/v1/templates/streaming", ""); code != 404 {
		t.Fatalf("delete without overlay file must 404, got %d", code)
	}

	// 11. Unknown template toggle → 404.
	if code, _ := do(t, ts, "POST", "/api/v1/templates/nope/toggle", `{"disabled":true}`); code != 404 {
		t.Fatalf("unknown toggle must 404, got %d", code)
	}
}

// TestTemplatesBuiltinDisable: disabling a BUILTIN id persists in
// settings and apply refuses with 409.
func TestTemplatesBuiltinDisable(t *testing.T) {
	srv, ts, _ := newTemplatesTestServer(t)
	defer ts.Close()

	code, body := do(t, ts, "POST", "/api/v1/templates/ai/toggle", `{"disabled":true}`)
	if code != 200 || !strings.Contains(body, `"disabled":true`) {
		t.Fatalf("toggle builtin: %d %s", code, body)
	}
	st, _ := srv.Store.Load()
	if len(st.Settings.DisabledTemplates) != 1 || st.Settings.DisabledTemplates[0] != "ai" {
		t.Fatalf("builtin disable must persist: %v", st.Settings.DisabledTemplates)
	}
	// Apply → 409.
	if code, _ := do(t, ts, "POST", "/api/v1/templates/ai/apply", `{"target":{"type":"direct"}}`); code != 409 {
		t.Fatalf("apply disabled builtin must 409, got %d", code)
	}
	// Toggle off removes it from the settings list.
	if code, _ := do(t, ts, "POST", "/api/v1/templates/ai/toggle", `{"disabled":false}`); code != 200 {
		t.Fatalf("re-enable: %d", code)
	}
	st, _ = srv.Store.Load()
	if len(st.Settings.DisabledTemplates) != 0 {
		t.Fatalf("disabled list must be empty: %v", st.Settings.DisabledTemplates)
	}
}

// TestOverlayWarningsSurface: a broken overlay file produces warnings
// in GET /templates; the shipped catalog still serves.
func TestOverlayWarningsSurface(t *testing.T) {
	_, ts, overlay := newTemplatesTestServer(t)
	defer ts.Close()
	if err := os.WriteFile(filepath.Join(overlay, "broken.yaml"), []byte("id: broken\nname: x\ntypical_target: zz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// GET /templates serves the in-memory catalog; force a reload
	// through a write endpoint (create + delete of a scratch template).
	code, body := do(t, ts, "POST", "/api/v1/templates", `{"id":"tmp","name":"T"}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if code, _ := do(t, ts, "DELETE", "/api/v1/templates/tmp", ""); code != 200 {
		t.Fatal("delete tmp")
	}
	code, body = do(t, ts, "GET", "/api/v1/templates", "")
	if code != 200 || !strings.Contains(body, "broken.yaml") || !strings.Contains(body, "warnings") {
		t.Fatalf("warnings must surface: %d %.300s", code, body)
	}
	// The broken template itself is absent.
	if strings.Contains(body, `"id":"broken"`) {
		t.Fatalf("broken overlay must be skipped: %.300s", body)
	}
}

// TestGeodataTagsEndpoint: tags endpoint returns the static fallback
// (no local .dat in tests) and rejects unknown kinds.
func TestGeodataTagsEndpoint(t *testing.T) {
	srv, ts, _ := newTemplatesTestServer(t)
	defer ts.Close()
	// No local .dat: fall back to the curated list (contains "ru").
	code, body := do(t, ts, "GET", "/api/v1/geodata/tags", "")
	if code != 200 || !strings.Contains(body, `"ru"`) || !strings.Contains(body, `"tags"`) {
		t.Fatalf("tags: %d %.200s", code, body)
	}
	if code, _ := do(t, ts, "GET", "/api/v1/geodata/tags?kind=geoip", ""); code != 400 {
		t.Fatalf("unknown kind must 400, got %d", code)
	}
	// kind=geosite explicit → same answer.
	if code, _ := do(t, ts, "GET", "/api/v1/geodata/tags?kind=geosite", ""); code != 200 {
		t.Fatalf("kind=geosite must 200, got %d", code)
	}

	// Probe list override: a fixture .dat wins over the static list.
	dir := srv4k(t)
	srv.SetGeositeDatPaths([]string{filepath.Join(dir, "geosite.dat")})
	// A tiny v2fly-format .dat with two tags (0x0a-len-tag records).
	dat := []byte{0x0a, 0x03, 0x0a, 0x01, 'A', 0x0a, 0x03, 0x0a, 0x01, 'B'}
	if err := os.WriteFile(filepath.Join(dir, "geosite.dat"), dat, 0o644); err != nil {
		t.Fatal(err)
	}
	code, body = do(t, ts, "GET", "/api/v1/geodata/tags", "")
	if code != 200 || !strings.Contains(body, `"a"`) || !strings.Contains(body, `"b"`) {
		t.Fatalf("tags from dat: %d %.200s", code, body)
	}
}

// srv4k is a temp dir helper with a short name.
func srv4k(t *testing.T) string {
	return t.TempDir()
}

// TestSettingsGeodataPatchRoundTrip: the new geodata settings persist
// and return through GET.
func TestSettingsGeodataPatchRoundTrip(t *testing.T) {
	_, ts, _ := newTemplatesTestServer(t)
	defer ts.Close()

	code, body := do(t, ts, "PATCH", "/api/v1/settings", `{
		"geosite_source":"custom",
		"geosite_custom_url":"https://example.invalid/geosite.dat",
		"geoip_source":"metacubex",
		"geodata_additions":{"geosite:youtube":["myvids.example","more.example"],"geoip:ru":["203.0.113.0/24"]}
	}`)
	if code != 200 {
		t.Fatalf("patch: %d %s", code, body)
	}
	code, body = do(t, ts, "GET", "/api/v1/settings", "")
	if code != 200 ||
		!strings.Contains(body, `"geosite_source":"custom"`) ||
		!strings.Contains(body, `"geosite_custom_url":"https://example.invalid/geosite.dat"`) ||
		!strings.Contains(body, `"geoip_source":"metacubex"`) ||
		!strings.Contains(body, `"geodata_additions"`) {
		t.Fatalf("settings round-trip: %d %s", code, body)
	}
	// Empty-additions replace clears the map.
	if code, _ := do(t, ts, "PATCH", "/api/v1/settings", `{"geodata_additions":{}}`); code != 200 {
		t.Fatalf("empty additions replace must 200, got %d", code)
	}
}

var _ = model.Settings{}
