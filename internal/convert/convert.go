// Package convert turns subscription/share-link payloads (vless://,
// trojan://, ss://, vmess://, hysteria2://, tuic://, …) into mihomo proxy
// dictionaries (initial TZ FR-1.3 / 5.7.2).
//
// Implementation policy (docs/DECISIONS.md C1/C2): this is a THIN WRAPPER
// around github.com/metacubex/mihomo/common/convert.ConvertsV2Ray. WellBoard
// deliberately does NOT reimplement link parsing — the upstream converter
// is the same code mihomo itself uses, so parity with the core is automatic.
//
// LICENSE CAVEAT (docs/DECISIONS.md C2): mihomo is GPL-3.0. Importing its
// code into this MIT project makes the combined binary effectively
// GPL-compatible at best; the license question is recorded in DECISIONS and
// the final call belongs to the project owner. The wrapper is deliberately
// the ONLY file that touches mihomo code, so swapping the implementation
// later (own parser / subprocess / relicensing) is a one-file change.
package convert

import (
	"fmt"
	"strings"

	"github.com/metacubex/mihomo/common/convert"
)

// SupportedSchemes is the share-link set WellBoard accepts for manual
// servers (FR-1.3) and plain/base64 subscription lists (5.7.2). The
// underlying converter handles a superset; anything else it can parse is
// also accepted — this list only feeds user-facing validation messages.
var SupportedSchemes = []string{
	"vless", "trojan", "ss", "vmess", "hysteria2", "tuic",
}

// ErrEmpty is returned when the input contains no share links at all.
var ErrEmpty = fmt.Errorf("convert: no share links found in input")

// ErrUnknownFormat is returned when the converter rejects the payload.
var ErrUnknownFormat = fmt.Errorf("convert: unknown subscription format")

// Links converts one or more share-link lines into mihomo proxy maps.
// Input may be a plain newline-separated list of links or a whole-payload
// base64 blob (the converter decodes base64 transparently). Malformed
// lines are skipped by upstream; zero parsed proxies → ErrEmpty.
//
// Ports and other numeric fields come back as strings/JSON-decoded
// values; the generator normalizes them before YAML emission (G3).
func Links(body []byte) ([]map[string]any, error) {
	body = trimUTF8BOM(body)
	proxies, err := convert.ConvertsV2Ray(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownFormat, err.Error())
	}
	if len(proxies) == 0 {
		return nil, ErrEmpty
	}
	for _, p := range proxies {
		if p == nil {
			continue
		}
		if _, ok := p["name"].(string); !ok {
			return nil, fmt.Errorf("convert: proxy without a name: %v", p)
		}
	}
	return proxies, nil
}

// IsLinkLine reports whether a single line looks like a supported share
// link (scheme://). Used to pick between list parsing and YAML parsing of
// a subscription body (5.7.2).
func IsLinkLine(line string) bool {
	s, _, found := strings.Cut(strings.TrimSpace(line), "://")
	if !found {
		return false
	}
	s = strings.ToLower(s)
	for _, sup := range SupportedSchemes {
		if s == sup || strings.HasPrefix(s, sup+"+") {
			return true
		}
	}
	return false
}

// trimUTF8BOM removes a UTF-8 byte-order mark from the payload.
func trimUTF8BOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
