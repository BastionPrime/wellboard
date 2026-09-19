package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/hwid"
	"github.com/wellboard/wellboard/internal/model"
)

// ----------------------------------------------------------------------------
// Header decoding
// ----------------------------------------------------------------------------

func TestParseUserInfo(t *testing.T) {
	ui := ParseUserInfo("upload=1000; download=2000; total=107374182400; expire=1893456000")
	if ui == nil {
		t.Fatal("nil userinfo")
	}
	if ui.Upload != 1000 || ui.Download != 2000 || ui.Total != 107374182400 || ui.Expire != 1893456000 {
		t.Fatalf("bad userinfo: %+v", ui)
	}
}

func TestParseUserInfoMalformed(t *testing.T) {
	ui := ParseUserInfo("garbage; upload=abc; download=5; =10; x=")
	if ui == nil {
		t.Fatal("partial parse must still return a struct")
	}
	if ui.Download != 5 {
		t.Fatalf("download must parse, got %d", ui.Download)
	}
	if ui.Upload != 0 || ui.Total != 0 || ui.Expire != 0 {
		t.Fatalf("other fields must stay 0: %+v", ui)
	}
	if ParseUserInfo("") != nil {
		t.Fatal("empty string must yield nil")
	}
	if ParseUserInfo("   ") != nil {
		t.Fatal("blank string must yield nil")
	}
}

func TestParseIntervalSec(t *testing.T) {
	cases := []struct {
		in   string
		sec  int
		want bool
	}{
		{"3600", 3600, true}, // seconds (Remnawave)
		{"12", 43200, true},  // hours convention (clash)
		{"", 0, false},
		{"abc", 0, false},
		{"-5", 0, false},
		{"0", 0, false},
	}
	for _, c := range cases {
		sec, ok := ParseIntervalSec(c.in)
		if sec != c.sec || ok != c.want {
			t.Errorf("ParseIntervalSec(%q) = (%d,%v), want (%d,%v)", c.in, sec, ok, c.sec, c.want)
		}
	}
}

func TestDecodeMaybeBase64(t *testing.T) {
	plain := "Scheduled maintenance"
	b64 := "base64:" + base64.StdEncoding.EncodeToString([]byte(plain))
	if got := DecodeMaybeBase64(b64); got != plain {
		t.Fatalf("got %q", got)
	}
	if got := DecodeMaybeBase64(plain); got != plain {
		t.Fatalf("plain must pass through: %q", got)
	}
	if got := DecodeMaybeBase64(""); got != "" {
		t.Fatalf("empty must stay empty: %q", got)
	}
	if got := DecodeMaybeBase64("base64:!!!not-base64!!!"); got != "base64:!!!not-base64!!!" {
		t.Fatalf("undecodable must pass through unchanged: %q", got)
	}
	// Raw (no padding) base64 payload is also handled.
	raw := base64.RawStdEncoding.EncodeToString([]byte("hi"))
	if got := DecodeMaybeBase64("base64:" + raw); got != "hi" {
		t.Fatalf("raw std base64: got %q", got)
	}
}

// ----------------------------------------------------------------------------
// Body parsing
// ----------------------------------------------------------------------------

const yamlBody = `proxies:
  - name: NL-1
    type: ss
    server: 203.0.113.10
    port: 8388
    cipher: aes-256-gcm
    password: "pass1"
    udp: true
  - name: NL-2
    type: ss
    server: 203.0.113.11
    port: 8388
    cipher: aes-256-gcm
    password: "pass2"
    udp: true
proxy-groups:
  - name: PG
    type: select
    proxies: [NL-1, NL-2]
`

