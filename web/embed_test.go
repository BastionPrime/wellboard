package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSPAHandlerIndex verifies phase 4 acceptance: GET / serves the
// embedded index.html (HTML with the app mount point).
func TestSPAHandlerIndex(t *testing.T) {
	h := SPAHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="app"`) {
		t.Errorf("GET / body does not contain the app mount div:\n%.200s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("GET / Content-Type = %q, want text/html", ct)
	}
}

// TestSPAHandlerIndexFile verifies /index.html (the file server 301s it
// to /, which serves the same document — assert the redirect target).
func TestSPAHandlerIndexFile(t *testing.T) {
	h := SPAHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if rec.Code != http.StatusOK && rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /index.html status = %d, want 200 or 301-to-/", rec.Code)
	}
	if rec.Code == http.StatusMovedPermanently && rec.Header().Get("Location") != "/" && rec.Header().Get("Location") != "./" {
		t.Fatalf("GET /index.html redirect Location = %q, want / or ./", rec.Header().Get("Location"))
	}
}

// TestSPAHandlerMethodNotAllowed verifies non-GET requests are rejected
// (the SPA is read-only static content).
func TestSPAHandlerMethodNotAllowed(t *testing.T) {
	h := SPAHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d, want 405", rec.Code)
	}
}

// TestDistEmbedded verifies the bundle actually contains the built
// assets (guards against an empty dist/ sneaking into a build).
func TestDistEmbedded(t *testing.T) {
	found := false
	err := fs.WalkDir(Dist(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css") {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded dist: %v", err)
	}
	if !found {
		t.Errorf("embedded dist contains no built .js/.css assets — rebuild with `cd web && npm run build`")
	}
}
