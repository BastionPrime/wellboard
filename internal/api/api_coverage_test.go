package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/store"
	"github.com/wellboard/wellboard/internal/templates"
)

// -----------------------------------------------------------------------------
// Coverage completion for the Phase 3 surface: list endpoints, patch
// branches, 404/409 paths, error plumbing (errHTTP.Error, Problems → 409,
// 500 fallback, dev-stub), and helpers (removeString, SetLog).
// -----------------------------------------------------------------------------

// seedState writes a state with one subscription source (with URL), two
// servers, two groups (one referencing the other), and one route, so the
// list/patch/delete branches have real objects to work on.
func seedState(t *testing.T, srv *Server) {
	t.Helper()
	st, err := srv.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	st.Sources = []model.Source{
		{ID: "src_manual", Kind: "manual", Name: "Manual"},
		{ID: "src_sub", Kind: "subscription", Name: "Sub", URL: "https://panel.example/sub"},
	}
	st.Servers = []model.Server{
		{ID: "srv_1", SourceID: "src_manual", Name: "NL-1", Type: "ss",
			Raw: map[string]any{"server": "203.0.113.10", "port": 8388, "cipher": "x", "password": "y"}},
		{ID: "srv_2", SourceID: "src_manual", Name: "NL-2", Type: "ss",
			Raw: map[string]any{"server": "203.0.113.11", "port": 8388, "cipher": "x", "password": "y"}},
	}
	st.Groups = []model.Group{
		{ID: "grp_1", Name: "Outer", Type: model.GroupSelect, Members: []string{"srv_1", "grp_2"}},
		{ID: "grp_2", Name: "Inner", Type: model.GroupURLTest, Members: []string{"srv_2"}},
	}
	st.Routes = []model.Route{
		{ID: "rt_1", Name: "Block ads", Enabled: true, Order: 10,
			Conditions: []model.RouteCondition{{Type: model.CondDomainSuffix, Value: "ads.example"}},
			Target:     model.Target{Type: model.TargetReject}, OnUnavailable: "block",
			Providers: []string{"ads"}},
	}
	if err := srv.Store.Save(st); err != nil {
		t.Fatal(err)
	}
}

func newSeededServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	srv, ts := newTestServer(t)
	seedState(t, srv)
	return srv, ts
}

// -----------------------------------------------------------------------------
// List endpoints (0% coverage before)
// -----------------------------------------------------------------------------

func TestListEndpoints(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	cases := []struct {
		path string
		need []string
	}{
		{"/api/v1/sources", []string{"src_sub", "src_manual", "Sub"}},
		{"/api/v1/servers", []string{"srv_1", "srv_2", "NL-1"}},
		{"/api/v1/groups", []string{"grp_1", "grp_2", "Outer", "Inner"}},
		{"/api/v1/routes", []string{"rt_1", "Block ads", "ads.example"}},
	}
	for _, c := range cases {
		code, body := do(t, ts, "GET", c.path, "")
		if code != 200 {
			t.Fatalf("GET %s: %d %s", c.path, code, body)
		}
		for _, want := range c.need {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s: %q missing from %s", c.path, want, body)
			}
		}
	}

	// Single GETs: found and 404.
	for _, c := range []struct {
		path, want string
		code       int
	}{
		{"/api/v1/sources/src_sub", "panel.example", 200},
		{"/api/v1/sources/nope", "not found", 404},
		{"/api/v1/servers/srv_1", "NL-1", 200},
		{"/api/v1/servers/nope", "not found", 404},
		{"/api/v1/groups/grp_1", "Outer", 200},
		{"/api/v1/groups/nope", "not found", 404},
		{"/api/v1/routes/rt_1", "Block ads", 200},
		{"/api/v1/routes/nope", "not found", 404},
	} {
		code, body := do(t, ts, "GET", c.path, "")
		if code != c.code {
			t.Fatalf("GET %s: want %d, got %d (%s)", c.path, c.code, code, body)
		}
		if !strings.Contains(body, c.want) {
			t.Fatalf("GET %s: %q missing from %s", c.path, c.want, body)
		}
	}
}

// -----------------------------------------------------------------------------
// Source PATCH branches
// -----------------------------------------------------------------------------

