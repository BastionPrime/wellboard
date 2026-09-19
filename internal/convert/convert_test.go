package convert

import (
	"encoding/base64"
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