func TestParseBodyYAML(t *testing.T) {
	proxies, err := ParseBody([]byte(yamlBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 {
		t.Fatalf("want 2 proxies, got %d", len(proxies))
	}
	if proxies[0]["name"] != "NL-1" {
		t.Fatalf("bad proxy: %v", proxies[0])
	}
	// yaml.v3 decodes 8388 as int.
	if proxies[0]["port"] != 8388 {
		t.Fatalf("port type: %T = %v", proxies[0]["port"], proxies[0]["port"])
	}
}

func TestParseBodyBase64List(t *testing.T) {
	links := "vless://8f1c4d2a-3e5f-6a7b-8c9d-0e1f2a3b4c5d@example.com:443?encryption=none&security=tls&sni=example.com&type=ws&path=%2Fws#Node%20A\n" +
		"ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example2.com:8388#Node%20B\n"
	body := base64.StdEncoding.EncodeToString([]byte(links))
	proxies, err := ParseBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 {
		t.Fatalf("want 2, got %d", len(proxies))
	}
	if proxies[0]["name"] != "Node A" {
		t.Fatalf("url-escaped fragment must be decoded: %q", proxies[0]["name"])
	}
}

func TestParseBodyPlainList(t *testing.T) {
	links := "trojan://pass123@9.8.7.6:443?sni=tro.example.com#TR1\n"
	proxies, err := ParseBody([]byte(links))
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 1 || proxies[0]["type"] != "trojan" {
		t.Fatalf("bad: %v", proxies)
	}
}

func TestParseBodyYAMLWithoutProxiesKey(t *testing.T) {
	// A YAML document that is not a clash config must fall through to the
	// link parser (and then fail as unknown format).
	if _, err := ParseBody([]byte("foo: bar\n")); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("want ErrUnknownFormat, got %v", err)
	}
}

func TestParseBodyEmpty(t *testing.T) {
	if _, err := ParseBody(nil); !errors.Is(err, ErrEmptySubscription) {
		t.Fatalf("want ErrEmptySubscription, got %v", err)
	}
}

func TestParseBodyGarbage(t *testing.T) {
	if _, err := ParseBody([]byte("plain text, definitely not a subscription")); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("want ErrUnknownFormat, got %v", err)
	}
}

// ----------------------------------------------------------------------------
// Merge logic
// ----------------------------------------------------------------------------

func testState() *model.State {
	return &model.State{
		Version: 2,
		Sources: []model.Source{
			{ID: "sub_1", Kind: "subscription", Name: "Sub", Enabled: true, URL: "http://x/sub"},
			{ID: "src_manual", Kind: "manual", Name: "Manual"},
		},
	}
}

func TestMergeFresh(t *testing.T) {
	st := testState()
	proxies := []map[string]any{
		{"name": "NL-1", "type": "ss", "server": "203.0.113.10", "port": 8388, "cipher": "x", "password": "y"},
		{"name": "NL-2", "type": "ss", "server": "203.0.113.11", "port": 8388, "cipher": "x", "password": "y"},
	}
	res := Merge(st, "sub_1", proxies)
	if res.Added != 2 || res.Updated != 0 {
		t.Fatalf("bad result: %+v", res)
	}
	if len(st.Servers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(st.Servers))
	}
	if st.Servers[0].ID == st.Servers[1].ID {
		t.Fatal("server ids must be unique")
	}
	for _, s := range st.Servers {
		if s.SourceID != "sub_1" || s.Stale {
			t.Fatalf("bad server: %+v", s)
		}
	}
}

func TestMergeStableIDOnReupdate(t *testing.T) {
	st := testState()
	p := map[string]any{"name": "NL-1", "type": "ss", "server": "203.0.113.10", "port": 8388}
	Merge(st, "sub_1", []map[string]any{p})
	id1 := st.Servers[0].ID

	st2 := testState()
	Merge(st2, "sub_1", []map[string]any{p})
	if st2.Servers[0].ID != id1 {
		t.Fatalf("IDs must be deterministic across states: %s vs %s", id1, st2.Servers[0].ID)
	}
}

