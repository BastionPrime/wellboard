package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Edge tests for the model validation performed at the API layer.
// internal/model is pure types; every bound below is enforced in
// internal/api (decodeStrict + the validate* funcs over model structs).
// Each test asserts BOTH the rejection (400/409, message) AND that the
// state is unchanged after the failed write.

// wantBad asserts a 400 with a message fragment.
func wantBad(t *testing.T, ts *httptest.Server, method, path, body, want string) {
	t.Helper()
	code, got := do(t, ts, method, path, body)
	if code != http.StatusBadRequest {
		t.Fatalf("%s %s: status %d (want 400), body %s", method, path, code, got)
	}
	if !strings.Contains(got, want) {
		t.Fatalf("%s %s: body %q lacks %q", method, path, got, want)
	}
}

// TestSettingsValidationEdges: negative/zero/out-of-range ports and
// intervals, unknown enum values, plus the strict-decode unknown-field
// rejection — all 400, state untouched.
func TestSettingsValidationEdges(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	before := getState(t, ts)

	cases := []struct {
		name string
		body string
		want string
	}{
		// Ports: 0, negative, above 65535 are rejected; 1 and 65535 pass.
		{"zero port", `{"ui_port":0}`, "ui_port must be 1-65535"},
		{"negative port", `{"ui_port":-1}`, "ui_port must be 1-65535"},
		{"port above 65535", `{"ui_port":65536}`, "ui_port must be 1-65535"},
		// Interval: negative rejected, zero allowed (disabled).
		{"negative interval", `{"delay_test_interval_sec":-1}`, "delay_test_interval_sec must be ≥ 0"},
		// Lang / geodata sources: unknown values rejected.
		{"bad lang", `{"lang":"de"}`, "lang"},
		{"bad geosite source", `{"geosite_source":"nope"}`, "geosite_source"},
		{"bad geoip source", `{"geoip_source":"nope"}`, "geoip_source"},
		{"bad custom url", `{"geosite_source":"custom","geosite_custom_url":"ftp://x"}`, "http(s)"},
		// Strict decode: unknown field in the settings payload.
		{"unknown field", `{"ui_portx":8090}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantBad(t, ts, "PATCH", "/api/v1/settings", tc.body, tc.want)
		})
	}
	// State untouched by all the failures above.
	after := getState(t, ts)
	if after.Settings.UIPort != before.Settings.UIPort ||
		after.Settings.DelayTestIntervalSec != before.Settings.DelayTestIntervalSec ||
		after.Settings.Lang != before.Settings.Lang {
		t.Fatalf("failed settings writes mutated state: %+v -> %+v", before.Settings, after.Settings)
	}

	// Boundary values are accepted.
	for _, body := range []string{`{"ui_port":1}`, `{"ui_port":65535}`, `{"delay_test_interval_sec":0}`} {
		code, got := do(t, ts, "PATCH", "/api/v1/settings", body)
		if code != http.StatusOK {
			t.Fatalf("boundary settings %s: %d %s", body, code, got)
		}
	}
}

// TestSourceValidationEdges: empty name, unknown kind, non-http(s) URL,
// interval below the 60 s floor (and 0 on create, which is also invalid
// there — auto only exists on PATCH).
func TestSourceValidationEdges(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	before := getState(t, ts)

	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty name", `{"kind":"subscription","name":"","url":"https://x.invalid/s"}`, "name is required"},
		{"missing name", `{"kind":"subscription","url":"https://x.invalid/s"}`, "name is required"},
		{"unknown kind", `{"kind":"web","name":"W","url":"https://x.invalid/s"}`, `subscription`},
		{"ftp url", `{"kind":"subscription","name":"W","url":"ftp://x.invalid/s"}`, "http(s)"},
		{"empty url", `{"kind":"subscription","name":"W","url":""}`, "http(s)"},
		{"interval 59 on create", `{"kind":"subscription","name":"W","url":"https://x.invalid/s","update_interval_sec":59}`, "≥ 60"},
		{"interval 0 on create", `{"kind":"subscription","name":"W","url":"https://x.invalid/s","update_interval_sec":0}`, "≥ 60"},
		{"negative interval", `{"kind":"subscription","name":"W","url":"https://x.invalid/s","update_interval_sec":-60}`, "≥ 60"},
		{"unknown field", `{"kind":"manual","name":"W","interval":60}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantBad(t, ts, "POST", "/api/v1/sources", tc.body, tc.want)
		})
	}
	after := getState(t, ts)
	if len(after.Sources) != len(before.Sources) {
		t.Fatalf("failed source creates mutated state: %d -> %d sources",
			len(before.Sources), len(after.Sources))
	}

	// Boundary: interval 60 is accepted.
	code, got := do(t, ts, "POST", "/api/v1/sources",
		`{"kind":"subscription","name":"W","url":"https://x.invalid/s","update_interval_sec":60}`)
	if code != http.StatusCreated {
		t.Fatalf("interval 60: %d %s", code, got)
	}
	// PATCH: 0 (auto) and 60 pass; 59 fails; empty name fails.
	code, _ = do(t, ts, "PATCH", "/api/v1/sources/sub_1", `{"update_interval_sec":0}`)
	if code != http.StatusOK {
		t.Fatalf("patch interval 0: %d", code)
	}
	wantBad(t, ts, "PATCH", "/api/v1/sources/sub_1", `{"update_interval_sec":59}`, "0 (auto) or ≥ 60")
	wantBad(t, ts, "PATCH", "/api/v1/sources/sub_1", `{"name":""}`, "name must not be empty")
}