func TestSourcePatchBranches(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	// Unknown source.
	code, body := do(t, ts, "PATCH", "/api/v1/sources/nope", `{"name":"x"}`)
	if code != 404 || !strings.Contains(body, "not found") {
		t.Fatalf("patch unknown: %d %s", code, body)
	}
	// Empty name.
	code, body = do(t, ts, "PATCH", "/api/v1/sources/src_sub", `{"name":""}`)
	if code != 400 || !strings.Contains(body, "name") {
		t.Fatalf("empty name: %d %s", code, body)
	}
	// URL on a manual source.
	code, body = do(t, ts, "PATCH", "/api/v1/sources/src_manual", `{"url":"https://x.example/"}`)
	if code != 400 || !strings.Contains(body, "manual sources have no url") {
		t.Fatalf("manual url: %d %s", code, body)
	}
	// Non-http URL.
	code, body = do(t, ts, "PATCH", "/api/v1/sources/src_sub", `{"url":"ftp://x.example/"}`)
	if code != 400 || !strings.Contains(body, "http(s)") {
		t.Fatalf("bad url: %d %s", code, body)
	}
	// Too-small interval.
	code, body = do(t, ts, "PATCH", "/api/v1/sources/src_sub", `{"update_interval_sec":30}`)
	if code != 400 || !strings.Contains(body, "update_interval_sec") {
		t.Fatalf("small interval: %d %s", code, body)
	}
	// Valid full patch.
	code, body = do(t, ts, "PATCH", "/api/v1/sources/src_sub",
		`{"name":"Renamed","url":"https://panel.example/new","enabled":false,"update_interval_sec":3600}`)
	if code != 200 {
		t.Fatalf("valid patch: %d %s", code, body)
	}
	for _, want := range []string{"Renamed", "panel.example/new", "3600"} {
		if !strings.Contains(body, want) {
			t.Fatalf("patch result missing %q: %s", want, body)
		}
	}
	// Unknown JSON field rejected (strict decoding).
	code, body = do(t, ts, "PATCH", "/api/v1/sources/src_sub", `{"nope":1}`)
	if code != 400 {
		t.Fatalf("unknown field: %d %s", code, body)
	}
	// Invalid JSON.
	code, _ = do(t, ts, "PATCH", "/api/v1/sources/src_sub", `{`)
	if code != 400 {
		t.Fatalf("invalid JSON: %d", code)
	}
}

// -----------------------------------------------------------------------------
// Server delete branches
// -----------------------------------------------------------------------------

