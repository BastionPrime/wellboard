package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthHandlerGet verifies the Phase 0 acceptance contract: GET →
// HTTP 200, JSON content type, exact health document shape.
func TestHealthHandlerGet(t *testing.T) {
	const testVersion = "0.0.0-test"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()

	NewHealthMux(testVersion).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want %q", ct, "application/json")
	}

	var body HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	if body.App != "wellboard" {
		t.Errorf("app = %q, want %q", body.App, "wellboard")
	}
	if body.Version != testVersion {
		t.Errorf("version = %q, want %q", body.Version, testVersion)
	}
}

// TestHealthMethodNotAllowed (Phase 0 review fix): the health route is
// GET-only; POST/PUT/DELETE must yield 405 with an Allow header.
func TestHealthMethodNotAllowed(t *testing.T) {
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch,
	} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/health", nil)
			rec := httptest.NewRecorder()
			NewHealthMux("test").ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s status = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
			}
			if allow := rec.Header().Get("Allow"); allow == "" {
				t.Errorf("%s: Allow header not set", method)
			}
		})
	}
}
