// Package apply implements the FR-6 configuration apply flow:
//
//	generate → validate (mihomo -t) → WriteProfile → Activate
//	→ Health (15 s) → on failure auto-rollback to LastGood.
//
// The flow is adapter-agnostic: the nikki.Adapter interface isolates
// router specifics (UCI, procd) from the dry-run dev stand (local
// mihomo process). Applied profile history is kept on disk (N=5,
// rotation, FR-6.3). "Pending changes" is a comparison of the current
// state hash against the hash of the last applied state (FR-6.1).
package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wellboard/wellboard/internal/generator"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
)

// HistorySize is how many applied profiles are kept (FR-6.3, N=5).
const HistorySize = 5

// HealthTimeout is the post-activation health budget (FR-6.4: 15 s).
const HealthTimeout = 15 * time.Second

// Result is the outcome of an apply attempt (success or failure).
type Result struct {
	// Profile is the applied profile name (e.g. "wellboard-20260920-153000").
	Profile string `json:"profile"`
	// OK reports whether the flow reached the healthy state.
	OK bool `json:"ok"`
	// Stage is where the flow stopped: "generate", "validate",
	// "write", "activate", "health" or "" on success.
	Stage string `json:"stage,omitempty"`
	// Error is the human-readable failure reason ("" on success).
	Error string `json:"error,omitempty"`
	// RolledBack is true when the failure triggered an auto-rollback
	// (FR-6.4) and LastGoodProfile names the restored profile.
	RolledBack     bool   `json:"rolled_back,omitempty"`
	LastGoodProfile string `json:"last_good_profile,omitempty"`
	// AppliedAt is the completion time.
	AppliedAt time.Time `json:"applied_at"`
}

// Record is one entry of the applied-profile history (FR-6.3).
type Record struct {
	// Profile is the profile file name (adapter's name space).
	Profile string `json:"profile"`
	// StateHash is the SHA-256 of the state JSON at apply time; used
	// for the pending-changes indicator (FR-6.1).
	StateHash string `json:"state_hash"`
	// AppliedAt is when the flow completed successfully.
	AppliedAt time.Time `json:"applied_at"`
	// Result carries the failure context for records kept for
	// diagnostics (only successful applies are history entries).
	Result *Result `json:"result,omitempty"`
}

// Applyer runs the flow against one Adapter. Construct with New.
type Applyer struct {
	// Adapter is the nikki integration (real or dry-run).
	Adapter nikki.Adapter
	// Store loads the state (read-only for the flow; the state is
	// never modified by applying).
	Store StateLoader
	// KnownProviders guards provider references in generation
	// (nil = check disabled).
	KnownProviders map[string]bool
	// Dir keeps the applied-history JSON + copies of applied states
	// for pending comparison (created on demand).
	Dir string

	mu sync.Mutex // serializes apply/rollback (single flight)
}

// StateLoader is the read-only state source (satisfied by *store.Store).
type StateLoader interface {
	Load() (*model.State, error)
}

// New builds an Applyer writing history into dir.
func New(adapter nikki.Adapter, st StateLoader, known map[string]bool, dir string) *Applyer {
	return &Applyer{Adapter: adapter, Store: st, KnownProviders: known, Dir: dir}
}

// historyPath returns the applied-history file location.
func (a *Applyer) historyPath() string {
	return filepath.Join(a.Dir, "applied.json")
}