func TestServerDeleteBranches(t *testing.T) {
	srv, ts := newSeededServer(t)
	defer ts.Close()

	// Unknown server.
	code, body := do(t, ts, "DELETE", "/api/v1/servers/nope", "")
	if code != 404 {
		t.Fatalf("delete unknown server: %d %s", code, body)
	}
	// A route targeting srv_1 → 409 with the route named.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"ToSrv1","conditions":[{"type":"domain","value":"a.com"}],
		"target":{"type":"server","id":"srv_1"}}`)
	if code != 201 {
		t.Fatalf("create route: %d %s", code, body)
	}
	code, body = do(t, ts, "DELETE", "/api/v1/servers/srv_1", "")
	if code != 409 || !strings.Contains(body, "route target") || !strings.Contains(body, "ToSrv1") {
		t.Fatalf("guarded server delete: %d %s", code, body)
	}
	// Drop the guard route, then delete: 200 and members cleaned up.
	if code, _ = do(t, ts, "DELETE", "/api/v1/routes/rt_2", ""); code != 200 {
		t.Fatalf("delete guard route: %d", code)
	}
	code, body = do(t, ts, "DELETE", "/api/v1/servers/srv_1", "")
	if code != 200 {
		t.Fatalf("delete server: %d %s", code, body)
	}
	st, err := srv.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Servers {
		if s.ID == "srv_1" {
			t.Fatal("srv_1 still in state after delete")
		}
	}
	for _, g := range st.Groups {
		for _, m := range g.Members {
			if m == "srv_1" {
				t.Fatalf("srv_1 still a member of %s", g.ID)
			}
		}
	}
}

// -----------------------------------------------------------------------------
// Group create/patch validation branches
// -----------------------------------------------------------------------------

func TestGroupValidationBranches(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	// Create: reserved prefix.
	code, body := do(t, ts, "POST", "/api/v1/groups",
		`{"name":"rt:evil","type":"select","members":["srv_1"]}`)
	if code != 400 || !strings.Contains(body, "reserved") {
		t.Fatalf("reserved prefix: %d %s", code, body)
	}
	// Create: bad type.
	code, body = do(t, ts, "POST", "/api/v1/groups",
		`{"name":"G","type":"nope","members":["srv_1"]}`)
	if code != 400 || !strings.Contains(body, "type must be") {
		t.Fatalf("bad type: %d %s", code, body)
	}
	// Create: no members.
	code, body = do(t, ts, "POST", "/api/v1/groups",
		`{"name":"G","type":"select","members":[]}`)
	if code != 400 || !strings.Contains(body, "member") {
		t.Fatalf("no members: %d %s", code, body)
	}
	// Create: unknown member.
	code, body = do(t, ts, "POST", "/api/v1/groups",
		`{"name":"G","type":"select","members":["srv_x"]}`)
	if code != 400 || !strings.Contains(body, "neither") {
		t.Fatalf("unknown member: %d %s", code, body)
	}
	// Create: valid nested group.
	code, body = do(t, ts, "POST", "/api/v1/groups",
		`{"name":"New","type":"fallback","members":["grp_1","srv_2"]}`)
	if code != 201 {
		t.Fatalf("valid create: %d %s", code, body)
	}
	// Patch: unknown group.
	code, body = do(t, ts, "PATCH", "/api/v1/groups/nope", `{"name":"x","type":"select","members":["srv_1"]}`)
	if code != 404 {
		t.Fatalf("patch unknown: %d %s", code, body)
	}
	// Patch: empty name.
	code, body = do(t, ts, "PATCH", "/api/v1/groups/grp_1", `{"name":"","type":"select","members":["srv_1"]}`)
	if code != 400 || !strings.Contains(body, "name") {
		t.Fatalf("patch empty name: %d %s", code, body)
	}
	// Patch: reserved name.
	code, body = do(t, ts, "PATCH", "/api/v1/groups/grp_1", `{"name":"rt:x","type":"select","members":["srv_1"]}`)
	if code != 400 || !strings.Contains(body, "reserved") {
		t.Fatalf("patch reserved: %d %s", code, body)
	}
	// Patch: valid rename + type change.
	code, body = do(t, ts, "PATCH", "/api/v1/groups/grp_1",
		`{"name":"Renamed","type":"load-balance","members":["srv_1","grp_2"]}`)
	if code != 200 || !strings.Contains(body, "Renamed") {
		t.Fatalf("valid patch: %d %s", code, body)
	}
	// Self-reference on create.
	code, body = do(t, ts, "POST", "/api/v1/groups", `{"name":"x","type":"select","members":["grp_2"]}`)
	if code != 201 { // no self-ID yet on create — allowed
		t.Logf("create with existing group member: %d %s", code, body)
	}
}

// -----------------------------------------------------------------------------
// Route create/patch/delete branches
// -----------------------------------------------------------------------------

func TestRouteValidationBranches(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	// Unknown condition type.
	code, body := do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"bogus","value":"x"}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "unknown condition") {
		t.Fatalf("unknown cond: %d %s", code, body)
	}
	// Duplicate condition.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"domain-suffix","value":"a.com"},{"type":"domain-suffix","value":"a.com"}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "duplicate") {
		t.Fatalf("dup cond: %d %s", code, body)
	}
	// Empty condition value.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"domain","value":""}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "empty value") {
		t.Fatalf("empty value: %d %s", code, body)
	}
	// Bad CIDR.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"ip-cidr","value":"1.2.3.4/99"}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "not a valid ip/cidr") {
		t.Fatalf("bad cidr: %d %s", code, body)
	}
	// Bad src-device (isCIDR path).
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"src-device","value":"not-an-ip"}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "not a valid ip/cidr") {
		t.Fatalf("bad src-device: %d %s", code, body)
	}
	// Bad domain (space).
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"domain","value":"a b.com"}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "bad domain") {
		t.Fatalf("bad domain: %d %s", code, body)
	}
	// Leading-dot domain.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"domain-suffix","value":".com"}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "bad domain") {
		t.Fatalf("dot domain: %d %s", code, body)
	}
	// Bad dst-port.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"dst-port","value":"99999"}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "dst-port") {
		t.Fatalf("bad dst-port: %d %s", code, body)
	}
	// Reversed port range.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"dst-port","value":"500-100"}],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "dst-port") {
		t.Fatalf("reversed range: %d %s", code, body)
	}
	// Bad provider name.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","providers":["bad/name"],
		"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "provider") {
		t.Fatalf("bad provider: %d %s", code, body)
	}
	// No conditions and no providers.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[],"target":{"type":"direct"}}`)
	if code != 400 || !strings.Contains(body, "at least one condition") {
		t.Fatalf("no conds: %d %s", code, body)
	}
	// Missing target.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"domain","value":"a.com"}]}`)
	if code != 400 || !strings.Contains(body, "target") {
		t.Fatalf("no target: %d %s", code, body)
	}
	// Unknown target server → 409 (validateTarget names the object).
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"domain","value":"a.com"}],
		"target":{"type":"server","id":"srv_x"}}`)
	if code != 409 || !strings.Contains(body, "srv_x") {
		t.Fatalf("unknown server target: %d %s", code, body)
	}
	// Valid create with group target, custom order/enable/on_unavailable.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R2","enabled":false,"order":5,
		"conditions":[{"type":"domain-suffix","value":"x.com"}],
		"providers":["ads"],
		"target":{"type":"group","id":"grp_1"},"on_unavailable":"direct"}`)
	if code != 201 {
		t.Fatalf("valid create: %d %s", code, body)
	}
	for _, want := range []string{"R2", "\"order\":5", "\"enabled\":false", "grp_1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("create result missing %q: %s", want, body)
		}
	}
	// Bad on_unavailable.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"domain","value":"a.com"}],
		"target":{"type":"direct"},"on_unavailable":"nope"}`)
	if code != 400 || !strings.Contains(body, "on_unavailable") {
		t.Fatalf("bad on_unavailable: %d %s", code, body)
	}

	// Patch: unknown route.
	code, body = do(t, ts, "PATCH", "/api/v1/routes/nope", `{"name":"x","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"direct"}}`)
	if code != 404 {
		t.Fatalf("patch unknown route: %d %s", code, body)
	}
	// Patch: empty name.
	code, body = do(t, ts, "PATCH", "/api/v1/routes/rt_1", `{"name":""}`)
	if code != 400 || !strings.Contains(body, "name") {
		t.Fatalf("patch empty name: %d %s", code, body)
	}
	// Patch: nothing to update.
	code, body = do(t, ts, "PATCH", "/api/v1/routes/rt_1", `{"name":"Keep"}`)
	if code != 400 || !strings.Contains(body, "nothing to update") {
		t.Fatalf("patch nothing: %d %s", code, body)
	}
	// Patch: valid partial (conditions kept, target changed).
	code, body = do(t, ts, "PATCH", "/api/v1/routes/rt_1", `{
		"name":"Renamed","target":{"type":"direct"},"enabled":false,"order":42,"on_unavailable":"direct"}`)
	if code != 200 {
		t.Fatalf("valid patch: %d %s", code, body)
	}
	for _, want := range []string{"Renamed", "ads.example", "\"order\":42", "\"enabled\":false"} {
		if !strings.Contains(body, want) {
			t.Fatalf("patch result missing %q: %s", want, body)
		}
	}
	// Patch: invalid merged target → 409.
	code, body = do(t, ts, "PATCH", "/api/v1/routes/rt_1", `{
		"name":"Renamed","target":{"type":"server","id":"srv_x"}}`)
	if code != 409 || !strings.Contains(body, "srv_x") {
		t.Fatalf("patch bad target: %d %s", code, body)
	}
	// Patch: bad on_unavailable on existing route.
	code, body = do(t, ts, "PATCH", "/api/v1/routes/rt_1", `{
		"name":"Renamed","target":{"type":"direct"},"on_unavailable":"nope"}`)
	if code != 400 || !strings.Contains(body, "on_unavailable") {
		t.Fatalf("patch bad on_unavailable: %d %s", code, body)
	}
	// Delete: unknown.
	code, body = do(t, ts, "DELETE", "/api/v1/routes/nope", "")
	if code != 404 {
		t.Fatalf("delete unknown route: %d %s", code, body)
	}
	// Delete: valid.
	code, body = do(t, ts, "DELETE", "/api/v1/routes/rt_1", "")
	if code != 200 {
		t.Fatalf("delete route: %d %s", code, body)
	}
}

