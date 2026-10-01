package generator

import (
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
)

// geoState builds a minimal state with one geosite route + settings.
func geoState() *model.State {
	st := storeDefault()
	st.Settings.GeositeSource = "runetfreedom"
	st.Settings.GeoipSource = "metacubex"
	st.Routes = []model.Route{{
		ID: "rt_geo", Name: "Geo", Enabled: true, Order: 10,
		Conditions: []model.RouteCondition{
			{Type: model.CondGeosite, Value: "youtube"},
			{Type: model.CondGeoIP, Value: "ru"},
		},
		Target: model.Target{Type: model.TargetDirect},
	}}
	return st
}

// TestGeoXURLEmitted: geox-url is ALWAYS emitted — known defaults for
// known sources (OPE-3045 B2 decision: keeps the profile explicit).
func TestGeoXURLEmitted(t *testing.T) {
	out, err := Generate(geoState())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "geox-url:") {
		t.Fatalf("geox-url section missing:\n%s", s)
	}
	if !strings.Contains(s, "geosite: https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geosite.dat") {
		t.Fatalf("geosite URL missing:\n%s", s)
	}
	if !strings.Contains(s, "geoip: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat") {
		t.Fatalf("geoip URL missing:\n%s", s)
	}
}

// TestGeoXURLCustom: custom sources carry their URLs; the kinds are
// resolved independently (geosite custom + geoip known here).
func TestGeoXURLCustom(t *testing.T) {
	st := geoState()
	st.Settings.GeositeSource = "custom"
	st.Settings.GeositeCustomURL = "https://example.invalid/my-geosite.dat"
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "geosite: https://example.invalid/my-geosite.dat") {
		t.Fatalf("custom geosite URL missing:\n%s", s)
	}
	if !strings.Contains(s, "geoip: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat") {
		t.Fatalf("known geoip URL missing:\n%s", s)
	}
}

// TestGeoXURLCustomMissing: custom source without a URL is a Problems
// entry (config error surfaced, never silently defaulted).
func TestGeoXURLCustomMissing(t *testing.T) {
	st := geoState()
	st.Settings.GeoipSource = "custom" // URL empty
	_, err := Generate(st)
	if err == nil {
		t.Fatal("custom source without URL must fail generation")
	}
	if !strings.Contains(err.Error(), "geoip") {
		t.Fatalf("error must name the kind: %v", err)
	}
	var prob *Problems
	if !asProblems(err, &prob) {
		t.Fatalf("error must be *Problems: %T", err)
	}
}

// TestAdditionsEmitted: entries land right after the matching
// GEOSITE/GEOIP rule, aimed at the same route group.
func TestAdditionsEmitted(t *testing.T) {
	st := geoState()
	st.Settings.GeodataAdditions = map[string][]string{
		"geosite:youtube": {"myvids.example", "more.example"},
		"geoip:ru":        {"203.0.113.0/24"},
	}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"GEOSITE,youtube,rt:rt_geo",
		"DOMAIN-SUFFIX,myvids.example,rt:rt_geo",
		"DOMAIN-SUFFIX,more.example,rt:rt_geo",
		"GEOIP,ru,rt:rt_geo",
		"IP-CIDR,203.0.113.0/24,rt:rt_geo",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("rule missing: %q in\n%s", want, s)
		}
	}
	// Order: DOMAIN-SUFFIX right after GEOSITE (before GEOIP).
	iGeo := strings.Index(s, "GEOSITE,youtube,rt:rt_geo")
	iAdd := strings.Index(s, "DOMAIN-SUFFIX,myvids.example,rt:rt_geo")
	iGip := strings.Index(s, "GEOIP,ru,rt:rt_geo")
	if !(iGeo < iAdd && iAdd < iGip) {
		t.Fatalf("additions must follow their GEOSITE rule: geo=%d add=%d geoip=%d", iGeo, iAdd, iGip)
	}
}

// TestAdditionsIgnoredForUnreferencedCategories: additions for
// categories no route uses are silently ignored (documented behavior).
func TestAdditionsIgnoredForUnreferencedCategories(t *testing.T) {
	st := geoState()
	st.Settings.GeodataAdditions = map[string][]string{
		"geosite:netflix": {"n.example"},
		"geoip:us":        {"198.51.100.0/24"},
	}
	out, err := Generate(st)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "n.example") || strings.Contains(s, "198.51.100.0/24") {
		t.Fatalf("unreferenced additions must not be emitted:\n%s", s)
	}
}

// TestAdditionsInvalidEntries: bad entries surface as Problems.
func TestAdditionsInvalidEntries(t *testing.T) {
	st := geoState()
	st.Settings.GeodataAdditions = map[string][]string{
		"geosite:youtube": {"bad domain"},
	}
	_, err := Generate(st)
	if err == nil {
		t.Fatal("invalid geosite addition must fail generation")
	}
	if !strings.Contains(err.Error(), "geosite:youtube") {
		t.Fatalf("error must name the key: %v", err)
	}

	st = geoState()
	st.Settings.GeodataAdditions = map[string][]string{
		"geoip:ru": {"not-a-cidr"},
	}
	if _, err := Generate(st); err == nil {
		t.Fatal("invalid geoip addition must fail generation")
	}

	// Rule-line injection attempt: commas must fail.
	st = geoState()
	st.Settings.GeodataAdditions = map[string][]string{
		"geosite:youtube": {"a.com,DIRECT"},
	}
	if _, err := Generate(st); err == nil {
		t.Fatal("comma injection must fail")
	}
}

// asProblems is a tiny errors.As replacement (no import list churn).
func asProblems(err error, target **Problems) bool {
	if p, ok := err.(*Problems); ok {
		*target = p
		return true
	}
	return false
}
