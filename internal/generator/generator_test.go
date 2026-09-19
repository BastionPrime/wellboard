package generator

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
	"gopkg.in/yaml.v3"
)

// goldenUpdate regenerates the golden files: go test ./internal/generator -run TestGolden -update
var goldenUpdate = flag.Bool("update", false, "rewrite golden files")

// goldenDir points at the shared golden corpus (repo root/test/golden).
func goldenDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "test", "golden")
}

// subY and subW are two subscription sources; manual is the manual bucket.
var (
	subY = model.Source{ID: "sub_a1", Kind: "subscription", Name: "WellDone", URL: "https://example.com/sub", Enabled: true, UpdateIntervalSec: 43200}
	subW = model.Source{ID: "sub_b2", Kind: "subscription", Name: "Waves", URL: "https://example.org/sub", Enabled: true}
	manu = model.Source{ID: "src_manual", Kind: "manual", Name: "Вручную"}
)

func mkServer(id, src, name, typ string, port int) model.Server {
	return model.Server{
		ID: id, SourceID: src, Name: name, Type: typ,
		Raw: map[string]any{
			"type": typ, "server": "203.0.113.10", "port": port, "password": "secret",
		},
	}
}

func baseState() *model.State {
	st := storeDefault()
	st.Sources = []model.Source{subY, subW, manu}
	return st
}

// storeDefault mirrors the store package defaults without an import cycle
// risk (store imports model only; but keep the test self-contained).
func storeDefault() *model.State {
	return &model.State{
		Version: 1,
		Settings: model.Settings{
			UIPort: 8090, Lang: "ru", Geodata: "runetfreedom",
			DefaultPolicy:        model.Target{Type: model.TargetDirect},
			DelayTestIntervalSec: 300,
		},
	}
}

// scenarios: golden corpus. Each has a name, a state factory, and whether
// generation is expected to fail (golden captures the error text).
type scenario struct {
	name     string
	state    func() *model.State
	wantFail bool
}

