// E2E smoke for the v1.0.4 external-rules feature (OPE-3740): the
// phase 3 e2e (phase3_e2e_test.go) was written before external-rules
// existed; this file adds the missing scenario in smoke volume:
// read-only view of the live nikki rules, one-rule import (verbatim
// rule check + policy→target mapping), route enable, profile
// generation, bulk import with dedup, and the read-only invariant on
// the source file.
//
// Unlike TestPhase3E2E the API part of this test needs NO mihomo
// binary (pure HTTP against internal/api over a temp store). When
// bin/mihomo IS present, the profile generated from the imported
// route is additionally validated with `mihomo -t` (the same offline
// contract as Phase 1 M3: -t never reads provider payloads).
//
// Each step is bound to the endpoint code that implements it:
//
//	GET  /api/v1/external-rules           internal/api/api.go handleExternalRulesList
//	                                      (read-only, no store lock)
//	POST /api/v1/external-rules/import    handleExternalRuleImport (rule must exist
//	                                      verbatim; targetFromPolicy +
//	                                      serverOrGroupByName resolve the policy)
//	PATCH /api/v1/routes/{id}             handleRoutePatch (enable; FR-4.8
//	                                      re-validation of the merged route)
//	POST /api/v1/external-rules/import-all handleExternalRulesImportAll
//	                                      (disabled routes, dedup by condition
//	                                      type+value)
//	profile check                         internal/generator/generator.go
//	                                      (DOMAIN-SUFFIX → "DOMAIN-SUFFIX,<v>,rt:<id>")
//
// Run: go test ./test/e2e/ -run Phase3ExternalRules
package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/api"
	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"github.com/wellboard/wellboard/internal/store"
	"github.com/wellboard/wellboard/internal/templates"
)

// erOwnerConfig mimics the owner's real nikki run config: process
// rule (view-only), port rule, geoip rule with no-resolve, a domain
// rule aimed at a named policy, and the MATCH tail.
const erOwnerConfig = `mode: rule
proxies: []
proxy-groups: []
rules:
  - PROCESS-NAME,bittorrent,DIRECT
  - DST-PORT,6881-6889,DIRECT
  - GEOIP,ru,DIRECT,no-resolve
  - DOMAIN-SUFFIX,openai.com,proxy-vietnam
  - MATCH,proxy
`

