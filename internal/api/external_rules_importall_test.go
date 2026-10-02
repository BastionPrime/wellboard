package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
)

// nikkiDirSnapshot lists the files under the nikki fixture directory so
// a test can prove the import paths added nothing to it.
func nikkiDirSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[p] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExternalRulesImportAllCreatesDisabledRoutes(t *testing.T) {
	externalFixture(t, ownerLikeConfig)
	nikkiDir := filepath.Dir(nikki.RunConfigPath)
	before := nikkiDirSnapshot(t, nikkiDir)

	srv, ts := newTestServer(t)
	defer ts.Close()
	_ = srv

	code, body := do(t, ts, "POST", "/api/v1/external-rules/import-all", "{}")
	if code != http.StatusCreated {
		t.Fatalf("import-all: %d %s", code, body)
	}
	var out struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
		Total    int `json:"total"`
		Rules    []struct {
			Raw    string `json:"raw"`
			Reason string `json:"reason"`
		} `json:"skipped_rules"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 9 {
		t.Fatalf("total = %d, want 9 rules read from the fixture", out.Total)
	}
	if out.Imported != 4 {
		t.Fatalf("imported = %d, want 4 (DIRECT-mapped DST-PORT/GEOIP×2/SRC-IP-CIDR)", out.Imported)
	}
	if out.Skipped != 5 || len(out.Rules) != 5 {
		t.Fatalf("skipped = %d / notes = %d, want 5 each", out.Skipped, len(out.Rules))
	}

	// Every imported route is DISABLED: nothing goes live until the
	// owner presses Apply.
	code, body = do(t, ts, "GET", "/api/v1/routes", "")
	if code != http.StatusOK {
		t.Fatalf("routes: %d %s", code, body)
	}
	var routesOut struct {
		Routes []model.Route `json:"routes"`
	}
	if err := json.Unmarshal([]byte(body), &routesOut); err != nil {
		t.Fatal(err)
	}
	routes := routesOut.Routes
	if len(routes) != 4 {
		t.Fatalf("routes = %d, want 4", len(routes))
	}
	for _, rt := range routes {
		if rt.Enabled {
			t.Fatalf("imported route %s must stay disabled until Apply", rt.ID)
		}
	}

	// Read-only invariant: the running nikki config is byte-identical
	// (sha256 per file) after listing + importing.
	after := nikkiDirSnapshot(t, nikkiDir)
	if len(after) != len(before) {
		t.Fatalf("nikki dir gained files: before=%d after=%d", len(before), len(after))
	}
	for p, sum := range before {
		if after[p] != sum {
			t.Fatalf("nikki file %s changed: %s -> %s", p, sum, after[p])
		}
	}
}

func TestExternalRulesImportAllIsIdempotent(t *testing.T) {
	externalFixture(t, ownerLikeConfig)
	_, ts := newTestServer(t)
	defer ts.Close()

	if code, body := do(t, ts, "POST", "/api/v1/external-rules/import-all", "{}"); code != http.StatusCreated {
		t.Fatalf("first import-all: %d %s", code, body)
	}
	code, body := do(t, ts, "POST", "/api/v1/external-rules/import-all", "{}")
	if code != http.StatusCreated {
		t.Fatalf("second import-all: %d %s", code, body)
	}
	var out struct {
		Imported int `json:"imported"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Imported != 0 {
		t.Fatalf("second import-all imported %d routes, want 0 (already imported)", out.Imported)
	}
}

func TestExternalRulesImportAllMissingConfigIsUsable(t *testing.T) {
	old := nikki.RunConfigPath
	nikki.RunConfigPath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	t.Cleanup(func() { nikki.RunConfigPath = old })

	_, ts := newTestServer(t)
	defer ts.Close()
	// A missing config is an empty rule set, not a failure: the owner
	// may open the Routes tab before nikki is installed.
	code, body := do(t, ts, "POST", "/api/v1/external-rules/import-all", "{}")
	if code != http.StatusCreated {
		t.Fatalf("import-all without nikki present: %d %s", code, body)
	}
	var out struct {
		Imported int `json:"imported"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Imported != 0 || out.Total != 0 {
		t.Fatalf("no nikki → imported=%d total=%d, want 0/0", out.Imported, out.Total)
	}
}