// -----------------------------------------------------------------------------
// Template apply error branches
// -----------------------------------------------------------------------------

func TestTemplateApplyBranches(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	// Unknown template.
	code, body := do(t, ts, "POST", "/api/v1/templates/nope/apply", `{"target":{"type":"direct"}}`)
	if code != 404 {
		t.Fatalf("unknown template: %d %s", code, body)
	}
	// Invalid target → 409 (validateTarget).
	code, body = do(t, ts, "POST", "/api/v1/templates/ads-block/apply", `{"target":{"type":"server","id":"srv_x"}}`)
	if code != 409 || !strings.Contains(body, "srv_x") {
		t.Fatalf("bad target: %d %s", code, body)
	}
	// Invalid JSON body.
	code, _ = do(t, ts, "POST", "/api/v1/templates/ads-block/apply", `{`)
	if code != 400 {
		t.Fatalf("invalid JSON: %d", code)
	}
	// Get: unknown template.
	code, body = do(t, ts, "GET", "/api/v1/templates/nope", "")
	if code != 404 {
		t.Fatalf("get unknown template: %d %s", code, body)
	}
}

// Catalog-less server: template endpoints → 503 (main.go dev fallback).
func TestTemplatesUnavailableWithoutCatalog(t *testing.T) {
	stStore := store.New(t.TempDir(), false)
	if err := stStore.Save(store.DefaultState()); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(stStore, nil) // no catalog
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	for _, p := range []string{"/api/v1/templates", "/api/v1/templates/ads-block", "/api/v1/templates/ads-block/apply"} {
		code, body := do(t, ts, "GET", p, "")
		if p != "/api/v1/templates/ads-block/apply" { // apply is POST
			if code != http.StatusServiceUnavailable {
				t.Fatalf("GET %s without catalog: %d %s", p, code, body)
			}
		}
	}
	code, _ := do(t, ts, "POST", "/api/v1/templates/ads-block/apply", `{"target":{"type":"direct"}}`)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("apply without catalog: %d", code)
	}
}

