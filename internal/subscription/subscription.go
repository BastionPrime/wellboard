// Package subscription fetches, parses and merges proxy subscriptions
// (initial TZ 5.7 / FR-1).
//
// Fetch strategy (docs/DECISIONS.md R1-R3, R6):
//   - UA is "WellBoard/<version> mihomo" — no vanilla Remnawave rule matches
//     it (R3), so when the URL path has no clientType segment we first try
//     <base>/mihomo (the guaranteed path-based format selector, R2);
//     the plain URL is the fallback.
//   - Requests carry the HWID headers (FR-2.3): x-hwid (required),
//     x-device-os / x-ver-os / x-device-model.
//   - 20s timeout, 2 retries with linear backoff; network errors never
//     remove the previously stored servers (NFR-5 / 5.7.4).
//
// Parsing (5.7.2): YAML with a proxies: list wins; otherwise the body is a
// base64 or plain list of share links converted via internal/convert.
//
// Merge (5.7.5): servers are matched by (source_id, name, server, port);
// servers absent from a successful update become stale, and stale servers
// are dropped after 3 consecutive missed updates. Routes keep referencing
// the IDs (FR-4.8); the generator reports "target lost" for stale ones.
//
// The Updater binds the pure fetch/merge pipeline to the store; the
// scheduler (internal/scheduler) drives it on a ticker.
package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wellboard/wellboard/internal/convert"
	"github.com/wellboard/wellboard/internal/model"
	"gopkg.in/yaml.v3"
)

// DefaultUserAgent is the UA template (initial TZ 5.7.1; R3 strategy).
const DefaultUserAgent = "WellBoard/%s mihomo"

// FetchTimeout is the per-request timeout (initial TZ 5.7.4: 20s).
const FetchTimeout = 20 * time.Second

// Retries is the number of retry attempts after the first request
// (initial TZ 5.7.4: "up to 2 retries").
const Retries = 2

// StaleAfterUpdates is how many consecutive updates a vanished server
// survives as stale before being deleted (initial TZ 5.7.5: 3).
const StaleAfterUpdates = 3

// DefaultIntervalSec is the fallback auto-update interval (FR-1.1: 12h
// when no profile-update-interval header is given).
const DefaultIntervalSec = 12 * 60 * 60

// maxBodyBytes caps the subscription body (defensive; a panel answer is
// kilobytes-to-megabytes).
const maxBodyBytes = 64 << 20

// errNoHWID is a programming error: the updater was built without an HWID.
var errNoHWID = errors.New("subscription: no HWID configured")

// ----------------------------------------------------------------------------
// Fetch layer
// ----------------------------------------------------------------------------

// FetchResult is what one subscription fetch yields.
type FetchResult struct {
	Body []byte
	// Proxies is the parsed proxy list (nil until ParseBody runs).
	Proxies []map[string]any
	// UserInfo is the decoded subscription-userinfo (nil when absent).
	UserInfo *model.UserInfo
	// UpdateIntervalSec is the parsed profile-update-interval (0 when
	// absent or invalid).
	UpdateIntervalSec int
	// Title is the decoded profile-title ("" when absent).
	Title string
	// Announce is the decoded announce message ("" when absent).
	Announce string
	// HWIDStatus mirrors the x-hwid-* headers (FR-2.4).
	HWIDStatus *model.HWIDStatus
}

// Fetcher performs subscription HTTP requests. An interface so tests and
// the production stack can be swapped.
type Fetcher interface {
	// Fetch retrieves the subscription body and response headers, applying
	// the timeout/retry policy and the /mihomo path strategy.
	Fetch(ctx context.Context, url, hwid string, dev DeviceInfo) (body []byte, hdr ResponseHeaders, err error)
}

// DeviceInfo carries the optional FR-2.3 headers.
type DeviceInfo struct {
	OS        string
	OSVersion string
	Model     string
}

// ResponseHeaders is the subset of response headers WellBoard interprets
// (initial TZ 5.7.3; docs/DECISIONS.md R5).
type ResponseHeaders struct {
	Userinfo       string // subscription-userinfo
	UpdateInterval string // profile-update-interval (seconds)
	Title          string // profile-title (plain or "base64:…")
	Announce       string // announce (plain or "base64:…")
	HWIDActive     bool   // x-hwid-active: true
	HWIDNotSupp    bool   // x-hwid-not-supported: true
	HWIDMaxDevices bool   // x-hwid-max-devices-reached: true
	HWIDLimit      bool   // x-hwid-limit: true
}

// KnownClientTypes mirrors Remnawave REQUEST_TEMPLATE_TYPE (R2):
// stash|singbox|mihomo|json|v2ray-json|clash.
var KnownClientTypes = []string{"stash", "singbox", "mihomo", "json", "v2ray-json", "clash"}

