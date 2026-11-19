package scheduler

// Fake-clock tests for the scheduler's time behavior: tick drift /
// skipped ticks, fetch-failure backoff growth and reset, and interval
// changes taking effect without a restart.
//
// Design note: the scheduler itself has no injectable clock — its
// ticker is real (MinInterval 1m) — so these tests drive the same code
// path the loop runs per tick (Scheduler.round → Updater.Update) and
// inject time through Updater.Now (the fake clock the updater already
// exposes). That covers the due/overdue arithmetic, which is what
// drift and skipped ticks actually change. The ticker-period behavior
// itself is covered by TestSchedulerTickerFires and
// TestEffectiveTickClamped in scheduler_test.go.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/subscription"
)

// fakeClock is a controllable time source for Updater.Now.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// stampingFetcher records the (fake) time of each fetch and can fail
// on demand (fetch-error backoff test). Fetch is called synchronously
// from Updater.update, so it can safely read the clock passed to the
// round helper below.
type stampingFetcher struct {
	mu   sync.Mutex
	nowC *fakeClock
	st   []time.Time
	fail bool
}

var proxyBody = "proxies:\n  - name: NL-1\n    type: ss\n    server: 1.2.3.4\n    port: 8388\n    cipher: aes-256-gcm\n    password: x\n"

func (f *stampingFetcher) Fetch(ctx context.Context, url, hwidVal string, dev subscription.DeviceInfo) ([]byte, subscription.ResponseHeaders, error) {
	f.mu.Lock()
	f.st = append(f.st, f.nowC.Now())
	fail := f.fail
	f.mu.Unlock()
	if fail {
		return nil, subscription.ResponseHeaders{}, fmt.Errorf("network down")
	}
	return []byte(proxyBody), subscription.ResponseHeaders{}, nil
}

func (f *stampingFetcher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.st)
}

func (f *stampingFetcher) times() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.st...)
}

// scheduledState builds a state with one due subscription source.
func scheduledState() *model.State {
	return &model.State{
		Version: 2,
		Sources: []model.Source{
			{ID: "sub_1", Kind: "subscription", Name: "S", Enabled: true, URL: "http://x/sub"},
		},
	}
}

// newSched wires a scheduler with a fake clock around f and st.
func newSched(f *stampingFetcher, st *model.State, clk *fakeClock) *Scheduler {
	upd := &subscription.Updater{
		Fetcher: f,
		Store:   &memStore{st: st},
		HWID:    "ABCDEFGHJKLM0123456",
		Now:     clk.Now,
	}
	f.nowC = clk
	return New(upd, time.Hour)
}

// round is one scheduled tick (same call the loop makes on <-t.C).
func round(ctx context.Context, s *Scheduler) { s.round(ctx) }

// lastUpdateOf returns the parsed LastUpdate of the single test source.
func lastUpdateOf(t *testing.T, st *model.State) time.Time {
	t.Helper()
	last, err := time.Parse(time.RFC3339, st.Sources[0].LastUpdate)
	if err != nil {
		t.Fatalf("bad LastUpdate %q: %v", st.Sources[0].LastUpdate, err)
	}
	return last
}

// TestSkippedTickSingleRecovery: the router slept over TWO tick periods
// (drift). When the loop wakes, exactly ONE update round must run, and
// the next round immediately after must NOT fetch again — the source is
// not due anymore. No catch-up burst.
func TestSkippedTickSingleRecovery(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	f := &stampingFetcher{}
	st := scheduledState()
	s := newSched(f, st, clk)
	ctx := context.Background()

	// Round 1 at T0: fetch (source is due, LastUpdate empty).
	round(ctx, s)
	if got := f.calls(); got != 1 {
		t.Fatalf("first round must fetch once, got %d", got)
	}

	interval := 12 * time.Hour // state default after round 1

	// Router sleeps two full intervals: next round at T0+2*interval.
	clk.advance(2 * interval)
	round(ctx, s)
	if got := f.calls(); got != 2 {
		t.Fatalf("one overdue round must fetch exactly once, got %d fetches", got)
	}
	if lu := lastUpdateOf(t, st); !lu.Equal(clk.Now()) {
		t.Fatalf("LastUpdate must be the fake now %v, got %v", clk.Now(), lu)
	}

	// The immediately following tick (no time advanced): must NOT fetch.
	round(ctx, s)
	if got := f.calls(); got != 2 {
		t.Fatalf("back-to-back round after recovery must not fetch (no catch-up burst), got %d fetches", got)
	}
}

// TestDriftPartialInterval: a smaller drift (less than one interval)
// must not fetch either — the source is simply not due yet.
func TestDriftPartialInterval(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	f := &stampingFetcher{}
	st := scheduledState()
	s := newSched(f, st, clk)
	ctx := context.Background()

	round(ctx, s)
	if got := f.calls(); got != 1 {
		t.Fatalf("first round must fetch once, got %d", got)
	}

	// Advance 11.5h of the 12h interval: still not due.
	clk.advance(12*time.Hour - 30*time.Minute)
	round(ctx, s)
	if got := f.calls(); got != 1 {
		t.Fatalf("round before the interval elapses must not fetch, got %d", got)
	}

	// Cross the boundary: fetch.
	clk.advance(time.Hour)
	round(ctx, s)
	if got := f.calls(); got != 2 {
		t.Fatalf("round past the interval must fetch, got %d", got)
	}
}

