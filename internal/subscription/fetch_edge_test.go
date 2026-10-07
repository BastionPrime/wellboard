package subscription_test

// Edge tests for the subscription fetch transport: 5xx retry, partial
// body / broken pipe, Content-Length mismatch, slow-response timeout,
// empty body, and the 64 MiB body cap. Each scenario uses a dedicated
// httptest server so the transport behavior is exercised for real.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/subscription"
)

// overCapBody is bigger than the 64 MiB maxBodyBytes cap.
var overCapBody = make([]byte, 64<<20+1)

// fetchEdgeClient is a client with a short backoff for retry tests.
func fetchEdgeClient(url string) *subscription.Client {
	c := subscription.NewClient("edge-test")
	c.HTTP = &http.Client{Timeout: 5 * time.Second}
	c.Backoff = 5 * time.Millisecond
	return c
}

const edgeHWID = "ABCDEFGHJKLM0123456"

// ----------------------------------------------------------------------------
// 5xx handling
// ----------------------------------------------------------------------------

// TestFetchRetryOn500: a transient 5xx must be retried and succeed on a
// later attempt.
func TestFetchRetryOn500(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits <= 2 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, "trojan://pass123@9.8.7.6:443?sni=tro.example.com#TR1\n")
	}))
	defer srv.Close()

	c := fetchEdgeClient(srv.URL)
	body, _, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if err != nil {
		t.Fatalf("5xx must be retried and recovered: %v", err)
	}
	if hits != 3 {
		t.Fatalf("want 3 requests (2x 5xx + 1 ok), got %d", hits)
	}
	if len(body) == 0 {
		t.Fatal("body must be returned")
	}
}

// TestFetch500ExhaustsRetries: a persistent 5xx must fail after the
// first request plus Retries extra attempts.
func TestFetch500ExhaustsRetries(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := fetchEdgeClient(srv.URL)
	_, _, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if err == nil {
		t.Fatal("persistent 5xx must error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error must mention the status, got %v", err)
	}
	if hits != 1+subscription.Retries {
		t.Fatalf("want %d requests (1 + %d retries), got %d", 1+subscription.Retries, subscription.Retries, hits)
	}
}

// ----------------------------------------------------------------------------
// Broken / truncated bodies
// ----------------------------------------------------------------------------

// TestFetchPartialBodyBrokenPipe: a connection cut mid-body must fail as
// a network error and be retried (the retry succeeding on the next
// attempt).
func TestFetchPartialBodyBrokenPipe(t *testing.T) {
	var hits int
	good := "trojan://pass123@9.8.7.6:443?sni=tro.example.com#TR1\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			// Announce more than we send, then hang up mid-body.
			w.Header().Set("Content-Length", "1024")
			fmt.Fprint(w, "trojan://pass123@9.8.7.6:443?par")
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
					return
				}
			}
		}
		fmt.Fprint(w, good)
	}))
	defer srv.Close()

	c := fetchEdgeClient(srv.URL)
	body, _, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if err != nil {
		t.Fatalf("mid-body hangup must be retried and recovered: %v", err)
	}
	if string(body) != good {
		t.Fatalf("want the good body, got %q", body)
	}
	if hits != 2 {
		t.Fatalf("want 2 requests (1 broken + 1 ok), got %d", hits)
	}
}

// TestFetchShorterBodyThanContentLength: a server that closes after
// sending less than Content-Length must yield an error, not a silently
// truncated body.
func TestFetchShorterBodyThanContentLength(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Length", "1024")
		fmt.Fprint(w, "trojan://pass123@9.8.7.6:443?partial=")
	}))
	defer srv.Close()

	c := fetchEdgeClient(srv.URL)
	_, _, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if err == nil {
		t.Fatal("body shorter than Content-Length must error, not return a truncated body")
	}
	if hits != 1+subscription.Retries {
		t.Fatalf("network-class error must exhaust retries (1+%d), got %d requests", subscription.Retries, hits)
	}
}

// TestFetchLongerBodyThanContentLength: a server that sends MORE than
// Content-Length must yield an error too — net/http closes the
// connection when the handler over-writes, so the client sees an
// unexpected EOF instead of a silently truncated body.
func TestFetchLongerBodyThanContentLength(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Length", "16")
		// 40 bytes vs the announced 16.
		fmt.Fprint(w, "trojan://pass123@9.8.7.6:443?sni=x#TR1")
	}))
	defer srv.Close()

	c := fetchEdgeClient(srv.URL)
	_, _, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if err == nil {
		t.Fatal("body longer than Content-Length must error, not return a truncated body")
	}
	if !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("want an EOF-class error, got %v", err)
	}
	if hits != 1+subscription.Retries {
		t.Fatalf("network-class error must exhaust retries (1+%d), got %d requests", subscription.Retries, hits)
	}
}