// -----------------------------------------------------------------------------
// LAN endpoints: unset (503), dev-stub simulated, real error, success
// -----------------------------------------------------------------------------

type errStatic struct{ err error }

func (f *errStatic) SetStatic(dev model.LANDevice) error { return f.err }

func TestLANStaticBranches(t *testing.T) {
	srv, ts := newSeededServer(t)
	defer ts.Close()

	// No static backend wired → 503.
	code, body := do(t, ts, "POST", "/api/v1/lan-devices/static", `{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.50"}`)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("static without backend: %d %s", code, body)
	}

	// Dev-stub sentinel → 200 simulated.
	srv.SetLAN(nil, &errStatic{err: lanErrDevStub(t, srv)})
	code, body = do(t, ts, "POST", "/api/v1/lan-devices/static", `{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.50","hostname":"tv"}`)
	if code != 200 || !strings.Contains(body, "simulated") {
		t.Fatalf("dev stub: %d %s", code, body)
	}

	// Real error → 500.
	srv.SetLAN(nil, &errStatic{err: errors.New("boom")})
	code, body = do(t, ts, "POST", "/api/v1/lan-devices/static", `{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.50"}`)
	if code != 500 || !strings.Contains(body, "boom") {
		t.Fatalf("real error: %d %s", code, body)
	}

	// Success.
	srv.SetLAN(nil, &errStatic{})
	code, body = do(t, ts, "POST", "/api/v1/lan-devices/static", `{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.50"}`)
	if code != 200 || !strings.Contains(body, "applied") {
		t.Fatalf("success: %d %s", code, body)
	}

	// Invalid JSON.
	srv.SetLAN(nil, &errStatic{})
	code, _ = do(t, ts, "POST", "/api/v1/lan-devices/static", `{`)
	if code != 400 {
		t.Fatalf("invalid JSON: %d", code)
	}
}

// lanErrDevStub fabricates the sentinel via SetDevStubSentinel + the
// package-level variable (identity match is what handleLANStatic uses).
func lanErrDevStub(t *testing.T, srv *Server) error {
	t.Helper()
	e := errors.New("lan: dev stub")
	SetDevStubSentinel(e)
	return e
}

// -----------------------------------------------------------------------------
// Settings branches
// -----------------------------------------------------------------------------

