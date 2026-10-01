package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
)

// TestMigrateV1ToV2 checks the Phase 2 migration: a v1 state with a stale
// server gets StaleMisses=1; non-stale servers stay 0; version bumps.
func TestMigrateV1ToV2(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, false)

	v1 := &model.State{
		Version: 1,
		Sources: []model.Source{{ID: "sub_1", Kind: "subscription", Name: "S"}},
		Servers: []model.Server{
			{ID: "srv_a", SourceID: "sub_1", Name: "A", Stale: true,
				Raw: map[string]any{"server": "1.1.1.1", "port": 1}},
			{ID: "srv_b", SourceID: "sub_1", Name: "B", Stale: false,
				Raw: map[string]any{"server": "2.2.2.2", "port": 2}},
		},
	}
	data, _ := json.Marshal(v1)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != CurrentVersion {
		t.Fatalf("version must migrate to %d, got %d", CurrentVersion, st.Version)
	}
	if st.Servers[0].StaleMisses != 1 {
		t.Fatalf("stale v1 server must get misses=1, got %d", st.Servers[0].StaleMisses)
	}
	if st.Servers[1].StaleMisses != 0 {
		t.Fatalf("fresh server must stay 0, got %d", st.Servers[1].StaleMisses)
	}
}

// TestMigrateV2ToV3 (OPE-3045 B2): the legacy single geodata value is
// split into geosite_source + geoip_source; the old field disappears.
func TestMigrateV2ToV3(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, false)
	// A v2 file with the legacy geodata field.
	v2 := `{
		"version": 2,
		"settings": {
			"ui_port": 8090,
			"lang": "ru",
			"geodata": "metacubex",
			"default_policy": {"type": "direct"},
			"delay_test_interval_sec": 300
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != 3 {
		t.Fatalf("version must migrate to 3, got %d", st.Version)
	}
	if st.Settings.GeositeSource != "metacubex" || st.Settings.GeoipSource != "metacubex" {
		t.Fatalf("legacy geodata must split into both sources: %q/%q",
			st.Settings.GeositeSource, st.Settings.GeoipSource)
	}
	// Saved forward: the legacy field is gone, the split ones stay.
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if strings.Contains(string(data), `"geodata"`) {
		t.Fatalf("legacy geodata field must be dropped on save:\n%s", data)
	}
	if !strings.Contains(string(data), `"geosite_source": "metacubex"`) ||
		!strings.Contains(string(data), `"geoip_source": "metacubex"`) {
		t.Fatalf("split sources must persist:\n%s", data)
	}
}

// TestMigrateV2EmptyGeodata: an empty legacy value falls back to the
// runetfreedom default (decision Q7).
func TestMigrateV2EmptyGeodata(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, false)
	v2 := `{"version":2,"settings":{"ui_port":8090,"lang":"ru","default_policy":{"type":"direct"}}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Settings.GeositeSource != "runetfreedom" || st.Settings.GeoipSource != "runetfreedom" {
		t.Fatalf("empty legacy geodata must default to runetfreedom: %q/%q",
			st.Settings.GeositeSource, st.Settings.GeoipSource)
	}
}

// TestMigrateV3Unchanged: a v3 file with split sources loads as-is.
func TestMigrateV3Unchanged(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, false)
	v3 := `{"version":3,"settings":{"ui_port":8090,"lang":"ru","geosite_source":"metacubex","geoip_source":"custom","geoip_custom_url":"https://example.invalid/g.dat","default_policy":{"type":"direct"}}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(v3), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Settings.GeositeSource != "metacubex" || st.Settings.GeoipSource != "custom" ||
		st.Settings.GeoipCustomURL != "https://example.invalid/g.dat" {
		t.Fatalf("v3 state must load unchanged: %+v", st.Settings)
	}
}

// TestV2StateRoundTrip ensures a v2 state loads without migration side
// effects.
func TestV2StateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, false)
	st := DefaultState()
	st.Servers = []model.Server{
		{ID: "srv_x", SourceID: "sub_1", Name: "X", Stale: true, StaleMisses: 2,
			Raw: map[string]any{"server": "1.1.1.1", "port": 1}},
	}
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Servers[0].StaleMisses != 2 {
		t.Fatalf("stale_misses must round-trip, got %d", got.Servers[0].StaleMisses)
	}
	if got.Version != CurrentVersion {
		t.Fatalf("version must be %d, got %d", CurrentVersion, got.Version)
	}
}