// stateHash computes the canonical state fingerprint: the state is
// marshalled with sorted keys (encoding/json sorts map keys) so the
// hash is independent of field ordering in memory.
func stateHash(st *model.State) (string, error) {
	data, err := json.Marshal(st)
	if err != nil {
		return "", fmt.Errorf("apply: hash state: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// loadHistory reads the applied records (empty when absent).
func (a *Applyer) loadHistory() []Record {
	data, err := os.ReadFile(a.historyPath())
	if err != nil {
		return nil
	}
	var recs []Record
	if err := json.Unmarshal(data, &recs); err != nil {
		return nil
	}
	return recs
}

// saveHistory atomically persists recs.
func (a *Applyer) saveHistory(recs []Record) error {
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		return fmt.Errorf("apply: create history dir: %w", err)
	}
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return fmt.Errorf("apply: marshal history: %w", err)
	}
	tmp := a.historyPath() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("apply: write history: %w", err)
	}
	if err := os.Rename(tmp, a.historyPath()); err != nil {
		return fmt.Errorf("apply: rename history: %w", err)
	}
	return nil
}

// History returns the applied-profile records, newest first
// (FR-6.3: POST /applied listing). Rotation keeps HistorySize entries.
func (a *Applyer) History() []Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	recs := a.loadHistory()
	// Newest first for display.
	out := make([]Record, len(recs))
	copy(out, recs)
	sort.SliceStable(out, func(i, j int) bool { return out[i].AppliedAt.After(out[j].AppliedAt) })
	return out
}

// LastApplied returns the newest successful record, if any.
func (a *Applyer) LastApplied() (Record, bool) {
	recs := a.loadHistory()
	if len(recs) == 0 {
		return Record{}, false
	}
	return recs[len(recs)-1], true
}

// Pending reports whether the current state differs from the last
// applied state (FR-6.1 indicator). With no applied history the state
// is pending unless it is effectively empty (fresh install).
func (a *Applyer) Pending() (bool, string, error) {
	st, err := a.Store.Load()
	if err != nil {
		return false, "", err
	}
	hash, err := stateHash(st)
	if err != nil {
		return false, "", err
	}
	last, ok := a.LastApplied()
	if !ok {
		// Nothing applied yet: pending when there is any routing
		// content to apply, not pending for a virgin state.
		pending := len(st.Routes) > 0 || len(st.Groups) > 0 || len(st.Servers) > 0 ||
			st.Settings.DefaultPolicy.Type != model.TargetDirect
		return pending, hash, nil
	}
	return last.StateHash != hash, hash, nil
}

// profileSeq disambiguates applies within the same second.
var profileSeq int64

// profileName mints a unique, sortable profile name.
func profileName(now time.Time) string {
	seq := atomic.AddInt64(&profileSeq, 1)
	return fmt.Sprintf("wellboard-%s-%03d", now.UTC().Format("20060102-150405"), seq%1000)
}