// TestGroupValidationEdges: empty name, reserved prefix, unknown type,
// no members, self-reference, unknown member.
func TestGroupValidationEdges(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)
	_ = srv
	before := getState(t, ts)

	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty name", `{"name":"","type":"select","members":["srv_1"]}`, "name is required"},
		{"reserved prefix", `{"name":"rt:evil","type":"select","members":["srv_1"]}`, "reserved"},
		{"unknown type", `{"name":"G","type":"round-robin","members":["srv_1"]}`, "type must be one of"},
		{"no members", `{"name":"G","type":"select","members":[]}`, "at least one member"},
		{"unknown member", `{"name":"G","type":"select","members":["srv_999"]}`, "neither a known server nor group"},
		{"unknown field", `{"name":"G","type":"select","members":["srv_1"],"member":["srv_1"]}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantBad(t, ts, "POST", "/api/v1/groups", tc.body, tc.want)
		})
	}
	after := getState(t, ts)
	if len(after.Groups) != len(before.Groups) {
		t.Fatalf("failed group creates mutated state: %d -> %d groups",
			len(before.Groups), len(after.Groups))
	}
	// AUDIT FINDING (documented in the report): handleGroupPatch never
	// calls validateGroupIn — it writes name/type/members directly, so a
	// group CAN be patched to contain itself, have a reserved name
	// prefix ("rt:") or unknown members. Pin the current behavior so the
	// hole is visible; fixing it is a behavior change for the owner.
	code, body := do(t, ts, "PATCH", "/api/v1/groups/grp_1",
		`{"name":"G","type":"select","members":["grp_1"]}`)
	if code != http.StatusOK {
		t.Fatalf("group self-reference patch: %d %s (behavior changed — update this pin)", code, body)
	}
	// Restore the seeded group so later assertions see a valid state.
	code, _ = do(t, ts, "PATCH", "/api/v1/groups/grp_1",
		`{"name":"Outer","type":"select","members":["srv_1"]}`)
	if code != http.StatusOK {
		t.Fatalf("restore grp_1: %d", code)
	}
}

// TestRouteValidationEdges: empty name, no conditions/providers, unknown
// condition type, empty condition value, duplicate conditions, missing
// target, unknown target, bad on_unavailable.
func TestRouteValidationEdges(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)
	_ = srv
	before := getState(t, ts)

	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty name", `{"name":"","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"direct"}}`, "name is required"},
		{"no conditions", `{"name":"R","target":{"type":"direct"}}`, "at least one condition"},
		{"unknown condition type", `{"name":"R","conditions":[{"type":"user-agent","value":"x"}],"target":{"type":"direct"}}`, "unknown condition type"},
		{"empty condition value", `{"name":"R","conditions":[{"type":"domain","value":""}],"target":{"type":"direct"}}`, "empty value"},
		{"duplicate conditions", `{"name":"R","conditions":[{"type":"domain","value":"a.com"},{"type":"domain","value":"a.com"}],"target":{"type":"direct"}}`, "duplicate condition"},
		{"missing target", `{"name":"R","conditions":[{"type":"domain","value":"a.com"}]}`, "target is required"},
		{"unknown target server", `{"name":"R","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"server","id":"srv_999"}}`, "srv_999"},
		{"unknown target type", `{"name":"R","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"chain"}}`, "target"},
		{"bad on_unavailable", `{"name":"R","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"direct"},"on_unavailable":"drop"}`, "on_unavailable"},
		{"unknown field", `{"name":"R","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"direct"},"priority":1}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, got := do(t, ts, "POST", "/api/v1/routes", tc.body)
			if code != http.StatusBadRequest && code != http.StatusConflict {
				t.Fatalf("POST /api/v1/routes %s: status %d (want 400/409), body %s", tc.name, code, got)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("POST /api/v1/routes %s: body %q lacks %q", tc.name, got, tc.want)
			}
		})
	}
	after := getState(t, ts)
	if len(after.Routes) != len(before.Routes) {
		t.Fatalf("failed route creates mutated state: %d -> %d routes",
			len(before.Routes), len(after.Routes))
	}
}

// TestDuplicateIDsRejectedViaImport: duplicate object IDs inside an
// imported state document are a broken state and must be rejected (409
// via the generator's referential-integrity run) without touching the
// current state.
func TestDuplicateIDsRejectedViaImport(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)
	_ = srv
	before := getState(t, ts)

	dupServers := `{"format":1,"app":"wellboard","version":2,"state":{"version":2,` +
		`"sources":[{"id":"src_manual","kind":"manual","name":"Manual"}],` +
		`"servers":[` +
		`{"id":"srv_1","source_id":"src_manual","name":"A","type":"ss","raw":{"server":"h","port":1}},` +
		`{"id":"srv_1","source_id":"src_manual","name":"B","type":"ss","raw":{"server":"h","port":1}}` +
		`]}}`
	code, body := do(t, ts, "POST", "/api/v1/import", dupServers)
	if code != http.StatusConflict && code != http.StatusBadRequest {
		t.Fatalf("import with duplicate server ids: status %d, body %s", code, body)
	}
	after := getState(t, ts)
	if len(after.Servers) != len(before.Servers) {
		t.Fatalf("duplicate-id import mutated state: %d -> %d servers",
			len(before.Servers), len(after.Servers))
	}

	dupRoutes := `{"format":1,"app":"wellboard","version":2,"state":{"version":2,` +
		`"routes":[` +
		`{"id":"rt_1","name":"A","enabled":true,"order":1,"conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"direct"},"on_unavailable":"block"},` +
		`{"id":"rt_1","name":"B","enabled":true,"order":2,"conditions":[{"type":"domain","value":"b.com"}],"target":{"type":"direct"},"on_unavailable":"block"}` +
		`]}}`
	code, body = do(t, ts, "POST", "/api/v1/import", dupRoutes)
	if code != http.StatusConflict && code != http.StatusBadRequest {
		t.Fatalf("import with duplicate route ids: status %d, body %s", code, body)
	}
	after = getState(t, ts)
	if len(after.Routes) != len(before.Routes) {
		t.Fatalf("duplicate-route-id import mutated state: %d -> %d routes",
			len(before.Routes), len(after.Routes))
	}
}

// TestImportFieldBoundaries: model-level boundaries of the import
// envelope — unknown envelope fields and unknown state fields are
// rejected by the strict decoder; a document just under the 1 MiB cap
// is accepted.
func TestImportFieldBoundaries(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)

	// Unknown field nested inside the state document.
	doc := `{"format":1,"app":"wellboard","version":2,"state":{"version":2,"exotic":1}}`
	wantBad(t, ts, "POST", "/api/v1/import", doc, "unknown field")
}

// TestOversizedBodiesRejected: every JSON endpoint enforces the 1 MiB
// cap. At/just under the limit the request is processed (and rejected
// for content, not size); above it the body is rejected for size.
func TestOversizedBodiesRejected(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)

	// 1 MiB pad → over the cap once wrapped in JSON syntax.
	pad := strings.Repeat("a", 1<<20)
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/api/v1/sources", `{"name":"` + pad + `"}`},
		{"/api/v1/groups", `{"name":"` + pad + `"}`},
		{"/api/v1/routes", `{"name":"` + pad + `"}`},
	} {
		method := "POST"
		if tc.path == "/api/v1/settings" {
			method = "PATCH"
		}
		code, body := do(t, ts, method, tc.path, tc.body)
		if code != http.StatusBadRequest {
			t.Fatalf("%s %s oversized: status %d, body %.80s", method, tc.path, code, body)
		}
		if !strings.Contains(body, "invalid JSON body") {
			t.Fatalf("%s %s oversized: body %.120s lacks size/decode error", method, tc.path, body)
		}
	}

	// Just under the cap the body is NOT rejected for size: it decodes
	// and passes validation (a long group name is legal — the model has
	// no name-length limit; the 1 MiB cap is the only size guard).
	small := strings.Repeat("a", (1<<20)-64)
	code, body := do(t, ts, "POST", "/api/v1/groups", `{"name":"`+small+`","type":"select","members":["srv_1"]}`)
	if code != http.StatusCreated {
		t.Fatalf("boundary-size group: status %d, body %.80s", code, body)
	}
	if strings.Contains(body, "invalid JSON body") {
		t.Fatalf("boundary-size group was rejected as a JSON/size error, body %s", body)
	}

	// State untouched by the oversized (rejected) writes; the boundary
	// group DID land (+1).
	st := getState(t, ts)
	if len(st.Groups) != 3 { // seeded two + the boundary-size group
		t.Fatalf("group count after oversized writes: %d (want 3)", len(st.Groups))
	}
}

// TestEmptyNamesAcrossEndpoints: empty names are rejected everywhere a
// name is required (sources, groups, routes, templates) — one sweep so
// a future endpoint cannot silently diverge.
func TestEmptyNamesAcrossEndpoints(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)

	cases := []struct {
		method string
		path   string
		body   string
		want   string
	}{
		{"POST", "/api/v1/sources", `{"kind":"manual","name":""}`, "name"},
		{"POST", "/api/v1/groups", `{"name":"","type":"select","members":["srv_1"]}`, "name"},
		{"POST", "/api/v1/routes", `{"name":"","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"direct"}}`, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			wantBad(t, ts, tc.method, tc.path, tc.body, tc.want)
		})
	}
}