func TestMergeUpdateKeepsIDAndDelay(t *testing.T) {
	st := testState()
	Merge(st, "sub_1", []map[string]any{
		{"name": "NL-1", "type": "ss", "server": "203.0.113.10", "port": 8388, "password": "old"},
	})
	st.Servers[0].DelayMS = 42
	st.Routes = []model.Route{{ID: "rt_1", Target: model.Target{Type: model.TargetServer, ID: st.Servers[0].ID}}}

	// Same key, changed raw.
	res := Merge(st, "sub_1", []map[string]any{
		{"name": "NL-1", "type": "ss", "server": "203.0.113.10", "port": 8388, "password": "new"},
	})
	if res.Updated != 1 || res.Added != 0 {
		t.Fatalf("bad result: %+v", res)
	}
	srv := st.Servers[0]
	if srv.DelayMS != 42 {
		t.Fatal("delay must survive the update")
	}
	if srv.Raw["password"] != "new" {
		t.Fatalf("raw must be refreshed: %v", srv.Raw)
	}
	if st.Routes[0].Target.ID != srv.ID {
		t.Fatal("route reference must stay valid (FR-4.8)")
	}
}

func TestMergeVanishedGoesStaleThenDeleted(t *testing.T) {
	st := testState()
	Merge(st, "sub_1", []map[string]any{
		{"name": "NL-1", "type": "ss", "server": "203.0.113.10", "port": 8388},
		{"name": "NL-2", "type": "ss", "server": "203.0.113.11", "port": 8388},
	})
	victim := st.Servers[0].ID

	// Update 1: NL-1 vanished → stale, miss=1.
	res := Merge(st, "sub_1", []map[string]any{
		{"name": "NL-2", "type": "ss", "server": "203.0.113.11", "port": 8388},
	})
	if res.Stale != 1 || res.Deleted != 0 {
		t.Fatalf("update 1: %+v", res)
	}
	if !st.Servers[0].Stale || st.Servers[0].StaleMisses != 1 || st.Servers[0].ID != victim {
		t.Fatalf("update 1 server: %+v", st.Servers[0])
	}

	// Update 2: still gone → miss=2, still present.
	Merge(st, "sub_1", []map[string]any{
		{"name": "NL-2", "type": "ss", "server": "203.0.113.11", "port": 8388},
	})
	if len(st.Servers) != 2 || st.Servers[0].StaleMisses != 2 {
		t.Fatalf("update 2 servers: %+v", st.Servers)
	}

	// Update 3: still gone → deleted (5.7.5: delete after 3 updates).
	res = Merge(st, "sub_1", []map[string]any{
		{"name": "NL-2", "type": "ss", "server": "203.0.113.11", "port": 8388},
	})
	if res.Deleted != 1 || len(st.Servers) != 1 {
		t.Fatalf("update 3: %+v, servers=%d", res, len(st.Servers))
	}
	if st.Servers[0].ID == victim {
		t.Fatal("the vanished server must be gone")
	}
}

func TestMergeStaleServerResurrected(t *testing.T) {
	st := testState()
	p1 := map[string]any{"name": "NL-1", "type": "ss", "server": "203.0.113.10", "port": 8388}
	p2 := map[string]any{"name": "NL-2", "type": "ss", "server": "203.0.113.11", "port": 8388}
	Merge(st, "sub_1", []map[string]any{p1, p2})
	id1 := st.Servers[0].ID

	// NL-1 misses one update.
	Merge(st, "sub_1", []map[string]any{p2})
	if !st.Servers[0].Stale {
		t.Fatal("NL-1 must be stale")
	}

	// NL-1 comes back: same ID, stale cleared, counter reset.
	res := Merge(st, "sub_1", []map[string]any{p1, p2})
	if res.Updated != 2 {
		t.Fatalf("bad result: %+v", res)
	}
	var srv model.Server
	for _, s := range st.Servers {
		if s.ID == id1 {
			srv = s
		}
	}
	if srv.ID == "" {
		t.Fatal("resurrected server must keep its id")
	}
	if srv.Stale || srv.StaleMisses != 0 {
		t.Fatalf("resurrected server must be fresh: %+v", srv)
	}
}

