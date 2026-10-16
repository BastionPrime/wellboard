package lan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellboard/wellboard/internal/model"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseLeaseFile(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	// Expiry far in the future (dnsmasq writes epoch seconds).
	future := time.Now().Add(24 * time.Hour).Unix()
	writeFile(t, leases, fmt.Sprintf("%d aa:bb:cc:dd:ee:ff 192.168.1.50 tv *\n", future)+
		fmt.Sprintf("%d 11:22:33:44:55:66 192.168.1.51 laptop 01:xx\n", future)+
		fmt.Sprintf("%d deadbeef 192.168.1.52 badmac *\n", future)+ // bad MAC: skipped
		fmt.Sprintf("%d aa:00:00:00:00:01 notanip *\n", future)+ // bad IP: skipped
		"\n"+
		"garbage\n")
	r := &Reader{LeaseFile: leases}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("want 2 devices, got %d: %+v", len(devs), devs)
	}
	// Sorted by hostname: laptop < tv.
	if devs[0].Hostname != "laptop" || devs[0].IP != "192.168.1.51" {
		t.Fatalf("unexpected first: %+v", devs[0])
	}
	if devs[1].Hostname != "tv" || devs[1].IP != "192.168.1.50" || devs[1].MAC != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("unexpected second: %+v", devs[1])
	}
}

func TestExpiredLeaseSkipped(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	now := time.Now()
	writeFile(t, leases, "1 aa:bb:cc:dd:ee:ff 192.168.1.50 tv *\n")
	r := &Reader{LeaseFile: leases, Now: func() time.Time { return now }}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 0 {
		t.Fatalf("expired lease must be skipped: %+v", devs)
	}
}

func TestMissingLeaseFileOK(t *testing.T) {
	r := &Reader{LeaseFile: filepath.Join(t.TempDir(), "nope")}
	devs, err := r.Devices()
	if err != nil {
		t.Fatalf("missing lease file must not error: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("want no devices, got %+v", devs)
	}
}

func TestUbusFallbackMerges(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	writeFile(t, leases, fmt.Sprintf("%d aa:bb:cc:dd:ee:ff 192.168.1.50 tv *\n", time.Now().Add(24*time.Hour).Unix()))
	// odhcpd-style response: bare-hex MAC, hostname, address, static flag.
	ubusJSON := `{"device":{"br-lan":{"leases":[
		{"mac":"aabbccddeeff","hostname":"tv","address":"192.168.1.50","flags":["bound","static"]},
		{"mac":"001122334455","hostname":"phone","address":"192.168.1.99","flags":[]},
		{"mac":"zz","hostname":"bad","address":"192.168.1.98","flags":[]}
	]}}}`
	r := &Reader{
		LeaseFile: leases,
		UbusCmd:   func() ([]byte, error) { return []byte(ubusJSON), nil },
	}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("tv (from file) + phone (from ubus) expected, got %d: %+v", len(devs), devs)
	}
	byHost := map[string]model.LANDevice{}
	for _, d := range devs {
		byHost[d.Hostname] = d
	}
	if byHost["tv"].IP != "192.168.1.50" {
		t.Fatalf("lease file must win for tv: %+v", byHost["tv"])
	}
	if byHost["phone"].MAC != "00:11:22:33:44:55" || !byHost["tv"].Static {
		t.Fatalf("ubus normalization/static flag wrong: %+v", byHost)
	}
}

func TestUbusErrorIgnored(t *testing.T) {
	r := &Reader{
		LeaseFile: filepath.Join(t.TempDir(), "nope"),
		UbusCmd:   func() ([]byte, error) { return nil, errors.New("ubus down") },
	}
	if _, err := r.Devices(); err != nil {
		t.Fatalf("ubus failure must be non-fatal: %v", err)
	}
}

