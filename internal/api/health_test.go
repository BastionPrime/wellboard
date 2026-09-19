package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthHandler verifies the Phase 0 acceptance contract: HTTP 200,
// JSON content type, and the exact health document shape.
func TestHealthHandler(t *testing.T) {
	const testVersion = "0.0.0-test"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()

	HealthHandler(testVersion).ServeHTTP(rec, req)

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
