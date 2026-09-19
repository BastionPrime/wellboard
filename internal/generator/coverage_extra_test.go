package generator

import (
	"errors"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
)

// TestNormalizeDeep exercises the recursive value normalizer: float64 →
// int64 for integral values, float passthrough, nested maps and lists.
func TestNormalizeDeep(t *testing.T) {
	in := map[string]any{
		"port":   float64(443),
		"ratio":  1.5,
		"big":    2e16, // non-integral magnitude: stays float
		"nested": map[string]any{"inner": float64(8)},
		"list":   []any{float64(1), "two", nil},
	}
	out := normalize(in).(map[string]any)

	if v, ok := out["port"].(int64); !ok || v != 443 {
		t.Errorf("port = %T(%v), want int64(443)", out["port"], out["port"])
	}
	if v, ok := out["ratio"].(float64); !ok || v != 1.5 {
		t.Errorf("ratio = %T(%v), want float64(1.5)", out["ratio"], out["ratio"])
	}
	if _, ok := out["big"].(float64); !ok {
		t.Errorf("big = %T, want float64", out["big"])
	}
	if m, ok := out["nested"].(map[string]any); !ok || m["inner"] != int64(8) {
		t.Errorf("nested = %#v, want inner int64(8)", out["nested"])
	}
	if l, ok := out["list"].([]any); !ok || l[0] != int64(1) || l[1] != "two" || l[2] != nil {
		t.Errorf("list = %#v", out["list"])
	}
}

// TestConditionRuleErrors covers the per-branch error paths of
// conditionRule: empty geosite, empty src-device, unknown type.
func TestConditionRuleErrors(t *testing.T) {
	cases := []struct {
		cond model.RouteCondition
		want string
	}{
		{model.RouteCondition{Type: model.CondGeosite, Value: ""}, "without category"},
		{model.RouteCondition{Type: model.CondSrcDevice, Value: ""}, "without ip"},
		{model.RouteCondition{Type: "bogus", Value: "x"}, "unknown condition type"},
	}
	for _, tc := range cases {
		_, _, err := conditionRule(tc.cond)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("condition %+v: err = %v, want %q", tc.cond, err, tc.want)
		}
	}
}

// TestConditionRuleHappyPaths: every condition type renders the right
// mihomo rule prefix and geosite flag.
func TestConditionRuleHappyPaths(t *testing.T) {
	cases := []struct {
		cond  model.RouteCondition
		line  string
		isGeo bool
	}{
		{model.RouteCondition{Type: model.CondDomain, Value: "a.com"}, "DOMAIN,a.com", false},
		{model.RouteCondition{Type: model.CondDomainSuffix, Value: "b.com"}, "DOMAIN-SUFFIX,b.com", false},
		{model.RouteCondition{Type: model.CondDomainKeyword, Value: "kw"}, "DOMAIN-KEYWORD,kw", false},
		{model.RouteCondition{Type: model.CondGeosite, Value: "youtube"}, "GEOSITE,youtube", true},
		{model.RouteCondition{Type: model.CondGeoIP, Value: "ru"}, "GEOIP,ru", false},
		{model.RouteCondition{Type: model.CondIPCIDR, Value: "10.0.0.0/8"}, "IP-CIDR,10.0.0.0/8", false},
		{model.RouteCondition{Type: model.CondSrcDevice, Value: "192.168.1.5/32"}, "SRC-IP-CIDR,192.168.1.5/32", false},
		{model.RouteCondition{Type: model.CondDstPort, Value: "443"}, "DST-PORT,443", false},
	}
	for _, tc := range cases {
		line, isGeo, err := conditionRule(tc.cond)
		if err != nil {
			t.Fatalf("condition %+v: %v", tc.cond, err)
		}
		if line != tc.line || isGeo != tc.isGeo {
			t.Errorf("condition %+v = (%q,%v), want (%q,%v)", tc.cond, line, isGeo, tc.line, tc.isGeo)
		}
	}
}

// TestUnknownGroupTypeAndIntervalDefaults: unknown group type is
// reported; missing delay interval falls back to 300.
func TestUnknownGroupTypeAndIntervalDefaults(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Groups = []model.Group{{ID: "grp_x", Name: "X", Type: "weird", Members: []string{"srv_1"}}}
	_, err := Generate(st)
	if err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("want unknown-group-type error, got %v", err)
	}
}

func TestGroupIntervalDefault(t *testing.T) {
	st := baseState()
	st.Settings.DelayTestIntervalSec = 0
	if got := groupInterval(st); got != UserGroupURLTestIntervalSec {
		t.Errorf("groupInterval default = %d, want %d", got, UserGroupURLTestIntervalSec)
	}
	st.Settings.DelayTestIntervalSec = 120
	if got := groupInterval(st); got != 120 {
		t.Errorf("groupInterval from settings = %d, want 120", got)
	}
}

// TestUniqueName: first use keeps the name, repeats get " #2", " #3".
func TestUniqueName(t *testing.T) {
	used := map[string]int{}
	if got := uniqueName("N", used); got != "N" {
		t.Errorf("first = %q, want N", got)
	}
	if got := uniqueName("N", used); got != "N #2" {
		t.Errorf("second = %q, want 'N #2'", got)
	}
	if got := uniqueName("N", used); got != "N #3" {
		t.Errorf("third = %q, want 'N #3'", got)
	}
	if got := uniqueName("M", used); got != "M" {
		t.Errorf("other name = %q, want M", got)
	}
}