func allScenarios() []scenario {
	// Two live servers used across scenarios.
	srvNL1 := mkServer("srv_9f3", "sub_a1", "NL-1", "socks5", 443)
	srvNL2 := mkServer("srv_1c2", "src_manual", "NL-2", "trojan", 443)

	empty := storeDefault()

	serversOnly := baseState()
	serversOnly.Servers = []model.Server{srvNL1, srvNL2}

	groupAllTypes := baseState()
	groupAllTypes.Servers = []model.Server{srvNL1, srvNL2}
	groupAllTypes.Groups = []model.Group{
		{ID: "grp_sel", Name: "Pick", Type: model.GroupSelect, Members: []string{"srv_9f3", "srv_1c2"}},
		{ID: "grp_nl", Name: "NL auto", Type: model.GroupURLTest, Members: []string{"srv_9f3", "srv_1c2"}},
		{ID: "grp_fb", Name: "Chain", Type: model.GroupFallback, Members: []string{"srv_9f3", "srv_1c2"}},
		{ID: "grp_lb", Name: "Balanced", Type: model.GroupLoadBalance, Members: []string{"srv_9f3", "srv_1c2"}},
	}

	routeBlock := baseState()
	routeBlock.Servers = []model.Server{srvNL1}
	routeBlock.Routes = []model.Route{{
		ID: "rt_1", Name: "Streaming", Enabled: true, Order: 10,
		Conditions: []model.RouteCondition{
			{Type: model.CondGeosite, Value: "youtube"},
			{Type: model.CondDomainSuffix, Value: "netflix.com"},
		},
		Target:        model.Target{Type: model.TargetServer, ID: "srv_9f3"},
		OnUnavailable: "block",
	}}

	routeDirect := baseState()
	routeDirect.Servers = []model.Server{srvNL1}
	routeDirect.Routes = []model.Route{{
		ID: "rt_1", Name: "TV", Enabled: true, Order: 10,
		Conditions:    []model.RouteCondition{{Type: model.CondDomainSuffix, Value: "netflix.com"}},
		Target:        model.Target{Type: model.TargetServer, ID: "srv_9f3"},
		OnUnavailable: "direct",
	}}

	srcDevice := baseState()
	srcDevice.Servers = []model.Server{srvNL1}
	srcDevice.Routes = []model.Route{{
		ID: "rt_2", Name: "TV", Enabled: true, Order: 20,
		Conditions: []model.RouteCondition{{
			Type: model.CondSrcDevice, Value: "192.168.1.50/32", Label: "Samsung TV",
		}},
		Target:        model.Target{Type: model.TargetServer, ID: "srv_9f3"},
		OnUnavailable: "direct",
	}}
	srcDevice.LANDevices = []model.LANDevice{
		{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.50", Hostname: "tv", Static: true},
	}

	// default_policy target + mixed conditions, exercising full rule set.
	defaultTarget := baseState()
	defaultTarget.Servers = []model.Server{srvNL1, srvNL2}
	defaultTarget.Groups = []model.Group{
		{ID: "grp_nl", Name: "NL auto", Type: model.GroupURLTest, Members: []string{"srv_9f3", "srv_1c2"}},
	}
	defaultTarget.Routes = []model.Route{
		{
			ID: "rt_1", Name: "Streaming", Enabled: true, Order: 10,
			Conditions: []model.RouteCondition{
				{Type: model.CondGeosite, Value: "youtube"},
				{Type: model.CondDomainSuffix, Value: "netflix.com"},
				{Type: model.CondDomainKeyword, Value: "disney"},
				{Type: model.CondGeoIP, Value: "ru"},
				{Type: model.CondIPCIDR, Value: "10.0.0.0/8"},
				{Type: model.CondDstPort, Value: "443"},
			},
			Target:        model.Target{Type: model.TargetGroup, ID: "grp_nl"},
			OnUnavailable: "block",
		},
		{
			ID: "rt_2", Name: "TV", Enabled: true, Order: 20,
			Conditions:    []model.RouteCondition{{Type: model.CondSrcDevice, Value: "192.168.1.50/32"}},
			Target:        model.Target{Type: model.TargetServer, ID: "srv_9f3"},
			OnUnavailable: "direct",
		},
	}
	defaultTarget.Settings.DefaultPolicy = model.Target{Type: model.TargetGroup, ID: "grp_nl"}

	// Name collision: same display name from different sources.
	collision := baseState()
	collision.Servers = []model.Server{
		mkServer("srv_a", "sub_a1", "NL-1", "socks5", 443),
		mkServer("srv_b", "sub_b2", "NL-1", "socks5", 443),
		mkServer("srv_c", "sub_a1", "NL-1", "socks5", 443),
	}

	lostTarget := baseState()
	lostTarget.Servers = []model.Server{srvNL1}
	lostTarget.Routes = []model.Route{{
		ID: "rt_1", Name: "Ghost", Enabled: true, Order: 10,
		Conditions:    []model.RouteCondition{{Type: model.CondDomainSuffix, Value: "netflix.com"}},
		Target:        model.Target{Type: model.TargetServer, ID: "srv_gone"}, // gone
		OnUnavailable: "block",
	}}

	return []scenario{
		{name: "empty-state", state: func() *model.State { return empty }},
		{name: "servers-only", state: func() *model.State { return cloneState(serversOnly) }},
		{name: "groups-all-types", state: func() *model.State { return cloneState(groupAllTypes) }},
		{name: "route-block", state: func() *model.State { return cloneState(routeBlock) }},
		{name: "route-direct", state: func() *model.State { return cloneState(routeDirect) }},
		{name: "src-device", state: func() *model.State { return cloneState(srcDevice) }},
		{name: "default-policy-target", state: func() *model.State { return cloneState(defaultTarget) }},
		{name: "name-collision", state: func() *model.State { return cloneState(collision) }},
		{name: "lost-target", state: func() *model.State { return cloneState(lostTarget) }, wantFail: true},
	}
}

func cloneState(st *model.State) *model.State {
	c := *st
	c.Sources = append([]model.Source(nil), st.Sources...)
	c.Servers = append([]model.Server(nil), st.Servers...)
	c.Groups = append([]model.Group(nil), st.Groups...)
	c.Routes = append([]model.Route(nil), st.Routes...)
	c.LANDevices = append([]model.LANDevice(nil), st.LANDevices...)
	return &c
}

// TestGolden is the golden corpus driver: generate, compare with (or
// rewrite) test/golden/<name>.yaml. For wantFail scenarios the golden file
// holds the error report instead of YAML.
func TestGolden(t *testing.T) {
	dir := goldenDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, sc := range allScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			out, err := Generate(sc.state())
			goldenPath := filepath.Join(dir, sc.name+".yaml")

			var got string
			if err != nil {
				if !sc.wantFail {
					t.Fatalf("unexpected generation error: %v", err)
				}
				got = "ERROR\n" + err.Error() + "\n"
			} else {
				if sc.wantFail {
					t.Fatalf("expected generation error, got profile:\n%s", out)
				}
				got = string(out)
			}

			if *goldenUpdate {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, rerr := os.ReadFile(goldenPath)
			if rerr != nil {
				t.Fatalf("golden file missing (run with -update): %v", rerr)
			}
			if got != string(want) {
				t.Errorf("generated profile differs from golden %s:\n--- got ---\n%s\n--- want ---\n%s",
					goldenPath, got, want)
			}
		})
	}
}

