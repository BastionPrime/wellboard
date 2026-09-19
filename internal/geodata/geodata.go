// Package geodata manages the geodata source (FR-5.4, decision Q7):
// runetfreedom by default, MetaCubeX as the alternative — configured in
// settings. It checks availability of the geosite categories used by the
// shipped templates and parses .dat files to enumerate tags.
//
// Download URLs (verified 2026-09-19, see docs/DECISIONS.md Phase 3):
//   - runetfreedom:
//     https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/{geosite,geoip}.dat
//   - MetaCubeX:
//     https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/{geosite,geoip}.dat
//
// The .dat format is v2fly domain-list-community protobuf:
// GeositeList { repeated GeoSite site = 1 }, where each record is
// length-prefixed with a protobuf varint and GeoSite { string
// country_code = 1; repeated Domain domain = 2 } — tag names are
// UPPERCASE.
package geodata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SourceKind is a geodata provider.
type SourceKind string

const (
	// Runetfreedom is the default RU-oriented source (decision Q7).
	Runetfreedom SourceKind = "runetfreedom"
	// MetaCubeX is the alternative source.
	MetaCubeX SourceKind = "metacubex"
)

// Download URLs per source and file kind ("geosite" / "geoip").
var downloadURL = map[SourceKind]map[string]string{
	Runetfreedom: {
		"geosite": "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geosite.dat",
		"geoip":   "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geoip.dat",
	},
	MetaCubeX: {
		"geosite": "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat",
		"geoip":   "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat",
	},
}

// Valid reports whether s is a known source kind.
func Valid(s string) bool {
	return SourceKind(s) == Runetfreedom || SourceKind(s) == MetaCubeX
}

// URL returns the download URL for kind ("geosite"/"geoip").
func URL(s SourceKind, kind string) (string, error) {
	m, ok := downloadURL[s]
	if !ok {
		return "", fmt.Errorf("geodata: unknown source %q", s)
	}
	u, ok := m[kind]
	if !ok {
		return "", fmt.Errorf("geodata: unknown kind %q", kind)
	}
	return u, nil
}

// Checker verifies geodata availability.
type Checker struct {
	// HTTP is the client used for availability checks; default 15s
	// timeout, no redirects following beyond defaults.
	HTTP *http.Client
}

// Availability is the outcome of one source check.
type Availability struct {
	// Source is the checked source kind.
	Source SourceKind `json:"source"`
	// GeositeOK / GeoipOK: the download endpoint answered 200/206.
	GeositeOK bool `json:"geosite_ok"`
	GeoipOK   bool `json:"geoip_ok"`
	// CheckedAt is when the check ran.
	CheckedAt time.Time `json:"checked_at"`
	// Error is a combined human-readable failure reason ("" = healthy).
	Error string `json:"error,omitempty"`
}

// Check probes the source download endpoints (HEAD, falling back to a
// 1-byte ranged GET) and reports availability. It never downloads the
// whole file.
func (c *Checker) Check(ctx context.Context, s SourceKind) Availability {
	res := Availability{Source: s, CheckedAt: time.Now()}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	var errs []string
	res.GeositeOK = c.probe(ctx, client, s, "geosite")
	if !res.GeositeOK {
		errs = append(errs, "geosite.dat endpoint unreachable")
	}
	res.GeoipOK = c.probe(ctx, client, s, "geoip")
	if !res.GeoipOK {
		errs = append(errs, "geoip.dat endpoint unreachable")
	}
	res.Error = strings.Join(errs, "; ")
	return res
}

func (c *Checker) probe(ctx context.Context, client *http.Client, s SourceKind, kind string) bool {
	u, err := URL(s, kind)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		// 200 (plain) / 206 (ranged) / some CDNs answer HEAD oddly →
		// treat 2xx/3xx as reachable.
		if resp.StatusCode < 400 {
			return true
		}
	}
	// Fallback: 1-byte ranged GET.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err = client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
	return resp.StatusCode < 400
}

// ErrBadTag is returned for category checks against a file with no such
// tag (the tag list is exact, not fuzzy).
var ErrBadTag = errors.New("geodata: tag not present in the .dat file")

// TagPresent reports whether tag (UPPERCASE v2fly name) exists in the
// parsed .dat bytes. Tags are matched exactly.
func TagPresent(dat []byte, tag string) bool {
	tags, _ := ParseTags(dat)
	_, ok := tags[tag]
	return ok
}

// CheckCategories verifies that every category in want exists in the
// geosite .dat. Returns the missing ones (empty = all present).
func CheckCategories(dat []byte, want []string) []string {
	tags, _ := ParseTags(dat)
	var missing []string
	for _, w := range want {
		if _, ok := tags[w]; !ok {
			missing = append(missing, w)
		}
	}
	return missing
}

// ParseTags scans a v2fly geosite.dat protobuf stream and returns all
// GeoSite tags. The outer stream is a sequence of
//
//	0x0a <varint len> <GeoSite bytes>
//
// records; inside, GeoSite starts with 0x0a <varint len> <tag>. Any
// desync aborts the scan (partial results are returned with ok=false).
func ParseTags(dat []byte) (tags map[string]bool, ok bool) {
	tags = map[string]bool{}
	pos := 0
	for pos < len(dat) {
		if dat[pos] != 0x0a {
			return tags, false
		}
		pos++
		ln, np, okv := readVarint(dat, pos)
		if !okv || ln <= 0 || np+int(ln) > len(dat) {
			return tags, false
		}
		entry := dat[np : np+int(ln)]
		pos = np + int(ln)
		if len(entry) < 2 || entry[0] != 0x0a {
			continue // record without a tag field — skip
		}
		tl, tp, okv := readVarint(entry, 1)
		if !okv || tl < 0 || tp+int(tl) > len(entry) {
			continue
		}
		tags[string(entry[tp:tp+int(tl)])] = true
	}
	return tags, true
}

// readVarint decodes a protobuf varint from data[pos:]; returns the
// value, the new position, and whether decoding succeeded.
func readVarint(data []byte, pos int) (int64, int, bool) {
	var val int64
	var shift uint
	for {
		if pos >= len(data) || shift > 63 {
			return 0, pos, false
		}
		b := data[pos]
		pos++
		val |= int64(b&0x7f) << shift
		if b < 0x80 {
			return val, pos, true
		}
		shift += 7
	}
}
