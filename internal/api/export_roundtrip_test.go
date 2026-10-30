// Export/import round-trip property test (FR-6.5). This is the property
// lock for the backup contract: whatever the user has — sources, servers,
// groups, routes, LAN devices, settings — export → import into a fresh
// store returns byte-equivalent state, and the export document never
// contains the HWID or its salt (they live in a separate file, so no
// *code* is supposed to copy them into the export; this test fails if
// anyone ever adds it, e.g. by putting hwid fields into model.State).
//
// Two entry points are locked:
//  1. the API path (GET /api/v1/export → POST /api/v1/import) — what
//     the UI and a migration to another router use;
//  2. the store-level path (Save on store A → Save on store B) — what
//     the round-trip is ultimately built on, including the
//     load-with-migration edge for documents written by older builds.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/hwid"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/store"
)

// osWriteFile is a thin wrapper so the file plumbing in the store-level
// tests reads as one line each.
func osWriteFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}

// roundTripFixture is the full-surface state: every collection the
// export claims to carry (sources/servers/groups/routes/lan_devices/
// settings) with at least two entries per collection, non-default
// settings, and nested group references. The variant without LAN
// devices and geodata extras is used for the API round-trip (the
// LAN reader is a runtime object, not state, and the API surface only
// exposes it through its own endpoints).
func roundTripFixture() *model.State {
	legacyErr := "subscription fetch failed"
	announce := "panel upgraded to v2"
	st := store.DefaultState()
	st.Settings = model.Settings{
		UIPort:               8091,
		Lang:                 "en",
		GeositeSource:        "metacubex",
		GeoipSource:          "runetfreedom",
		DefaultPolicy:        model.Target{Type: model.TargetDirect},
		DelayTestIntervalSec: 600,
		DisabledTemplates:    []string{"games"},
	}
	st.LANDevices = []model.LANDevice{
		{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.1.50", Hostname: "tv", Static: true},
		{MAC: "aa:bb:cc:dd:ee:02", IP: "192.168.1.51", Hostname: "laptop"},
	}
	st.Sources = []model.Source{
		{ID: "src_manual", Kind: "manual", Name: "Manual"},
		{
			ID: "src_sub", Kind: "subscription", Name: "Sub", Enabled: true,
			URL: "https://panel.example/sub", UpdateIntervalSec: 3600,
			LastUpdate: "2026-10-01T10:00:00Z", LastError: &legacyErr,
			Announce: &announce,
			UserInfo: &model.UserInfo{
				Upload: 1 << 20, Download: 10 << 20, Total: 100 << 20, Expire: 1800000000,
			},
			HWIDStatus: &model.HWIDStatus{Active: true},
		},
	}
	st.Servers = []model.Server{
		{ID: "srv_1", SourceID: "src_manual", Name: "NL-1", Type: "ss", DelayMS: 120,
			Raw: map[string]any{"server": "203.0.113.10", "port": 8388, "cipher": "aes-256-gcm", "password": "pw"}},
		{ID: "srv_2", SourceID: "src_sub", Name: "NL-2", Type: "ss", DelayMS: 95, Stale: true, StaleMisses: 1,
			Raw: map[string]any{"server": "203.0.113.11", "port": 8443, "cipher": "x", "password": "y"}},
	}
	st.Groups = []model.Group{
		{ID: "grp_1", Name: "Outer", Type: model.GroupSelect, Members: []string{"srv_1", "grp_2"}},
		{ID: "grp_2", Name: "Inner", Type: model.GroupURLTest, Members: []string{"srv_2"}},
	}
	st.Routes = []model.Route{
		{
			ID: "rt_1", Name: "TV to VPN", Enabled: true, Order: 10,
			Conditions: []model.RouteCondition{
				{Type: model.CondSrcDevice, Value: "192.168.1.50/32", Label: "tv"},
			},
			Target:        model.Target{Type: model.TargetServer, ID: "srv_1"},
			OnUnavailable: "direct",
		},
		{
			ID: "rt_2", Name: "Block ads", Enabled: true, Order: 20,
			Conditions:    []model.RouteCondition{{Type: model.CondDomainSuffix, Value: "ads.example"}},
			Target:        model.Target{Type: model.TargetReject},
			OnUnavailable: "block",
		},
	}
	return st
}

// assertStateDeepEqual compares two states collection by collection
// (Sources, Servers, Groups, Routes, LANDevices, Settings) with a
// per-item message, so a regression prints WHICH part of the round-trip
// broke instead of one opaque reflect.DeepEqual dump.
//
// Servers are compared through canonical JSON: Raw is a
// map[string]any, and a JSON round-trip turns `port: 8388` (int) into
// float64(8388) — semantically equal, structurally different. Canonical
// re-marshal of both sides makes the comparison exact for everything
// that survives the document encoding (the export/import contract).
func assertStateDeepEqual(t *testing.T, want, got *model.State) {
	t.Helper()
	if want.Version != got.Version {
		t.Errorf("Version: want %d, got %d", want.Version, got.Version)
	}
	cmpJSON := func(field string, w, g any) {
		wb, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		gb, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		if string(wb) != string(gb) {
			t.Errorf("%s: want %s, got %s", field, wb, gb)
		}
	}
	if len(want.Sources) != len(got.Sources) {
		t.Errorf("Sources: want %d, got %d", len(want.Sources), len(got.Sources))
	} else if !reflect.DeepEqual(want.Sources, got.Sources) {
		for i := range want.Sources {
			if !reflect.DeepEqual(want.Sources[i], got.Sources[i]) {
				t.Errorf("Sources[%d]: want %+v, got %+v", i, want.Sources[i], got.Sources[i])
			}
		}
	}
	if len(want.Servers) != len(got.Servers) {
		t.Errorf("Servers: want %d, got %d", len(want.Servers), len(got.Servers))
	} else {
		for i := range want.Servers {
			if !reflect.DeepEqual(want.Servers[i], got.Servers[i]) {
				// Raw map differs structurally (int vs float64 after
				// JSON): compare the canonical document form.
				cmpJSON(fmt.Sprintf("Servers[%d]", i), want.Servers[i], got.Servers[i])
			}
		}
	}
	if !reflect.DeepEqual(want.Groups, got.Groups) {
		t.Errorf("Groups: want %+v, got %+v", want.Groups, got.Groups)
	}
	if !reflect.DeepEqual(want.Routes, got.Routes) {
		t.Errorf("Routes: want %+v, got %+v", want.Routes, got.Routes)
	}
	if !reflect.DeepEqual(want.LANDevices, got.LANDevices) {
		t.Errorf("LANDevices: want %+v, got %+v", want.LANDevices, got.LANDevices)
	}
	if !reflect.DeepEqual(want.Settings, got.Settings) {
		t.Errorf("Settings: want %+v, got %+v", want.Settings, got.Settings)
	}
}

// TestStoreRoundTripFreshDirectory is the store-level property: the same
// state written to two independent stores is read back identical. This
// is what "backup and restore on another router" reduces to once the
// document is decoded, including the LAN-device and settings fields the
// API fixture cannot set through requests.
func TestStoreRoundTripFreshDirectory(t *testing.T) {
	want := roundTripFixture()

	dirA := t.TempDir()
	a := store.New(dirA, false)
	if err := a.Save(want); err != nil {
		t.Fatal(err)
	}

	// A second, empty store (the "another router" case).
	b := store.New(t.TempDir(), false)
	got, err := b.Load() // no file → defaults, then import:
	if err != nil {
		t.Fatal(err)
	}
	*got = *want
	if err := b.Save(got); err != nil {
		t.Fatal(err)
	}

	back, err := b.Load()
	if err != nil {
		t.Fatal(err)
	}
	assertStateDeepEqual(t, want, back)
}

// TestStoreRoundTripFullPipeline runs the store path through a real
// JSON document: save → read the raw state.json bytes (what an export
// document embeds) → write them into a fresh directory → Load. The
// bytes here are a CURRENT-version document, so no migration applies;
// the older-schema path is covered separately below.
func TestStoreRoundTripFullPipeline(t *testing.T) {
	want := roundTripFixture()

	a := store.New(t.TempDir(), false)
	if err := a.Save(want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatal(err)
	}

	dirB := t.TempDir()
	b := store.New(dirB, false)
	if err := osWriteFile(b.Path(), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := b.Load()
	if err != nil {
		t.Fatal(err)
	}
	assertStateDeepEqual(t, want, got)
}

// TestImportOlderSchemaDocument locks the versioned-import edge case:
// a state document produced by an OLDER build (schema v1 and v2) must
// import without error and come out at the CURRENT schema. v1 lacks the
// StaleMisses counter (store migration seeds it for stale servers);
// v2 still carries the single legacy "geodata" settings field (strict
// decode rejects it — this test documents that behavior so a change to
// it is a conscious one, not an accident).
func TestImportOlderSchemaDocument(t *testing.T) {
	t.Run("v1 document rejected without migration", func(t *testing.T) {
		v1 := `{
			"version": 1,
			"settings": {"ui_port": 8090, "lang": "ru", "default_policy": {"type": "direct"}, "delay_test_interval_sec": 300},
			"sources": [{"id": "sub_1", "kind": "subscription", "name": "S"}],
			"servers": [
				{"id": "srv_a", "source_id": "sub_1", "name": "A", "type": "ss", "stale": true, "raw": {"server": "1.1.1.1", "port": 1}},
				{"id": "srv_b", "source_id": "sub_1", "name": "B", "type": "ss", "raw": {"server": "2.2.2.2", "port": 2}}
			]
		}`
		// Envelope says version 1 (an old export); the embedded state is v1.
		doc := fmt.Sprintf(`{"format":1,"app":"wellboard","version":1,"state":%s}`, v1)

		_, ts := newTestServer(t)
		defer ts.Close()

		// The API import accepts the version=1 envelope (the version
		// check only rejects NEWER schemas), but does NOT run store
		// migrations: the embedded v1 state reaches the generator with
		// empty geosite/geoip sources and is rejected 409 as an
		// unresolved reference. Locked deliberately: older-schema
		// documents must be migrated BEFORE import (store.Load over
		// the exported bytes does exactly that). If import starts
		// accepting v1 documents, the contract changed — update this
		// test AND the import docs together.
		code, body := do(t, ts, "POST", "/api/v1/import", doc)
		if code != http.StatusConflict {
			t.Fatalf("import of a v1 document: want 409 (no migration on import), got %d: %s", code, body)
		}
		if !strings.Contains(body, "geosite_source") && !strings.Contains(body, "unresolved") {
			t.Fatalf("v1 rejection must name the unresolved reference, body: %s", body)
		}
	})

	t.Run("v2 document with legacy geodata field", func(t *testing.T) {
		v2 := `{
			"version": 2,
			"settings": {"ui_port": 8090, "lang": "ru", "geodata": "metacubex", "default_policy": {"type": "direct"}, "delay_test_interval_sec": 300},
			"sources": [{"id": "sub_1", "kind": "subscription", "name": "S"}],
			"servers": []
		}`
		doc := fmt.Sprintf(`{"format":1,"app":"wellboard","version":2,"state":%s}`, v2)

		_, ts := newTestServer(t)
		defer ts.Close()

		// Current behavior, locked deliberately: the API import decodes
		// strictly and does NOT run store migrations, so the legacy
		// geodata field is rejected with 400 "unknown field". A schema
		// older than CurrentVersion must therefore be migrated BEFORE
		// handing the document to POST /import (store.Load on the
		// exported bytes does exactly that). If this test starts
		// failing because import accepted the field, the contract
		// changed — update the test AND the import docs together.
		code, body := do(t, ts, "POST", "/api/v1/import", doc)
		if code != http.StatusBadRequest {
			t.Fatalf("v2 document with legacy geodata: want 400 (strict decode, no migration on import), got %d: %s", code, body)
		}
		if !strings.Contains(body, "unknown field") && !strings.Contains(body, "geodata") {
			t.Fatalf("v2 rejection must name the unknown field, body: %s", body)
		}
	})
}

// TestExportImportRoundTripAllCollections is the API-level property
// test: build the state through the real HTTP surface (create sources,
// servers via the manual bucket, groups, routes, patch settings), then
// export it, import it into a store that was wiped to a detectably
// different state, and require deep equality through the read path.
func TestExportImportRoundTripAllCollections(t *testing.T) {
	srv, ts := newTestServer(t)
	defer ts.Close()

	// Settings: non-default values.
	if code, body := do(t, ts, "PATCH", "/api/v1/settings",
		`{"ui_port":8091,"lang":"en","geosite_source":"metacubex","delay_test_interval_sec":600}`); code != 200 {
		t.Fatalf("settings patch: %d %s", code, body)
	}
	// Sources: one subscription, plus the manual bucket.
	if code, body := do(t, ts, "POST", "/api/v1/sources",
		`{"kind":"subscription","name":"Sub","url":"https://panel.example/sub"}`); code != 201 {
		t.Fatalf("source create: %d %s", code, body)
	}
	sub := struct{ ID string }{}
	code, body := do(t, ts, "GET", "/api/v1/sources", "")
	if code != 200 {
		t.Fatalf("sources list: %d %s", code, body)
	}
	var list struct {
		Sources []model.Source `json:"sources"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	for _, s := range list.Sources {
		if s.Kind == "subscription" {
			sub.ID = s.ID
		}
	}
	if sub.ID == "" {
		t.Fatal("no subscription source created")
	}

	// Servers into the manual bucket (POST /servers with share links,
	// FR-1.3; names come from the link fragments). Both links in ONE
	// request: each merge treats servers absent from the answer as
	// vanished, so two separate calls would mark the first server
	// stale on the second call.
	bothLinks := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@203.0.113.10:8388#NL-1\nss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@203.0.113.11:8443#NL-2"
	if code, body := do(t, ts, "POST", "/api/v1/servers", fmt.Sprintf(`{"links":%q}`, bothLinks)); code != 201 {
		t.Fatalf("server create: %d %s", code, body)
	}
	var srvList struct {
		Servers []struct {
			ID string `json:"id"`
		} `json:"servers"`
	}
	code, body = do(t, ts, "GET", "/api/v1/servers", "")
	if code != 200 || json.Unmarshal([]byte(body), &srvList) != nil {
		t.Fatalf("servers list: %d %s", code, body)
	}
	if len(srvList.Servers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(srvList.Servers))
	}
	srvID1, srvID2 := srvList.Servers[0].ID, srvList.Servers[1].ID

	// Groups: outer references the inner group; inner references a server.
	inner := fmt.Sprintf(`{"name":"Inner","type":"url-test","members":[%q]}`, srvID2)
	code, body = do(t, ts, "POST", "/api/v1/groups", inner)
	if code != 201 {
		t.Fatalf("inner group: %d %s", code, body)
	}
	var grp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &grp); err != nil {
		t.Fatal(err)
	}
	outer := fmt.Sprintf(`{"name":"Outer","type":"select","members":[%q,%q]}`, srvID1, grp.ID)
	code, body = do(t, ts, "POST", "/api/v1/groups", outer)
	if code != 201 {
		t.Fatalf("outer group: %d %s", code, body)
	}

	// Routes.
	route1 := fmt.Sprintf(`{"name":"VPN default","conditions":[{"type":"domain-suffix","value":"example.com"}],"target":{"type":"server","id":%q},"on_unavailable":"direct"}`, srvID1)
	code, body = do(t, ts, "POST", "/api/v1/routes", route1)
	if code != 201 {
		t.Fatalf("route 1: %d %s", code, body)
	}
	route2 := `{"name":"Block ads","conditions":[{"type":"domain-suffix","value":"ads.example"}],"target":{"type":"reject"},"providers":["ads"]}`
	code, body = do(t, ts, "POST", "/api/v1/routes", route2)
	if code != 201 {
		t.Fatalf("route 2: %d %s", code, body)
	}

	// Snapshot the pre-export state (the round-trip must return to it).
	before := getState(t, ts)

	// HWID exists on disk in the SAME state directory: the export must
	// not contain it even though the device is identified.
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

	// Export.
	code, exportBody := do(t, ts, "GET", "/api/v1/export", "")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, exportBody)
	}
	var doc struct {
		Format  int         `json:"format"`
		App     string      `json:"app"`
		Version int         `json:"version"`
		State   model.State `json:"state"`
	}
	if err := json.Unmarshal([]byte(exportBody), &doc); err != nil {
		t.Fatalf("export body not JSON: %v", err)
	}
	if doc.Format != 1 || doc.App != "wellboard" {
		t.Fatalf("export envelope: %+v", doc)
	}

	// FR-6.5: the HWID and its salt must be absent from the export
	// bytes — the invariant this test exists to guard. Fails loudly if
	// someone ever adds them to the export document.
	if strings.Contains(exportBody, id.HWID) {
		t.Fatalf("export contains the HWID %q", id.HWID)
	}
	if strings.Contains(exportBody, id.Salt) {
		t.Fatalf("export contains the HWID salt %q", id.Salt)
	}
	if strings.Contains(strings.ToLower(exportBody), "hwid_salt") {
		t.Fatalf("export mentions hwid salt: %q", exportBody)
	}
	// The MAC the HWID was derived from is not part of state either.
	if strings.Contains(exportBody, "aa:bb:cc:dd:ee:ff") {
		t.Fatalf("export contains the HWID source MAC: %q", exportBody)
	}

	// Wipe the store to a bare default with a detectable marker.
	fresh := store.DefaultState()
	fresh.Settings.UIPort = 9999
	if err := srv.Store.Save(fresh); err != nil {
		t.Fatal(err)
	}

	// Import the exported document verbatim.
	code, body = do(t, ts, "POST", "/api/v1/import", exportBody)
	if code != http.StatusOK {
		t.Fatalf("import: %d %s", code, body)
	}

	// The state is back, deep-equal to before the export.
	after := getState(t, ts)
	assertStateDeepEqual(t, before, after)
	if after.Settings.UIPort == 9999 {
		t.Fatalf("settings were not replaced by the import (ui_port=%d)", after.Settings.UIPort)
	}
	if after.Settings.GeositeSource != "metacubex" || after.Settings.Lang != "en" {
		t.Fatalf("settings did not round-trip: %+v", after.Settings)
	}

	// The import response reports counts consistent with the state.
	var imp struct {
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal([]byte(body), &imp); err != nil {
		t.Fatal(err)
	}
	if imp.Counts["sources"] != len(after.Sources) || imp.Counts["servers"] != len(after.Servers) ||
		imp.Counts["groups"] != len(after.Groups) || imp.Counts["routes"] != len(after.Routes) {
		t.Fatalf("import counts %v vs state: %d/%d/%d/%d", imp.Counts,
			len(after.Sources), len(after.Servers), len(after.Groups), len(after.Routes))
	}
}

// TestExportImportRoundTripStoreCopy locks the migration use case from
// the ticket matrix: the SAME document imported into a DIFFERENT
// physical store (another router / a reinstalled system), where the
// target starts from defaults, not from the exported state. Deep
// equality is asserted against the exported state, not merely against
// itself.
func TestExportImportRoundTripStoreCopy(t *testing.T) {
	src, ts := newTestServer(t)
	defer ts.Close()
	seedState(t, src) // reuse the standard fixture (src_manual, srv_1/srv_2, grp_1/grp_2, rt_1)

	code, exportBody := do(t, ts, "GET", "/api/v1/export", "")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, exportBody)
	}

	// A second server with its own (empty) store: the migration target.
	_, ts2 := newTestServer(t)
	defer ts2.Close()

	code, body := do(t, ts2, "POST", "/api/v1/import", exportBody)
	if code != http.StatusOK {
		t.Fatalf("cross-store import: %d %s", code, body)
	}

	want := getState(t, ts) // read through the SOURCE server
	got := getState(t, ts2) // read through the TARGET server
	assertStateDeepEqual(t, want, got)
}
