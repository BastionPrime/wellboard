package subscription

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

var (
	fmtVless = "vless://8f1c4d2a-3e5f-6a7b-8c9d-0e1f2a3b4c5d@example.com:443?encryption=none&security=tls&sni=example.com&type=ws&path=%2Fws#Node%20A"
	fmtSS    = "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example2.com:8388#Node%20B"
	// fmtSSUrlSafe: fragment chosen so the whole-list std encoding
	// contains '+' — its URL-safe encoding genuinely differs from std.
	fmtSSUrlSafe = "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example2.com:8388#jz%8_HaOP4n~"
)

// wantFmtLinks asserts the two fixture links came out with decoded names.
func wantFmtLinks(t *testing.T, proxies []map[string]any) {
	t.Helper()
	if len(proxies) != 2 {
		t.Fatalf("want 2 proxies, got %d", len(proxies))
	}
	if proxies[0]["name"] != "Node A" || proxies[1]["name"] != "Node B" {
		t.Fatalf("bad names: %q / %q", proxies[0]["name"], proxies[1]["name"])
	}
}

// TestParseBodyBase64StdOneLine: whole-body base64 of a link list with
// trailing padding ("=…"), no line breaks — the classic provider blob.
// Supported: ParseBody → convert.Links → mihomo DecodeBase64 (StdEncoding).
func TestParseBodyBase64StdOneLine(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(fmtVless + "\n" + fmtSS))
	proxies, err := ParseBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	wantFmtLinks(t, proxies)
}

// TestParseBodyBase64StdOneLineNoTrailingNewline: same without the trailing
// newline inside the encoded payload (raw encoding, no padding).
func TestParseBodyBase64StdOneLineNoTrailingNewline(t *testing.T) {
	body := base64.RawStdEncoding.EncodeToString([]byte(fmtVless + "\n" + fmtSS))
	proxies, err := ParseBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	wantFmtLinks(t, proxies)
}

// TestParseBodyBase64WithLineWrapping: base64 of the same list, split into
// 76-char lines with \n (and CRLF) between chunks, as sent by providers
// that line-wrap the blob S/MIME-style.
//
// Supported: Go's base64 decoder (and therefore mihomo's DecodeBase64,
// which feeds the whole buffer to base64.Raw/StdEncoding.Decode) ignores
// \r and \n inside the input, so the wrapped blob decodes transparently.
func TestParseBodyBase64WithLineWrapping(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte(fmtVless + "\n" + fmtSS))
	var sb strings.Builder
	for i := 0; i < len(blob); i += 76 {
		end := min(i+76, len(blob))
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(blob[i:end])
	}
	proxies, err := ParseBody([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	wantFmtLinks(t, proxies)
}

// TestParseBodyBase64WithCRLFLineWrapping: same with \r\n line endings —
// also transparently tolerated by the decoder.
func TestParseBodyBase64WithCRLFLineWrapping(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte(fmtVless + "\n" + fmtSS))
	var sb strings.Builder
	for i := 0; i < len(blob); i += 76 {
		end := min(i+76, len(blob))
		if i > 0 {
			sb.WriteString("\r\n")
		}
		sb.WriteString(blob[i:end])
	}
	proxies, err := ParseBody([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	wantFmtLinks(t, proxies)
}

// TestParseBodyBase64UrlSafe: whole-body blob in the URL-safe alphabet
// (- and _ instead of + and /). The fixture fragment is chosen so the
// two encodings actually differ (std contains + or /).
//
// NOT SUPPORTED: mihomo's tryDecodeBase64 only tries RawStd/Std — not the
// URL-safe alphabets — so the blob stays undecoded and no links are found
// → ErrEmpty/ErrUnknownFormat. Gap → separate ticket if a provider needs
// it (retry with URLEncoding in internal/convert, like DecodeMaybeBase64
// already does for headers).
func TestParseBodyBase64UrlSafe(t *testing.T) {
	urlsafe := base64.URLEncoding.EncodeToString([]byte(fmtVless + "\n" + fmtSSUrlSafe))
	if urlsafe == base64.StdEncoding.EncodeToString([]byte(fmtVless+"\n"+fmtSSUrlSafe)) {
		t.Fatal("fixture must produce different std/url-safe encodings")
	}
	_, err := ParseBody([]byte(urlsafe))
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("url-safe base64 must fail as unknown format today, got %v", err)
	}
}

// TestParseBodyPlainListMultiLine: plain (unencoded) list, several lines,
// tolerated blank line in the middle.
func TestParseBodyPlainListMultiLine(t *testing.T) {
	body := "\n" + fmtVless + "\n\n" + fmtSS + "\n"
	proxies, err := ParseBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	wantFmtLinks(t, proxies)
}

// TestParseBodyPlainListCRLF: plain list with \r\n line endings.
func TestParseBodyPlainListCRLF(t *testing.T) {
	body := fmtVless + "\r\n" + fmtSS + "\r\n"
	proxies, err := ParseBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	wantFmtLinks(t, proxies)
}

// TestParseBodyPlainOnlyOneLink: single plain link, no newline at all.
func TestParseBodyPlainOnlyOneLink(t *testing.T) {
	proxies, err := ParseBody([]byte(fmtSS))
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 1 || proxies[0]["name"] != "Node B" {
		t.Fatalf("bad: %v", proxies)
	}
}

// TestParseBodyYAMLQuotedLinkList: plain link list written as a YAML
// document of quoted strings (no proxies: key). The YAML-with-proxies
// path does not claim it, and the upstream link parser splits on lines
// without unquoting, so the quotes break every link — ErrUnknownFormat.
//
// NOT SUPPORTED: gap → separate ticket if a provider actually sends this
// (mihomo core behaves the same way, so this matches upstream behaviour).
func TestParseBodyYAMLQuotedLinkList(t *testing.T) {
	body := "  - \"" + fmtVless + "\"\n  - \"" + fmtSS + "\"\n"
	if _, err := ParseBody([]byte(body)); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("want ErrUnknownFormat, got %v", err)
	}
}

// TestParseBodyBase64DecodeMaybeBase64Header: header values use the
// "base64:<payload>" prefix (R5) with padded Std payload — covered; here
// the full matrix for both header positions in one place.
func TestParseBodyDecodeMaybeBase64Matrix(t *testing.T) {
	plain := "Заголовок"
	for name, enc := range map[string]func([]byte) string{
		"std-padded": base64.StdEncoding.EncodeToString,
		"raw-std":    base64.RawStdEncoding.EncodeToString,
		"url-padded": base64.URLEncoding.EncodeToString,
		"raw-url":    base64.RawURLEncoding.EncodeToString,
	} {
		wrapped := "base64:" + enc([]byte(plain))
		if got := DecodeMaybeBase64(wrapped); got != plain {
			t.Errorf("%s: want %q, got %q", name, plain, got)
		}
	}
	if got := DecodeMaybeBase64(plain); got != plain {
		t.Errorf("plain passthrough broken: %q", got)
	}
	// Undecodable payload after the prefix is returned as-is.
	got := DecodeMaybeBase64("base64:!!!not base64!!!")
	if got != "base64:!!!not base64!!!" {
		t.Errorf("undecodable must pass through, got %q", got)
	}
}
