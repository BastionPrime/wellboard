// Command mock-remnawave is a fixture HTTP server that mimics a Remnawave
// panel subscription endpoint (initial TZ 5.10) for WellBoard tests.
//
// The handler set lives in internal/subscription/testfixtures (single
// source of truth — the in-process contract tests use the exact same
// mux); this command is the human-runnable form for manual testing:
//
//	go run ./test/mock-remnawave [-addr 127.0.0.1:8642]
//
// Endpoints (see testfixtures/fixtures.go for authoritative bodies):
//
//	/sub                 200, YAML proxies + subscription-userinfo +
//	                        x-hwid-active (UA must contain "mihomo",
//	                        else 403 — the R1 response-rule emulation)
//	/sub/mihomo          200, same body for any UA (path clientType, R2)
//	/sub2                200, YAML with one proxy (vanishing fixture)
//	/sub/links           200, base64-encoded list of vless/ss links
//	/sub/plain           200, plain list of vless/ss links
//	/404-hwid            404 + x-hwid-active + x-hwid-max-devices-reached
//	/404-plain           404, no x-hwid headers
//	/limit               200 + x-hwid-max-devices-reached: true + userinfo
//	/announce            200 + announce + profile-title (base64:) +
//	                        profile-update-interval
//	/notsupported        200 + x-hwid-not-supported: true
//	/fail                closes the connection (network-error fixture)
//	/timeout             hangs until the client gives up
//	/hdr                 200, echoes the received request headers as JSON
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/wellboard/wellboard/internal/subscription/testfixtures"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8642", "listen address")
	flag.Parse()

	log.Printf("mock-remnawave listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, testfixtures.NewMux()))
}
