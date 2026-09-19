// Package api hosts the WellBoard HTTP handlers. Phase 0/1 ship only the
// health endpoint; auth middleware and the real API surface land in later
// phases (see docs/initial-tz.md, sections 5.1 and FR-9).
package api

import (
	"encoding/json"
	"net/http"
)

// AppName identifies WellBoard in health responses and logs.
const AppName = "wellboard"

// HealthResponse is the JSON body served by GET /api/v1/health.
type HealthResponse struct {
	Status  string `json:"status"`
	App     string `json:"app"`
	Version string `json:"version"`
}

// NewHealthMux registers the health route on a fresh mux and returns it.
//
// The route is registered with the method pattern "GET /api/v1/health":
// net/http answers other methods with 405 Method Not Allowed (Phase 0
// review fix — health must be read-only).
func NewHealthMux(version string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", handleHealth(version))
	return mux
}

// handleHealth returns the health handler (also reachable via NewHealthMux).
func handleHealth(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := HealthResponse{
			Status:  "ok",
			App:     AppName,
			Version: version,
		}
		// Encoding a fixed struct cannot fail in practice; nothing to do.
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// HealthHandler returns a handler that reports daemon liveness. Kept for
// direct handler tests; the daemon wires routes through NewHealthMux.
func HealthHandler(version string) http.HandlerFunc {
	return handleHealth(version)
}
