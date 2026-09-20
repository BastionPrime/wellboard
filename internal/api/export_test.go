// Phase 7 tests: state export/import (FR-6.5) and the HWID-never-leaks
// guarantee. Uses seedState from api_coverage_test.go (src_manual,
// srv_1/srv_2, grp_1/grp_2, rt_1).

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/hwid"
	"github.com/wellboard/wellboard/internal/store"
)

// TestExportImportRoundTrip: export → wipe store → import → the state
// comes back identical (sources/servers/groups/routes).
func TestExportImportRoundTrip(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)

	// 1. Export.
	code, body := do(t, ts, "GET", "/api/v1/export", "")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, body)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("export body not JSON: %v", err)
	}
	if f, _ := doc["format"].(float64); int(f) != 1 {
		t.Fatalf("export format = %v, want 1", doc["format"])
	}
	if app, _ := doc["app"].(string); app != "wellboard" {
		t.Fatalf("export app = %q", app)
	}
	// Content-Disposition: it is a backup download.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/export", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "wellboard-state.json") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	resp.Body.Close()

	// 2. Wipe the store to a bare default (a value we can detect).
	fresh := store.DefaultState()
	fresh.Settings.UIPort = 9999
	if err := srv.Store.Save(fresh); err != nil {
		t.Fatal(err)
	}

	// 3. Import the exported document verbatim.
	code, body = do(t, ts, "POST", "/api/v1/import", body)
	if code != http.StatusOK {
		t.Fatalf("import: %d %s", code, body)
	}

	// 4. The state is back via the normal read path.
	st := getState(t, ts)
	if len(st.Sources) != 2 || st.Sources[0].ID != "src_manual" {
		t.Fatalf("sources after import: %+v", st.Sources)
	}
	if len(st.Servers) != 2 || st.Servers[0].ID != "srv_1" {
		t.Fatalf("servers after import: %+v", st.Servers)
	}
	if len(st.Groups) != 2 || st.Groups[0].ID != "grp_1" || st.Groups[0].Members[0] != "srv_1" {
		t.Fatalf("groups after import: %+v", st.Groups)
	}
	if len(st.Routes) != 1 || st.Routes[0].ID != "rt_1" {
		t.Fatalf("routes after import: %+v", st.Routes)
	}
	if st.Settings.UIPort == 9999 {
		t.Fatalf("settings were not replaced by the import (ui_port=%d)", st.Settings.UIPort)
	}
}

// TestExportDoesNotContainHWID: generate a real HWID in the state dir
// behind the server, export, and assert the HWID + salt strings are
// absent from the document (FR-6.5).
func TestExportDoesNotContainHWID(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)

	st, ok := srv.Store.(*store.Store)
	if !ok {
		t.Fatalf("expected *store.Store, got %T", srv.Store)
	}
	mgr := hwid.NewManager(st.Root, false)
	mgr.MAC = "aa:bb:cc:dd:ee:ff" // deterministic in tests
	id, err := mgr.Generate()
	if err != nil {
		t.Fatal(err)
	}

	code, body := do(t, ts, "GET", "/api/v1/export", "")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, body)
	}
	if strings.Contains(body, id.HWID) {
		t.Fatalf("export contains the HWID %q", id.HWID)
	}
	if strings.Contains(body, id.Salt) {
		t.Fatalf("export contains the HWID salt %q", id.Salt)
	}
	if strings.Contains(strings.ToLower(body), "hwid") {
		t.Fatalf("export mentions hwid: %q", body)
	}
}

// TestImportValidation: failure modes must not touch the state.
func TestImportValidation(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)

	before := getState(t, ts)

	cases := []struct {
		name string
		body string
		code int
		want string
	}{
		{"no state", `{"format":1,"app":"wellboard","version":2}`, 400, "state is required"},
		{"wrong format", `{"format":99,"app":"wellboard","version":2,"state":{"version":2}}`, 400, "format"},
		{"missing format", `{"app":"wellboard","version":2,"state":{"version":2}}`, 400, "format"},
		{"foreign app", `{"format":1,"app":"other","version":2,"state":{"version":2}}`, 400, "not exported by wellboard"},
		{"too new version", `{"format":1,"app":"wellboard","version":99,"state":{"version":99}}`, 400, "version"},
		{"unknown field", `{"format":1,"app":"wellboard","version":2,"state":{"version":2},"extra":1}`, 400, "unknown field"},
		{"broken refs", `{"format":1,"app":"wellboard","version":2,"state":{"version":2,"routes":[{"id":"rt_9","name":"x","enabled":true,"order":10,"conditions":[{"type":"domain","value":"a.com"}],"target":{"type":"group","id":"grp_missing"},"on_unavailable":"block"}]}}`, 409, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := do(t, ts, "POST", "/api/v1/import", tc.body)
			if code != tc.code {
				t.Fatalf("import %s: status %d (want %d), body %s", tc.name, code, tc.code, body)
			}
			if tc.want != "" && !strings.Contains(body, tc.want) {
				t.Fatalf("import %s: body %q lacks %q", tc.name, body, tc.want)
			}
			// State untouched.
			after := getState(t, ts)
			if len(after.Servers) != len(before.Servers) || len(after.Routes) != len(before.Routes) {
				t.Fatalf("import %s mutated state: %+v", tc.name, after)
			}
		})
	}
}

// TestImportOversizedBody: >1 MiB import bodies are rejected (input
// size limit, the same 1 MiB cap as every other JSON endpoint).
func TestImportOversizedBody(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)

	pad := strings.Repeat("a", 1<<20)
	code, body := do(t, ts, "POST", "/api/v1/import", `{"pad":"`+pad+`"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("oversized import: %d %s", code, body)
	}
}

// TestRouteConditionRejectsCommaColon: rule-line injection guard at
// the API layer (security audit Phase 7).
func TestRouteConditionRejectsCommaColon(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, srv)

	for _, val := range []string{"a,b", "x:y"} {
		payload := `{"name":"bad","conditions":[{"type":"domain","value":"` + val + `"}],"target":{"type":"direct"}}`
		code, body := do(t, ts, "POST", "/api/v1/routes", payload)
		if code != http.StatusBadRequest {
			t.Fatalf("condition %q: status %d, body %s", val, code, body)
		}
		if !strings.Contains(body, "must not contain") {
			t.Fatalf("condition %q: body %s", val, body)
		}
	}
}