// FetchErrors returned by Client.Fetch.
var (
	// ErrNotFound: 404 without device-limit headers — the link itself is
	// bad or the account is gone (FR-2.4).
	ErrNotFound = errors.New("subscription not found: check the link is current")
	// ErrHWIDLimit: device-limit responses (FR-2.4).
	ErrHWIDLimit = errors.New("device limit reached on the subscription: remove an extra device in the user panel")
	// ErrHWIDNotSupported: panel did not accept our HWID (our bug, FR-2.4).
	ErrHWIDNotSupported = errors.New("subscription server did not accept the HWID headers")
	// ErrForbidden: 403 without device-limit context (R1: no response rule
	// matched — the admin must add one or the path clientType applies).
	ErrForbidden = errors.New("subscription server refused the request (403): the panel has no response rule for this client")
)

// Client is the production Fetcher.
type Client struct {
	// HTTP is the underlying client; nil means a default client with the
	// package timeout.
	HTTP *http.Client
	// UserAgent is the full UA string (default: DefaultUserAgent/dev).
	UserAgent string
	// Backoff is the base retry delay (linear ramp); tests shrink it.
	Backoff time.Duration
}

// NewClient builds a production fetcher. version lands in the UA
// (5.7.1: "WellBoard/<ver> mihomo").
func NewClient(version string) *Client {
	return &Client{
		UserAgent: fmt.Sprintf(DefaultUserAgent, version),
		Backoff:   2 * time.Second,
	}
}

// Fetch implements Fetcher with the R2/R3 strategy, 20s timeout and 2
// retries with linear backoff.
func (c *Client) Fetch(ctx context.Context, rawURL, hwid string, dev DeviceInfo) ([]byte, ResponseHeaders, error) {
	if hwid == "" {
		return nil, ResponseHeaders{}, errNoHWID
	}
	ua := c.UserAgent
	if ua == "" {
		ua = fmt.Sprintf(DefaultUserAgent, "dev")
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}

	candidates := mihomoPathCandidates(rawURL)
	var lastErr error
	for attempt := 0; attempt <= Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoff * time.Duration(attempt)):
			case <-ctx.Done():
				return nil, ResponseHeaders{}, ctx.Err()
			}
		}
		for i, url := range candidates {
			body, hdr, status, err := c.requestOnce(ctx, url, hwid, dev, ua)
			switch {
			case err != nil:
				lastErr = err // network error: retry
			case status == http.StatusOK:
				return body, hdr, nil
			case status == http.StatusNotFound && i == 0 && len(candidates) > 1:
				// The /mihomo candidate 404'd: try the plain URL right
				// away (R2 path may be disabled or the URL already has a
				// different shape) — not a retry.
				lastErr = classifyStatus(status, hdr)
			default:
				// Definitive answers (404 plain, 403, 401, hwid-limit
				// 404) are returned, not retried.
				return nil, hdr, classifyStatus(status, hdr)
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("subscription fetch failed")
	}
	return nil, ResponseHeaders{}, lastErr
}

// mihomoPathCandidates returns the URLs to try, in order (R2/R3): when the
// URL path ends with no clientType-like segment, try <base>/mihomo first,
// then the URL as given.
func mihomoPathCandidates(rawURL string) []string {
	trimmed := strings.TrimRight(rawURL, "/")
	if trimmed == "" || trimmed == "mihomo" || strings.HasSuffix(trimmed, "/mihomo") {
		return []string{rawURL}
	}
	// Preserve query parameters on the /mihomo variant.
	base, query := rawURL, ""
	if i := strings.Index(rawURL, "?"); i >= 0 {
		base, query = rawURL[:i], rawURL[i:]
	}
	seg := trimmed
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	for _, ct := range KnownClientTypes {
		if strings.EqualFold(seg, ct) {
			return []string{rawURL} // the URL already pins the format
		}
	}
	return []string{strings.TrimRight(base, "/") + "/mihomo" + query, rawURL}
}

// classifyStatus turns a non-200 status + its HWID headers into a stable
// error (FR-2.4 messages).
func classifyStatus(status int, hdr ResponseHeaders) error {
	switch status {
	case http.StatusNotFound:
		if hdr.HWIDMaxDevices || hdr.HWIDLimit || hdr.HWIDNotSupp {
			return fmt.Errorf("%w; also check the link is current", ErrHWIDLimit)
		}
		return ErrNotFound
	case http.StatusForbidden:
		if hdr.HWIDMaxDevices || hdr.HWIDLimit {
			return ErrHWIDLimit
		}
		return ErrForbidden
	case http.StatusUnauthorized:
		return fmt.Errorf("subscription server returned 401: check the link")
	default:
		if hdr.HWIDMaxDevices || hdr.HWIDLimit {
			return ErrHWIDLimit
		}
		return fmt.Errorf("subscription server returned HTTP %d", status)
	}
}