// TestScenarioCount guards the initial TZ Phase 1 acceptance: ≥ 8 golden
// scenarios.
func TestScenarioCount(t *testing.T) {
	if n := len(allScenarios()); n < 8 {
		t.Errorf("golden scenario count = %d, want ≥ 8 (initial TZ section 7)", n)
	}
}

// TestDeterminism proves two runs over the same state are byte-identical.
func TestDeterminism(t *testing.T) {
	for _, sc := range allScenarios() {
		out1, err1 := Generate(sc.state())
		out2, err2 := Generate(sc.state())
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("%s: inconsistent error behavior", sc.name)
		}
		if err1 == nil && string(out1) != string(out2) {
			t.Errorf("%s: output not deterministic", sc.name)
		}
	}
}

// TestRouteOrdering: rules follow ascending route order; equal order is
// broken by ID.
func TestRouteOrdering(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443), mkServer("srv_2", "src_manual", "B", "socks5", 443)}
	st.Routes = []model.Route{
		{ID: "rt_zz", Enabled: true, Order: 30, Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "c.com"}}, Target: model.Target{Type: model.TargetDirect}},
		{ID: "rt_mm", Enabled: true, Order: 10, Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "a.com"}}, Target: model.Target{Type: model.TargetDirect}},
		{ID: "rt_aa", Enabled: true, Order: 20, Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "b.com"}}, Target: model.Target{Type: model.TargetDirect}},
	}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Match the full rule so "c.com" cannot hit "gstatic.com" in the
	// health-check URL.
	iA, iB, iC := strings.Index(s, "DOMAIN,a.com,"), strings.Index(s, "DOMAIN,b.com,"), strings.Index(s, "DOMAIN,c.com,")
	if !(iA < iB && iB < iC) {
		t.Errorf("rules out of order: a=%d b=%d c=%d\n%s", iA, iB, iC, s)
	}
	rulesSection := s[strings.Index(s, "rules:"):]
	if !strings.HasSuffix(strings.TrimSpace(rulesSection), "MATCH,rt:default") {
		t.Errorf("MATCH,rt:default must be the last rule:\n%s", s)
	}
}

// TestDisabledRouteAndServer: disabled routes emit nothing; disabled
// source's servers are excluded.
func TestDisabledRouteAndServer(t *testing.T) {
	st := baseState()
	st.Sources = []model.Source{
		{ID: "sub_off", Kind: "subscription", Name: "Off", Enabled: false},
		manu,
	}
	st.Servers = []model.Server{
		mkServer("srv_off", "sub_off", "X", "socks5", 443),
		mkServer("srv_on", "src_manual", "Y", "socks5", 443),
	}
	st.Routes = []model.Route{{
		ID: "rt_off", Enabled: false, Order: 10,
		Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "x.com"}},
		Target:     model.Target{Type: model.TargetDirect},
	}}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "X [Off]") {
		t.Errorf("disabled source server leaked into proxies:\n%s", s)
	}
	if strings.Contains(s, "x.com") || strings.Contains(s, "rt:rt_off") {
		t.Errorf("disabled route leaked into rules:\n%s", s)
	}
	if !strings.Contains(s, "Y [Вручную]") {
		t.Errorf("enabled manual server missing:\n%s", s)
	}
}

// TestStaleServerExcluded (FR-4.8): stale servers drop out of the pool and
// a route targeting one reports a lost target.
func TestStaleServerExcluded(t *testing.T) {
	st := baseState()
	srv := mkServer("srv_stale", "src_manual", "Old", "socks5", 443)
	srv.Stale = true
	st.Servers = []model.Server{srv}
	st.Routes = []model.Route{{
		ID: "rt_1", Enabled: true, Order: 10,
		Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "x.com"}},
		Target:     model.Target{Type: model.TargetServer, ID: "srv_stale"},
	}}
	_, err := Generate(st)
	if err == nil {
		t.Fatal("expected lost-target error for stale server")
	}
	var prob *Problems
	if !errors.As(err, &prob) || len(prob.LostTargets) == 0 {
		t.Fatalf("want *Problems with lost targets, got %T: %v", err, err)
	}
}

