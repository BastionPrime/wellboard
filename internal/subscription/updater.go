package subscription

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wellboard/wellboard/internal/hwid"
	"github.com/wellboard/wellboard/internal/model"
)

// Store is the persistence contract the Updater needs (implemented by
// *store.Store).
type Store interface {
	Load() (*model.State, error)
	Save(st *model.State) error
}

// Updater performs subscription update rounds: fetch every enabled
// subscription, parse, merge into the state, persist. Failed sources
// record last_error and KEEP their servers (5.7.4: network failure never
// deletes the old list; NFR-5).
type Updater struct {
	// Fetcher pulls subscription bodies (production: *Client).
	Fetcher Fetcher
	// Store persists the merged state between rounds.
	Store Store
	// HWID is the device hardware ID sent with every request (FR-2.3).
	// When empty, the updater lazily resolves it via the hwid Manager.
	HWID string
	// HWIDManager resolves the HWID when HWID is empty; nil disables
	// lazy resolution (Update then fails with errNoHWID).
	HWIDManager *hwid.Manager
	// Device supplies the optional FR-2.3 headers; when nil, the device
	// facts are discovered from the router filesystem.
	Device *DeviceInfo
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
	// Log receives human-readable update traces (never URLs — guardrail 5).
	Log func(format string, args ...any)
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) logf(format string, args ...any) {
	if u.Log != nil {
		u.Log(format, args...)
	}
}

// resolveHWID returns the HWID to send: the explicit field, else the
// manager's persistent identity.
func (u *Updater) resolveHWID() (string, error) {
	if u.HWID != "" {
		return u.HWID, nil
	}
	if u.HWIDManager == nil {
		return "", errNoHWID
	}
	id, err := u.HWIDManager.Load()
	if err != nil {
		return "", fmt.Errorf("subscription: resolve hwid: %w", err)
	}
	if !hwid.ValidPattern.MatchString(id.HWID) {
		return "", fmt.Errorf("subscription: hwid %q fails validation", id.HWID)
	}
	return id.HWID, nil
}

// resolveDevice returns the FR-2.3 device headers.
func (u *Updater) resolveDevice() DeviceInfo {
	if u.Device != nil {
		return *u.Device
	}
	d := hwid.DiscoverDevice()
	return DeviceInfo{OS: d.OS, OSVersion: d.OSVersion, Model: d.Model}
}

// UpdateResult reports per-source outcomes of one round.
type UpdateResult struct {
	// Errors maps source ID → human-readable error (absent = success).
	Errors map[string]string
	// Servers maps source ID → live (non-stale) server count after merge.
	Servers map[string]int
}

// Update runs one update round for all enabled subscription sources.
// Per-source failures are recorded in the result (and in each source's
// last_error); only a total store failure returns a non-nil error.
func (u *Updater) Update(ctx context.Context) (*UpdateResult, error) {
	st, err := u.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("subscription: load state: %w", err)
	}

	hwidVal, err := u.resolveHWID()
	if err != nil {
		return nil, err
	}
	dev := u.resolveDevice()

	res := &UpdateResult{Errors: map[string]string{}, Servers: map[string]int{}}
	now := u.now()
	dirty := false

	for i, src := range st.Sources {
		if src.Kind != string(model.SourceSubscription) || !src.Enabled || src.URL == "" {
			continue
		}
		body, hdr, err := u.Fetcher.Fetch(ctx, src.URL, hwidVal, dev)
		if err != nil {
			st.Sources[i].LastError = errString(err)
			st.Sources[i].LastUpdate = now.UTC().Format(time.RFC3339)
			res.Errors[src.ID] = err.Error()
			u.logf("source %s: fetch failed: %v", src.ID, err)
			dirty = true
			continue
		}

		proxies, perr := ParseBody(body)
		if perr != nil {
			st.Sources[i].LastError = errString(perr)
			st.Sources[i].LastUpdate = now.UTC().Format(time.RFC3339)
			res.Errors[src.ID] = perr.Error()
			u.logf("source %s: parse failed: %v", src.ID, perr)
			dirty = true
			continue
		}

		merge := Merge(st, src.ID, proxies)

		st.Sources[i].LastError = nil
		st.Sources[i].LastUpdate = now.UTC().Format(time.RFC3339)
		st.Sources[i].UserInfo = ParseUserInfo(hdr.Userinfo)
		st.Sources[i].Announce = announcePtr(DecodeMaybeBase64(hdr.Announce))
		st.Sources[i].HWIDStatus = hwidStatusOf(hdr)
		// FR-1.1: the panel interval is the DEFAULT; a user-set value wins.
		if st.Sources[i].UpdateIntervalSec == 0 {
			if iv, ok := ParseIntervalSec(hdr.UpdateInterval); ok {
				st.Sources[i].UpdateIntervalSec = iv
			} else {
				st.Sources[i].UpdateIntervalSec = DefaultIntervalSec
			}
		}
		res.Servers[src.ID] = countLive(st, src.ID)
		u.logf("source %s: +%d ~%d stale %d del %d", src.ID,
			merge.Added, merge.Updated, merge.Stale, merge.Deleted)
		dirty = true
	}

	if !dirty {
		return res, nil
	}
	if err := u.Store.Save(st); err != nil {
		return res, fmt.Errorf("subscription: save state: %w", err)
	}
	return res, nil
}

