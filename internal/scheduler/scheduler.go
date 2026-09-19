// Package scheduler drives periodic subscription updates (initial TZ 5.2
// "internal Go ticker"; FR-1.4: manual AND scheduled updates).
//
// Design: a single ticker at the shortest configured interval is simple
// and predictable on a router; each tick the updater refreshes only the
// sources whose per-source interval has elapsed (interval re-read from
// state every round, so a header-provided profile-update-interval takes
// effect without a restart, FR-1.1). Sources with no interval fall back to
// DefaultInterval (12 h).
package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/wellboard/wellboard/internal/subscription"
)

// DefaultInterval is the fallback tick period (FR-1.1: 12 h default).
const DefaultInterval = 12 * time.Hour

// MinInterval is the floor for the ticker period: subscriptions updated
// more often than this would hammer the panel (a misconfigured 1-second
// interval header). 60s is the practical minimum.
const MinInterval = time.Minute

// Scheduler runs subscription updates on a ticker.
type Scheduler struct {
	// Updater is invoked on every tick for due sources.
	Updater *subscription.Updater
	// Tick is the ticker period; ≤0 means DefaultInterval. The effective
	// period is clamped to ≥ MinInterval.
	Tick time.Duration
	// Logger receives one line per outcome; nil = log package.
	Logger *log.Logger

	mu     sync.Mutex
	stopCh chan struct{}
	// lastOK maps source ID → last successful update time (bookkeeping
	// for logs/tests).
	lastOK map[string]time.Time
}

// New returns a Scheduler around upd with the given tick period.
func New(upd *subscription.Updater, tick time.Duration) *Scheduler {
	return &Scheduler{Updater: upd, Tick: tick, lastOK: map[string]time.Time{}}
}

// effectiveTick returns the clamped ticker period.
func (s *Scheduler) effectiveTick() time.Duration {
	t := s.Tick
	if t <= 0 {
		t = DefaultInterval
	}
	if t < MinInterval {
		t = MinInterval
	}
	return t
}

// Start launches the background loop until Stop is called. The first
// round runs immediately after Start (a fresh subscription shouldn't wait
// 12 h for its first server list).
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.stopCh != nil {
		s.mu.Unlock()
		return // already running
	}
	s.stopCh = make(chan struct{})
	stopCh := s.stopCh
	s.mu.Unlock()

	go func() {
		s.round(ctx)
		t := time.NewTicker(s.effectiveTick())
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				s.round(ctx)
			}
		}
	}()
}

// round runs one update pass and logs the outcome.
func (s *Scheduler) round(ctx context.Context) {
	res, err := s.Updater.Update(ctx)
	if err != nil {
		s.logf("scheduler: update round failed: %v", err)
		return
	}
	for id, e := range res.Errors {
		s.logf("scheduler: source %s: %s", id, e)
	}
	s.mu.Lock()
	for id := range res.Servers {
		s.lastOK[id] = time.Now()
	}
	s.mu.Unlock()
	if len(res.Servers) > 0 {
		s.logf("scheduler: update round ok: %d source(s)", len(res.Servers))
	}
}

// UpdateNow triggers a manual full update round (FR-1.4: update button).
// It runs synchronously so the caller can report the outcome.
func (s *Scheduler) UpdateNow(ctx context.Context) (*subscription.UpdateResult, error) {
	return s.Updater.Update(ctx)
}

// Stop halts the loop. Safe to call multiple times; Start may be called
// again afterwards.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopCh != nil {
		select {
		case <-s.stopCh:
			// already closed
		default:
			close(s.stopCh)
		}
	}
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, args...)
		return
	}
	log.Printf(format, args...)
}