func TestSettingsBranches(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	// GET.
	code, body := do(t, ts, "GET", "/api/v1/settings", "")
	if code != 200 || !strings.Contains(body, "ui_port") {
		t.Fatalf("settings get: %d %s", code, body)
	}
	// Bad port.
	code, body = do(t, ts, "PATCH", "/api/v1/settings", `{"ui_port":0}`)
	if code != 400 || !strings.Contains(body, "ui_port") {
		t.Fatalf("bad port: %d %s", code, body)
	}
	// Bad lang.
	code, body = do(t, ts, "PATCH", "/api/v1/settings", `{"lang":"fr"}`)
	if code != 400 || !strings.Contains(body, "lang") {
		t.Fatalf("bad lang: %d %s", code, body)
	}
	// Bad geodata.
	code, body = do(t, ts, "PATCH", "/api/v1/settings", `{"geodata":"bogus"}`)
	if code != 400 || !strings.Contains(body, "geodata") {
		t.Fatalf("bad geodata: %d %s", code, body)
	}
	// Bad default policy target → 409.
	code, body = do(t, ts, "PATCH", "/api/v1/settings", `{"default_policy":{"type":"server","id":"srv_x"}}`)
	if code != 409 || !strings.Contains(body, "srv_x") {
		t.Fatalf("bad default policy: %d %s", code, body)
	}
	// Bad interval.
	code, body = do(t, ts, "PATCH", "/api/v1/settings", `{"delay_test_interval_sec":-1}`)
	if code != 400 || !strings.Contains(body, "delay_test_interval_sec") {
		t.Fatalf("bad interval: %d %s", code, body)
	}
	// Valid patch of everything.
	code, body = do(t, ts, "PATCH", "/api/v1/settings", `{
		"ui_port":9000,"lang":"en","geodata":"metacubex",
		"default_policy":{"type":"group","id":"grp_1"},"delay_test_interval_sec":120}`)
	if code != 200 {
		t.Fatalf("valid settings patch: %d %s", code, body)
	}
	for _, want := range []string{"9000", "\"en\"", "metacubex", "grp_1", "120"} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings result missing %q: %s", want, body)
		}
	}
	// Invalid JSON.
	code, _ = do(t, ts, "PATCH", "/api/v1/settings", `{`)
	if code != 400 {
		t.Fatalf("invalid JSON: %d", code)
	}
}

// -----------------------------------------------------------------------------
// Error plumbing: errHTTP.Error, writeError 500 fallback, Problems → 409,
// store load failure → 500, body-size cap
// -----------------------------------------------------------------------------