// Apply runs the full FR-6.2 flow. The returned Result reports the
// outcome; the error is non-nil only for infrastructure failures
// (state unreadable, history unwritable) — flow failures are data in
// the Result so the API can answer 409/503 with context.
func (a *Applyer) Apply() (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	res := Result{AppliedAt: time.Now()}

	st, err := a.Store.Load()
	if err != nil {
		return res, fmt.Errorf("apply: load state: %w", err)
	}
	hash, err := stateHash(st)
	if err != nil {
		return res, err
	}
	// Skip no-op applies when the state is identical to the last
	// applied one — but still allow re-apply for recovery ( callers
	// may want to force restart). Fresh content: proceed.
	name := profileName(res.AppliedAt)

	// 1. Generate.
	out, err := generator.GenerateWithProviders(st, a.KnownProviders)
	if err != nil {
		res.Stage, res.Error = "generate", err.Error()
		var prob *generator.Problems
		if errors.As(err, &prob) {
			res.Error = "profile generation failed (unresolved references)"
		}
		return res, nil
	}

	// 2. Write profile (before validate so mihomo -t sees the exact
	// bytes that would run; also materializes the provider payloads).
	path, err := a.Adapter.WriteProfile(name, out)
	if err != nil {
		res.Stage, res.Error = "write", err.Error()
		return res, nil
	}

	// 3. Validate with the real mihomo binary (FR-6.2: mihomo -t).
	if err := a.Adapter.Validate(path); err != nil {
		res.Stage, res.Error = "validate", err.Error()
		return res, nil
	}

	// 4. Activate (UCI select + service restart on the router; local
	// mihomo restart in dry-run).
	if err := a.Adapter.Activate(name); err != nil {
		res.Stage, res.Error = "activate", err.Error()
		return res, nil
	}

	// 5. Health check with the FR-6.4 budget; failure rolls back to
	// the last good profile (FR-6.3/6.4).
	if err := a.Adapter.Health(HealthTimeout); err != nil {
		res.Stage, res.Error = "health", err.Error()
		lgName, ok := a.Adapter.LastGood()
		switch {
		case !ok:
			// First apply ever: nothing to roll back to.
			res.Error = fmt.Sprintf("%s (no previous good profile to roll back to)", res.Error)
		case lgName == name:
			res.Error = fmt.Sprintf("%s (previous good profile is this apply; nothing to roll back to)", res.Error)
		default:
			if rbErr := a.adapterRollback(lgName); rbErr == nil {
				res.RolledBack = true
				res.LastGoodProfile = lgName
				res.Error = fmt.Sprintf("%s (auto-rolled back to %s)", res.Error, lgName)
			} else {
				res.Error = fmt.Sprintf("%s (rollback to %s ALSO failed: %s)", res.Error, lgName, rbErr)
			}
		}
		return res, nil
	}

	// Success: record in history with rotation. Mark the profile as
	// the adapter's last-good so a later failed apply rolls back to
	// THIS one (dry-run real mode; the router adapter tracks it via
	// the UCI profile + service state).
	res.OK = true
	res.Profile = name
	if marker, ok := a.Adapter.(interface{ MarkHealthy(string) }); ok {
		marker.MarkHealthy(name)
	}
	rec := Record{Profile: name, StateHash: hash, AppliedAt: res.AppliedAt}
	if err := a.appendHistory(rec); err != nil {
		// The apply itself succeeded; history failure is surfaced.
		return res, err
	}
	return res, nil
}

// adapterRollback re-activates the named profile and re-checks health.
func (a *Applyer) adapterRollback(name string) error {
	if err := a.Adapter.Activate(name); err != nil {
		return err
	}
	return a.Adapter.Health(HealthTimeout)
}

// appendHistory adds rec and trims the history to HistorySize,
// oldest dropped (FR-6.3 rotation).
func (a *Applyer) appendHistory(rec Record) error {
	recs := a.loadHistory()
	recs = append(recs, rec)
	if len(recs) > HistorySize {
		recs = recs[len(recs)-HistorySize:]
	}
	return a.saveHistory(recs)
}

// Rollback re-applies the previous successful profile (FR-6.3
// "Откатить к предыдущему" button). stepBack selects which record:
// 1 = the one before the newest (the usual case), 2+ for deeper steps.
func (a *Applyer) Rollback(stepBack int) (Record, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if stepBack < 1 {
		stepBack = 1
	}
	recs := a.loadHistory()
	if len(recs) == 0 {
		return Record{}, fmt.Errorf("apply: no applied profiles to roll back to")
	}
	idx := len(recs) - 1 - stepBack
	if idx < 0 {
		return Record{}, fmt.Errorf("apply: only %d applied profiles in history, cannot step back %d", len(recs), stepBack)
	}
	rec := recs[idx]

	// Re-activate the old profile and require it to become healthy.
	if err := a.adapterRollback(rec.Profile); err != nil {
		return rec, fmt.Errorf("apply: rollback to %s failed: %w", rec.Profile, err)
	}

	// The rolled-back state becomes the new "last applied": append a
	// record with the ORIGINAL state hash so the pending indicator
	// compares against the restored configuration.
	newRec := Record{
		Profile:   rec.Profile,
		StateHash: rec.StateHash,
		AppliedAt: time.Now(),
	}
	if err := a.appendHistory(newRec); err != nil {
		return newRec, err
	}
	return newRec, nil
}
