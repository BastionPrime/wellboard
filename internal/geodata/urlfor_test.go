package geodata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestURLForKnown: known sources resolve to their stable URLs.
func TestURLForKnown(t *testing.T) {
	u, err := URLFor(KindGeosite, "runetfreedom", "")
	if err != nil || u != "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geosite.dat" {
		t.Fatalf("runetfreedom geosite: %v %v", u, err)
	}
	u, err = URLFor(KindGeoip, "metacubex", "")
	if err != nil || u != "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat" {
		t.Fatalf("metacubex geoip: %v %v", u, err)
	}
}

// TestURLForCustom: custom sources pass the URL through; missing or
// non-http(s) URLs error (B2: the error is surfaced by the
// generator as a Problems entry, never silently defaulted).
func TestURLForCustom(t *testing.T) {
	u, err := URLFor(KindGeosite, "custom", "https://example.invalid/my-geosite.dat")
	if err != nil || u != "https://example.invalid/my-geosite.dat" {
		t.Fatalf("custom geosite: %v %v", u, err)
	}
	if _, err := URLFor(KindGeosite, "custom", ""); err == nil {
		t.Fatal("custom without URL must error")
	}
	if _, err := URLFor(KindGeoip, "custom", "ftp://nope/x.dat"); err == nil {
		t.Fatal("non-http custom URL must error")
	}
	if _, err := URLFor("weird", "runetfreedom", ""); err == nil {
		t.Fatal("unknown kind must error")
	}
	if _, err := URLFor(KindGeoip, "bogus", ""); err == nil {
		t.Fatal("unknown source must error")
	}
}

// TestGeositeGeoipValid: the split validators accept the three values.
func TestGeositeGeoipValid(t *testing.T) {
	for _, s := range []string{"runetfreedom", "metacubex", "custom"} {
		if !GeositeValid(s) || !GeoipValid(s) {
			t.Errorf("%q must be valid for both kinds", s)
		}
	}
	if GeositeValid("bogus") || GeoipValid("") {
		t.Fatal("bogus/empty must be invalid")
	}
}

// TestCheckKindCustom: CheckKind probes the custom URL per kind.
func TestCheckKindCustom(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer failSrv.Close()

	c := &Checker{}
	av := c.CheckKind(context.Background(), KindGeosite, "custom", okSrv.URL+"/geosite.dat")
	if !av.GeositeOK {
		t.Fatal("custom geosite endpoint must be reachable")
	}
	av = c.CheckKind(context.Background(), KindGeoip, "custom", failSrv.URL+"/geoip.dat")
	if av.GeoipOK || av.Error == "" {
		t.Fatalf("404 custom endpoint must be down: %+v", av)
	}
	// Missing custom URL: no probe, error surface.
	av = c.CheckKind(context.Background(), KindGeosite, "custom", "")
	if av.GeositeOK || av.Error == "" {
		t.Fatalf("custom without URL must be not-ok: %+v", av)
	}
}

// TestKnownGeositeTags: a readable .dat wins over the static list; a
// missing file falls back to the curated list.
func TestKnownGeositeTags(t *testing.T) {
	dir := t.TempDir()
	dat := buildDat([]string{"YOUTUBE", "CATEGORY-ADS-ALL", "MY-CUSTOM"})
	if err := os.WriteFile(filepath.Join(dir, "geosite.dat"), dat, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope", "geosite.dat")
	tags := KnownGeositeTags([]string{missing, filepath.Join(dir, "geosite.dat")})
	if len(tags) != 3 || tags[0] != "category-ads-all" || tags[2] != "youtube" {
		t.Fatalf("tags from .dat = %v", tags)
	}
	// Pure fallback: the curated static list (~30 popular categories).
	fb := KnownGeositeTags([]string{missing})
	if len(fb) < 25 {
		t.Fatalf("fallback list too short: %d", len(fb))
	}
	for _, want := range []string{"ru", "youtube", "google", "netflix", "telegram", "openai", "github"} {
		found := false
		for _, tag := range fb {
			if tag == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fallback list missing %q", want)
		}
	}
}