func TestMergeOtherSourcesUntouched(t *testing.T) {
	st := testState()
	st.Servers = append(st.Servers, model.Server{
		ID: "srv_manual_1", SourceID: "src_manual", Name: "MAN", Type: "ss",
		Raw: map[string]any{"server": "9.9.9.9", "port": 1},
	})
	Merge(st, "sub_1", []map[string]any{
		{"name": "NL-1", "type": "ss", "server": "203.0.113.10", "port": 8388},
	})
	// A round with no proxies for sub_1: NL-1 misses once → stale.
	res := Merge(st, "sub_1", nil)
	if res.Stale != 1 || res.Deleted != 0 {
		t.Fatalf("unexpected: %+v", res)
	}
	var manual model.Server
	for _, s := range st.Servers {
		if s.SourceID == "src_manual" {
			manual = s
		}
	}
	if manual.ID != "srv_manual_1" || manual.Stale || manual.StaleMisses != 0 {
		t.Fatalf("manual server must be untouched: %+v", manual)
	}
}

func TestMergePortTypeNormalization(t *testing.T) {
	// Port as int (yaml) vs string (link parser) must still match.
	st := testState()
	Merge(st, "sub_1", []map[string]any{
		{"name": "NL-1", "type": "ss", "server": "1.2.3.4", "port": 8388},
	})
	res := Merge(st, "sub_1", []map[string]any{
		{"name": "NL-1", "type": "ss", "server": "1.2.3.4", "port": "8388"},
	})
	if res.Updated != 1 || res.Added != 0 {
		t.Fatalf("port normalization broke matching: %+v", res)
	}
	if len(st.Servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(st.Servers))
	}
}

func TestMergeDuplicateEntriesInOneAnswer(t *testing.T) {
	st := testState()
	p := map[string]any{"name": "NL-1", "type": "ss", "server": "1.2.3.4", "port": 8388}
	res := Merge(st, "sub_1", []map[string]any{p, p})
	if res.Added != 1 {
		t.Fatalf("duplicates must be collapsed: %+v", res)
	}
}

func TestStaleServerIDs(t *testing.T) {
	st := testState()
	Merge(st, "sub_1", []map[string]any{
		{"name": "NL-1", "type": "ss", "server": "1.2.3.4", "port": 8388},
		{"name": "NL-2", "type": "ss", "server": "1.2.3.5", "port": 8388},
	})
	Merge(st, "sub_1", []map[string]any{
		{"name": "NL-2", "type": "ss", "server": "1.2.3.5", "port": 8388},
	})
	ids := StaleServerIDs(st, "sub_1")
	if len(ids) != 1 {
		t.Fatalf("want 1 stale id, got %v", ids)
	}
}

// ----------------------------------------------------------------------------
// HWID status mapping
// ----------------------------------------------------------------------------

func TestHwidStatusOf(t *testing.T) {
	if got := hwidStatusOf(ResponseHeaders{}); got != nil {
		t.Fatalf("no signals → nil, got %+v", got)
	}
	got := hwidStatusOf(ResponseHeaders{HWIDActive: true})
	if got == nil || !got.Active || got.LimitReached || got.NotSupported {
		t.Fatalf("bad: %+v", got)
	}
	got = hwidStatusOf(ResponseHeaders{HWIDMaxDevices: true})
	if got == nil || !got.LimitReached {
		t.Fatalf("bad: %+v", got)
	}
	got = hwidStatusOf(ResponseHeaders{HWIDLimit: true})
	if got == nil || !got.LimitReached {
		t.Fatalf("x-hwid-limit must map to LimitReached: %+v", got)
	}
}

// ----------------------------------------------------------------------------
// Mock fetcher — updater behavior without network
// ----------------------------------------------------------------------------

type mockFetcher struct {
	calls    int
	body     []byte
	hdr      ResponseHeaders
	err      error
	lastHWID string
	lastDev  DeviceInfo
}

func (f *mockFetcher) Fetch(ctx context.Context, url, hwidVal string, dev DeviceInfo) ([]byte, ResponseHeaders, error) {
	f.calls++
	f.lastHWID = hwidVal
	f.lastDev = dev
	return f.body, f.hdr, f.err
}

