package store

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	if got.Version != 2 {
		t.Fatalf("version must be 2, got %d", got.Version)
	}
}
