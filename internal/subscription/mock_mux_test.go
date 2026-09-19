package subscription_test

import (
	"net/http"

	"github.com/wellboard/wellboard/internal/subscription/testfixtures"
)

// mockMux returns the shared fixture handler set used by the contract
// tests (same handlers as test/mock-remnawave/main.go).
func mockMux() http.Handler {
	return testfixtures.NewMux()
}
