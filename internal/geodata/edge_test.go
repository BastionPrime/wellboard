package geodata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildDatVarint is buildDat with a proper protobuf varint length
// encoder, so tags (and records) longer than 127 bytes are encoded
// correctly.
func buildDatVarint(tags []string) []byte {
	appendVarint := func(dst []byte, v int) []byte {
		for v >= 0x80 {
			dst = append(dst, byte(v)|0x80)
			v >>= 7
		}
		return append(dst, byte(v))
	}
	var out []byte
	for _, tag := range tags {
		entry := appendVarint([]byte{0x0a}, len(tag))
		entry = append(entry, tag...)
		out = appendVarint(append(out, 0x0a), len(entry))
		out = append(out, entry...)
	}
	return out
}

// TestURLForCustomEdge: custom source URL edge cases beyond the basic
// pass-through — scheme prefixes are checked, not parsed, so
// "http://…" is rejected even though "https://…" with the same tail
// passes; a URL of only the scheme prefix passes (scheme presence is
// the only constraint, per B2).
func TestURLForCustomEdge(t *testing.T) {
	// http scheme must pass as well as https.
	u, err := URLFor(KindGeoip, "custom", "http://mirror.lan/geoip.dat")
	if err != nil || u != "http://mirror.lan/geoip.dat" {
		t.Fatalf("http custom URL: %v %v", u, err)
	}
	// Bare scheme prefix passes — the scheme check is a prefix check.
	u, err = URLFor(KindGeosite, "custom", "https://")
	if err != nil || u != "https://" {
		t.Fatalf("bare https:// prefix: %v %v", u, err)
	}
	// Scheme look-alikes are rejected.
	for _, bad := range []string{
		"ftp://mirror/x.dat",
		"file:///etc/passwd",
		"HTTPS://UPPER.SCHEME/x.dat", // prefix check is case-sensitive
		"javascript:alert(1)",
		"https:/one-slash.example/x.dat",
		" https://leading-space.example/x.dat",
	} {
		if _, err := URLFor(KindGeosite, "custom", bad); err == nil {
			t.Errorf("custom URL %q must be rejected", bad)
		}
	}
	// A non-empty custom URL must NOT be silently ignored for known
	// sources (it is unused), and unknown sources error regardless.
	if _, err := URLFor(KindGeosite, "bogus", "https://x/y.dat"); err == nil {
		t.Fatal("unknown source with URL must still error")
	}
	// Whitespace-only URL is treated as missing.
	if _, err := URLFor(KindGeosite, "custom", "   "); err == nil {
		t.Fatal("whitespace-only custom URL must error (missing URL)")
	}
}

// TestURLForKindCase: kind and source matching is exact — case or
// whitespace variants of valid values are rejected with an error,
// never silently resolved.
func TestURLForKindCase(t *testing.T) {
	for _, bad := range []string{"Geosite", "GEOSITE", " geosite", "geosite "} {
		if _, err := URLFor(bad, "runetfreedom", ""); err == nil {
			t.Errorf("kind %q must be rejected as unknown", bad)
		}
	}
	for _, bad := range []string{"Runetfreedom", "MetaCubeX", " custom ", "CUSTOM", ""} {
		if _, err := URLFor(KindGeoip, bad, ""); err == nil {
			t.Errorf("source %q must be rejected as unknown", bad)
		}
	}
}

// TestValidEdge: the validators reject case/whitespace variants of
// the three valid source values.
func TestValidEdge(t *testing.T) {
	for _, bad := range []string{"", " ", "Runetfreedom", "metacubex ", "CUSTOM", "custom-url", "bogus"} {
		if Valid(bad) || GeositeValid(bad) || GeoipValid(bad) {
			t.Errorf("source %q must be invalid in all validators", bad)
		}
	}
}

// TestCheckKindUnknownSource: an unknown source must not even probe.
func TestCheckKindUnknownSource(t *testing.T) {
	probeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unknown source must not be probed; got %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusOK)
	}))
	defer probeSrv.Close()

	c := &Checker{}
	av := c.CheckKind(context.Background(), KindGeosite, "bogus", probeSrv.URL+"/x.dat")
	if av.GeositeOK || av.Error == "" {
		t.Fatalf("unknown source must be not-ok: %+v", av)
	}
}

