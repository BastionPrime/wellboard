package scheduler

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/subscription"
)

// countingFetcher counts rounds (one round = one fetch call per source).
type countingFetcher struct {
	calls atomic.Int32
}

func (f *countingFetcher) Fetch(ctx context.Context, url, hwidVal string, dev subscription.DeviceInfo) ([]byte, subscription.ResponseHeaders, error) {
	f.calls.Add(1)
	body := "proxies:\n  - name: NL-1\n    type: ss\n    server: 1.2.3.4\n    port: 8388\n    cipher: aes-256-gcm\n    password: x\n"
	return []byte(body), subscription.ResponseHeaders{}, nil
}

type memStore struct {
	st *model.State
}

func (s *memStore) Load() (*model.State, error) { return s.st, nil }
func (s *memStore) Save(st *model.State) error  { s.st = st; return nil }

func testState() *model.State {
	return &model.State{
		Version: 2,
		Sources: []model.Source{
			{ID: "sub_1", Kind: "subscription", Name: "S", Enabled: true, URL: "http://x/sub"},
		},
	}
}

func newUpdater(fetch subscription.Fetcher, st *model.State) *subscription.Updater {
	return &subscription.Updater{Fetcher: fetch, Store: &memStore{st}, HWID: "ABCDEFGHJKLM0123456"}
}

func TestSchedulerManualUpdateNow(t *testing.T) {
	f := &countingFetcher{}
	s := New(newUpdater(f, testState()), time.Hour)
	res, err := s.UpdateNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if res.Servers["sub_1"] != 1 {
		t.Fatalf("want 1 server, got %v", res.Servers)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("want 1 fetch, got %d", f.calls.Load())
	}
}

func TestSchedulerTickerFires(t *testing.T) {
	f := &countingFetcher{}
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	// MinInterval clamps the period to 1 minute, so the clamped test
	// drives the loop directly via round() instead of waiting.
	s := New(newUpdater(f, testState()), 50*time.Millisecond)
	s.Logger = logger

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	defer s.Stop()

	// First round runs immediately.
	deadline := time.Now().Add(2 * time.Second)
	for f.calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.calls.Load() < 1 {
		t.Fatal("first round must run immediately after Start")
	}

	// Manual rounds work while running (the API manual-update path).
	// UpdateNow is FORCED (review follow-up B2): it must fetch even right
	// after a scheduled round recorded a fresh LastUpdate.
	if _, err := s.UpdateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() < 2 {
		t.Fatalf("manual round must run, got %d calls", f.calls.Load())
	}

	// The internal tick path is exercised through round() so the test
	// does not depend on the 1-minute clamp.
	before := f.calls.Load()
	s.round(ctx)
	if f.calls.Load() != before {
		t.Fatalf("scheduled round must respect the per-source interval gate (not yet due): %d → %d",
			before, f.calls.Load())
	}
	if !strings.Contains(buf.String(), "update round ok") {
		t.Fatalf("logger must report the round: %q", buf.String())
	}
	cancel()
	s.Stop()
	s.Stop() // double stop must be safe
}

// TestSchedulerCadenceGateNotDue is the regression test for review
// follow-up B2: a source with a 12h interval and a fresh LastUpdate must
// NOT be fetched by a scheduled round (round), while the manual button
// (UpdateNow) still refreshes it immediately.
func TestSchedulerCadenceGateNotDue(t *testing.T) {
	f := &countingFetcher{}
	st := testState()
	st.Sources[0].UpdateIntervalSec = 43200 // 12h
	st.Sources[0].LastUpdate = time.Now().UTC().Format(time.RFC3339)
	s := New(newUpdater(f, st), time.Hour)

	// Three scheduled ticks in a row: zero fetches expected.
	for i := 0; i < 3; i++ {
		if _, err := s.Updater.Update(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.calls.Load(); got != 0 {
		t.Fatalf("scheduled rounds must skip a not-due source, got %d fetches", got)
	}

	// Manual update forces the refresh.
	if _, err := s.UpdateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("manual update must force a fetch, got %d", got)
	}
}

func TestSchedulerStartIdempotent(t *testing.T) {
	f := &countingFetcher{}
	s := New(newUpdater(f, testState()), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	s.Start(ctx) // second start is a no-op
	s.Stop()
}

func TestEffectiveTickClamped(t *testing.T) {
	s := New(nil, 0)
	if s.effectiveTick() != DefaultInterval {
		t.Fatalf("default tick: %v", s.effectiveTick())
	}
	s2 := New(nil, time.Second)
	if s2.effectiveTick() != MinInterval {
		t.Fatalf("tick must clamp to MinInterval, got %v", s2.effectiveTick())
	}
	s3 := New(nil, 2*time.Hour)
	if s3.effectiveTick() != 2*time.Hour {
		t.Fatalf("explicit tick must win: %v", s3.effectiveTick())
	}
}
