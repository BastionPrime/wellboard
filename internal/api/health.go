// Package api hosts the WellBoard HTTP handlers. Phase 0 ships only the
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

// HealthHandler returns a handler that reports daemon liveness. The response
// is a fixed JSON document:
//
//	{"status":"ok","app":"wellboard","version":"<version>"}
func HealthHandler(version string) http.HandlerFunc {
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