// TestProblemsAddLostEmpty: Problems accumulates and empties correctly.
func TestProblemsAddLostEmpty(t *testing.T) {
	p := &Problems{}
	if !p.empty() {
		t.Fatal("fresh Problems must be empty")
	}
	p.add("issue %d", 1)
	p.lost("rt_1", "gone")
	if p.empty() {
		t.Fatal("Problems with entries must not be empty")
	}
	msg := p.Error()
	if !strings.Contains(msg, "issue 1") || !strings.Contains(msg, "rt_1") {
		t.Errorf("Error() = %q", msg)
	}
}

// TestDefaultPolicyFallbackDirect: a default policy pointing at a missing
// target reports a problem and falls back to DIRECT.
func TestDefaultPolicyFallbackDirect(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Settings.DefaultPolicy = model.Target{Type: model.TargetGroup, ID: "grp_missing"}
	_, err := Generate(st)
	if err == nil || !strings.Contains(err.Error(), "default_policy") {
		t.Fatalf("want default_policy problem, got %v", err)
	}
}

// TestGenerateMarshalError: a raw map value that YAML cannot encode (e.g.
// a channel) surfaces a marshal error, not a panic.
func TestGenerateMarshalError(t *testing.T) {
	st := baseState()
	srv := mkServer("srv_bad", "src_manual", "Bad", "socks5", 443)
	srv.Raw["evil"] = make(chan int)
	st.Servers = []model.Server{srv}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Generate panicked: %v", r)
		}
	}()
	_, err := Generate(st)
	if err == nil {
		t.Fatal("expected marshal error for channel value")
	}
	if !strings.Contains(err.Error(), "marshal") {
		t.Errorf("error = %v, want marshal context", err)
	}
}

// TestRouteWithNoConditions: an enabled route with zero conditions still
// gets its service group but no rule lines, and is reported.
func TestRouteWithNoConditions(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Routes = []model.Route{{
		ID: "rt_empty", Enabled: true, Order: 5,
		Target: model.Target{Type: model.TargetServer, ID: "srv_1"},
	}}
	_, err := Generate(st)
	if err == nil || !strings.Contains(err.Error(), "no conditions") {
		t.Fatalf("want no-conditions report, got %v", err)
	}
}

// TestTargetRejectRoute: a route targeting REJECT degenerates to a single
// member service group.
func TestTargetRejectRoute(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Routes = []model.Route{{
		ID: "rt_rej", Enabled: true, Order: 5,
		Conditions:    []model.RouteCondition{{Type: model.CondDomain, Value: "ads.com"}},
		Target:        model.Target{Type: model.TargetReject},
		OnUnavailable: "block",
	}}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "REJECT\n        - REJECT") {
		t.Errorf("redundant duplicate REJECT member:\n%s", s)
	}
	if !strings.Contains(s, "DOMAIN,ads.com,rt:rt_rej") {
		t.Errorf("rule line missing:\n%s", s)
	}
}

// TestDefaultPolicyTargetVariants: default policy as server and reject.
func TestDefaultPolicyTargetVariants(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Settings.DefaultPolicy = model.Target{Type: model.TargetServer, ID: "srv_1"}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "name: rt:default") ||
		!strings.Contains(string(out), "- A [Вручную]") {
		t.Errorf("default policy server variant wrong:\n%s", out)
	}

	st2 := baseState()
	st2.Servers = st.Servers
	st2.Settings.DefaultPolicy = model.Target{Type: model.TargetReject}
	out2, err := Generate(st2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out2), "MATCH,rt:default") {
		t.Errorf("reject default variant missing MATCH:\n%s", out2)
	}
}

// TestGenerateWithProvidersMissing: a route referencing a provider that
// has no local payload is reported as *Problems (so the API maps it to
// 409 with the name), not a plain error (which would 500).
func TestGenerateWithProvidersMissing(t *testing.T) {
	st := baseState()
	st.Servers = []model.Server{mkServer("srv_1", "src_manual", "A", "socks5", 443)}
	st.Routes = []model.Route{{
		ID: "rt_p", Enabled: true, Order: 5,
		Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "x.com"}},
		Target:     model.Target{Type: model.TargetDirect},
		Providers:  []string{"bogus"},
	}}
	_, err := GenerateWithProviders(st, map[string]bool{"ads": true})
	if err == nil {
		t.Fatal("missing provider must fail generation")
	}
	var prob *Problems
	if !errors.As(err, &prob) || len(prob.Invalid) == 0 {
		t.Fatalf("want *Problems, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error must name the missing provider: %v", err)
	}

	// Known provider passes; nil known skips the check entirely.
	st2 := baseState()
	st2.Servers = st.Servers
	st2.Routes = []model.Route{{
		ID: "rt_p", Enabled: true, Order: 5,
		Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "x.com"}},
		Target:     model.Target{Type: model.TargetDirect},
		Providers:  []string{"ads"},
	}}
	if _, err := GenerateWithProviders(st2, map[string]bool{"ads": true}); err != nil {
		t.Fatalf("known provider must pass: %v", err)
	}
	if _, err := GenerateWithProviders(st2, nil); err != nil {
		t.Fatalf("nil known must skip the check: %v", err)
	}
}
