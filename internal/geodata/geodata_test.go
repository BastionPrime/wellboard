package geodata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// buildDat encodes a minimal v2fly geosite.dat: tags only.
func buildDat(tags []string) []byte {
	var out []byte
	for _, tag := range tags {
		entry := []byte{0x0a, byte(len(tag))}
		entry = append(entry, tag...)
		out = append(out, 0x0a, byte(len(entry)))
		out = append(out, entry...)
	}
	return out
}

func TestParseTags(t *testing.T) {
	dat := buildDat([]string{"YOUTUBE", "CATEGORY-ADS-ALL"})
	tags, ok := ParseTags(dat)
	if !ok {
		t.Fatal("well-formed dat must parse fully")
	}
	if !tags["YOUTUBE"] || !tags["CATEGORY-ADS-ALL"] || len(tags) != 2 {
		t.Fatalf("tags = %v", tags)
	}
}

func TestParseTagsDesync(t *testing.T) {
	dat := append(buildDat([]string{"A"}), 0xff)
	tags, ok := ParseTags(dat)
	if ok {
		t.Fatal("garbage tail must desync")
	}
	if !tags["A"] {
		t.Fatalf("partial results still returned: %v", tags)
	}
}

func TestTagPresentAndCheckCategories(t *testing.T) {
	dat := buildDat([]string{"YOUTUBE", "NETFLIX"})
	if !TagPresent(dat, "YOUTUBE") {
		t.Fatal("YOUTUBE must be present")
	}
	if TagPresent(dat, "TWITCH") {
		t.Fatal("TWITCH must be absent")
	}
	// Case-sensitive (v2fly names are uppercase).
	if TagPresent(dat, "youtube") {
		t.Fatal("tags are exact-match uppercase")
	}
	missing := CheckCategories(dat, []string{"YOUTUBE", "TWITCH", "NETFLIX"})
	if len(missing) != 1 || missing[0] != "TWITCH" {
		t.Fatalf("missing = %v, want [TWITCH]", missing)
	}
}

func TestParseVarint(t *testing.T) {
	// 300 = 0xAC 0x02 (two-byte varint).
	v, n, ok := readVarint([]byte{0xac, 0x02}, 0)
	if !ok || v != 300 || n != 2 {
		t.Fatalf("readVarint = %d,%d,%v", v, n, ok)
	}
	if _, _, ok := readVarint([]byte{0x80}, 0); ok {
		t.Fatal("truncated varint must fail")
	}
}

func TestURLs(t *testing.T) {
	u, err := URL(Runetfreedom, "geosite")
	if err != nil || u != "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geosite.dat" {
		t.Fatalf("runetfreedom geosite URL wrong: %v %v", u, err)
	}
	u, err = URL(MetaCubeX, "geoip")
	if err != nil || u != "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat" {
		t.Fatalf("metacubex geoip URL wrong: %v %v", u, err)
	}
	if _, err := URL(SourceKind("bogus"), "geosite"); err == nil {
		t.Fatal("unknown source must error")
	}
	if _, err := URL(Runetfreedom, "nope"); err == nil {
		t.Fatal("unknown kind must error")
	}
	if !Valid("runetfreedom") || !Valid("metacubex") || Valid("bogus") {
		t.Fatal("Valid misbehaves")
	}
}

func TestCheckHTTP(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer failSrv.Close()

	// Point both kinds at the test servers.
	orig := downloadURL[Runetfreedom]
	downloadURL[Runetfreedom] = map[string]string{
		"geosite": okSrv.URL + "/geosite.dat",
		"geoip":   failSrv.URL + "/geoip.dat",
	}
	defer func() { downloadURL[Runetfreedom] = orig }()

	c := &Checker{}
	av := c.Check(context.Background(), Runetfreedom)
	if !av.GeositeOK {
		t.Fatal("geosite endpoint must be reachable")
	}
	if av.GeoipOK {
		t.Fatal("404 endpoint must be reported down")
	}
	if av.Error == "" || av.Source != Runetfreedom {
		t.Fatalf("availability report wrong: %+v", av)
	}
	if av.CheckedAt.IsZero() {
		t.Fatal("CheckedAt must be set")
	}
}

func TestCheckHeadFallback(t *testing.T) {
	// HEAD answers 405, GET with Range answers 206 → still reachable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer srv.Close()
	orig := downloadURL[MetaCubeX]
	downloadURL[MetaCubeX] = map[string]string{
		"geosite": srv.URL + "/geosite.dat",
		"geoip":   srv.URL + "/geoip.dat",
	}
	defer func() { downloadURL[MetaCubeX] = orig }()

	c := &Checker{}
	av := c.Check(context.Background(), MetaCubeX)
	if !av.GeositeOK || !av.GeoipOK {
		t.Fatalf("HEAD-405/GET-206 endpoint must be reachable: %+v", av)
	}
}