// TestFetchErrorKeepsCadence (scheduler level): for a source that HAS
// an interval (a prior successful round), a failed fetch still
// advances LastUpdate, so the failure path does not turn into a
// retry storm — the next rounds back off for the whole interval.
// See subscription.backoff_test.go for the growth/reset of the retry
// backoff inside a single fetch.
func TestFetchErrorKeepsCadence(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	f := &stampingFetcher{}
	st := scheduledState()
	s := newSched(f, st, clk)
	ctx := context.Background()

	// Round 1 succeeds: interval (12h default) is now pinned in state.
	round(ctx, s)
	if got := f.calls(); got != 1 {
		t.Fatalf("first round must fetch once, got %d", got)
	}
	if st.Sources[0].UpdateIntervalSec != 12*3600 {
		t.Fatalf("successful round must pin the interval, got %d", st.Sources[0].UpdateIntervalSec)
	}

	// Round 2 fails: recorded, no fetch storm on the following ticks.
	clk.advance(12 * time.Hour)
	f.fail = true
	round(ctx, s)
	if got := f.calls(); got != 2 {
		t.Fatalf("due round must attempt one fetch, got %d", got)
	}
	if st.Sources[0].LastError == nil {
		t.Fatal("failed fetch must record last_error")
	}

	// Three ticks right after the failure: no re-fetch (backoff by
	// the full cadence gate — LastUpdate was updated on the failure).
	for i := 0; i < 3; i++ {
		round(ctx, s)
	}
	if got := f.calls(); got != 2 {
		t.Fatalf("rounds after a failure must back off (no immediate retry), got %d fetches", got)
	}

	// A full interval later it tries again and succeeds: last_error
	// is cleared.
	f.fail = false
	clk.advance(12 * time.Hour)
	round(ctx, s)
	if got := f.calls(); got != 3 {
		t.Fatalf("next due round must retry, got %d fetches", got)
	}
	if st.Sources[0].LastError != nil {
		t.Fatalf("successful round must clear last_error, got %v", *st.Sources[0].LastError)
	}
}

// TestNeverSucceededSourceRetriesEveryTick documents ACTUAL behavior
// (doc/behavior mismatch, listed in the PR): a source that has never
// updated successfully has no UpdateIntervalSec, so the cadence gate
// (intervalElapsed) treats it as always-due and EVERY scheduled tick
// retries it. Not "backoff growth across ticks" — the retry throttling
// for such a source is only the ticker period (>= 1 minute) plus the
// in-fetch retry backoff. Kept as a test so a future change is a
// conscious one.
func TestNeverSucceededSourceRetriesEveryTick(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	f := &stampingFetcher{}
	st := scheduledState()
	s := newSched(f, st, clk)
	ctx := context.Background()

	f.fail = true
	round(ctx, s)
	round(ctx, s)
	round(ctx, s)
	if got := f.calls(); got != 3 {
		t.Fatalf("documented behavior: a never-succeeded source is retried every tick, got %d", got)
	}
}

// TestIntervalChangeWithoutRestart (D8): a per-source interval written
// into the state takes effect on the next round with no restart. The
// fake clock makes the boundary observable.
func TestIntervalChangeWithoutRestart(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	f := &stampingFetcher{}
	st := scheduledState()
	s := newSched(f, st, clk)
	ctx := context.Background()

	round(ctx, s)
	if got := f.calls(); got != 1 {
		t.Fatalf("first round must fetch once, got %d", got)
	}

	// Operator pins a shorter interval in the state (settings change,
	// no scheduler restart — the updater re-reads state every round).
	st.Sources[0].UpdateIntervalSec = 3 * 3600

	// Not due after 1h under the NEW interval.
	clk.advance(time.Hour)
	round(ctx, s)
	if got := f.calls(); got != 1 {
		t.Fatalf("must respect the new 3h interval before it elapses, got %d fetches", got)
	}

	// Due after 3h total.
	clk.advance(2 * time.Hour)
	round(ctx, s)
	if got := f.calls(); got != 2 {
		t.Fatalf("must fetch once the new interval elapses, got %d fetches", got)
	}

	// The pinned interval also survives the round (user value wins,
	// FR-1.1) — it must not be reset back to the 12h default.
	if st.Sources[0].UpdateIntervalSec != 3*3600 {
		t.Fatalf("pinned interval must persist, got %d", st.Sources[0].UpdateIntervalSec)
	}
}

// TestSkippedTickBoundary: exactly one interval elapsed counts as due
// (>= comparison), so a tick arriving at the boundary fetches.
func TestSkippedTickBoundary(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	f := &stampingFetcher{}
	st := scheduledState()
	s := newSched(f, st, clk)
	ctx := context.Background()

	round(ctx, s)
	clk.advance(12 * time.Hour) // exactly one interval
	round(ctx, s)
	if got := f.calls(); got != 2 {
		t.Fatalf("tick at the exact interval boundary must fetch, got %d fetches", got)
	}
}
