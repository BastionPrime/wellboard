// Phase 7: state export/import (FR-6.5). The export is a self-contained
// JSON document (the WellBoard state plus an envelope) for backup and
// migration to another router. The HWID and its salt are NEVER part of
// the state file (internal/hwid keeps them in a separate file), so the
// export cannot leak them — enforced by a test that greps the export
// for the live HWID. Import validates the version, rejects unknown
// fields, and checks referential integrity through the same
// generator problems path the UI relies on.

package api

import (
	"net/http"

	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/store"
)

// exportFormat is the export envelope version. Bumped on incompatible
// envelope changes (field renames, structure). The inner state schema
// is versioned separately by store.CurrentVersion.
const exportFormat = 1

// exportDoc is the GET /api/v1/export response body.
type exportDoc struct {
	// Format is the envelope version (see exportFormat).
	Format int `json:"format"`
	// App is always "wellboard" (documents the producer).
	App string `json:"app"`
	// Version is the state schema version of the embedded state
	// (store.CurrentVersion at export time).
	Version int         `json:"version"`
	State   model.State `json:"state"`
}

// handleExport serves the full state as a downloadable JSON document.
// The HWID is not part of the state (it lives in <state_dir>/hwid), so
// it is never included (FR-6.5, NFR-2.3).
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	st, err := s.load()
	if err != nil {
		writeError(w, err)
		return
	}
	defer s.unlock()
	doc := exportDoc{
		Format:  exportFormat,
		App:     AppName,
		Version: st.Version,
		State:   *st,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="wellboard-state.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// importIn is the POST /api/v1/import body: an export document as
// produced by handleExport (strict decoding: unknown fields rejected).
type importIn struct {
	Format  *int         `json:"format"`
	App     *string      `json:"app"`
	Version *int         `json:"version"`
	State   *model.State `json:"state"`
}

// handleImport replaces the whole state from an export document.
// Validation: envelope version (future formats rejected), state schema
// version (the store's own too-new check runs on Save/Load), and full
// referential integrity via the generator BEFORE the destructive save.
// On any failure the previous state stays untouched.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var in importIn
	// Export documents are state.json-sized; 1 MiB (decodeStrict
	// default) is plenty for realistic pools (thousands of servers
	// marshal to ~100-300 KB). Oversized imports are rejected here.
	if err := decodeStrict(r, &in, 1<<20); err != nil {
		writeError(w, err)
		return
	}
	if in.State == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "state is required"})
		return
	}
	if in.Format == nil || *in.Format != exportFormat {
		got := -1
		if in.Format != nil {
			got = *in.Format
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":    "unsupported export format",
			"got":      got,
			"expected": exportFormat,
		})
		return
	}
	if in.App != nil && *in.App != AppName {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "document was not exported by wellboard",
			"app":   *in.App,
		})
		return
	}
	if in.Version == nil || *in.Version > store.CurrentVersion {
		got := -1
		if in.Version != nil {
			got = *in.Version
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":    "state version missing or newer than this build supports",
			"got":      got,
			"expected": store.CurrentVersion,
		})
		return
	}

	st := in.State
	if st.Version == 0 {
		st.Version = *in.Version
	}

	// Referential integrity before saving: a broken import must not
	// replace a working state. The generator reports lost targets and
	// broken objects as *Problems → 409 with the per-object list.
	if _, err := generator.GenerateWithProviders(st, s.knownProviders()); err != nil {
		s.log("import rejected: %v", err)
		writeError(w, err)
		return
	}

	// Lock, save, report.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.Save(st); err != nil {
		writeError(w, err)
		return
	}
	s.log("state imported: %d sources, %d servers, %d groups, %d routes",
		len(st.Sources), len(st.Servers), len(st.Groups), len(st.Routes))
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "imported",
		"version": st.Version,
		"counts": map[string]int{
			"sources": len(st.Sources),
			"servers": len(st.Servers),
			"groups":  len(st.Groups),
			"routes":  len(st.Routes),
		},
	})
}