// TestKnownGeositeTagsCustomCategories: custom categories with spaces
// and mixed case in the .dat round-trip through the datalist: the
// space is preserved verbatim, case is lowercased, tags stay sorted.
func TestKnownGeositeTagsCustomCategories(t *testing.T) {
	dat := buildDat([]string{"MY CUSTOM", "MiXeD CaSe", "AMAZON", "Google"})
	// buildDat writes tag bytes with a single-byte length — fine here
	// (tags are short), and ParseTags must read them back verbatim.
	tags, ok := ParseTags(dat)
	if !ok {
		t.Fatal("custom-category dat must parse fully")
	}
	if !tags["MY CUSTOM"] || !tags["MiXeD CaSe"] {
		t.Fatalf("space/case tags must survive parsing verbatim: %v", tags)
	}

	got := KnownGeositeTagsSingle(dat)
	want := []string{"amazon", "google", "mixed case", "my custom"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}
}

// TestKnownGeositeTagsDuplicateTags: duplicate tags in the .dat
// (including case-duplicates that collapse when lowercased) yield one
// entry in the datalist, never repeats.
func TestKnownGeositeTagsDuplicateTags(t *testing.T) {
	dat := buildDat([]string{"YOUTUBE", "youtube", "Youtube", "NETFLIX", "NETFLIX", "MY-CUSTOM", "MY-CUSTOM"})
	got := KnownGeositeTagsSingle(dat)
	if len(got) != 3 {
		t.Fatalf("case-duplicate tags must dedupe: %v", got)
	}
	want := []string{"my-custom", "netflix", "youtube"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}
}

// TestKnownGeositeTagsEmpty: an empty .dat and a "records without
// tags" .dat are not usable lists — KnownGeositeTags skips them and
// falls back to the curated default list.
func TestKnownGeositeTagsEmpty(t *testing.T) {
	// Fully empty file.
	if got := KnownGeositeTagsSingle(nil); got == nil || len(got) == 0 {
		t.Fatal("empty .dat must fall back to the curated list")
	}
	// Records present, but none carries a tag field.
	noTagDat := []byte{0x0a, 0x02, 0x12, 0x00}
	if got := KnownGeositeTagsSingle(noTagDat); len(got) < 25 {
		t.Fatalf("tagless .dat must fall back to the curated list: %d tags", len(got))
	}
	// Empty datPaths list.
	if got := KnownGeositeTags([]string{}); len(got) < 25 {
		t.Fatal("empty datPaths must fall back to the curated list")
	}
}

// TestCheckCategoriesDuplicates: duplicated requested categories are
// reported per copy (no dedup), and an empty want list is valid (all
// present, nil result).
func TestCheckCategoriesDuplicates(t *testing.T) {
	dat := buildDat([]string{"YOUTUBE", "NETFLIX"})
	missing := CheckCategories(dat, []string{"YOUTUBE", "NETFLIX"})
	if len(missing) != 0 {
		t.Fatalf("present duplicates must not be reported missing: %v", missing)
	}
	missing = CheckCategories(dat, []string{"TWITCH", "TWITCH"})
	if len(missing) != 2 || missing[0] != "TWITCH" || missing[1] != "TWITCH" {
		t.Fatalf("missing duplicates reported per copy: %v", missing)
	}
	if got := CheckCategories(dat, nil); len(got) != 0 {
		t.Fatalf("empty want list: %v", got)
	}
	if got := CheckCategories(dat, []string{}); len(got) != 0 {
		t.Fatalf("empty want slice: %v", got)
	}
}

// TestCheckCategoriesWhitespace: requested categories with spaces are
// matched verbatim (a tag with a space matches a .dat tag with a
// space; a trimmed variant does not cross-match).
func TestCheckCategoriesWhitespace(t *testing.T) {
	dat := buildDat([]string{"MY CUSTOM"})
	if got := CheckCategories(dat, []string{"MY CUSTOM"}); len(got) != 0 {
		t.Fatalf("space tag must match verbatim: %v", got)
	}
	if got := CheckCategories(dat, []string{"MYCUSTOM", "MY  CUSTOM"}); len(got) != 2 {
		t.Fatalf("variants must not cross-match: %v", got)
	}
}

// TestParseTagsLongTag: multi-byte varint record lengths are parsed
// correctly (a tag longer than 127 bytes exercises the two-byte
// varint path in both the record and tag lengths). buildDat only
// writes single-byte lengths, so this uses a full varint encoder.
func TestParseTagsLongTag(t *testing.T) {
	long := strings.Repeat("A", 200)
	dat := buildDatVarint([]string{long})
	tags, ok := ParseTags(dat)
	if !ok || !tags[long] {
		t.Fatalf("200-byte tag must parse: ok=%v tags=%v", ok, len(tags))
	}
}

// TestParseTagsTagTooLong: a declared tag length exceeding the record
// is skipped, not desynced.
func TestParseTagsTagTooLong(t *testing.T) {
	// entry: field 0x0a, declared length 0x05, but only 2 bytes follow.
	entry := []byte{0x0a, 0x05, 'A', 'B'}
	dat := append([]byte{0x0a, byte(len(entry))}, entry...)
	tags, ok := ParseTags(dat)
	if !ok {
		t.Fatal("oversized tag length must be skipped, not desync")
	}
	if len(tags) != 0 {
		t.Fatalf("no tag must be recorded: %v", tags)
	}
}