// TestProblemsErrorShape checks the structured report fields.
func TestProblemsErrorShape(t *testing.T) {
	st := baseState()
	st.Routes = []model.Route{
		{ID: "rt_1", Enabled: true, Order: 10,
			Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "x.com"}},
			Target:     model.Target{Type: model.TargetServer, ID: "srv_no"}, OnUnavailable: "block"},
	}
	_, err := Generate(st)
	if err == nil {
		t.Fatal("expected error")
	}
	var prob *Problems
	if !errors.As(err, &prob) {
		t.Fatalf("error type %T, want *generator.Problems", err)
	}
	if len(prob.LostTargets) != 1 || !strings.Contains(prob.LostTargets[0], "rt_1") {
		t.Errorf("LostTargets = %v, want rt_1 entry", prob.LostTargets)
	}
	if !strings.Contains(err.Error(), "target lost") {
		t.Errorf("error text %q missing 'target lost'", err)
	}
}

// TestEmptyRawServerReported: a server with no raw map is reported, not
// silently skipped.
func TestEmptyRawServerReported(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{{ID: "srv_empty", SourceID: "src_manual", Name: "E", Type: "socks5"}}
	_, err := Generate(st)
	if err == nil {
		t.Fatal("expected problem for empty raw server")
	}
	if !strings.Contains(err.Error(), "srv_empty") {
		t.Errorf("error %q does not mention srv_empty", err)
	}
}

// TestGroupWithMissingMemberAndEmpty: missing members are reported; a
// group with zero resolvable members substitutes DIRECT (still reported).
func TestGroupWithMissingMemberAndEmpty(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Groups = []model.Group{
		{ID: "grp_1", Name: "G", Type: model.GroupSelect, Members: []string{"srv_1", "srv_ghost"}},
		{ID: "grp_2", Name: "H", Type: model.GroupSelect, Members: []string{"srv_ghost2"}},
	}
	_, err := Generate(st)
	if err == nil {
		t.Fatal("expected problems")
	}
	msg := err.Error()
	for _, want := range []string{"srv_ghost", "srv_ghost2", "substituted DIRECT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

// TestGroupForwardReference: a group member may reference a group defined
// later in the state (mihomo allows forward references).
func TestGroupForwardReference(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Groups = []model.Group{
		{ID: "grp_top", Name: "Top", Type: model.GroupSelect, Members: []string{"grp_bottom"}},
		{ID: "grp_bottom", Name: "Bottom", Type: model.GroupSelect, Members: []string{"srv_1"}},
	}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "name: Top") || !strings.Contains(s, "name: Bottom") {
		t.Fatalf("groups missing:\n%s", s)
	}
	// Top's member list must contain exactly "Bottom" (the forward
	// reference resolved to the later group's name).
	topSection := s[strings.Index(s, "name: Top"):]
	topSection = topSection[:strings.Index(topSection, "type:")]
	if !strings.Contains(topSection, "- Bottom") {
		t.Errorf("Top should reference Bottom (forward ref):\n%s", s)
	}
}

// TestReservedGroupPrefixRejected: user groups may not start with "rt:".
func TestReservedGroupPrefixRejected(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Groups = []model.Group{{ID: "grp_evil", Name: "rt:fake", Type: model.GroupSelect, Members: []string{"srv_1"}}}
	_, err := Generate(st)
	if err == nil || !strings.Contains(err.Error(), "reserved prefix") {
		t.Fatalf("want reserved-prefix error, got %v", err)
	}
}

// TestUnknownConditionType: an unknown condition type is reported.
func TestUnknownConditionType(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Routes = []model.Route{{
		ID: "rt_1", Enabled: true, Order: 10,
		Conditions: []model.RouteCondition{{Type: "process", Value: "nginx"}},
		Target:     model.Target{Type: model.TargetDirect},
	}}
	_, err := Generate(st)
	if err == nil || !strings.Contains(err.Error(), "process") {
		t.Fatalf("want unknown-condition error, got %v", err)
	}
}

// TestNumberNormalization: JSON float64 443 must render as 443, not 4.43e+02.
func TestNumberNormalization(t *testing.T) {
	st := baseState()
	srv := mkServer("srv_1", "src_manual", "A", "socks5", 443)
	srv.Raw["port"] = float64(443) // as encoding/json decodes it
	srv.Raw["cipher"] = "aes-128-gcm"
	st.Servers = []model.Server{srv}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "443.0") || strings.Contains(s, "e+02") {
		t.Errorf("un-normalized number in output:\n%s", s)
	}
	if !strings.Contains(s, "port: 443") {
		t.Errorf("expected 'port: 443' in output:\n%s", s)
	}
}