// fakeStore is an in-memory Store.
type fakeStore struct{ st *model.State }

func (s *fakeStore) Load() (*model.State, error) { return s.st, nil }
func (s *fakeStore) Save(st *model.State) error  { s.st = st; return nil }

func TestUpdaterNetworkErrorKeepsServers(t *testing.T) {
	st := testState()
	st.Servers = []model.Server{{
		ID: "srv_1", SourceID: "sub_1", Name: "NL-1", Type: "ss",
		Raw: map[string]any{"server": "1.2.3.4", "port": 8388},
	}}
	f := &mockFetcher{err: errors.New("network: connection refused")}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}

	res, err := u.Update(context.Background())
	if err != nil {
		t.Fatalf("round must not hard-fail: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("want 1 source error, got %v", res.Errors)
	}
	if len(st.Servers) != 1 || st.Servers[0].ID != "srv_1" {
		t.Fatalf("old servers must be kept on network error (5.7.4): %+v", st.Servers)
	}
	if st.Sources[0].LastError == nil {
		t.Fatal("last_error must be recorded")
	}
}

func TestUpdaterHeadersAppliedAndHWIDSent(t *testing.T) {
	st := testState()
	f := &mockFetcher{body: []byte(yamlBody)}
	u := &Updater{
		Fetcher: f, Store: &fakeStore{st},
		HWID:   "ABCDEFGHJKLM0123456789",
		Device: &DeviceInfo{OS: "OpenWrt", OSVersion: "24.10", Model: "BPI-R4"},
	}
	if _, err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.lastHWID != "ABCDEFGHJKLM0123456789" {
		t.Fatalf("hwid not sent: %q", f.lastHWID)
	}
	if !hwid.ValidPattern.MatchString(f.lastHWID) {
		t.Fatalf("hwid fails regex: %q", f.lastHWID)
	}
	if f.lastDev.OS != "OpenWrt" || f.lastDev.OSVersion != "24.10" || f.lastDev.Model != "BPI-R4" {
		t.Fatalf("device headers not passed: %+v", f.lastDev)
	}
}

func TestUpdaterSuccessUpdatesSource(t *testing.T) {
	st := testState()
	f := &mockFetcher{
		body: []byte(yamlBody),
		hdr: ResponseHeaders{
			Userinfo:       "upload=1; download=2; total=100; expire=1893456000",
			UpdateInterval: "3600",
			Title:          "base64:Rml4dHVyZQ==",
			Announce:       "hello",
			HWIDActive:     true,
		},
	}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}
	res, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if res.Servers["sub_1"] != 2 {
		t.Fatalf("want 2 servers, got %v", res.Servers)
	}
	src := st.Sources[0]
	if src.UserInfo == nil || src.UserInfo.Total != 100 {
		t.Fatalf("userinfo not stored: %+v", src.UserInfo)
	}
	if src.UpdateIntervalSec != 3600 {
		t.Fatalf("interval not stored: %d", src.UpdateIntervalSec)
	}
	if src.Announce == nil || *src.Announce != "hello" {
		t.Fatalf("announce not stored: %+v", src.Announce)
	}
	if src.HWIDStatus == nil || !src.HWIDStatus.Active {
		t.Fatalf("hwid status not stored: %+v", src.HWIDStatus)
	}
	if src.LastError != nil {
		t.Fatalf("last_error must be cleared: %v", src.LastError)
	}
	if src.LastUpdate == "" {
		t.Fatal("last_update must be set")
	}
}

func TestUpdaterDefaultIntervalApplied(t *testing.T) {
	st := testState()
	f := &mockFetcher{body: []byte(yamlBody)} // no interval header
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}
	if _, err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.Sources[0].UpdateIntervalSec != DefaultIntervalSec {
		t.Fatalf("default 12h interval expected, got %d", st.Sources[0].UpdateIntervalSec)
	}
}

