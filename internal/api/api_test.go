package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/store"
	"github.com/wellboard/wellboard/internal/templates"
)

// newTestServer returns a server on a temp store plus its http test
// server and a helper for requests.
func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	stStore := store.New(t.TempDir(), false)
	if err := stStore.Save(store.DefaultState()); err != nil {
		t.Fatal(err)
	}
	catalog, err := templates.Load("../../templates")
	if err != nil {
		t.Fatalf("shipped catalog: %v", err)
	}
	srv := NewServer(stStore, catalog)
	mux := http.NewServeMux()
	srv.Register(mux)
	return srv, httptest.NewServer(mux)
}

// do performs a request and returns status + body.
func do(t *testing.T, ts *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	var rdr *bytes.Reader
	if body == "" {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

// stateFromFile loads the store behind ts. We instead re-GET /state.
func getState(t *testing.T, ts *httptest.Server) *model.State {
	t.Helper()
	code, body := do(t, ts, "GET", "/api/v1/state", "")
	if code != 200 {
		t.Fatalf("GET /state: %d %s", code, body)
	}
	st := &model.State{}
	if err := json.Unmarshal([]byte(body), st); err != nil {
		t.Fatal(err)
	}
	return st
}

// addServerDirect inserts a server directly through the store used by
// the server (constructed via newTestServer). Returns its ID.
func addServerDirect(t *testing.T, srv *Server, id string) {
	t.Helper()
	st, err := srv.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	st.Servers = append(st.Servers, model.Server{
		ID: id, SourceID: "src_manual", Name: "NL-" + id, Type: "ss",
		Raw: map[string]any{"server": "203.0.113.10", "port": 8388, "cipher": "x", "password": "y"},
	})
	st.Sources = append(st.Sources, model.Source{ID: "src_manual", Kind: "manual", Name: "Manual"})
	if err := srv.Store.Save(st); err != nil {
		t.Fatal(err)
	}
}

// ----------------------------------------------------------------------------
// Health of the surface
// ----------------------------------------------------------------------------

func TestRoutes405OnWrongMethod(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	if code, _ := do(t, ts, "POST", "/api/v1/routes", ""); code != http.StatusBadRequest {
		// POST with empty body → 400 (invalid JSON), fine; DELETE on the
		// collection should 405.
		t.Logf("empty POST → %d", code)
	}
	if code, _ := do(t, ts, "DELETE", "/api/v1/routes", ""); code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /routes must 405, got %d", code)
	}
}

// ----------------------------------------------------------------------------
// Sources
// ----------------------------------------------------------------------------

func TestSourcesCRUD(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()

	code, body := do(t, ts, "POST", "/api/v1/sources",
		`{"kind":"subscription","name":"WellDone","url":"https://panel.example/sub"}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	src := model.Source{}
	_ = json.Unmarshal([]byte(body), &src)
	if src.ID == "" || !strings.HasPrefix(src.ID, "sub_") {
		t.Fatalf("server must mint sub_ id: %q", src.ID)
	}

	// Get.
	code, body = do(t, ts, "GET", "/api/v1/sources/"+src.ID, "")
	if code != 200 || !strings.Contains(body, "WellDone") {
		t.Fatalf("get: %d %s", code, body)
	}

	// Patch enabled.
	code, body = do(t, ts, "PATCH", "/api/v1/sources/"+src.ID, `{"enabled":false}`)
	if code != 200 {
		t.Fatalf("patch: %d %s", code, body)
	}
	// Re-fetch: PATCH returns the source; "enabled" is omitted when false
	// (model uses omitempty), so verify through the list endpoint.
	code, body = do(t, ts, "GET", "/api/v1/sources/"+src.ID, "")
	if code != 200 || strings.Contains(body, `"enabled":true`) {
		t.Fatalf("patch not applied: %d %s", code, body)
	}

	// Validation.
	if code, _ := do(t, ts, "POST", "/api/v1/sources", `{"kind":"subscription","name":"x","url":"ftp://nope"}`); code != 400 {
		t.Fatalf("non-http url must 400, got %d", code)
	}
	if code, _ := do(t, ts, "POST", "/api/v1/sources", `{"kind":"weird","name":"x"}`); code != 400 {
		t.Fatalf("bad kind must 400, got %d", code)
	}
	if code, _ := do(t, ts, "POST", "/api/v1/sources", `{"name":"x"}`); code != 400 {
		t.Fatalf("missing kind must 400, got %d", code)
	}
	if code, _ := do(t, ts, "POST", "/api/v1/sources", `{"kind":"manual","name":"x","typo":1}`); code != 400 {
		t.Fatalf("unknown field must 400 (strict), got %d", code)
	}

	// Delete.
	if code, _ := do(t, ts, "DELETE", "/api/v1/sources/"+src.ID, ""); code != 200 {
		t.Fatalf("delete failed: %d", code)
	}
	if code, _ := do(t, ts, "GET", "/api/v1/sources/"+src.ID, ""); code != 404 {
		t.Fatalf("deleted source must 404, got %d", code)
	}
}

// ----------------------------------------------------------------------------
// Groups
// ----------------------------------------------------------------------------

func TestGroupsCRUD(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	addServerDirect(t, srv, "srv_1")

	code, body := do(t, ts, "POST", "/api/v1/groups",
		`{"name":"NL auto","type":"url-test","members":["srv_1"]}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	g := model.Group{}
	_ = json.Unmarshal([]byte(body), &g)
	if g.ID == "" || !strings.HasPrefix(g.ID, "grp_") {
		t.Fatalf("grp_ id expected: %q", g.ID)
	}

	// Unknown member → 400 with member named.
	if code, body := do(t, ts, "POST", "/api/v1/groups",
		`{"name":"bad","type":"select","members":["srv_missing"]}`); code != 400 || !strings.Contains(body, "srv_missing") {
		t.Fatalf("unknown member must 400 naming it: %d %s", code, body)
	}
	// Reserved prefix.
	if code, _ := do(t, ts, "POST", "/api/v1/groups",
		`{"name":"rt:evil","type":"select","members":["srv_1"]}`); code != 400 {
		t.Fatalf("rt: prefix must be rejected, got %d", code)
	}
	// Bad type.
	if code, _ := do(t, ts, "POST", "/api/v1/groups",
		`{"name":"x","type":"round-robin","members":["srv_1"]}`); code != 400 {
		t.Fatalf("bad group type must 400, got %d", code)
	}

	// Patch.
	code, body = do(t, ts, "PATCH", "/api/v1/groups/"+g.ID, `{"name":"NL auto2","type":"fallback","members":["srv_1"]}`)
	if code != 200 || !strings.Contains(body, "NL auto2") {
		t.Fatalf("patch: %d %s", code, body)
	}

	// Delete guard: route targeting group blocks deletion (FR-1.5).
	_, rbody := do(t, ts, "POST", "/api/v1/routes", fmt.Sprintf(
		`{"name":"R","conditions":[{"type":"domain-suffix","value":"x.com"}],"target":{"type":"group","id":%q}}`, g.ID))
	rt := model.Route{}
	_ = json.Unmarshal([]byte(rbody), &rt)
	code, body = do(t, ts, "DELETE", "/api/v1/groups/"+g.ID, "")
	if code != 409 || !strings.Contains(body, rt.ID) {
		t.Fatalf("delete of targeted group must 409 naming the route: %d %s", code, body)
	}
	// Delete the route first, then the group.
	if code, _ := do(t, ts, "DELETE", "/api/v1/routes/"+rt.ID, ""); code != 200 {
		t.Fatalf("route delete failed: %d", code)
	}
	if code, _ := do(t, ts, "DELETE", "/api/v1/groups/"+g.ID, ""); code != 200 {
		t.Fatalf("group delete after route removal failed: %d", code)
	}
}

// ----------------------------------------------------------------------------
// Routes
// ----------------------------------------------------------------------------

func TestRoutesCRUDAndValidation(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	addServerDirect(t, srv, "srv_1")

	code, body := do(t, ts, "POST", "/api/v1/routes", `{
		"name":"Streaming",
		"conditions":[
			{"type":"geosite","value":"YOUTUBE"},
			{"type":"domain-suffix","value":"netflix.com"}
		],
		"target":{"type":"server","id":"srv_1"}
	}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	rt := model.Route{}
	_ = json.Unmarshal([]byte(body), &rt)
	if rt.ID == "" || rt.Order == 0 || rt.OnUnavailable != "block" {
		t.Fatalf("route defaults wrong: %+v", rt)
	}

	// Target validation: unknown server → 409 (not 400: referential).
	if code, body := do(t, ts, "POST", "/api/v1/routes",
		`{"name":"X","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"server","id":"srv_404"}}`); code != 409 {
		t.Fatalf("unknown target must 409, got %d %s", code, body)
	}
	// Stale target → 409 with stale reason.
	st, _ := srv.Store.Load()
	st.Servers[0].Stale = true
	_ = srv.Store.Save(st)
	if code, body := do(t, ts, "POST", "/api/v1/routes",
		`{"name":"X","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"server","id":"srv_1"}}`); code != 409 || !strings.Contains(body, "stale") {
		t.Fatalf("stale target must 409 naming stale: %d %s", code, body)
	}
	st, _ = srv.Store.Load()
	st.Servers[0].Stale = false
	_ = srv.Store.Save(st)

	// Condition validation.
	badConditions := []string{
		`{"name":"X","conditions":[],"target":{"type":"direct"}}`,                              // empty
		`{"name":"X","conditions":[{"type":"geosite","value":""}],"target":{"type":"direct"}}`, // empty value
		`{"name":"X","conditions":[{"type":"bogus","value":"x"}],"target":{"type":"direct"}}`,  // unknown type
		`{"name":"X","conditions":[{"type":"ip-cidr","value":"not-a-cidr"}],"target":{"type":"direct"}}`,
		`{"name":"X","conditions":[{"type":"dst-port","value":"99999"}],"target":{"type":"direct"}}`,
		`{"name":"X","conditions":[{"type":"domain-suffix","value":".bad"}],"target":{"type":"direct"}}`,
	}
	for _, b := range badConditions {
		if code, _ := do(t, ts, "POST", "/api/v1/routes", b); code != 400 {
			t.Fatalf("bad condition must 400: %s → %d", b, code)
		}
	}
	// Duplicate conditions.
	if code, _ := do(t, ts, "POST", "/api/v1/routes",
		`{"name":"X","conditions":[{"type":"domain","value":"a.com"},{"type":"domain","value":"a.com"}],"target":{"type":"direct"}}`); code != 400 {
		t.Fatalf("duplicate condition must 400, got %d", code)
	}
	// Missing target.
	if code, _ := do(t, ts, "POST", "/api/v1/routes",
		`{"name":"X","conditions":[{"type":"domain","value":"a.com"}]}`); code != 400 {
		t.Fatalf("missing target must 400, got %d", code)
	}
	// Bad on_unavailable.
	if code, _ := do(t, ts, "POST", "/api/v1/routes",
		`{"name":"X","conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"direct"},"on_unavailable":"sideways"}`); code != 400 {
		t.Fatalf("bad on_unavailable must 400, got %d", code)
	}

	// Order patch (FR-4.5).
	code, body = do(t, ts, "PATCH", "/api/v1/routes/"+rt.ID,
		`{"name":"Streaming","conditions":[{"type":"domain-suffix","value":"netflix.com"}],"target":{"type":"server","id":"srv_1"},"order":5}`)
	if code != 200 {
		t.Fatalf("patch: %d %s", code, body)
	}
	_ = body

	// Delete server with dependent route → 409.
	code, body = do(t, ts, "DELETE", "/api/v1/servers/srv_1", "")
	if code != 409 || !strings.Contains(body, rt.ID) {
		t.Fatalf("server delete with dependent route must 409: %d %s", code, body)
	}
}

// ----------------------------------------------------------------------------
// Templates
// ----------------------------------------------------------------------------

func TestTemplatesListAndGet(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	code, body := do(t, ts, "GET", "/api/v1/templates", "")
	if code != 200 || !strings.Contains(body, "streaming") {
		t.Fatalf("list: %d %.200s", code, body)
	}
	code, body = do(t, ts, "GET", "/api/v1/templates/streaming", "")
	if code != 200 || !strings.Contains(body, "YOUTUBE") {
		t.Fatalf("get: %d %.200s", code, body)
	}
	if code, _ := do(t, ts, "GET", "/api/v1/templates/nope", ""); code != 404 {
		t.Fatalf("unknown template must 404, got %d", code)
	}
}

func TestTemplateApplyCreatesRoute(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	addServerDirect(t, srv, "srv_1")

	code, body := do(t, ts, "POST", "/api/v1/templates/streaming/apply",
		`{"target":{"type":"server","id":"srv_1"}}`)
	if code != 201 {
		t.Fatalf("apply: %d %s", code, body)
	}
	rt := model.Route{}
	_ = json.Unmarshal([]byte(body), &rt)
	if len(rt.Conditions) != 6 {
		t.Fatalf("streaming template must carry 6 conditions, got %d", len(rt.Conditions))
	}
	if rt.Target.ID != "srv_1" || rt.Providers == nil || len(rt.Providers) != 1 || rt.Providers[0] != "streaming" {
		t.Fatalf("route from template wrong: %+v", rt)
	}
	// FR-5.2: the created route is a normal route — patchable.
	code, body = do(t, ts, "PATCH", "/api/v1/routes/"+rt.ID,
		`{"name":"My streaming","conditions":[{"type":"domain-suffix","value":"netflix.com"}],"target":{"type":"server","id":"srv_1"}}`)
	if code != 200 || !strings.Contains(body, "My streaming") {
		t.Fatalf("patch of template-created route: %d %s", code, body)
	}

	// Unknown target → 409.
	if code, _ := do(t, ts, "POST", "/api/v1/templates/streaming/apply",
		`{"target":{"type":"server","id":"srv_404"}}`); code != 409 {
		t.Fatalf("apply with unknown target must 409, got %d", code)
	}
}

func TestTemplateApplyAllVPNChangesDefaultPolicy(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	addServerDirect(t, srv, "srv_1")

	code, body := do(t, ts, "POST", "/api/v1/templates/all-vpn/apply",
		`{"target":{"type":"server","id":"srv_1"}}`)
	if code != 200 || !strings.Contains(body, "default-policy") {
		t.Fatalf("all-vpn apply: %d %s", code, body)
	}
	st := getState(t, ts)
	if st.Settings.DefaultPolicy.Type != model.TargetServer || st.Settings.DefaultPolicy.ID != "srv_1" {
		t.Fatalf("default policy must be updated: %+v", st.Settings.DefaultPolicy)
	}
	if len(st.Routes) != 0 {
		t.Fatalf("all-vpn must not create a route, got %d", len(st.Routes))
	}
}

// ----------------------------------------------------------------------------
// LAN devices
// ----------------------------------------------------------------------------

type fakeLAN struct {
	devs []model.LANDevice
	err  error
}

func (f *fakeLAN) Devices() ([]model.LANDevice, error) { return f.devs, f.err }

type fakeStatic struct{ called []model.LANDevice }

func (f *fakeStatic) SetStatic(dev model.LANDevice) error {
	f.called = append(f.called, dev)
	return nil
}

func TestLANDevicesAndStatic(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	srv.SetLAN(&fakeLAN{devs: []model.LANDevice{
		{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.50", Hostname: "tv"},
	}}, &fakeStatic{})

	code, body := do(t, ts, "GET", "/api/v1/lan-devices", "")
	if code != 200 || !strings.Contains(body, "192.168.1.50") {
		t.Fatalf("devices: %d %s", code, body)
	}

	code, body = do(t, ts, "POST", "/api/v1/lan-devices/static",
		`{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.50","hostname":"tv"}`)
	if code != 200 {
		t.Fatalf("static: %d %s", code, body)
	}
}

func TestLANUnset(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	if code, _ := do(t, ts, "GET", "/api/v1/lan-devices", ""); code != 503 {
		t.Fatalf("unset LAN reader must 503, got %d", code)
	}
}

// ----------------------------------------------------------------------------
// Settings
// ----------------------------------------------------------------------------

func TestSettingsPatch(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	code, body := do(t, ts, "GET", "/api/v1/settings", "")
	if code != 200 || !strings.Contains(body, `"geodata":"runetfreedom"`) {
		t.Fatalf("default settings: %d %s", code, body)
	}
	code, body = do(t, ts, "PATCH", "/api/v1/settings", `{"geodata":"metacubex","lang":"en"}`)
	if code != 200 || !strings.Contains(body, `"geodata":"metacubex"`) {
		t.Fatalf("patch: %d %s", code, body)
	}
	if code, _ := do(t, ts, "PATCH", "/api/v1/settings", `{"geodata":"bogus"}`); code != 400 {
		t.Fatalf("bad geodata must 400, got %d", code)
	}
	if code, _ := do(t, ts, "PATCH", "/api/v1/settings", `{"ui_port":99999}`); code != 400 {
		t.Fatalf("bad port must 400, got %d", code)
	}
	// Default policy target must resolve.
	if code, _ := do(t, ts, "PATCH", "/api/v1/settings",
		`{"default_policy":{"type":"server","id":"srv_404"}}`); code != 409 {
		t.Fatalf("unknown default policy target must 409, got %d", code)
	}
}

// ----------------------------------------------------------------------------
// Route provider validation (review follow-up, phase 3)
// ----------------------------------------------------------------------------

// TestRouteProvidersValidatedAgainstCatalog: a route referencing a
// provider that is not in the template catalog must be rejected at
// write time (400 naming the provider), and — for state written
// through other paths (store migrations, older versions) — the
// profile preview must 409 with the provider name, never silently
// render a profile pointing at a missing payload file.
func TestRouteProvidersValidatedAgainstCatalog(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	addServerDirect(t, srv, "srv_1")

	// POST /routes with an unknown provider → 400 naming it.
	code, body := do(t, ts, "POST", "/api/v1/routes", `{
		"name":"Bogus",
		"conditions":[{"type":"domain","value":"b.com"}],
		"providers":["bogus"],
		"target":{"type":"direct"}
	}`)
	if code != 400 || !strings.Contains(body, "bogus") {
		t.Fatalf("unknown provider on create must 400 naming it: %d %s", code, body)
	}

	// Create a valid route, then PATCH it with a bogus provider → 400.
	code, body = do(t, ts, "POST", "/api/v1/routes", `{
		"name":"Good",
		"conditions":[{"type":"domain","value":"g.com"}],
		"target":{"type":"server","id":"srv_1"}
	}`)
	if code != 201 {
		t.Fatalf("valid route create: %d %s", code, body)
	}
	rt := model.Route{}
	_ = json.Unmarshal([]byte(body), &rt)

	code, body = do(t, ts, "PATCH", "/api/v1/routes/"+rt.ID, `{
		"name":"Good",
		"conditions":[{"type":"domain","value":"g.com"}],
		"providers":["bogus"],
		"target":{"type":"server","id":"srv_1"}
	}`)
	if code != 400 || !strings.Contains(body, "bogus") {
		t.Fatalf("PATCH with unknown provider must 400 naming it: %d %s", code, body)
	}

	// Valid provider (ads, shipped payload) → 201/200.
	code, _ = do(t, ts, "PATCH", "/api/v1/routes/"+rt.ID, `{
		"name":"Good",
		"conditions":[{"type":"domain","value":"g.com"}],
		"providers":["ads"],
		"target":{"type":"server","id":"srv_1"}
	}`)
	if code != 200 {
		t.Fatalf("known provider must pass: %d", code)
	}

	// Belt and braces: even if a bogus provider lands in the store
	// (e.g. written by an older build), /profile must 409 with the
	// provider named — not 500, not a silent 200.
	st, _ := srv.Store.Load()
	for i := range st.Routes {
		if st.Routes[i].ID == rt.ID {
			st.Routes[i].Providers = []string{"bogus"}
		}
	}
	_ = srv.Store.Save(st)
	code, body = do(t, ts, "GET", "/api/v1/profile", "")
	if code != 409 || !strings.Contains(body, "bogus") {
		t.Fatalf("/profile with unknown provider must 409 naming it: %d %s", code, body)
	}
	var resp map[string]any
	_ = json.Unmarshal([]byte(body), &resp)
	if probs, ok := resp["problems"].([]any); !ok || len(probs) == 0 {
		t.Fatalf("409 body must carry a problems list: %s", body)
	}
}

// ----------------------------------------------------------------------------
// Profile preview + generator error mapping (FR-4.8 → 409)
// ----------------------------------------------------------------------------

func TestProfilePreviewAndLostTarget(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	addServerDirect(t, srv, "srv_1")

	// Healthy state → 200 YAML.
	code, body := do(t, ts, "POST", "/api/v1/routes", `{
		"name":"R","conditions":[{"type":"domain-suffix","value":"x.com"}],
		"target":{"type":"server","id":"srv_1"}}`)
	if code != 201 {
		t.Fatalf("route create: %d %s", code, body)
	}
	rt := model.Route{}
	_ = json.Unmarshal([]byte(body), &rt)

	code, body = do(t, ts, "GET", "/api/v1/profile", "")
	if code != 200 || !strings.Contains(body, "rt:"+rt.ID) {
		t.Fatalf("profile preview: %d %.300s", code, body)
	}

	// Break the target server (stale) → generation fails → 409 with the
	// problem list naming the route (FR-4.8 mapping).
	st, _ := srv.Store.Load()
	for i := range st.Servers {
		if st.Servers[i].ID == "srv_1" {
			st.Servers[i].Stale = true
		}
	}
	_ = srv.Store.Save(st)
	code, body = do(t, ts, "GET", "/api/v1/profile", "")
	if code != 409 {
		t.Fatalf("lost target must map to 409, got %d %s", code, body)
	}
	var resp map[string]any
	_ = json.Unmarshal([]byte(body), &resp)
	probs, _ := resp["problems"].([]any)
	if len(probs) == 0 || !strings.Contains(body, rt.ID) {
		t.Fatalf("409 body must list the route problem: %s", body)
	}
}

// ----------------------------------------------------------------------------
// Helper coverage
// ----------------------------------------------------------------------------

func TestHelperFunctions(t *testing.T) {
	if !isCIDR("192.168.1.0/24") || !isCIDR("192.168.1.5") || isCIDR("nope") || isCIDR("1.2.3.4/99") {
		t.Fatal("isCIDR broken")
	}
	if !isPortOrRange("443") || !isPortOrRange("100-200") || isPortOrRange("0") || isPortOrRange("200-100") || isPortOrRange("a") {
		t.Fatal("isPortOrRange broken")
	}
	if err := validateOnUnavailable("block"); err != nil {
		t.Fatal(err)
	}
	if err := validateOnUnavailable("nope"); err == nil {
		t.Fatal("bad on_unavailable must error")
	}
}

func TestFilePathSafety(t *testing.T) {
	// newID must not collide with seeded ids.
	srv, ts := newTestServer(t)
	defer ts.Close()
	addServerDirect(t, srv, "srv_1")
	st, _ := srv.Store.Load()
	st.Routes = append(st.Routes, model.Route{ID: "rt_1", Name: "existing", Order: 10,
		Conditions: []model.RouteCondition{{Type: model.CondDomain, Value: "a.com"}},
		Target:     model.Target{Type: model.TargetDirect}})
	_ = srv.Store.Save(st)
	code, body := do(t, ts, "POST", "/api/v1/routes",
		`{"name":"new","conditions":[{"type":"domain","value":"b.com"}],"target":{"type":"direct"}}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	rt := model.Route{}
	_ = json.Unmarshal([]byte(body), &rt)
	if rt.ID == "rt_1" {
		t.Fatal("newID must skip taken ids")
	}
	if rt.Order <= 10 {
		t.Fatalf("nextOrder must exceed existing max: %d", rt.Order)
	}
}

func TestConcurrentWrites(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	done := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() {
			code, _ := do(t, ts, "POST", "/api/v1/routes",
				fmt.Sprintf(`{"name":"c","conditions":[{"type":"domain","value":"c%d.example"}],"target":{"type":"direct"}}`, i))
			done <- code
		}()
	}
	for i := 0; i < 8; i++ {
		if c := <-done; c != 201 {
			t.Fatalf("concurrent create failed: %d", c)
		}
	}
	st := getState(t, ts)
	if len(st.Routes) != 8 {
		t.Fatalf("all 8 routes must persist, got %d", len(st.Routes))
	}
	seen := map[string]bool{}
	for _, rt := range st.Routes {
		if seen[rt.ID] {
			t.Fatalf("duplicate id %s", rt.ID)
		}
		seen[rt.ID] = true
	}
}

func TestStateEndpointAndNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()
	if code, _ := do(t, ts, "GET", "/api/v1/state", ""); code != 200 {
		t.Fatal("state must 200")
	}
	if code, _ := do(t, ts, "GET", "/api/v1/routes/rt_none", ""); code != 404 {
		t.Fatal("unknown route must 404")
	}
	if code, _ := do(t, ts, "GET", "/api/v1/servers/srv_none", ""); code != 404 {
		t.Fatal("unknown server must 404")
	}
}