// erFixture points the nikki run-config reader at a temp file and
// returns its path (the read-only invariant check re-reads it).
func erFixture(t *testing.T, body string) string {
	t.Helper()
	old := nikki.RunConfigPath
	dir := t.TempDir()
	nikki.RunConfigPath = filepath.Join(dir, "run", "config.yaml")
	t.Cleanup(func() { nikki.RunConfigPath = old })
	if err := os.MkdirAll(filepath.Dir(nikki.RunConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nikki.RunConfigPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return nikki.RunConfigPath
}

// erDo performs a JSON request against the test server.
func erDo(t *testing.T, ts *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	var rdr *bytes.Reader
	if body == "" {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func TestPhase3ExternalRulesSmoke(t *testing.T) {
	// 0. "Live" nikki run config, snapshotted for the read-only check.
	fixturePath := erFixture(t, erOwnerConfig)
	before, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}

	// Stack: temp store (DefaultState + manual server whose NAME
	// matches the external policy "proxy-vietnam", so the by-name
	// target resolution path is exercised) + shipped catalog + API.
	stStore := store.New(t.TempDir(), false)
	st := store.DefaultState()
	st.Sources = []model.Source{{ID: "src_manual", Kind: string(model.SourceManual), Name: "Manual"}}
	st.Servers = []model.Server{{
		ID: "srv_1", SourceID: "src_manual", Name: "proxy-vietnam", Type: "socks5",
		Raw: map[string]any{"name": "proxy-vietnam", "type": "socks5", "server": "127.0.0.1", "port": 1080},
	}}
	if err := stStore.Save(st); err != nil {
		t.Fatal(err)
	}
	catalog, err := templates.Load("../../templates")
	if err != nil {
		t.Fatalf("shipped catalog: %v", err)
	}
	srv := api.NewServer(stStore, catalog)
	srv.SetLog(func(format string, args ...any) { t.Logf("api: "+format, args...) })
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// 1. Read-only view: every rule line is surfaced with its index,
	// type, value, target and no-resolve flag; PROCESS-NAME and MATCH
	// are view-only; the source path is reported. (handleExternalRulesList)
	code, body := erDo(t, ts, http.MethodGet, "/api/v1/external-rules", "")
	if code != http.StatusOK {
		t.Fatalf("external-rules view: %d %s", code, body)
	}
	var view struct {
		Source  string `json:"source"`
		Count   int    `json:"count"`
		Targets []string
		Rules   []struct {
			Type       string `json:"type"`
			Value      string `json:"value"`
			Target     string `json:"target"`
			NoResolve  bool   `json:"no_resolve"`
			Importable bool   `json:"importable"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if view.Source != fixturePath {
		t.Fatalf("source = %q, want the fixture path %q", view.Source, fixturePath)
	}
	if view.Count != 5 || len(view.Rules) != 5 {
		t.Fatalf("count = %d / %d rules, want 5 (every line is surfaced)", view.Count, len(view.Rules))
	}
	if view.Rules[0].Type == "PROCESS-NAME" && view.Rules[0].Importable {
		t.Fatalf("PROCESS-NAME must be view-only: %+v", view.Rules[0])
	}
	if !view.Rules[3].Importable || view.Rules[3].Value != "openai.com" {
		t.Fatalf("DOMAIN-SUFFIX must be importable: %+v", view.Rules[3])
	}
	if !view.Rules[2].NoResolve {
		t.Fatalf("no-resolve flag lost: %+v", view.Rules[2])
	}
	found := false
	for _, tg := range view.Targets {
		if tg == "proxy-vietnam" {
			found = true
		}
	}
	if !found {
		t.Fatalf("named policy not in targets list: %v", view.Targets)
	}
	t.Logf("view OK: %d rules, targets %v", view.Count, view.Targets)

	// 2. One-rule import WITHOUT an explicit target: the policy
	// "proxy-vietnam" resolves by display name to the WellBoard server
	// srv_1; the route is created DISABLED (never applied silently).
	// (handleExternalRuleImport + serverOrGroupByName)
	code, body = erDo(t, ts, http.MethodPost, "/api/v1/external-rules/import",
		`{"rule":"DOMAIN-SUFFIX,openai.com,proxy-vietnam"}`)
	if code != http.StatusCreated {
		t.Fatalf("import: %d %s", code, body)
	}
	var imp struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Enabled       bool   `json:"enabled"`
		OnUnavailable string `json:"on_unavailable"`
		Target        struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"target"`
		Conditions []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"conditions"`
	}
	if err := json.Unmarshal([]byte(body), &imp); err != nil {
		t.Fatal(err)
	}
	if imp.Enabled {
		t.Fatalf("imported route must default to disabled")
	}
	if imp.Target.Type != "server" || imp.Target.ID != "srv_1" {
		t.Fatalf("policy must resolve to server srv_1 by name: %+v", imp.Target)
	}
	if imp.OnUnavailable != "block" {
		t.Fatalf("on_unavailable = %q, want block", imp.OnUnavailable)
	}
	if len(imp.Conditions) != 1 || imp.Conditions[0].Type != "domain-suffix" || imp.Conditions[0].Value != "openai.com" {
		t.Fatalf("condition wrong: %+v", imp.Conditions)
	}
	t.Logf("import OK: %s disabled, target %+v, name %q", imp.ID, imp.Target, imp.Name)

	// 3. The route is an ordinary route: visible in GET /routes and
	// enabled through the normal PATCH (handleRoutePatch).
	code, body = erDo(t, ts, http.MethodGet, "/api/v1/routes", "")
	if code != http.StatusOK || !strings.Contains(body, imp.ID) {
		t.Fatalf("imported route not in routes list: %d %.200s", code, body)
	}
	patch, err := json.Marshal(map[string]any{
		"name":       imp.Name,
		"enabled":    true,
		"conditions": imp.Conditions,
		"target":     map[string]string{"type": imp.Target.Type, "id": imp.Target.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	code, body = erDo(t, ts, http.MethodPatch, "/api/v1/routes/"+imp.ID, string(patch))
	if code != http.StatusOK {
		t.Fatalf("enable imported route: %d %s", code, body)
	}
	t.Logf("route %s enabled via PATCH /routes", imp.ID)

	// 4. The enabled imported route lands in the generated profile as
	// a DOMAIN-SUFFIX rule line aimed at its service group
	// (generator.go conditionRule + ServiceGroupPrefix).
	stAfter, err := stStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	out, err := generator.Generate(stAfter)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	ruleLine := "DOMAIN-SUFFIX,openai.com,rt:" + imp.ID
	if !strings.Contains(string(out), ruleLine) {
		t.Fatalf("generated profile missing %q:\n%.400s", ruleLine, out)
	}
	t.Logf("profile contains %q", ruleLine)

	// 5. Offline validation with the real mihomo binary when present
	// (same skip semantics as the other e2e files; -t never reads
	// provider payloads, Phase 1 M3).
	if bin := erMihomoBin(); bin != "" {
		dir := t.TempDir()
		profilePath := filepath.Join(dir, "profile.yaml")
		if err := os.WriteFile(profilePath, out, 0o644); err != nil {
			t.Fatal(err)
		}
		tst := exec.Command(bin, "-t", "-d", dir, "-f", profilePath)
		if o, err := tst.CombinedOutput(); err != nil {
			t.Fatalf("mihomo -t failed:\n%s\n%v", o, err)
		}
		t.Logf("mihomo -t OK on the profile with the imported route")
	} else {
		t.Log("mihomo binary absent — skipping the -t validation (API smoke still complete)")
	}

	// 6. Smuggled rule: the import must re-parse the LIVE file and
	// reject a rule that is not present verbatim (409).
	code, body = erDo(t, ts, http.MethodPost, "/api/v1/external-rules/import",
		`{"rule":"DOMAIN-SUFFIX,evil.example,DIRECT"}`)
	if code != http.StatusConflict {
		t.Fatalf("smuggled rule must be 409, got %d %s", code, body)
	}
	t.Log("smuggled rule rejected with 409")

	// 7. Bulk import: DST-PORT and GEOIP rules import (DIRECT policy),
	// the already-imported DOMAIN-SUFFIX is skipped as duplicate,
	// PROCESS-NAME and MATCH are view-only. (handleExternalRulesImportAll)
	code, body = erDo(t, ts, http.MethodPost, "/api/v1/external-rules/import-all", "")
	if code != http.StatusCreated {
		t.Fatalf("import-all: %d %s", code, body)
	}
	var all struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
		Total    int `json:"total"`
		SkippedD []struct {
			Index  int    `json:"index"`
			Raw    string `json:"raw"`
			Reason string `json:"reason"`
		} `json:"skipped_rules"`
	}
	if err := json.Unmarshal([]byte(body), &all); err != nil {
		t.Fatal(err)
	}
	if all.Total != 5 {
		t.Fatalf("total = %d, want 5", all.Total)
	}
	if all.Imported != 2 || all.Skipped != 3 {
		t.Fatalf("import-all: imported %d skipped %d, want 2/3 (dst-port + geoip import; process/match/duplicate skip): %s", all.Imported, all.Skipped, body)
	}
	sawDup, sawViewOnly := false, false
	for _, s := range all.SkippedD {
		if s.Raw == "DOMAIN-SUFFIX,openai.com,proxy-vietnam" && strings.Contains(s.Reason, "already imported") {
			sawDup = true
		}
		if strings.Contains(s.Reason, "read-only") {
			sawViewOnly = true
		}
	}
	if !sawDup || !sawViewOnly {
		t.Fatalf("skip reasons missing dup (%v) or view-only (%v): %+v", sawDup, sawViewOnly, all.SkippedD)
	}
	t.Logf("import-all: %d imported, %d skipped of %d", all.Imported, all.Skipped, all.Total)

	// 8. Re-running bulk import is a no-op (dedup by condition type+value).
	code, body = erDo(t, ts, http.MethodPost, "/api/v1/external-rules/import-all", "")
	if code != http.StatusCreated {
		t.Fatalf("import-all #2: %d %s", code, body)
	}
	var all2 struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(body), &all2); err != nil {
		t.Fatal(err)
	}
	if all2.Imported != 0 || all2.Skipped != 5 {
		t.Fatalf("second import-all must be a no-op: imported %d skipped %d", all2.Imported, all2.Skipped)
	}

	// 9. Route surface after everything: 1 manual import + 2 bulk = 3.
	code, body = erDo(t, ts, http.MethodGet, "/api/v1/routes", "")
	if code != http.StatusOK {
		t.Fatalf("routes: %d", code)
	}
	if n := strings.Count(body, `"id":"rt_`); n != 3 {
		t.Fatalf("expected 3 routes after imports, got %d: %.400s", n, body)
	}

	// 10. Read-only invariant: the external source file is byte-identical.
	after, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("external source file was MODIFIED by the import flows (read-only invariant broken)")
	}
	t.Log("read-only invariant holds: nikki run config untouched")
}

// erMihomoBin reports the mihomo binary when present (no skip: the
// smoke must run its API part even without the binary).
func erMihomoBin() string {
	if _, err := os.Stat(mihomoBin); err == nil {
		return mihomoBin
	}
	if v := os.Getenv("WELLBOARD_MIHOMO"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	return ""
}
