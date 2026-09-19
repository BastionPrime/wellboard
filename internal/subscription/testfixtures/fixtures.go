// Package testfixtures exposes the mock-remnawave HTTP handler set for
// in-process contract tests (initial TZ 5.10: the mock server is a test
// double; running the same mux under httptest keeps the fixtures and the
// standalone command byte-identical).
//
// test/mock-remnawave/main.go is a thin wrapper around this package: it
// only parses flags and serves NewMux() on a real port. One source of
// truth for fixture paths.
package testfixtures

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"time"
)

// YAMLProxies is a valid mihomo-format subscription body.
const YAMLProxies = `proxies:
  - name: NL-1
    type: ss
    server: 203.0.113.10
    port: 8388
    cipher: aes-256-gcm
    password: "pass1"
    udp: true
  - name: NL-2
    type: ss
    server: 203.0.113.11
    port: 8388
    cipher: aes-256-gcm
    password: "pass2"
    udp: true
proxy-groups:
  - name: PG
    type: select
    proxies: [NL-1, NL-2]
rules:
  - MATCH,PG
`

// YAMLProxies2 is the "one proxy vanished" variant of YAMLProxies.
const YAMLProxies2 = `proxies:
  - name: NL-1
    type: ss
    server: 203.0.113.10
    port: 8388
    cipher: aes-256-gcm
    password: "pass1"
    udp: true
`

// SubLinks is a vless + ss link list (plain format).
const SubLinks = "vless://8f1c4d2a-3e5f-6a7b-8c9d-0e1f2a3b4c5d@example.com:443?encryption=none&security=tls&sni=example.com&type=ws&path=%2Fws#Node%20A\n" +
	"ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example2.com:8388#Node%20B\n"

// NewMux builds the fixture handler set (see the package comment of
// test/mock-remnawave/main.go for the endpoint list).
func NewMux() *http.ServeMux {
	mux := http.NewServeMux()

	// --- normal subscription fixtures ---------------------------------

	mux.HandleFunc("/sub/mihomo", func(w http.ResponseWriter, r *http.Request) {
		// Path clientType (R2): format is pinned by the path, any UA.
		writeYAML(w, withNormalHeaders)
	})
	mux.HandleFunc("/sub", func(w http.ResponseWriter, r *http.Request) {
		// Response-rule emulation (R1): only a UA containing "mihomo"
		// gets the mihomo template; anything else is 403.
		if !containsMihomoUA(r.UserAgent()) {
			http.Error(w, "forbidden: no response rule matched", http.StatusForbidden)
			return
		}
		writeYAML(w, withNormalHeaders)
	})
	mux.HandleFunc("/sub2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/yaml")
		fmt.Fprint(w, YAMLProxies2)
	})
	mux.HandleFunc("/sub/links", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain")
		w.Header().Set("profile-title", "base64:"+base64.StdEncoding.EncodeToString([]byte("Fixture Sub")))
		fmt.Fprint(w, base64.StdEncoding.EncodeToString([]byte(SubLinks)))
	})
	mux.HandleFunc("/sub/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain")
		io.WriteString(w, SubLinks)
	})

	// --- FR-2.4 device-limit fixtures ---------------------------------

	mux.HandleFunc("/404-hwid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-hwid-active", "true")
		w.Header().Set("x-hwid-max-devices-reached", "true")
		http.Error(w, "subscription not found", http.StatusNotFound)
	})
	mux.HandleFunc("/404-plain", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "subscription not found", http.StatusNotFound)
	})
	mux.HandleFunc("/limit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-hwid-active", "true")
		w.Header().Set("x-hwid-max-devices-reached", "true")
		w.Header().Set("subscription-userinfo", "upload=0; download=0; total=107374182400; expire=1893456000")
		writeYAML(w, nil)
	})
	mux.HandleFunc("/announce", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("announce", "Scheduled maintenance on Sunday")
		w.Header().Set("profile-title", "base64:"+base64.StdEncoding.EncodeToString([]byte("Base64 Title")))
		w.Header().Set("profile-update-interval", "3600")
		writeYAML(w, nil)
	})
	mux.HandleFunc("/notsupported", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-hwid-not-supported", "true")
		writeYAML(w, nil)
	})

	// --- failure fixtures ----------------------------------------------

	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		// Abruptly close: the client sees a network error, not a status.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
				return
			}
		}
		panic("hijack unsupported")
	})
	mux.HandleFunc("/timeout", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Second)
	})

	// --- contract-test helper ------------------------------------------

	mux.HandleFunc("/hdr", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprintf(w, `{"headers":{%s},"path":%q}`, headerJSON(r), r.URL.Path)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mock-remnawave: unknown fixture path "+r.URL.Path, http.StatusNotFound)
	})

	return mux
}

func containsMihomoUA(ua string) bool {
	for i := 0; i+6 <= len(ua); i++ {
		if ua[i:i+6] == "mihomo" {
			return true
		}
	}
	// case-insensitive fallback
	lower := ua
	for i := 0; i < len(lower); i++ {
		if lower[i] >= 'A' && lower[i] <= 'Z' {
			lower = lower[:i] + string(rune(lower[i]+'a'-'A')) + lower[i+1:]
		}
	}
	for i := 0; i+6 <= len(lower); i++ {
		if lower[i:i+6] == "mihomo" {
			return true
		}
	}
	return false
}

// headerJSON renders the request headers as a JSON object literal.
func headerJSON(r *http.Request) string {
	out := ""
	first := true
	for k, vv := range r.Header {
		if !first {
			out += ","
		}
		first = false
		out += fmt.Sprintf("%q:[", k)
		for i, v := range vv {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf("%q", v)
		}
		out += "]"
	}
	return out
}

// withNormalHeaders applies the "healthy subscription" header set.
func withNormalHeaders(w http.ResponseWriter) {
	w.Header().Set("subscription-userinfo", "upload=1000; download=2000; total=107374182400; expire=1893456000")
	w.Header().Set("profile-update-interval", "3600")
	w.Header().Set("x-hwid-active", "true")
}

func writeYAML(w http.ResponseWriter, decorate func(http.ResponseWriter)) {
	if decorate != nil {
		decorate(w)
	}
	w.Header().Set("content-type", "text/yaml")
	fmt.Fprint(w, YAMLProxies)
}
