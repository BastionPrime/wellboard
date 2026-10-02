package subscription

// Fetch-retry backoff tests: linear growth across retries, reset after
// success (a fresh Fetch starts the ramp from 1x again), and
// ctx-cancellation cutting the wait short.
//
// The backoff lives in Client.Fetch (2 retries, linear ramp:
// backoff*1, backoff*2). The waits are observed through the package
// backoffWait seam with a tiny Backoff so the tests stay fast.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// recordBackoff swaps backoffWait for a recorder that logs the
// requested delay and waits only 1ms. Restored via cleanup.
func recordBackoff(t *testing.T) func() []time.Duration {
	t.Helper()
	var (
		mu    sync.Mutex
		waits []time.Duration
	)
	orig := backoffWait
	backoffWait = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		select {
		case <-time.After(time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.Cleanup(func() { backoffWait = orig })
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), waits...)
	}
}

func deadServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // port is closed: connection refused
	return url
}

// TestClientBackoffGrowsLinearly: consecutive retries of one failing
// fetch wait backoff*1 then backoff*2 (linear ramp).
func TestClientBackoffGrowsLinearly(t *testing.T) {
	waits := recordBackoff(t)
	url := deadServerURL(t)
	c := &Client{Backoff: 5 * time.Millisecond, UserAgent: "WellBoard/test"}

	start := time.Now()
	_, _, err := c.Fetch(context.Background(), url, "ABCDEFGHJKLM0123456", DeviceInfo{})
	if err == nil {
		t.Fatal("dead server must produce an error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("test must not wait wall-clock backoff: %v", elapsed)
	}

	got := waits()
	if len(got) != 2 {
		t.Fatalf("want 2 backoff waits (2 retries), got %d: %v", len(got), got)
	}
	base := 5 * time.Millisecond
	for i, w := range got {
		if want := base * time.Duration(i+1); w != want {
			t.Fatalf("wait %d must be linear %v, got %v", i, want, w)
		}
	}
}

// TestClientBackoffResetsAfterSuccess: after a successful fetch, the
// next failing fetch starts the ramp from 1x again — a success resets
// the backoff, no growth carries over between fetches.
func TestClientBackoffResetsAfterSuccess(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxies:\n  - name: A\n    type: ss\n    server: 1.2.3.4\n    port: 8388\n    cipher: aes-256-gcm\n    password: x\n"))
	}))
	defer ok.Close()

	waits := recordBackoff(t)
	c := &Client{Backoff: 5 * time.Millisecond, UserAgent: "WellBoard/test"}

	// Success: no backoff waits at all.
	if _, _, err := c.Fetch(context.Background(), ok.URL, "ABCDEFGHJKLM0123456", DeviceInfo{}); err != nil {
		t.Fatalf("ok server must not fail: %v", err)
	}
	if got := waits(); len(got) != 0 {
		t.Fatalf("successful fetch must not wait backoff, got %v", got)
	}

	// Failure AFTER the success: ramp restarts from 1x (reset).
	dead := deadServerURL(t)
	if _, _, err := c.Fetch(context.Background(), dead, "ABCDEFGHJKLM0123456", DeviceInfo{}); err == nil {
		t.Fatal("dead server must produce an error")
	}
	got := waits()
	if len(got) != 2 {
		t.Fatalf("want 2 waits after failure, got %v", got)
	}
	if got[0] != 5*time.Millisecond || got[1] != 10*time.Millisecond {
		t.Fatalf("backoff must restart from 1x after success (reset), got %v", got)
	}
}

// TestClientBackoffRespectsContext: a cancelled context aborts the
// backoff wait instead of sleeping it out.
func TestClientBackoffRespectsContext(t *testing.T) {
	orig := backoffWait
	t.Cleanup(func() { backoffWait = orig })
	backoffWait = func(ctx context.Context, d time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}
	url := deadServerURL(t)
	c := &Client{Backoff: time.Hour, UserAgent: "WellBoard/test"}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	_, _, err := c.Fetch(ctx, url, "ABCDEFGHJKLM0123456", DeviceInfo{})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context must abort the fetch, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellation must cut the wait short, took %v", elapsed)
	}
}