// TestNoTransportSections: the profile must not contain nikki-owned keys.
func TestNoTransportSections(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Check the transport keys only as TOP-LEVEL mapping keys: parse the
	// YAML and look at the document root. Proxy-level "port"/"password"
	// keys are legitimate and must not trigger the check.
	root := map[string]any{}
	if err := yaml.Unmarshal(out, &root); err != nil {
		t.Fatalf("generated profile is not valid YAML: %v\n%s", err, s)
	}
	for _, banned := range []string{
		"tun", "dns", "mixed-port", "external-controller", "port", "socks-port",
		"redir-port", "tproxy-port", "secret", "external-ui",
	} {
		if _, ok := root[banned]; ok {
			t.Errorf("profile must not contain nikki-owned top-level key %q (docs/DECISIONS.md N3):\n%s", banned, s)
		}
	}
	for _, want := range []string{"proxies", "proxy-groups", "rules"} {
		if _, ok := root[want]; !ok {
			t.Errorf("profile missing required key %q:\n%s", want, s)
		}
	}
}

// TestRuleProvidersSection: present (empty mapping) only when a geosite
// condition exists.
func TestRuleProvidersSection(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Routes = []model.Route{{
		ID: "rt_1", Enabled: true, Order: 10,
		Conditions: []model.RouteCondition{{Type: model.CondGeosite, Value: "youtube"}},
		Target:     model.Target{Type: model.TargetDirect},
	}}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "rule-providers: {}") {
		t.Errorf("geosite scenario must emit rule-providers section:\n%s", out)
	}

	st2 := baseState()
	st2.Servers = st.Servers
	st2.Routes = []model.Route{{
		ID: "rt_1", Enabled: true, Order: 10,
		Conditions: []model.RouteCondition{{Type: model.CondDomainSuffix, Value: "x.com"}},
		Target:     model.Target{Type: model.TargetDirect},
	}}
	out2, err := Generate(st2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out2), "rule-providers") {
		t.Errorf("non-geosite scenario must not emit rule-providers:\n%s", out2)
	}
}

// TestNilState.
func TestNilState(t *testing.T) {
	if _, err := Generate(nil); err == nil {
		t.Fatal("expected error for nil state")
	}
}

// TestMihomoValidate runs the REAL mihomo binary (bin/mihomo, fetched by
// scripts/fetch-mihomo.sh) against the non-failing golden profiles, when
// the binary exists. Profiles are rendered into a temp dir; GEOSITE
// scenarios rely on mihomo's geodata auto-download (or a geosite.dat next
// to the profile). If the binary is absent the test is skipped so the
// plain unit run stays hermetic.
func TestMihomoValidate(t *testing.T) {
	bin := mihomoBin(t)
	if bin == "" {
		t.Skip("bin/mihomo not present: run scripts/fetch-mihomo.sh (integration test)")
	}
	dir := t.TempDir()

	for _, sc := range allScenarios() {
		if sc.wantFail {
			continue
		}
		t.Run(sc.name, func(t *testing.T) {
			out, err := Generate(sc.state())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, sc.name+".yaml")
			if err := os.WriteFile(path, out, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "-t", "-d", dir, "-f", path)
			res, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("mihomo -t failed for %s:\n%s", path, res)
			}
		})
	}
}

// mihomoBin locates the dev mihomo binary relative to the repo root.
func mihomoBin(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(wd, "..", "..", "bin", "mihomo"),
		filepath.Join(wd, "..", "..", "..", "bin", "mihomo"),
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// TestGoldenFilesListed ensures the golden corpus is committed with the
// repo (no missing files).
func TestGoldenFilesListed(t *testing.T) {
	dir := goldenDir(t)
	for _, sc := range allScenarios() {
		p := filepath.Join(dir, sc.name+".yaml")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("golden file missing: %s (run go test -update)", p)
		}
	}
}

// end of tests
