package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wellboard/wellboard/internal/nikki"
)

// externalFixture points the nikki run-config reader at a temp file
// shaped like the owner's real config (mixin rules + subscription tail).
func externalFixture(t *testing.T, body string) {
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
}

const ownerLikeConfig = `mode: rule
proxies: []
proxy-groups: []
rules:
  - PROCESS-NAME,bittorrent,DIRECT
  - DST-PORT,6881-6889,DIRECT
  - GEOIP,private,DIRECT,no-resolve
  - GEOIP,ru,DIRECT
  - DOMAIN-SUFFIX,openai.com,proxy-vietnam
  - DOMAIN,claude.ai,proxy-vietnam
  - SRC-IP-CIDR,192.168.0.227/32,DIRECT
  - MATCH,proxy
  - MATCH,→ Remnawave
`

func TestExternalRulesListCountsEverything(t *testing.T) {
	externalFixture(t, ownerLikeConfig)
	_, ts := newTestServer(t)
	defer ts.Close()
	code, body := do(t, ts, "GET", "/api/v1/external-rules", "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var view struct {
		Source  string   `json:"source"`
		Count   int      `json:"count"`
		Targets []string `json:"targets"`
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
	if view.Count != 9 || len(view.Rules) != 9 {
		t.Fatalf("count mismatch: view.Count=%d len(rules)=%d, want 9 (count must equal the rule lines)", view.Count, len(view.Rules))
	}
	if view.Rules[0].Type != "PROCESS-NAME" || view.Rules[0].Importable {
		t.Fatalf("PROCESS-NAME must be view-only: %+v", view.Rules[0])
	}
	if !view.Rules[4].Importable || view.Rules[4].Value != "openai.com" {
		t.Fatalf("DOMAIN-SUFFIX must be importable: %+v", view.Rules[4])
	}
	if !view.Rules[2].NoResolve {
		t.Fatalf("no-resolve flag lost: %+v", view.Rules[2])
	}
	if view.Rules[7].Type != "MATCH" || view.Rules[7].Importable {
		t.Fatalf("MATCH must be view-only: %+v", view.Rules[7])
	}
	if view.Source != nikki.RunConfigPath {
		t.Fatalf("source path not surfaced: %q", view.Source)
	}
}

func TestExternalRulesListMissingFileIsUsable(t *testing.T) {
	old := nikki.RunConfigPath
	nikki.RunConfigPath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	t.Cleanup(func() { nikki.RunConfigPath = old })
	_, ts := newTestServer(t)
	defer ts.Close()
	code, body := do(t, ts, "GET", "/api/v1/external-rules", "")
	if code != 200 {
		t.Fatalf("missing file must not 500, got %d: %s", code, body)
	}
	if body == "" || !contains(body, `"count":0`) {
		t.Fatalf("missing file view wrong: %s", body)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && index(s, sub) >= 0)
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestExternalRuleImportCreatesDisabledRoute(t *testing.T) {
	externalFixture(t, ownerLikeConfig)
	srv, ts := newTestServer(t)
	defer ts.Close()
	// import DOMAIN-SUFFIX,openai.com,proxy-vietnam with an explicit
	// group target: WellBoard has no groups yet → create one first.
	grp := `{"name":"proxy-vietnam","type":"select","members":[]}`
	if code, body := do(t, ts, "POST", "/api/v1/groups", grp); code != 400 {
		// groups need members; use direct instead for the happy path
		_ = body
	}
	// happy path: DIRECT policy → target direct
	payload := `{"rule":"DOMAIN-SUFFIX,openai.com,proxy-vietnam","target":{"type":"group","id":"grp_1"}}`
	_ = payload
	// create a group properly (with a server is heavy; use direct route)
	code, body := do(t, ts, "POST", "/api/v1/external-rules/import",
		`{"rule":"DST-PORT,6881-6889,DIRECT"}`)
	if code != 201 {
		t.Fatalf("import DIRECT failed: %d %s", code, body)
	}
	var rt struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Target  struct {
			Type string `json:"type"`
		} `json:"target"`
		Conditions []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"conditions"`
	}
	if err := json.Unmarshal([]byte(body), &rt); err != nil {
		t.Fatal(err)
	}
	if rt.Enabled {
		t.Fatalf("imported route must default to disabled (never applied silently)")
	}
	if rt.Target.Type != "direct" {
		t.Fatalf("DIRECT policy must map to direct: %+v", rt.Target)
	}
	if len(rt.Conditions) != 1 || rt.Conditions[0].Type != "dst-port" || rt.Conditions[0].Value != "6881-6889" {
		t.Fatalf("condition wrong: %+v", rt.Conditions)
	}
	// the route appears in the normal routes list and is deletable
	code, body = do(t, ts, "GET", "/api/v1/routes", "")
	if code != 200 || !contains(body, `"dst-port"`) {
		t.Fatalf("imported route not in routes list: %d %s", code, body)
	}
	code, _ = do(t, ts, "DELETE", "/api/v1/routes/"+rt.ID, "")
	if code != 200 {
		t.Fatalf("imported route not deletable: %d", code)
	}
	// state must round-trip through the store
	if _, err := srv.Store.Load(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalRuleImportRejectsSmuggledRule(t *testing.T) {
	externalFixture(t, ownerLikeConfig)
	_, ts := newTestServer(t)
	defer ts.Close()
	// rule not present in the live external set → 409
	code, body := do(t, ts, "POST", "/api/v1/external-rules/import",
		`{"rule":"DOMAIN-SUFFIX,evil.example,REJECT"}`)
	if code != 409 {
		t.Fatalf("smuggled rule must 409, got %d: %s", code, body)
	}
	// view-only type → 400
	code, _ = do(t, ts, "POST", "/api/v1/external-rules/import",
		`{"rule":"MATCH,proxy"}`)
	if code != 400 {
		t.Fatalf("MATCH import must 400, got %d", code)
	}
	// unknown named policy → 400 naming it
	code, body = do(t, ts, "POST", "/api/v1/external-rules/import",
		`{"rule":"DOMAIN-SUFFIX,openai.com,proxy-vietnam"}`)
	if code != 400 || !contains(body, "proxy-vietnam") {
		t.Fatalf("unknown policy must 400 naming it, got %d: %s", code, body)
	}
}

func TestExternalRuleImportMapsGeoIPNoResolve(t *testing.T) {
	externalFixture(t, ownerLikeConfig)
	_, ts := newTestServer(t)
	defer ts.Close()
	code, body := do(t, ts, "POST", "/api/v1/external-rules/import",
		`{"rule":"GEOIP,private,DIRECT,no-resolve"}`)
	if code != 201 {
		t.Fatalf("geoip import failed: %d %s", code, body)
	}
	var rt struct {
		Conditions []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"conditions"`
	}
	if err := json.Unmarshal([]byte(body), &rt); err != nil {
		t.Fatal(err)
	}
	if len(rt.Conditions) != 1 || rt.Conditions[0].Type != "geoip" || rt.Conditions[0].Value != "private" {
		t.Fatalf("geoip condition wrong: %+v", rt.Conditions)
	}
}

// The acceptance stop-rule: importing must not touch the external
// source file. sha256/mtime is checked in the e2e router run; here we
// guard the file mode + write event cheaply by content equality.
func TestExternalRuleImportDoesNotTouchSource(t *testing.T) {
	externalFixture(t, ownerLikeConfig)
	before, err := os.ReadFile(nikki.RunConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	_, ts := newTestServer(t)
	defer ts.Close()
	if code, _ := do(t, ts, "POST", "/api/v1/external-rules/import",
		`{"rule":"GEOIP,ru,DIRECT"}`); code != 201 {
		t.Fatalf("import failed")
	}
	after, err := os.ReadFile(nikki.RunConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("external source file changed during import (stop-rule violation)")
	}
}