// requestOnce performs a single HTTP GET with the FR-2.3 header set.
func (c *Client) requestOnce(ctx context.Context, url, hwid string, dev DeviceInfo, ua string) ([]byte, ResponseHeaders, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ResponseHeaders{}, 0, fmt.Errorf("subscription: bad URL: %w", err)
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("x-hwid", hwid)
	if dev.OS != "" {
		req.Header.Set("x-device-os", dev.OS)
	}
	if dev.OSVersion != "" {
		req.Header.Set("x-ver-os", dev.OSVersion)
	}
	if dev.Model != "" {
		req.Header.Set("x-device-model", dev.Model)
	}

	hclient := c.HTTP
	if hclient == nil {
		hclient = &http.Client{Timeout: FetchTimeout}
	} else if hclient.Timeout == 0 {
		cp := *hclient
		cp.Timeout = FetchTimeout
		hclient = &cp
	}

	resp, err := hclient.Do(req)
	if err != nil {
		return nil, ResponseHeaders{}, 0, fmt.Errorf("subscription: network: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, ResponseHeaders{}, resp.StatusCode, fmt.Errorf("subscription: read body: %w", err)
	}
	return body, parseHeaders(resp.Header), resp.StatusCode, nil
}

// parseHeaders extracts the 5.7.3 header set.
func parseHeaders(h http.Header) ResponseHeaders {
	boolHeader := func(name string) bool {
		return strings.EqualFold(strings.TrimSpace(h.Get(name)), "true")
	}
	return ResponseHeaders{
		Userinfo:       h.Get("subscription-userinfo"),
		UpdateInterval: h.Get("profile-update-interval"),
		Title:          h.Get("profile-title"),
		Announce:       h.Get("announce"),
		HWIDActive:     boolHeader("x-hwid-active"),
		HWIDNotSupp:    boolHeader("x-hwid-not-supported"),
		HWIDMaxDevices: boolHeader("x-hwid-max-devices-reached"),
		HWIDLimit:      boolHeader("x-hwid-limit"),
	}
}

// ----------------------------------------------------------------------------
// Header value decoding
// ----------------------------------------------------------------------------

// ParseUserInfo decodes "upload=…; download=…; total=…; expire=…" into the
// model UserInfo (FR-7.1). Nil when the string is empty.
func ParseUserInfo(s string) *model.UserInfo {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	ui := &model.UserInfo{}
	for _, kv := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "upload":
			ui.Upload = n
		case "download":
			ui.Download = n
		case "total":
			ui.Total = n
		case "expire":
			ui.Expire = n
		}
	}
	return ui
}

// ParseIntervalSec parses profile-update-interval into seconds. Remnawave
// sends seconds; some providers send hours (clash convention: a value under
// 24 is hours). Non-positive or malformed values keep the default (ok=false).
func ParseIntervalSec(s string) (sec int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n < 24 { // "12" → 12 hours (clash convention)
		return n * 3600, true
	}
	return n, true
}

// DecodeMaybeBase64 unwraps "base64:<payload>" values (profile-title and
// announce may arrive base64-wrapped or plain, R5). Undecodable payloads
// are returned as-is — better a weird title than a lost one.
func DecodeMaybeBase64(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(s), "base64:") {
		return s
	}
	raw := s[len("base64:"):]
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(raw); err == nil {
			return string(b)
		}
	}
	return s
}

// ----------------------------------------------------------------------------
// Body parsing
// ----------------------------------------------------------------------------

// ErrUnknownFormat: the body parses as neither YAML-with-proxies nor a
// share-link list (5.7.2).
var ErrUnknownFormat = errors.New("unknown subscription format")

// ErrEmptySubscription: a successful fetch yielding zero proxies.
var ErrEmptySubscription = errors.New("subscription returned no proxies")

// ParseBody converts a subscription body into mihomo proxy maps (5.7.2):
// YAML with a proxies: list first, then share-link lists.
func ParseBody(body []byte) ([]map[string]any, error) {
	if len(body) == 0 {
		return nil, ErrEmptySubscription
	}
	if proxies, err := parseYAMLProxies(body); err == nil && len(proxies) > 0 {
		return proxies, nil
	}
	if proxies, err := convert.Links(body); err == nil {
		return proxies, nil
	}
	return nil, fmt.Errorf("%w: neither clash YAML nor a share-link list", ErrUnknownFormat)
}

// parseYAMLProxies extracts the proxies list from a clash-style YAML
// document. A document without a proxies key yields (nil, nil) — "not this
// format". Malformed proxy entries are an error.
func parseYAMLProxies(body []byte) ([]map[string]any, error) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc.Proxies == nil {
		return nil, nil
	}
	out := make([]map[string]any, 0, len(doc.Proxies))
	for _, p := range doc.Proxies {
		if p == nil {
			continue
		}
		if _, ok := p["name"].(string); !ok {
			return nil, fmt.Errorf("subscription: yaml proxy without a name: %v", p)
		}
		out = append(out, p)
	}
	return out, nil
}