func TestUpdaterUserIntervalWins(t *testing.T) {
	st := testState()
	st.Sources[0].UpdateIntervalSec = 60 // user pinned 1 minute
	f := &mockFetcher{body: []byte(yamlBody), hdr: ResponseHeaders{UpdateInterval: "3600"}}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}
	if _, err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.Sources[0].UpdateIntervalSec != 60 {
		t.Fatalf("user-set interval must win (FR-1.1): %d", st.Sources[0].UpdateIntervalSec)
	}
}

func TestUpdaterDisabledSourceSkipped(t *testing.T) {
	st := testState()
	st.Sources[0].Enabled = false
	f := &mockFetcher{body: []byte(yamlBody)}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}
	res, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("disabled source must not be fetched")
	}
	if len(res.Servers) != 0 {
		t.Fatalf("unexpected: %v", res.Servers)
	}
}

func TestUpdaterParseErrorKeepsServers(t *testing.T) {
	st := testState()
	st.Servers = []model.Server{{
		ID: "srv_1", SourceID: "sub_1", Name: "NL-1", Type: "ss",
		Raw: map[string]any{"server": "1.2.3.4", "port": 8388},
	}}
	f := &mockFetcher{body: []byte("not a subscription at all, nope")}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}
	res, err := u.Update(context.Background())
	if err != nil {
		t.Fatalf("round must not hard-fail: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("want parse error recorded: %v", res.Errors)
	}
	if len(st.Servers) != 1 {
		t.Fatal("parse failure must keep old servers")
	}
}

func TestUpdaterUpdateOne(t *testing.T) {
	st := testState()
	f := &mockFetcher{body: []byte(yamlBody)}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}
	res, err := u.UpdateOne(context.Background(), "sub_1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Servers["sub_1"] != 2 {
		t.Fatalf("want 2 servers, got %v", res.Servers)
	}

	if _, err := u.UpdateOne(context.Background(), "nope"); err == nil {
		t.Fatal("unknown source must error")
	}
	if _, err := u.UpdateOne(context.Background(), "src_manual"); err == nil {
		t.Fatal("manual source must be rejected")
	}
}

func TestUpdaterNoHWIDFails(t *testing.T) {
	st := testState()
	u := &Updater{Fetcher: &mockFetcher{body: []byte(yamlBody)}, Store: &fakeStore{st}}
	if _, err := u.Update(context.Background()); err == nil {
		t.Fatal("missing hwid must fail")
	}
}

func TestErrStringRedactsURLs(t *testing.T) {
	e := errString(errors.New(`Get "https://panel.example/sub?token=SECRET": dial tcp: refused`))
	if strings.Contains(*e, "SECRET") {
		t.Fatalf("url must be redacted (guardrail 5): %q", *e)
	}
	if !strings.Contains(*e, "[url redacted]") {
		t.Fatalf("redaction marker missing: %q", *e)
	}
}

func TestMihomoPathCandidates(t *testing.T) {
	cases := []struct {
		in, first string
		n         int
	}{
		{"https://p.io/api/sub/UUID123", "https://p.io/api/sub/UUID123/mihomo", 2},
		{"https://p.io/api/sub/UUID123/mihomo", "https://p.io/api/sub/UUID123/mihomo", 1},
		{"https://p.io/api/sub/UUID123?x=1", "https://p.io/api/sub/UUID123/mihomo?x=1", 2},
		{"https://p.io/api/sub/UUID123/clash", "https://p.io/api/sub/UUID123/clash", 1},
		{"https://p.io/api/sub/UUID123/json", "https://p.io/api/sub/UUID123/json", 1},
	}
	for _, c := range cases {
		got := mihomoPathCandidates(c.in)
		if len(got) != c.n || got[0] != c.first {
			t.Errorf("mihomoPathCandidates(%q) = %v, want first %q n=%d", c.in, got, c.first, c.n)
		}
	}
}

// ----------------------------------------------------------------------------
// Review follow-up fixes (B1 redaction, B2 cadence gate)
// ----------------------------------------------------------------------------

