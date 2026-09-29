package nikki

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRunConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withRunConfig(t *testing.T, body string) {
	t.Helper()
	old := RunConfigPath
	RunConfigPath = filepath.Join(t.TempDir(), "run", "config.yaml")
	t.Cleanup(func() { RunConfigPath = old })
	if body != "" {
		writeRunConfig(t, filepath.Dir(filepath.Dir(RunConfigPath)), body)
	}
}

func TestLoadExternalRulesParsesAllShapes(t *testing.T) {
	withRunConfig(t, `mode: rule
proxies: []
rules:
  - PROCESS-NAME,bittorrent,DIRECT
  - DST-PORT,6881-6889,DIRECT
  - GEOIP,private,DIRECT,no-resolve
  - GEOIP,ru,DIRECT
  - DOMAIN-SUFFIX,openai.com,proxy-vietnam
  - DOMAIN,claude.ai,proxy-vietnam
  - MATCH,proxy
`)
	rules, groups, err := LoadExternalRules()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(rules) != 7 {
		t.Fatalf("want 7 rules, got %d", len(rules))
	}
	if rules[0].Type != "PROCESS-NAME" || rules[0].Value != "bittorrent" || rules[0].Target != "DIRECT" {
		t.Errorf("rule 0 parsed wrong: %+v", rules[0])
	}
	if rules[2].NoResolve != true {
		t.Errorf("rule 2 no-resolve not captured: %+v", rules[2])
	}
	if rules[6].Type != "MATCH" || rules[6].Value != "" || rules[6].Target != "proxy" {
		t.Errorf("MATCH parsed wrong: %+v", rules[6])
	}
	wantGroups := []string{"DIRECT", "proxy", "proxy-vietnam"}
	if len(groups) != len(wantGroups) {
		t.Fatalf("groups = %v", groups)
	}
	for i, g := range wantGroups {
		if groups[i] != g {
			t.Fatalf("groups = %v", groups)
		}
	}
	// index is 1-based position inside rules:
	if rules[0].Index != 1 || rules[6].Index != 7 {
		t.Errorf("index numbering wrong: %d %d", rules[0].Index, rules[6].Index)
	}
	// raw keeps the full line verbatim:
	if rules[4].Raw != "DOMAIN-SUFFIX,openai.com,proxy-vietnam" {
		t.Errorf("raw line not verbatim: %q", rules[4].Raw)
	}
}

func TestLoadExternalRulesCountMatchesOwnerConfig(t *testing.T) {
	// Shape check against the owner's real mixin-generated config:
	// the rules section of run/config.yaml is what mihomo evaluates,
	// and the acceptance criterion is "count matches the rule lines of
	// the active profile". 112 entries in the file (110 rules + 2
	// MATCH tails) must yield 112 records, none dropped.
	withRunConfig(t, "rules:\n  - MATCH,DIRECT\n")
	rules, _, err := LoadExternalRules()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("want 1, got %d", len(rules))
	}
}

func TestLoadExternalRulesMissingFileIsEmpty(t *testing.T) {
	withRunConfig(t, "") // no file written
	rules, groups, err := LoadExternalRules()
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if rules != nil || groups != nil {
		t.Fatalf("missing file must return nil slices, got %v %v", rules, groups)
	}
}

func TestLoadExternalRulesNoRulesSection(t *testing.T) {
	withRunConfig(t, "mode: global\nport: 8080\n")
	rules, _, err := LoadExternalRules()
	if err != nil {
		t.Fatalf("no rules section must not error, got %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("want 0 rules, got %d", len(rules))
	}
}

func TestLoadExternalRulesOddLinesKeptAsRaw(t *testing.T) {
	withRunConfig(t, "rules:\n  - AND,((DOMAIN,a.com),(DOMAIN,b.com)),DIRECT\n")
	rules, _, err := LoadExternalRules()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("want 1 raw record, got %d", len(rules))
	}
	if rules[0].Raw != "AND,((DOMAIN,a.com),(DOMAIN,b.com)),DIRECT" {
		t.Fatalf("raw not preserved: %+v", rules[0])
	}
}

// The acceptance stop-rule is a WRITE invariant: LoadExternalRules must
// never touch the source file. Guard it by mode.
func TestLoadExternalRulesReadOnly(t *testing.T) {
	dir := t.TempDir()
	old := RunConfigPath
	RunConfigPath = filepath.Join(dir, "run", "config.yaml")
	t.Cleanup(func() { RunConfigPath = old })
	writeRunConfig(t, dir, "rules:\n  - MATCH,DIRECT\n")
	info, err := os.Stat(RunConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	before := info.Mode()
	if _, _, err := LoadExternalRules(); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(RunConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != before {
		t.Fatalf("source file mode changed: %v -> %v", before, info.Mode())
	}
}
