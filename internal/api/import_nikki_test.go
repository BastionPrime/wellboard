package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// OPE-3045 A3 tests. Secret rule: fixtures use FAKE URLs
// (https://example.invalid/…) only; assertions never echo a full URL
// into the test output.

const fakeNikkiURL1 = "https://example.invalid/nikki/one"
const fakeNikkiURL2 = "https://example.invalid/nikki/two"

const nikkiRunFixture = `mixed-port: 7890
proxy-providers:
  provider-a:
    type: http
    url: ` + fakeNikkiURL1 + `
  provider-b:
    type: file
    path: ./b.yaml
`

const nikkiProfileFixture = `proxy-providers:
  provider-c:
    type: http
    url: ` + fakeNikkiURL2 + `
`

// nikkiFixtureDir writes two fake nikki config files and returns the
// paths for SetNikkiPaths.
func nikkiFixtureDir(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	run := filepath.Join(dir, "config.yaml")
	prof := filepath.Join(dir, "profile.yaml")
	if err := os.WriteFile(run, []byte(nikkiRunFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prof, []byte(nikkiProfileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{run, prof}
}

func TestImportNikkiCreatesSources(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	srv.SetNikkiPaths(nikkiFixtureDir(t))

	code, body := do(t, ts, "POST", "/api/v1/sources/import-nikki", `{}`)
	if code != http.StatusOK {
		t.Fatalf("import-nikki: %d %s", code, body)
	}
	var out struct {
		Imported int `json:"imported"`
		Found    int `json:"found"`
		Sources  []struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			MaskedURL string `json:"masked_url"`
			Enabled   bool   `json:"enabled"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Imported != 2 || out.Found != 2 {
		t.Fatalf("imported=%d found=%d, want 2/2", out.Imported, out.Found)
	}
	if len(out.Sources) != 2 {
		t.Fatalf("sources array length = %d, want 2", len(out.Sources))
	}
	for _, src := range out.Sources {
		if src.Kind != "subscription" || !strings.HasPrefix(src.ID, "sub_") {
			t.Fatalf("imported source must be a subscription with sub_ id: %+v", src)
		}
		if !src.Enabled {
			t.Fatalf("imported source must be enabled: %+v", src)
		}
		if src.MaskedURL == "" || !strings.Contains(src.MaskedURL, "…(") {
			t.Fatalf("import response must carry a masked URL row, got %q", src.MaskedURL)
		}
	}
	// The response of this NEW endpoint must not leak the subscription
	// URL (review blocker on OPE-3045 A3).
	if strings.Contains(body, "example.invalid") && strings.Contains(body, "/nikki/") {
		t.Fatal("import-nikki response leaked a full subscription URL")
	}
	// Idempotent: re-import must add nothing.
	code, body = do(t, ts, "POST", "/api/v1/sources/import-nikki", `{}`)
	if code != http.StatusOK {
		t.Fatalf("second import: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Imported != 0 || len(out.Sources) != 2 {
		t.Fatalf("second import must be a no-op: imported=%d sources=%d", out.Imported, len(out.Sources))
	}
}

func TestImportNikkiNoFiles(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	srv.SetNikkiPaths([]string{"/nonexistent/nikki/config.yaml"})

	code, body := do(t, ts, "POST", "/api/v1/sources/import-nikki", "")
	if code != http.StatusOK {
		t.Fatalf("import-nikki without nikki present: %d %s", code, body)
	}
	var out struct {
		Imported int `json:"imported"`
		Found    int `json:"found"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Imported != 0 || out.Found != 0 {
		t.Fatalf("no nikki → imported=%d found=%d, want 0/0", out.Imported, out.Found)
	}
}

func TestSettingsGetHasMaskedSourcesSummary(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	srv.SetNikkiPaths(nikkiFixtureDir(t))

	// Import, then read the settings page data.
	if code, body := do(t, ts, "POST", "/api/v1/sources/import-nikki", `{}`); code != http.StatusOK {
		t.Fatalf("import-nikki: %d %s", code, body)
	}
	code, body := do(t, ts, "GET", "/api/v1/settings", "")
	if code != http.StatusOK {
		t.Fatalf("settings get: %d %s", code, body)
	}
	var out struct {
		UIPort         int `json:"ui_port"`
		SourcesSummary []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Kind      string `json:"kind"`
			MaskedURL string `json:"masked_url"`
			Enabled   bool   `json:"enabled"`
		} `json:"sources_summary"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.UIPort != 8090 {
		t.Fatalf("settings shape must be unchanged (ui_port=%d)", out.UIPort)
	}
	if len(out.SourcesSummary) != 2 {
		t.Fatalf("sources_summary rows = %d, want 2", len(out.SourcesSummary))
	}
	for _, row := range out.SourcesSummary {
		if row.Kind != "subscription" {
			t.Fatalf("summary row kind = %q", row.Kind)
		}
		// Masking: prefix only, never the full URL (secret rule).
		if row.MaskedURL == "" || strings.Contains(row.MaskedURL, "example.invalid") {
			t.Fatalf("masked_url must be masked, got %q", row.MaskedURL)
		}
		if !strings.HasPrefix(row.MaskedURL, "https://ex") || !strings.Contains(row.MaskedURL, "…(") {
			t.Fatalf("masked_url format unexpected: %q", row.MaskedURL)
		}
		// The full URL must NOT appear anywhere in the response.
		if strings.Contains(body, fakeNikkiURL1) || strings.Contains(body, fakeNikkiURL2) {
			t.Fatal("settings response must not contain a full URL")
		}
	}
	// Short/garbage URL → fully masked.
	if m := maskURL("short"); m != "****" {
		t.Fatalf("maskURL(short) = %q, want ****", m)
	}
	if m := maskURL(""); m != "****" {
		t.Fatalf("maskURL(empty) = %q, want ****", m)
	}
}

func TestImportNikkiRejectsUnknownField(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	if code, _ := do(t, ts, "POST", "/api/v1/sources/import-nikki", `{"bogus":1}`); code != http.StatusBadRequest {
		t.Fatalf("unknown field must 400 (strict), got %d", code)
	}
}