// TestUpdateResultErrorsRedacted (B1): UpdateResult.Errors and the Log
// callback must never contain the subscription URL — the same errString
// redaction that guards last_error (guardrail 5).
func TestUpdateResultErrorsRedacted(t *testing.T) {
	st := testState()
	f := &mockFetcher{err: errors.New(`subscription: network: Get "https://panel.example/sub?token=SECRET": dial tcp: refused`)}
	var logged strings.Builder
	u := &Updater{
		Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789",
		Log: func(format string, args ...any) { fmt.Fprintf(&logged, format, args...) },
	}
	res, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Errors["sub_1"], "SECRET") || strings.Contains(res.Errors["sub_1"], "http") {
		t.Fatalf("UpdateResult.Errors leaks a URL: %q", res.Errors["sub_1"])
	}
	if strings.Contains(logged.String(), "SECRET") || strings.Contains(logged.String(), "http") {
		t.Fatalf("Log callback leaks a URL: %q", logged.String())
	}
	if !strings.Contains(res.Errors["sub_1"], "[url redacted]") {
		t.Fatalf("redaction marker expected: %q", res.Errors["sub_1"])
	}
}

// TestUpdateOneErrorsRedacted (B1): the manual per-source update path gets
// the same redaction.
func TestUpdateOneErrorsRedacted(t *testing.T) {
	st := testState()
	f := &mockFetcher{err: errors.New(`subscription: network: Get "https://panel.example/sub?token=SECRET": dial tcp: refused`)}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}
	res, err := u.UpdateOne(context.Background(), "sub_1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Errors["sub_1"], "SECRET") || strings.Contains(res.Errors["sub_1"], "http") {
		t.Fatalf("UpdateOne result leaks a URL: %q", res.Errors["sub_1"])
	}
}

// TestUpdaterSkipsNotDueSource (B2): a scheduled round must skip a source
// whose per-source interval has not elapsed; a forced round must fetch it.
func TestUpdaterSkipsNotDueSource(t *testing.T) {
	st := testState()
	st.Sources[0].UpdateIntervalSec = 43200
	st.Sources[0].LastUpdate = time.Now().UTC().Format(time.RFC3339)
	f := &mockFetcher{body: []byte(yamlBody)}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}

	// Scheduled: skipped (not due).
	res, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 || len(res.Servers) != 0 {
		t.Fatalf("not-due source must be skipped: calls=%d servers=%v", f.calls, res.Servers)
	}
	// Forced: fetched.
	res, err = u.UpdateForced(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || res.Servers["sub_1"] != 2 {
		t.Fatalf("forced round must fetch: calls=%d servers=%v", f.calls, res.Servers)
	}
}

// TestUpdaterDueAfterInterval (B2): once the interval has elapsed, a
// scheduled round fetches again.
func TestUpdaterDueAfterInterval(t *testing.T) {
	st := testState()
	st.Sources[0].UpdateIntervalSec = 60
	st.Sources[0].LastUpdate = time.Now().UTC().Add(-90 * time.Second).Format(time.RFC3339)
	f := &mockFetcher{body: []byte(yamlBody)}
	u := &Updater{Fetcher: f, Store: &fakeStore{st}, HWID: "ABCDEFGHJKLM0123456789"}
	if _, err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("elapsed interval must allow a fetch, got %d", f.calls)
	}
}

// TestIntervalElapsed parses the due-gate edge cases.
func TestIntervalElapsed(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		last     string
		interval int
		want     bool
	}{
		{"no last update", "", 43200, true},
		{"no interval", now.Format(time.RFC3339), 0, true},
		{"garbage timestamp", "not-a-time", 43200, true},
		{"not due", now.Format(time.RFC3339), 43200, false},
		{
			"due (interval passed)",
			now.Add(-43300 * time.Second).Format(time.RFC3339), 43200, true,
		},
	}
	for _, c := range cases {
		got := intervalElapsed(model.Source{LastUpdate: c.last, UpdateIntervalSec: c.interval}, now)
		if got != c.want {
			t.Errorf("%s: intervalElapsed = %v, want %v", c.name, got, c.want)
		}
	}
}