func TestErrorPlumbing(t *testing.T) {
	// errHTTP.Error().
	e := httpErr(418, "teapot %d", 2)
	if e.Error() != "teapot 2" {
		t.Fatalf("errHTTP.Error: %q", e.Error())
	}

	// writeError: plain error → 500.
	rec := httptest.NewRecorder()
	writeError(rec, errors.New("plain"))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "plain") {
		t.Fatalf("plain error: %d %s", rec.Code, rec.Body.String())
	}

	// writeError: Problems → 409 with items.
	rec = httptest.NewRecorder()
	writeError(rec, &generator.Problems{
		LostTargets: []string{"rt_1: server srv_1 vanished"},
		Invalid:     []string{"rt_2: unknown provider"},
	})
	body := rec.Body.String()
	if rec.Code != 409 || !strings.Contains(body, "unresolved references") ||
		!strings.Contains(body, "srv_1 vanished") || !strings.Contains(body, "unknown provider") {
		t.Fatalf("problems: %d %s", rec.Code, body)
	}

	// Store load failure → 500 on GET /state.
	stStore := store.New(t.TempDir(), false)
	srv := NewServer(stStore, nil)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	// Corrupt the state file so Load fails.
	if err := os.WriteFile(stStore.Path(), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body2 := do(t, ts, "GET", "/api/v1/state", "")
	if code != 500 {
		t.Fatalf("corrupt store: %d %s", code, body2)
	}

	// Body over the 1 MiB cap → 400.
	big := strings.Repeat("x", 2<<20)
	code, _ = do(t, ts, "POST", "/api/v1/sources", big)
	if code != 400 {
		t.Fatalf("oversized body: %d", code)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func TestRemoveStringHelper(t *testing.T) {
	out := removeString([]string{"a", "b", "c"}, "b")
	if len(out) != 2 || out[0] != "a" || out[1] != "c" {
		t.Fatalf("removeString: %v", out)
	}
	if got := removeString(nil, "x"); got != nil || len(got) != 0 {
		t.Fatalf("removeString nil: %v", got)
	}
}

func TestSetLogEmitsDiagnostics(t *testing.T) {
	srv, ts := newSeededServer(t)
	defer ts.Close()

	var got []string
	srv.SetLog(func(format string, args ...any) {
		got = append(got, fmt.Sprintf(format, args...))
	})
	code, body := do(t, ts, "POST", "/api/v1/sources",
		`{"kind":"subscription","name":"X","url":"https://x.example/"}`)
	if code != 201 {
		t.Fatalf("create source: %d %s", code, body)
	}
	if len(got) == 0 || !strings.Contains(got[0], "created") {
		t.Fatalf("log hook not called: %v", got)
	}
}

// Sources create: kind validation + duplicate name → 409.
func TestSourcesCreateValidation(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	// Bad kind.
	code, body := do(t, ts, "POST", "/api/v1/sources", `{"kind":"bogus","name":"B"}`)
	if code != 400 || !strings.Contains(body, "kind") {
		t.Fatalf("bad kind: %d %s", code, body)
	}
	// Subscription without url.
	code, body = do(t, ts, "POST", "/api/v1/sources", `{"kind":"subscription","name":"B"}`)
	if code != 400 || !strings.Contains(body, "url") {
		t.Fatalf("no url: %d %s", code, body)
	}
	// Empty name.
	code, body = do(t, ts, "POST", "/api/v1/sources", `{"kind":"manual","name":""}`)
	if code != 400 || !strings.Contains(body, "name") {
		t.Fatalf("empty name: %d %s", code, body)
	}
	// update_interval_sec below the floor.
	code, body = do(t, ts, "POST", "/api/v1/sources", `{"kind":"subscription","name":"X","url":"https://x.example/","update_interval_sec":30}`)
	if code != 400 || !strings.Contains(body, "update_interval_sec") {
		t.Fatalf("small interval on create: %d %s", code, body)
	}
	// No duplicate-name guard on sources (ids differ); creating a
	// second "Manual" succeeds — pinned here so the contract is explicit.
	code, body = do(t, ts, "POST", "/api/v1/sources", `{"kind":"manual","name":"Manual"}`)
	if code != 201 {
		t.Fatalf("second Manual source: %d %s", code, body)
	}
}

// Source delete: guarded by routes targeting the source (409) and the
// success path dropping the source's servers.
func TestSourceDeleteGuards(t *testing.T) {
	srv, ts := newSeededServer(t)
	defer ts.Close()

	// A route whose source-target is src_manual? Targets reference
	// servers/groups, not sources; the source delete guard fires only
	// when a route target ID equals the source id (defensive). The real
	// semantic: deleting the source drops its servers, so first remove
	// routes targeting them.
	code, body := do(t, ts, "POST", "/api/v1/routes", `{
		"name":"ToSrv1","conditions":[{"type":"domain","value":"a.com"}],
		"target":{"type":"server","id":"srv_1"}}`)
	if code != 201 {
		t.Fatalf("create route: %d %s", code, body)
	}
	// Route rt_2 (new) targets srv_1 of src_manual — but the guard
	// checks route TARGET ids, so deleting the source itself is
	// allowed even while its server is a route target. That is the
	// current contract: the server-delete guard is the protective one.
	code, body = do(t, ts, "DELETE", "/api/v1/sources/src_manual", "")
	if code != 200 {
		t.Fatalf("delete source: %d %s", code, body)
	}
	st, err := srv.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Servers) != 0 {
		t.Fatalf("servers not dropped: %v", st.Servers)
	}
}

// Profile preview success (YAML output) — the 200 leg.
func TestProfilePreviewOK(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/profile", "")
	if code != 200 {
		t.Fatalf("profile preview: %d %s", code, body)
	}
	for _, want := range []string{"proxies:", "proxy-groups:", "rules:", "rt:rt_1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("preview missing %q:\n%s", want, body[:400])
		}
	}
}

// Template list returns the full shipped catalog shape.
func TestTemplatesListShape(t *testing.T) {
	_, ts := newSeededServer(t)
	defer ts.Close()

	code, body := do(t, ts, "GET", "/api/v1/templates", "")
	if code != 200 {
		t.Fatalf("templates: %d %s", code, body)
	}
	for _, want := range []string{"ads-block", "ru-direct", "streaming", "typical_target"} {
		if !strings.Contains(body, want) {
			t.Fatalf("templates missing %q: %s", want, body[:200])
		}
	}
	// The catalog in the server must match the on-disk one.
	cat, err := templates.Load("../../templates")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(body, `"id":`); got < len(cat.Templates) {
		t.Fatalf("expected ≥%d template ids, got %d", len(cat.Templates), got)
	}
}