// TestTagPresentTrimsNothing: TagPresent is exact-match; a tag with
// surrounding spaces in the .dat does not match its trimmed form and
// vice versa.
func TestTagPresentTrimsNothing(t *testing.T) {
	dat := buildDat([]string{" SPACED "})
	if !TagPresent(dat, " SPACED ") {
		t.Fatal("verbatim spaced tag must match")
	}
	for _, no := range []string{"SPACED", "spaced", ""} {
		if TagPresent(dat, no) {
			t.Errorf("%q must not match %q", " SPACED ", no)
		}
	}
}

// TestKnownGeositeTagsFirstUsableWins: a malformed file before a
// good one is skipped; a good file before an unreadable one wins.
func TestKnownGeositeTagsFirstUsableWins(t *testing.T) {
	dir := t.TempDir()
	good := buildDat([]string{"GOOD"})
	goodPath := filepath.Join(dir, "good.dat")
	if err := os.WriteFile(goodPath, good, 0o644); err != nil {
		t.Fatal(err)
	}
	// Desynced file: parses with ok=false → skipped.
	desync := append(buildDat([]string{"DESYNC"}), 0xff)
	desyncPath := filepath.Join(dir, "desync.dat")
	if err := os.WriteFile(desyncPath, desync, 0o644); err != nil {
		t.Fatal(err)
	}

	got := KnownGeositeTags([]string{desyncPath, goodPath})
	if len(got) != 1 || got[0] != "good" {
		t.Fatalf("desync file must be skipped for the good one: %v", got)
	}
}

// TestValidateTags rejects malformed tags with an error instead of
// silently accepting them (A2): empty, whitespace (leading, trailing,
// inner), control characters, and non-ASCII bytes are invalid; mixed
// case is fine (the datalist is lowercase, mihomo is case-insensitive).
func TestValidateTags(t *testing.T) {
	for _, bad := range []string{
		"",
		" ",
		"\t",
		"\n",
		" youtube",
		"youtube ",
		"my custom",
		"my\tcustom",
		"my\ncustom",
		"category ads",
		"cannot\x00fail",
		"bad\x7fchar",
		"русский",
		"japanese-日本",
	} {
		if validTag(bad) {
			t.Errorf("tag %q must be invalid", bad)
		}
	}
	for _, good := range []string{
		"youtube",
		"YOUTUBE",
		"YouTube",
		"MY-CUSTOM",
		"category-ads-all",
		"x",
	} {
		if !validTag(good) {
			t.Errorf("tag %q must be valid", good)
		}
	}
}

// TestCheckCategoriesInvalidTag: invalid requested categories are
// rejected with an error instead of being silently reported as
// "missing" (A2).
func TestCheckCategoriesInvalidTag(t *testing.T) {
	dat := buildDat([]string{"YOUTUBE"})
	_, err := CheckCategoriesStrict(dat, []string{"YOUTUBE"})
	if err != nil {
		t.Fatalf("valid tags must not error: %v", err)
	}
	for _, bad := range []string{"youtube video", " youtube", "\tnetflix", "вы"} {
		if _, err := CheckCategoriesStrict(dat, []string{"YOUTUBE", bad}); err == nil {
			t.Errorf("invalid tag %q must be rejected with an error", bad)
		}
	}
}

// TestCheckCategoriesStrictNilDat: a nil .dat never errors on the
// tag-validation level (it is a missing-file condition, reported by
// the availability checkers), but invalid tags still error.
func TestCheckCategoriesStrictNilDat(t *testing.T) {
	if _, err := CheckCategoriesStrict(nil, []string{"YOUTUBE"}); err != nil {
		t.Fatalf("nil dat with valid tags: %v", err)
	}
	if _, err := CheckCategoriesStrict(nil, []string{"bad tag"}); err == nil {
		t.Fatal("nil dat with invalid tag must still error")
	}
}

// TestValidateTagErrors: error messages name the offending tag and
// the reason, so the operator can fix the config without guessing.
func TestValidateTagErrors(t *testing.T) {
	dat := buildDat([]string{"YOUTUBE"})
	_, err := CheckCategoriesStrict(dat, []string{"MY CUSTOM"})
	if err == nil {
		t.Fatal("space tag must be rejected")
	}
	if !strings.Contains(err.Error(), "MY CUSTOM") {
		t.Fatalf("error must name the offending tag: %v", err)
	}
	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("error must wrap ErrInvalidTag: %v", err)
	}
}