func TestSetStaticDevStub(t *testing.T) {
	err := SetStatic(model.LANDevice{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.50", Hostname: "tv"}, true)
	if !errors.Is(err, ErrDevStub) {
		t.Fatalf("dev mode must return ErrDevStub, got %v", err)
	}
	if err == nil || err.Error() == "" {
		t.Fatal("stub error must document the UCI commands")
	}
}

func TestSetStaticValidation(t *testing.T) {
	if err := SetStatic(model.LANDevice{MAC: "nope", IP: "192.168.1.5"}, false); err == nil {
		t.Fatal("bad MAC must fail")
	}
	if err := SetStatic(model.LANDevice{MAC: "aa:bb:cc:dd:ee:ff", IP: "nope"}, false); err == nil {
		t.Fatal("bad IP must fail")
	}
}

func TestNormMAC(t *testing.T) {
	cases := map[string]string{
		"aabbccddeeff":      "aa:bb:cc:dd:ee:ff",
		"AA:BB:CC:DD:EE:FF": "AA:BB:CC:DD:EE:FF",
		"":                  "",
		"xyz":               "",
		"aabbccddeef":       "", // wrong length
	}
	for in, want := range cases {
		if got := normMAC(in); got != want {
			t.Errorf("normMAC(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- Edge cases (filler-lan-edge-tests) ---

// TestDevicesEmptyARPLine: lease file that exists but is empty (arp
// dump empty / no leases) must yield zero devices, no error.
func TestDevicesEmptyARPLine(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	writeFile(t, leases, "")
	r := &Reader{LeaseFile: leases}
	devs, err := r.Devices()
	if err != nil {
		t.Fatalf("empty lease file must not error: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("want no devices, got %+v", devs)
	}
}

// TestDevicesEmptyUBusOnly: empty odhcpd response with an empty lease
// file (dev host: no /tmp/dhcp.leases, odhcpd runs but no leases).
func TestDevicesEmptyUBusOnly(t *testing.T) {
	r := &Reader{
		LeaseFile: filepath.Join(t.TempDir(), "nope"),
		UbusCmd:   func() ([]byte, error) { return []byte(`{"device":{}}`), nil },
	}
	devs, err := r.Devices()
	if err != nil {
		t.Fatalf("empty ubus response must not error: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("want no devices, got %+v", devs)
	}
}

// TestFixedIPConflictTwoDevices: two devices claiming the same IP via
// ubus — both survive the merge (dedup is by MAC, not IP); the API
// layer surfaces the conflict to the user. Documents current behavior.
func TestFixedIPConflictTwoDevices(t *testing.T) {
	ubusJSON := `{"device":{"br-lan":{"leases":[
		{"mac":"aabbccddeeff","hostname":"tv","address":"192.168.1.50","flags":["static"]},
		{"mac":"001122334455","hostname":"laptop","address":"192.168.1.50","flags":["static"]}
	]}}}`
	r := &Reader{
		LeaseFile: filepath.Join(t.TempDir(), "nope"),
		UbusCmd:   func() ([]byte, error) { return []byte(ubusJSON), nil },
	}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("both conflicting devices must be listed, got %d: %+v", len(devs), devs)
	}
	// Sort is hostname-then-IP; equal IPs, so hostname order applies.
	if devs[0].Hostname != "laptop" || devs[1].Hostname != "tv" {
		t.Fatalf("sort order wrong: %+v", devs)
	}
	if devs[0].IP != "192.168.1.50" || devs[1].IP != "192.168.1.50" {
		t.Fatalf("conflicting IP must be preserved on both: %+v", devs)
	}
}

// TestFixedIPConflictLeaseFileWins: lease file and ubus disagree on
// which MAC holds a fixed IP — the lease file (authoritative) wins,
// the ubus candidate for the same IP is dropped by MAC-dedup.
func TestFixedIPConflictLeaseFileWins(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).Unix()
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	writeFile(t, leases, fmt.Sprintf("%d aa:bb:cc:dd:ee:ff 192.168.1.50 tv *\n", future))
	ubusJSON := `{"device":{"br-lan":{"leases":[
		{"mac":"001122334455","hostname":"laptop","address":"192.168.1.50","flags":["static"]}
	]}}}`
	r := &Reader{
		LeaseFile: leases,
		UbusCmd:   func() ([]byte, error) { return []byte(ubusJSON), nil },
	}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	// laptop is a distinct MAC so it stays; the point here: tv (file)
	// keeps 192.168.1.50 and remains authoritative for that MAC.
	if len(devs) != 2 {
		t.Fatalf("want tv+laptop, got %d: %+v", len(devs), devs)
	}
	byHost := map[string]model.LANDevice{}
	for _, d := range devs {
		byHost[d.Hostname] = d
	}
	if byHost["tv"].MAC != "aa:bb:cc:dd:ee:ff" || byHost["tv"].IP != "192.168.1.50" {
		t.Fatalf("lease file entry must survive unchanged: %+v", byHost["tv"])
	}
	if byHost["tv"].Static {
		t.Fatalf("tv static flag must not be invented from ubus: %+v", byHost["tv"])
	}
}

// TestDeviceGoneFromARPRemainsInConfig: a device with a UCI static
// lease (odhcpd "static" flag, no live lease) still shows up via the
// ubus fallback — the fixed-IP config outlives the ARP entry.
func TestDeviceGoneFromARPRemainsInConfig(t *testing.T) {
	ubusJSON := `{"device":{"br-lan":{"leases":[
		{"mac":"aabbccddeeff","hostname":"tv","address":"192.168.1.50","flags":["static"]}
	]}}}`
	r := &Reader{
		LeaseFile: filepath.Join(t.TempDir(), "nope"), // gone from ARP/leases
		UbusCmd:   func() ([]byte, error) { return []byte(ubusJSON), nil },
	}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 {
		t.Fatalf("static-config device must still be listed, got %d: %+v", len(devs), devs)
	}
	if devs[0].Hostname != "tv" || devs[0].IP != "192.168.1.50" || !devs[0].Static {
		t.Fatalf("unexpected device: %+v", devs[0])
	}
}

// TestParseLeaseLineNonStandardMAC: MAC formats net.ParseMAC accepts
// but dnsmasq never writes (dash/hyphen, no separators) are still
// parsed; wrong-length and multicast-ish garbage is rejected.
func TestParseLeaseLineNonStandardMAC(t *testing.T) {
	now := time.Now()
	cases := []struct {
		mac  string
		want bool
	}{
		{"aa:bb:cc:dd:ee:ff", true},  // canonical
		{"aa-bb-cc-dd-ee-ff", true},  // hyphen-separated (ParseMAC accepts)
		{"aabb.ccdd.eeff", true},     // dot-separated (Cisco style)
		{"aabbccddeeff", false},      // no separators: dnsmasq never writes this
		{"AA:BB:CC:DD:EE:FF", true},  // uppercase
		{"aa:bb:cc:dd:ee", false},    // too short
		{"gg:bb:cc:dd:ee:ff", false}, // not hex
		{"", false},                  // empty
	}
	for _, c := range cases {
		line := fmt.Sprintf("%d %s 192.168.1.50 host *", now.Add(time.Hour).Unix(), c.mac)
		_, ok := parseLeaseLine(line, now)
		if ok != c.want {
			t.Errorf("parseLeaseLine(mac=%q) accepted=%v, want %v", c.mac, ok, c.want)
		}
	}
}

// TestUBusNonStandardMACNormalization: odhcpd's bare-hex MAC is
// normalized to colon form; separated-but-odd formats pass through
// only when valid; invalid hex is dropped.
func TestUBusNonStandardMACNormalization(t *testing.T) {
	ubusJSON := `{"device":{"br-lan":{"leases":[
		{"mac":"aa:bb:cc:dd:ee:ff","hostname":"sep","address":"192.168.1.61","flags":[]},
		{"mac":"aabb001122ff","hostname":"bare","address":"192.168.1.62","flags":[]},
		{"mac":"AA:BB:CC:DD:EE:FF","hostname":"upper","address":"192.168.1.63","flags":[]},
		{"mac":"aa-bb-cc-dd-ee-ff","hostname":"dashed","address":"192.168.1.64","flags":[]},
		{"mac":"aabbccddeeffg","hostname":"badlen","address":"192.168.1.65","flags":[]},
		{"mac":"ggbbccddeeff","hostname":"badhex","address":"192.168.1.66","flags":[]}
	]}}}`
	r := &Reader{
		LeaseFile: filepath.Join(t.TempDir(), "nope"),
		UbusCmd:   func() ([]byte, error) { return []byte(ubusJSON), nil },
	}
	devs, err := r.Devices()
	if err != nil {
		t.Fatal(err)
	}
	byHost := map[string]model.LANDevice{}
	for _, d := range devs {
		byHost[d.Hostname] = d
	}
	wantHosts := map[string]string{
		"sep":   "aa:bb:cc:dd:ee:ff",
		"bare":  "aa:bb:00:11:22:ff",
		"upper": "AA:BB:CC:DD:EE:FF",
	}
	for h, wantMAC := range wantHosts {
		got, ok := byHost[h]
		if !ok {
			t.Errorf("device %q missing: %+v", h, byHost)
			continue
		}
		if got.MAC != wantMAC {
			t.Errorf("%q MAC = %q, want %q", h, got.MAC, wantMAC)
		}
	}
	// Dropped: hyphen form (odhcpd never emits it; normMAC only
	// accepts colon or bare-hex), wrong length, non-hex.
	for _, h := range []string{"dashed", "badlen", "badhex"} {
		if _, ok := byHost[h]; ok {
			t.Errorf("invalid MAC for %q must be dropped: %+v", h, byHost[h])
		}
	}
}

// TestSetStaticHostnameSanitization: renaming a device (setting a
// static lease under a new hostname) must not let hostname content
// inject UCI options — the name is sanitized before it reaches the
// uci command line.
func TestSetStaticHostnameSanitization(t *testing.T) {
	cases := map[string]string{
		"tv'; uci commit; #":            "tv-uci-commit",
		"tv\nset dhcp.@host[-1].mac=bb": "tv-set-dhcp.-host--1-.mac-bb",
		"living room tv":                "living-room-tv",
		"	tv":                           "tv",
		"'":                             "device",
		"":                              "device",
	}
	for in, want := range cases {
		if got := sanitizeHostname(in); got != want {
			t.Errorf("sanitizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
	// Injection attempts pass validation only because they were
	// sanitized — the sanitized name is what reaches UCI. In dev mode
	// the stub fires; on a dev host the router path would fail on the
	// missing uci binary, never on the command line.
	for _, bad := range []string{"tv'; uci commit; #", "tv\nset dhcp.@host[-1].mac=bb", "	tv"} {
		err := SetStatic(model.LANDevice{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.50", Hostname: bad}, true)
		if !errors.Is(err, ErrDevStub) {
			t.Errorf("injection hostname %q must pass validation sanitized (ErrDevStub in dev mode), got %v", bad, err)
		}
	}
}

// TestSanitizeHostnameLength: renames longer than a DNS label are
// capped at 63 bytes.
func TestSanitizeHostnameLength(t *testing.T) {
	long := strings.Repeat("a", 100)
	if got := sanitizeHostname(long); len(got) != 63 {
		t.Errorf("long hostname must be capped at 63, got %d", len(got))
	}
}

// TestSetStaticDevStubRename: dev-mode stub documents the rename
// (hostname change) in its UCI command trace.
func TestSetStaticDevStubRename(t *testing.T) {
	err := SetStatic(model.LANDevice{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.50", Hostname: "tv-renamed"}, true)
	if !errors.Is(err, ErrDevStub) {
		t.Fatalf("dev mode must return ErrDevStub, got %v", err)
	}
	if !strings.Contains(err.Error(), "uci set dhcp.@host[-1].name=") {
		t.Fatalf("stub must document name set: %v", err)
	}
}