// ----------------------------------------------------------------------------
// Timeout
// ----------------------------------------------------------------------------

// TestFetchTimeoutMidBody: a body that starts flowing but stalls past
// the client timeout must fail with a timeout error, and the failure
// must come quickly (not after 20s).
func TestFetchTimeoutMidBody(t *testing.T) {
	good := "trojan://pass123@9.8.7.6:443?sni=tro.example.com#TR1\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, good) // headers + start of the body…
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(2 * time.Second) // …then stall mid-body
	}))
	defer srv.Close()

	c := subscription.NewClient("test")
	c.HTTP = &http.Client{Timeout: 200 * time.Millisecond}
	c.Backoff = 5 * time.Millisecond
	start := time.Now()
	_, _, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if err == nil {
		t.Fatal("stalled body must time out")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout must be quick, took %v", elapsed)
	}
}

// ----------------------------------------------------------------------------
// Empty body
// ----------------------------------------------------------------------------

// TestFetchEmptyBody200: a 200 with an empty body must succeed at the
// transport level; the parse layer maps it to ErrEmptySubscription.
func TestFetchEmptyBody200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := fetchEdgeClient(srv.URL)
	body, hdr, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if err != nil {
		t.Fatalf("200 with empty body must not be a transport error: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("body must be empty, got %d bytes", len(body))
	}
	if hdr.HWIDActive {
		t.Fatal("no hwid headers on this fixture")
	}
	if _, err := subscription.ParseBody(body); !errors.Is(err, subscription.ErrEmptySubscription) {
		t.Fatalf("parse must map an empty body to ErrEmptySubscription, got %v", err)
	}
}

// ----------------------------------------------------------------------------
// 64 MiB body cap
// ----------------------------------------------------------------------------

// TestFetchOversizedBodyRejected: a body larger than the 64 MiB cap must
// be rejected with ErrBodyTooLarge, not returned.
func TestFetchOversizedBodyRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(overCapBody)))
		w.Write(overCapBody)
	}))
	defer srv.Close()

	c := fetchEdgeClient(srv.URL)
	start := time.Now()
	body, _, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if err == nil {
		t.Fatalf("oversized body must error, got %d bytes", len(body))
	}
	if !errors.Is(err, subscription.ErrBodyTooLarge) {
		t.Fatalf("want ErrBodyTooLarge, got %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("no body must be returned on error, got %d bytes", len(body))
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("rejection must not wait for the whole body, took %v", elapsed)
	}
}

// TestFetchOversizedNotBufferedPastCap: the cap must be enforced while
// reading — the client stops consuming the stream at the limit and
// aborts the connection, instead of draining an endless body. The server
// writes an infinite chunked stream and records how many bytes each
// connection pushed before the client hung up: every attempt must stop
// near the cap, far below what an unbounded read would take.
func TestFetchOversizedNotBufferedPastCap(t *testing.T) {
	const capBytes = 64 << 20
	chunk := make([]byte, 512*1024)
	var mu sync.Mutex
	var finished int
	var perConn []int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sent int64
		defer func() {
			mu.Lock()
			perConn = append(perConn, sent)
			finished++
			mu.Unlock()
		}()
		for {
			if _, err := w.Write(chunk); err != nil {
				return // client aborted the connection: the desired signal
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			sent += int64(len(chunk))
			if sent > 3*capBytes {
				return // belt-and-suspenders for a hypothetical no-cap client
			}
		}
	}))
	defer srv.Close()

	c := fetchEdgeClient(srv.URL)
	start := time.Now()
	_, _, err := c.Fetch(context.Background(), srv.URL+"/x/mihomo", edgeHWID, subscription.DeviceInfo{})
	if !errors.Is(err, subscription.ErrBodyTooLarge) {
		t.Fatalf("want ErrBodyTooLarge, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("rejection must be quick, took %v", elapsed)
	}
	// All attempts must have finished writing by now (each ended when
	// the client aborted the connection).
	mu.Lock()
	defer mu.Unlock()
	if finished < 1 {
		t.Fatalf("want at least 1 finished connection, got %d", finished)
	}
	// Reading stops at the cap (plus socket-buffer overshoot — TCP window
	// autotuning lets the server run ahead by tens of MiB before the
	// abort lands), far below the 3x-cap bound a no-cap client would
	// drain.
	for i, sent := range perConn {
		if sent > capBytes+capBytes/2 {
			t.Fatalf("connection %d: client must stop reading at the cap, server pushed %d bytes", i, sent)
		}
	}
}
