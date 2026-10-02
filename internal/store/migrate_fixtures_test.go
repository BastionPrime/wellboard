package store

// Fixture-driven migration tests for export formats of the tagged v1.0.0
// and v1.0.1 releases (both wrote state schema v2 with the single legacy
// settings.geodata field). The fixtures under testdata/ are byte-shaped
// like real state.json files of those releases so importing an old
// backup keeps working. Each fixture is tagged with the release that
// produced it; fields missing from the old schema are filled with
// defaults by migrate (documented per test below).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
)

// legacyFixtures maps each tagged-release fixture to the settings.geodata
// value it carries ("" means the old writer never set it).
var legacyFixtures = map[string]struct {
	path         string
	legacyGeodat string
}{
	"v1.0.0": {path: "testdata/state-v1.0.0.json", legacyGeodat: "metacubex"},
	"v1.0.1": {path: "testdata/state-v1.0.1.json", legacyGeodat: ""},
}

// TestMigrateLegacyFixtures loads each tagged v1.0.x fixture through
// Store.Load and asserts the forward migration to the current schema:
//
//   - version: 2 → CurrentVersion;
//   - settings.geodata (legacy single source) is split into
//     geosite_source + geoip_source, an empty value defaulting to
//     runetfreedom (decision Q7);
//   - v2 fields the old export did not carry (geodata custom URLs,
//     geodata additions, disabled templates) are zero-value defaults —
//     nothing to backfill, they only appear on save if set;
//   - servers: stale_misses already exists in the v2 schema the tagged
//     releases wrote, so it loads as recorded (srv_2: 2) — no seeding
//     applies, the v1→v2 seeding only concerns true Phase-1 files;
//   - all other sections (sources, groups, routes, lan_devices) load
//     as-is: their v2 shape is unchanged in v3.
func TestMigrateLegacyFixtures(t *testing.T) {
	for tag, fx := range legacyFixtures {
		t.Run(tag, func(t *testing.T) {
			data, err := os.ReadFile(fx.path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			st, err := New(dir, false).Load()
			if err != nil {
				t.Fatalf("load %s fixture: %v", tag, err)
			}

			if st.Version != CurrentVersion {
				t.Fatalf("%s: version must migrate to %d, got %d", tag, CurrentVersion, st.Version)
			}

			// Legacy geodata split. Empty → runetfreedom default.
			want := fx.legacyGeodat
			if want == "" {
				want = "runetfreedom"
			}
			if st.Settings.GeositeSource != want || st.Settings.GeoipSource != want {
				t.Fatalf("%s: legacy geodata %q must split into %q/%q, got %q/%q",
					tag, fx.legacyGeodat, want, want,
					st.Settings.GeositeSource, st.Settings.GeoipSource)
			}

			// v3-only settings absent from the old exports stay at
			// their zero values (nothing is invented for the user).
			if st.Settings.GeositeCustomURL != "" || st.Settings.GeoipCustomURL != "" ||
				len(st.Settings.GeodataAdditions) != 0 || len(st.Settings.DisabledTemplates) != 0 {
				t.Fatalf("%s: v3-only settings must default to zero values, got %+v",
					tag, st.Settings)
			}

			// Server values: stale_misses is already part of the v2
			// schema the tagged releases wrote — it must survive as
			// recorded, no re-seeding.
			for _, srv := range st.Servers {
				if srv.Stale && srv.StaleMisses == 0 {
					t.Fatalf("%s: stale server %s must keep its recorded stale_misses, got 0",
						tag, srv.ID)
				}
			}
		})
	}
}

// TestMigrateLegacyFixturesRoundTrip saves each migrated fixture state
// forward and re-loads it: the legacy settings.geodata field must be gone
// from the file, the split sources persist, and the migrated values
// survive the round trip untouched.
func TestMigrateLegacyFixturesRoundTrip(t *testing.T) {
	for tag, fx := range legacyFixtures {
		t.Run(tag, func(t *testing.T) {
			data, err := os.ReadFile(fx.path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			s := New(dir, false)
			st, err := s.Load()
			if err != nil {
				t.Fatalf("load %s fixture: %v", tag, err)
			}
			if err := s.Save(st); err != nil {
				t.Fatalf("save %s migrated state: %v", tag, err)
			}

			saved, err := os.ReadFile(filepath.Join(dir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			got := string(saved)
			if strings.Contains(got, `"geodata"`) {
				t.Fatalf("%s: legacy geodata field must be dropped on save:\n%s", tag, got)
			}
			want := fx.legacyGeodat
			if want == "" {
				want = "runetfreedom"
			}
			if !strings.Contains(got, `"geosite_source": "`+want+`"`) ||
				!strings.Contains(got, `"geoip_source": "`+want+`"`) {
				t.Fatalf("%s: split sources %q must persist:\n%s", tag, want, got)
			}

			// Reload: still valid, still the current version.
			again, err := s.Load()
			if err != nil {
				t.Fatalf("reload %s migrated state: %v", tag, err)
			}
			if again.Version != CurrentVersion {
				t.Fatalf("%s: reloaded version must stay %d, got %d", tag, CurrentVersion, again.Version)
			}
		})
	}
}

// TestLegacyFixtureDataSurvives checks the non-settings sections of the
// v1.0.0 fixture survive migration unchanged: the subscription source
// with its lifecycle fields, servers with raw proxy maps, the group, the
// route and the LAN device. Old exports must not lose data on import.
func TestLegacyFixtureDataSurvives(t *testing.T) {
	data, err := os.ReadFile("testdata/state-v1.0.0.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := New(dir, false).Load()
	if err != nil {
		t.Fatal(err)
	}

	if len(st.Sources) != 1 || st.Sources[0].ID != "sub_1" {
		t.Fatalf("source must survive: %+v", st.Sources)
	}
	src := st.Sources[0]
	if src.UserInfo == nil || src.UserInfo.Total != 10737418240 || src.UserInfo.Expire != 1798761600 {
		t.Fatalf("userinfo must survive: %+v", src.UserInfo)
	}
	if src.HWIDStatus == nil || !src.HWIDStatus.Active || src.HWIDStatus.LimitReached {
		t.Fatalf("hwid_status must survive: %+v", src.HWIDStatus)
	}

	if len(st.Servers) != 2 {
		t.Fatalf("both servers must survive, got %d", len(st.Servers))
	}
	if st.Servers[1].StaleMisses != 2 {
		t.Fatalf("stale_misses must round-trip as recorded (2), got %d", st.Servers[1].StaleMisses)
	}
	if st.Servers[0].DelayMS != 42 {
		t.Fatalf("delay_ms must survive, got %d", st.Servers[0].DelayMS)
	}
	if raw, _ := st.Servers[0].Raw["cipher"].(string); raw != "aes-128-gcm" {
		t.Fatalf("raw proxy map must survive: %v", st.Servers[0].Raw)
	}

	if len(st.Groups) != 1 || len(st.Groups[0].Members) != 2 {
		t.Fatalf("group members must survive: %+v", st.Groups)
	}
	if len(st.Routes) != 1 || st.Routes[0].OnUnavailable != "block" {
		t.Fatalf("route must survive: %+v", st.Routes)
	}
	if len(st.LANDevices) != 1 || !st.LANDevices[0].Static {
		t.Fatalf("lan device must survive: %+v", st.LANDevices)
	}
}

// TestLegacyFixtureManualSource: the v1.0.1 fixture carries the built-in
// manual source with no servers; after migration the manual bucket stays
// the only source and the state is usable (an empty pool is valid).
func TestLegacyFixtureManualSource(t *testing.T) {
	data, err := os.ReadFile("testdata/state-v1.0.1.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := New(dir, false).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Sources) != 1 || st.Sources[0].Kind != string(model.SourceManual) {
		t.Fatalf("manual source must survive: %+v", st.Sources)
	}
	if len(st.Servers) != 0 || len(st.Groups) != 0 || len(st.Routes) != 0 {
		t.Fatalf("empty pool must stay empty: %+v", st)
	}
}
