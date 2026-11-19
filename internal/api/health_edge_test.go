package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestProbeMihomoAPISuccess verifies that a healthy upstream mihomo external-controller
// returns OK=true with the trimmed core version reported in Detail.
func TestProbeMihomoAPISuccess(t *testing.T) {
	const expectedVer = "v1.18.1-alpha-community"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "  %s\n  ", expectedVer)
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	check := probeMihomoAPI(context.Background(), addr, "")
	if !check.OK {
		t.Fatalf("expected OK=true, got false, Detail=%q", check.Detail)
	}
	if check.Name != "mihomo" {
		t.Errorf("expected Name='mihomo', got %q", check.Name)
	}
	if !strings.Contains(check.Detail, expectedVer) {
		t.Errorf("expected Detail to contain %q, got %q", expectedVer, check.Detail)
	}
	if check.Measure == "" {
		t.Error("expected non-empty Measure duration")
	}
}

// TestProbeMihomoAPISecret verifies that probeMihomoAPI attaches the Bearer token
// when a non-empty secret is provided.
func TestProbeMihomoAPISecret(t *testing.T) {
	const secret = "test-mihomo-secret-12345"
	var receivedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("1.0.0"))
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	check := probeMihomoAPI(context.Background(), addr, secret)
	if !check.OK {
		t.Fatalf("expected OK=true, got %v: %s", check.OK, check.Detail)
	}
	expectedAuth := "Bearer " + secret
	if receivedAuth != expectedAuth {
		t.Errorf("expected Authorization %q, got %q", expectedAuth, receivedAuth)
	}
}

// TestProbeMihomoAPIErrors verifies error handling for unreachable endpoints,
// timeouts, and invalid URLs.
func TestProbeMihomoAPIErrors(t *testing.T) {
	t.Run("invalid_url", func(t *testing.T) {
		// Control character causes http.NewRequestWithContext to fail.
		check := probeMihomoAPI(context.Background(), "invalid host\x7f:9090", "")
		if check.OK {
			t.Errorf("expected OK=false for invalid URL, got %v", check)
		}
	})

	t.Run("unreachable_address", func(t *testing.T) {
		// 127.0.0.1 on unused high port refuses connection immediately.
		check := probeMihomoAPI(context.Background(), "127.0.0.1:54321", "")
		if check.OK {
			t.Errorf("expected OK=false for unreachable server, got %v", check)
		}
		if !strings.Contains(check.Detail, "API at 127.0.0.1:54321:") {
			t.Errorf("unexpected detail: %s", check.Detail)
		}
	})

	t.Run("context_timeout", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		addr := strings.TrimPrefix(ts.URL, "http://")
		check := probeMihomoAPI(ctx, addr, "")
		if check.OK {
			t.Errorf("expected OK=false on timeout, got %v", check)
		}
	})
}

// TestProbeMihomoAPINon200Status verifies that non-2xx HTTP responses report OK=false
// with the status code recorded in Detail.
func TestProbeMihomoAPINon200Status(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway} {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "upstream failure", code)
			}))
			defer ts.Close()

			addr := strings.TrimPrefix(ts.URL, "http://")
			check := probeMihomoAPI(context.Background(), addr, "")
			if check.OK {
				t.Fatalf("expected OK=false for status %d, got true", code)
			}
			expectedSubstr := fmt.Sprintf("status %d", code)
			if !strings.Contains(check.Detail, expectedSubstr) {
				t.Errorf("expected Detail to mention %q, got %q", expectedSubstr, check.Detail)
			}
		})
	}
}

// TestProbeMihomoAPIPartialAndTruncatedBody verifies handling of partial or oversized
// version responses (ensures LimitReader 1<<10 caps response size safely).
func TestProbeMihomoAPIPartialAndTruncatedBody(t *testing.T) {
	hugeVer := strings.Repeat("x", 2048)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(hugeVer))
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	check := probeMihomoAPI(context.Background(), addr, "")
	if !check.OK {
		t.Fatalf("expected OK=true, got %v", check)
	}
	// Body read is capped at 1<<10 (1024 bytes).
	if len(check.Detail) > 1200 {
		t.Errorf("Detail length %d exceeds expected limit", len(check.Detail))
	}
}

// TestProbeMihomoAPIConcurrency verifies that probeMihomoAPI is safe for concurrent
// callers with no shared state corruption.
func TestProbeMihomoAPIConcurrency(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v1.18.0"))
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	const concurrent = 32
	var wg sync.WaitGroup
	errCh := make(chan error, concurrent)

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			secret := fmt.Sprintf("secret-%d", id)
			check := probeMihomoAPI(context.Background(), addr, secret)
			if !check.OK {
				errCh <- fmt.Errorf("probe failed: %s", check.Detail)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent probe error: %v", err)
	}
}