// UpdateOne refreshes a single subscription source by ID (manual update
// button, FR-1.4). A source-level failure records last_error and returns
// nil error (state is still saved).
func (u *Updater) UpdateOne(ctx context.Context, sourceID string) (*UpdateResult, error) {
	st, err := u.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("subscription: load state: %w", err)
	}
	idx := -1
	for i, src := range st.Sources {
		if src.ID == sourceID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("subscription: source %s not found", sourceID)
	}
	if st.Sources[idx].Kind != string(model.SourceSubscription) {
		return nil, fmt.Errorf("subscription: source %s is not a subscription", sourceID)
	}

	hwidVal, err := u.resolveHWID()
	if err != nil {
		return nil, err
	}
	dev := u.resolveDevice()

	res := &UpdateResult{Errors: map[string]string{}, Servers: map[string]int{}}
	src := st.Sources[idx]

	body, hdr, ferr := u.Fetcher.Fetch(ctx, src.URL, hwidVal, dev)
	if ferr != nil {
		st.Sources[idx].LastError = errString(ferr)
		st.Sources[idx].LastUpdate = u.now().UTC().Format(time.RFC3339)
		_ = u.Store.Save(st)
		res.Errors[sourceID] = ferr.Error()
		return res, nil
	}
	proxies, perr := ParseBody(body)
	if perr != nil {
		st.Sources[idx].LastError = errString(perr)
		st.Sources[idx].LastUpdate = u.now().UTC().Format(time.RFC3339)
		_ = u.Store.Save(st)
		res.Errors[sourceID] = perr.Error()
		return res, nil
	}

	Merge(st, sourceID, proxies)
	st.Sources[idx].LastError = nil
	st.Sources[idx].LastUpdate = u.now().UTC().Format(time.RFC3339)
	st.Sources[idx].UserInfo = ParseUserInfo(hdr.Userinfo)
	st.Sources[idx].Announce = announcePtr(DecodeMaybeBase64(hdr.Announce))
	st.Sources[idx].HWIDStatus = hwidStatusOf(hdr)
	if st.Sources[idx].UpdateIntervalSec == 0 {
		if iv, ok := ParseIntervalSec(hdr.UpdateInterval); ok {
			st.Sources[idx].UpdateIntervalSec = iv
		} else {
			st.Sources[idx].UpdateIntervalSec = DefaultIntervalSec
		}
	}
	res.Servers[sourceID] = countLive(st, sourceID)

	if err := u.Store.Save(st); err != nil {
		return res, fmt.Errorf("subscription: save state: %w", err)
	}
	return res, nil
}

// hwidStatusOf maps the parsed x-hwid-* headers onto the model (FR-2.4).
func hwidStatusOf(hdr ResponseHeaders) *model.HWIDStatus {
	st := &model.HWIDStatus{
		Active:       hdr.HWIDActive,
		LimitReached: hdr.HWIDMaxDevices || hdr.HWIDLimit,
		NotSupported: hdr.HWIDNotSupp,
	}
	if !st.Active && !st.LimitReached && !st.NotSupported {
		return nil // no HWID signals on this response
	}
	return st
}

// announcePtr returns a *string for the source announce field.
func announcePtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// countLive counts non-stale servers of the source.
func countLive(st *model.State, sourceID string) int {
	n := 0
	for _, s := range st.Servers {
		if s.SourceID == sourceID && !s.Stale {
			n++
		}
	}
	return n
}

// errString wraps an error for the state's last_error field. Guardrail 5:
// subscription URLs are secrets — anything that looks like a URL fragment
// leaking into a network error is redacted to its host.
func errString(err error) *string {
	s := err.Error()
	if i := strings.Index(s, "http"); i >= 0 {
		s = s[:i] + "[url redacted]"
	}
	return &s
}
