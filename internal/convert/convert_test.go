package convert

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestLinksVless(t *testing.T) {
	in := []byte("vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:443?encryption=none&security=tls&sni=example.com&type=ws&path=%2Fpath#VL1\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 proxy, got %d", len(out))
	}
	p := out[0]
	if p["name"] != "VL1" || p["type"] != "vless" {
		t.Fatalf("bad proxy: %v", p)
	}
	if p["server"] != "5.6.7.8" || p["port"] != "443" {
		t.Fatalf("bad endpoint: %v", p)
	}
	if p["uuid"] != "5e3f1e2e-1111-2222-3333-444455556666" {
		t.Fatalf("bad uuid: %v", p["uuid"])
	}
}

func TestLinksTrojan(t *testing.T) {
	in := []byte("trojan://pass123@9.8.7.6:443?sni=tro.example.com#TR1\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0]["type"] != "trojan" || out[0]["password"] != "pass123" {
		t.Fatalf("bad proxy: %v", out)
	}
}

func TestLinksSS(t *testing.T) {
	in := []byte("ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@1.2.3.4:8388#TestSS\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	p := out[0]
	if p["type"] != "ss" || p["cipher"] != "aes-256-gcm" || p["password"] != "password" {
		t.Fatalf("bad ss proxy: %v", p)
	}
}

func TestLinksHysteria2(t *testing.T) {
	in := []byte("hysteria2://secretpw@3.4.5.6:8443?sni=hy.example.com&obfs=salamander&obfs-password=ob123#HY1\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	p := out[0]
	if p["type"] != "hysteria2" || p["password"] != "secretpw" {
		t.Fatalf("bad hy2 proxy: %v", p)
	}
}

func TestLinksTUIC(t *testing.T) {
	in := []byte("tuic://uuid-here:pw-here@7.7.7.7:443?alpn=h3&sni=tu.example.com#TU1\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	p := out[0]
	if p["type"] != "tuic" || p["uuid"] != "uuid-here" || p["password"] != "pw-here" {
		t.Fatalf("bad tuic proxy: %v", p)
	}
}

func TestLinksVMess(t *testing.T) {
	// V2RayN-style base64 JSON body.
	json := `{"ps":"VM1","add":"1.1.1.1","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":0,"scy":"auto","net":"ws","host":"vm.example.com","path":"/ws","tls":"tls","sni":"vm.example.com","alpn":"h2,http/1.1"}`
	in := []byte("vmess://" + base64.StdEncoding.EncodeToString([]byte(json)) + "\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	p := out[0]
	if p["type"] != "vmess" || p["name"] != "VM1" || p["server"] != "1.1.1.1" {
		t.Fatalf("bad vmess proxy: %v", p)
	}
}

func TestLinksWholeBodyBase64(t *testing.T) {
	lines := "vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:443?security=tls&sni=a.com#VL1\nss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@1.2.3.4:8388#TestSS\n"
	in := []byte(base64.StdEncoding.EncodeToString([]byte(lines)))
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
}

func TestLinksEmptyInput(t *testing.T) {
	if _, err := Links([]byte("\n\n")); err == nil {
		t.Fatal("empty input must error")
	}
	if _, err := Links([]byte("")); err == nil {
		t.Fatal("empty input must error")
	}
}

func TestLinksGarbage(t *testing.T) {
	// Valid-looking scheme lines but unparseable bodies are skipped; a
	// payload with zero usable links is ErrEmpty.
	if _, err := Links([]byte("random text, no links here")); err == nil {
		t.Fatal("garbage must error")
	}
}

func TestLinksMixedValidAndInvalid(t *testing.T) {
	in := []byte("vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:443?security=tls&sni=a.com#VL1\n" +
		"not-a-link\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("bad line must be skipped, want 1 got %d", len(out))
	}
}

func TestLinksUTF8BOM(t *testing.T) {
	in := append([]byte{0xEF, 0xBB, 0xBF}, []byte("ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@1.2.3.4:8388#BOM\n")...)
	out, err := Links(in)
	if err != nil {
		t.Fatalf("BOM must be tolerated: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
}

func TestIsLinkLine(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"vless://x@y:1", true},
		{"trojan://x@y:1", true},
		{"ss://x", true},
		{"vmess://x", true},
		{"hysteria2://x", true},
		{"tuic://x", true},
		{"HYSTERIA2://x", true}, // case-insensitive scheme
		{"http://example.com", false},
		{"random text", false},
		{"vless:", false},
	}
	for _, c := range cases {
		if got := IsLinkLine(c.in); got != c.want {
			t.Errorf("IsLinkLine(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLinksNameUniqueness(t *testing.T) {
	// Two links with the same fragment: upstream deduplicates with -01.
	in := []byte("ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@1.2.3.4:8388#Same\n" +
		"ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@5.6.7.8:8388#Same\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	if out[0]["name"] == out[1]["name"] {
		t.Fatalf("names must be deduplicated: %v vs %v", out[0]["name"], out[1]["name"])
	}
}

func TestLinksBadInput(t *testing.T) {
	// Whole-payload error cases: the input is rejected outright, not
	// silently turned into zero proxies.
	cases := []struct {
		name string
		in   string
	}{
		{"no_scheme_host_port", "5.6.7.8:443"},
		{"vless_missing_port", "vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8#NP"},
		{"vless_non_numeric_port", "vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:notaport#BP"},
		{"empty_host", "vless://5e3f1e2e-1111-2222-3333-444455556666@:443#EH"},
		{"empty_scheme_colon_only", "vless:"},
		{"whitespace_only", "   	  \n"},
		{"garbage_bytes", string([]byte{0xDE, 0xAD, 0xBE, 0xEF})},
		{"vmess_bad_base64", "vmess://!!!not-base64!!!"},
		{"vmess_bad_json", "vmess://" + base64.StdEncoding.EncodeToString([]byte("{not json"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Links([]byte(c.in))
			if err == nil {
				t.Fatalf("Links(%q) must fail, got proxies: %v", c.in, out)
			}
			if out != nil {
				t.Fatalf("Links(%q) must return nil proxies on error, got %v", c.in, out)
			}
		})
	}
}

func TestLinksBadLineSkipped(t *testing.T) {
	// A broken line alongside a valid one is skipped; the valid one wins.
	in := []byte("vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:notaport#bad\n" +
		"vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:443#good\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0]["name"] != "good" {
		t.Fatalf("want only the good line, got %v", out)
	}
}

func TestLinksNonUTF8Fragment(t *testing.T) {
	// Raw invalid-UTF-8 bytes in the fragment are tolerated by the
	// converter (they travel through as-is); parsing must not fail.
	in := []byte("vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:443?sni=a.com#" + string([]byte{0xFF, 0xFE}))
	out, err := Links(in)
	if err != nil {
		t.Fatalf("non-UTF-8 fragment must not fail parsing: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if out[0]["type"] != "vless" || out[0]["server"] != "5.6.7.8" || out[0]["port"] != "443" {
		t.Fatalf("bad proxy: %v", out[0])
	}
}

func TestLinksOversizedField(t *testing.T) {
	// A single ~70 KB query field: a manual paste should not blow up the
	// parser; the link either parses or is rejected as a whole — no
	// crash, no hang.
	link := "vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:443?sni=" +
		strings.Repeat("a", 70000) + "#BIG"
	out, err := Links([]byte(link))
	if err != nil {
		t.Fatalf("oversized field must not fail parsing: %v", err)
	}
	if len(out) != 1 || out[0]["port"] != "443" {
		t.Fatalf("bad proxy: %v", out)
	}
}

func TestLinksIPv6AndPortEdges(t *testing.T) {
	// IPv6 bracket hosts (with and without a zone id) and numeric edge
	// ports (0, 65536) come through the URL host parser unmangled.
	cases := []struct {
		name   string
		in     string
		server string
		port   string
	}{
		{"ipv6_bracket", "vless://5e3f1e2e-1111-2222-3333-444455556666@[2001:db8::1]:443#V6", "2001:db8::1", "443"},
		{"ipv6_zone_id", "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@[fe80::1%25eth0]:8388#V6Z", "fe80::1%eth0", "8388"},
		{"port_zero", "vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:0#P0", "5.6.7.8", "0"},
		{"port_over_65535", "vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:65536#P65", "5.6.7.8", "65536"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Links([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 {
				t.Fatalf("want 1, got %d", len(out))
			}
			if out[0]["server"] != c.server || out[0]["port"] != c.port {
				t.Fatalf("want server=%s port=%s, got server=%v port=%v", c.server, c.port, out[0]["server"], out[0]["port"])
			}
		})
	}
}

func TestLinksNoFragment(t *testing.T) {
	// Links without a #fragment parse; the proxy name is the empty
	// string (documented upstream behavior — mihomo dedup keeps names
	// unique only when a fragment is present).
	in := []byte("vless://5e3f1e2e-1111-2222-3333-444455556666@5.6.7.8:443\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if out[0]["type"] != "vless" || out[0]["server"] != "5.6.7.8" || out[0]["port"] != "443" {
		t.Fatalf("bad proxy: %v", out[0])
	}
	if name, ok := out[0]["name"].(string); !ok || name != "" {
		t.Fatalf("no-fragment link must parse with an empty name, got %v", out[0]["name"])
	}
}

func TestLinksNoFragmentDeduped(t *testing.T) {
	// Two identical no-fragment vless links to different servers: names
	// collide (both empty) but the proxies are still both returned.
	in := []byte("vless://5e3f1e2e-1111-2222-3333-444455556666@1.1.1.1:443\n" +
		"vless://5e3f1e2e-1111-2222-3333-444455556666@2.2.2.2:443\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
}

func TestLinksTUICVariants(t *testing.T) {
	// TUIC share links: v5 style (uuid:password userinfo) carries
	// uuid+password; v4 style (token-only userinfo, no colon) carries a
	// token instead. The unofficial format is documented at
	// https://github.com/daeuniverse/dae/discussions/182.
	cases := []struct {
		name     string
		in       string
		key      string // "uuid"/"password" for v5, "token" for v4
		uuid     string
		password string
		token    string
	}{
		{
			"v5_userinfo",
			"tuic://uuid-here:pw-here@7.7.7.7:443?alpn=h3&sni=tu.example.com&congestion_control=bbr&udp_relay_mode=native#TU1",
			"v5", "uuid-here", "pw-here", "",
		},
		{
			"v4_token_only",
			"tuic://token-here@7.7.7.7:443?alpn=h3#TU4",
			"v4", "", "", "token-here",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Links([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 {
				t.Fatalf("want 1, got %d", len(out))
			}
			p := out[0]
			if p["type"] != "tuic" {
				t.Fatalf("want type tuic, got %v", p["type"])
			}
			if p["server"] != "7.7.7.7" || p["port"] != "443" {
				t.Fatalf("bad endpoint: %v", p)
			}
			switch c.key {
			case "v5":
				if p["uuid"] != c.uuid || p["password"] != c.password {
					t.Fatalf("want uuid %q password %q, got %v / %v", c.uuid, c.password, p["uuid"], p["password"])
				}
				if _, hasToken := p["token"]; hasToken {
					t.Fatalf("v5 link must not carry a token: %v", p)
				}
			case "v4":
				if p["token"] != c.token {
					t.Fatalf("want token %q, got %v", c.token, p["token"])
				}
				if _, hasUUID := p["uuid"]; hasUUID {
					t.Fatalf("v4 link must not carry uuid/password: %v", p)
				}
			}
		})
	}
}

func TestLinksTUICV5ParamsCarried(t *testing.T) {
	// v5 query params map to mihomo keys: alpn (split into a list),
	// congestion control, UDP relay mode, sni.
	in := []byte("tuic://uuid-here:pw-here@7.7.7.7:443?alpn=h3,h2&congestion_control=bbr&udp_relay_mode=native&sni=tu.example.com#TU1\n")
	out, err := Links(in)
	if err != nil {
		t.Fatal(err)
	}
	p := out[0]
	alpn, ok := p["alpn"].([]string)
	if !ok || len(alpn) != 2 || alpn[0] != "h3" || alpn[1] != "h2" {
		t.Fatalf("want alpn [h3 h2], got %v (%T)", p["alpn"], p["alpn"])
	}
	if p["congestion-controller"] != "bbr" {
		t.Fatalf("want congestion-controller bbr, got %v", p["congestion-controller"])
	}
	if p["udp-relay-mode"] != "native" {
		t.Fatalf("want udp-relay-mode native, got %v", p["udp-relay-mode"])
	}
	if p["sni"] != "tu.example.com" {
		t.Fatalf("want sni tu.example.com, got %v", p["sni"])
	}
}

func TestLinksHysteria2ObfsParams(t *testing.T) {
	// obfs/obfs-password query params map to the mihomo keys of the
	// same name; insecure=1 maps to skip-cert-verify.
	cases := []struct {
		name         string
		in           string
		obfs         string
		obfsPassword string
		insecure     any // nil = key absent/false, true = true
	}{
		{
			"salamander_with_password",
			"hysteria2://secretpw@3.4.5.6:8443?sni=hy.example.com&obfs=salamander&obfs-password=ob123#HY1",
			"salamander", "ob123", false,
		},
		{
			"obfs_no_password",
			"hysteria2://secretpw@3.4.5.6:8443?obfs=SALAMANDER#HY2",
			"SALAMANDER", "", false,
		},
		{
			"insecure_flag",
			"hysteria2://secretpw@3.4.5.6:8443?insecure=1#HY3",
			"", "", true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Links([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 {
				t.Fatalf("want 1, got %d", len(out))
			}
			p := out[0]
			if p["type"] != "hysteria2" || p["password"] != "secretpw" {
				t.Fatalf("bad hy2 proxy: %v", p)
			}
			if p["obfs"] != c.obfs {
				t.Fatalf("want obfs %q, got %v", c.obfs, p["obfs"])
			}
			if p["obfs-password"] != c.obfsPassword {
				t.Fatalf("want obfs-password %q, got %v", c.obfsPassword, p["obfs-password"])
			}
			if p["skip-cert-verify"] != c.insecure {
				t.Fatalf("want skip-cert-verify %v, got %v", c.insecure, p["skip-cert-verify"])
			}
		})
	}
}

func TestLinksSSPluginSuffix(t *testing.T) {
	// SIP003 plugin suffix on the query string: obfs-local with opts and
	// the bare v2ray-plugin name both parse; the plugin/opts keys are
	// carried into the proxy map.
	cases := []struct {
		name       string
		in         string
		plugin     string
		pluginOpts map[string]any
	}{
		{
			"obfs_local_http",
			"ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@1.2.3.4:8388?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dexample.com#SSP",
			"obfs", map[string]any{"mode": "http", "host": "example.com"},
		},
		{
			"v2ray_plugin_bare",
			"ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@1.2.3.4:8388/?plugin=v2ray-plugin#SSV",
			"", nil, // bare name without opts: plugin key omitted
		},
		{
			"sip002_plain_userinfo",
			"ss://aes-256-gcm:password@1.2.3.4:8388#SIP002",
			"", nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Links([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 {
				t.Fatalf("want 1, got %d", len(out))
			}
			p := out[0]
			if p["type"] != "ss" || p["cipher"] != "aes-256-gcm" || p["password"] != "password" {
				t.Fatalf("bad ss proxy: %v", p)
			}
			if got, ok := p["plugin"].(string); c.plugin != "" && (!ok || got != c.plugin) {
				t.Fatalf("want plugin %q, got %v", c.plugin, p["plugin"])
			}
			if c.pluginOpts != nil {
				opts, ok := p["plugin-opts"].(map[string]any)
				if !ok {
					t.Fatalf("want plugin-opts map, got %v (%T)", p["plugin-opts"], p["plugin-opts"])
				}
				for k, want := range c.pluginOpts {
					if opts[k] != want {
						t.Fatalf("plugin-opts[%q] = %v, want %v", k, opts[k], want)
					}
				}
			}
		})
	}
}

func TestLinksVMessPaddingQuirks(t *testing.T) {
	// v2rayN emits StdEncoding (padded); other tools emit raw unpadded,
	// and some add a trailing newline. All variants must decode.
	base := `{"ps":"PADQ","add":"1.1.1.1","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":0,"scy":"auto","net":"tcp"}`
	std := base64.StdEncoding.EncodeToString([]byte(base))
	raw := base64.RawStdEncoding.EncodeToString([]byte(base))
	urlSafe := base64.URLEncoding.EncodeToString([]byte(base))
	cases := []struct {
		name string
		in   string
	}{
		{"std_padded", "vmess://" + std},
		{"std_padded_newline", "vmess://" + std + "\n"},
		{"raw_unpadded", "vmess://" + raw},
		{"raw_unpadded_newline", "vmess://" + raw + "\n"},
		{"url_safe_padded", "vmess://" + urlSafe},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Links([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 || out[0]["name"] != "PADQ" || out[0]["type"] != "vmess" {
				t.Fatalf("bad proxy: %v", out)
			}
		})
	}
}

func TestLinksVMessPortTypes(t *testing.T) {
	// The v2rayN JSON carries the port as a string ("443") or as a JSON
	// number (443) depending on the exporter; both must produce port 443.
	cases := []struct {
		name string
		json string
	}{
		{"port_string", `{"ps":"PT","add":"1.1.1.1","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":0,"scy":"auto","net":"tcp"}`},
		{"port_number", `{"ps":"PT","add":"1.1.1.1","port":443,"id":"11111111-2222-3333-4444-555555555555","aid":0,"scy":"auto","net":"tcp"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := []byte("vmess://" + base64.StdEncoding.EncodeToString([]byte(c.json)))
			out, err := Links(in)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 || fmt.Sprint(out[0]["port"]) != "443" {
				t.Fatalf("want port 443, got %v", out)
			}
		})
	}
}

func TestLinksUnknownFormatSurfaced(t *testing.T) {
	// A payload the converter cannot make sense of surfaces as
	// ErrUnknownFormat (the API maps it to a 400 with the cause).
	_, err := Links([]byte("definitely not a subscription"))
	if err == nil {
		t.Fatal("must error")
	}
	if !strings.Contains(err.Error(), "unknown subscription format") {
		t.Fatalf("want ErrUnknownFormat chain, got %v", err)
	}
}

func TestIsLinkLineEdges(t *testing.T) {
	// Whitespace around the line, mixed-case schemes and plus-suffixed
	// schemes are all recognized; unsupported schemes are not.
	cases := []struct {
		in   string
		want bool
	}{
		{"\tvless://x@y:1", true},
		{"  ss://x", true},
		{"Vless://x", true},
		{"vless+tls://x", true},
		{"ssr://x", false},
		{"hysteria://x", false}, // only hysteria2 is supported
		{"socks5://x", false},
		{"http://example.com", false},
		{"vmess://", true},
		{"", false},
		{"vless", false},
	}
	for _, c := range cases {
		if got := IsLinkLine(c.in); got != c.want {
			t.Errorf("IsLinkLine(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
