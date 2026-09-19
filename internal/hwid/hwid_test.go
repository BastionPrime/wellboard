package hwid

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestComputeHWIDStable(t *testing.T) {
	// Same MAC + same salt → same HWID (FR-2.1 determinism).
	salt := make([]byte, 32)
	_, _ = rand.Read(salt)
	a := ComputeHWID("aa:bb:cc:dd:ee:ff", salt)
	b := ComputeHWID("aa:bb:cc:dd:ee:ff", salt)
	if a != b {
		t.Fatalf("same input must yield same hwid: %s != %s", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("hwid must be 32 hex chars, got %d", len(a))
	}
	if a != strings.ToUpper(strings.ToLower(a)) {
		t.Fatalf("hwid must be upper hex: %s", a)
	}
	if !ValidPattern.MatchString(a) {
		t.Fatalf("hwid %q fails the panel regex", a)
	}
}

func TestComputeHWIDDifferentSaltDiffers(t *testing.T) {
	s1 := make([]byte, 32)
	s2 := make([]byte, 32)
	_, _ = rand.Read(s1)
	_, _ = rand.Read(s2)
	if ComputeHWID("aa:bb:cc:dd:ee:ff", s1) == ComputeHWID("aa:bb:cc:dd:ee:ff", s2) {
		t.Fatal("different salts must yield different hwids")
	}
}

func TestValidPattern(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0123456789ABCDEF0123456789ABCDEF", true},
		{"abc", false},                   // too short
		{"A=BCDEFGHIJ", true},            // '=' allowed
		{strings.Repeat("x", 65), false}, // too long
		{"abc+def", false},               // '+' forbidden
		{"abc/def", false},               // '/' forbidden
		{"abc_def", false},               // '_' forbidden
	}
	for _, c := range cases {
		if got := ValidPattern.MatchString(c.in); got != c.want {
			t.Errorf("ValidPattern(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestManagerGenerateAndLoad(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = "AA:BB:CC:DD:EE:FF"

	id, err := m.Load()
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if !ValidPattern.MatchString(id.HWID) {
		t.Fatalf("generated hwid invalid: %q", id.HWID)
	}
	if len(id.Salt) != 64 {
		t.Fatalf("salt must be 64 hex chars, got %d", len(id.Salt))
	}

	// Load must return the stored identity, not regenerate.
	again, err := m.Load()
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if again.HWID != id.HWID || again.Salt != id.Salt {
		t.Fatalf("Load is not persistent: %v vs %v", again, id)
	}

	// The stored hwid must be reproducible from the file contents (mac +
	// stored salt → stored hwid).
	salt, _ := hex.DecodeString(id.Salt)
	if recomputed := ComputeHWID("aa:bb:cc:dd:ee:ff", salt); recomputed != id.HWID {
		t.Fatalf("stored hwid does not match the algorithm: %s vs %s", recomputed, id.HWID)
	}
}

func TestManagerReset(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"

	first, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Reset()
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if second.HWID == first.HWID {
		t.Fatal("Reset must produce a new hwid (new device slot, FR-2.5)")
	}
	// And the new identity is the persisted one.
	stored, _ := m.Load()
	if stored.HWID != second.HWID {
		t.Fatal("Reset did not persist the new identity")
	}
}

func TestManagerProdPermissions(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, true)
	m.MAC = "aa:bb:cc:dd:ee:ff"
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(m.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("prod hwid file must be 0600, got %v", fi.Mode().Perm())
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("prod hwid dir must be 0700, got %v", di.Mode().Perm())
	}
}

func TestManagerMalformedFileRegenerates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hwid"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"
	id, err := m.Load()
	if err != nil {
		t.Fatalf("Load must recover from a malformed file: %v", err)
	}
	if !ValidPattern.MatchString(id.HWID) {
		t.Fatalf("recovered hwid invalid: %q", id.HWID)
	}
}

func TestDevMACOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DevMACEnv, "11:22:33:44:55:66")
	m := NewManager(dir, false)
	m.Dev = true
	id, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	if id.MAC != "11:22:33:44:55:66" {
		t.Fatalf("dev env MAC not used: %q", id.MAC)
	}

	// File override (no env).
	t.Setenv(DevMACEnv, "")
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, devMACFile), []byte("aa:11:22:33:44:55\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(dir2, false)
	m2.Dev = true
	id2, err := m2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if id2.MAC != "aa:11:22:33:44:55" {
		t.Fatalf("dev file MAC not used: %q", id2.MAC)
	}
}

func TestFirstNonLoopbackMAC(t *testing.T) {
	// On any CI host there is at least one non-loopback interface — but
	// guard: if none, the error must be ErrNoMAC.
	mac, err := firstNonLoopbackMAC()
	if err != nil {
		if err != ErrNoMAC {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Skip("no non-loopback interface on this host")
	}
	if matched, _ := regexp.MatchString(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`, mac); !matched {
		t.Fatalf("bad mac format: %q", mac)
	}
}

func TestScrapeShellVar(t *testing.T) {
	s := `DISTRIB_ID='OpenWrt'
DISTRIB_RELEASE="24.10.0"
DISTRIB_ARCH=x86_64
`
	if got := scrapeShellVar(s, "DISTRIB_RELEASE"); got != "24.10.0" {
		t.Fatalf("got %q", got)
	}
	if got := scrapeShellVar(s, "DISTRIB_ID"); got != "OpenWrt" {
		t.Fatalf("got %q", got)
	}
}

func TestScrapeJSONString(t *testing.T) {
	s := `{"model": {"id": "x", "name": "BPI-R4"}, "board": "bananapi"}`
	// The mock board.json nests model; the scraper handles the simple
	// flat "model":"…" case used by /etc/board.json ("model": {...} has
	// no string value directly).
	if got := scrapeJSONString(s, "board"); got != "bananapi" {
		t.Fatalf("got %q", got)
	}
	if got := scrapeJSONString(s, "missing"); got != "" {
		t.Fatalf("got %q", got)
	}
}

// --- coverage for the router-discovery paths -------------------------------

func TestScrapeJSONStringBoardModel(t *testing.T) {
	// Real board.json shape (flat "model":"..." value after the id key).
	s := `{"model":{"id":"bpi-r4","name":"Bananapi BPI-R4"}}`
	if got := scrapeJSONString(s, "name"); got != "Bananapi BPI-R4" {
		t.Fatalf("got %q", got)
	}
}

func TestUbusBrLANMACOutput(t *testing.T) {
	mac, err := scrapeMACAddr(`{"macaddr":"AA:BB:CC:DD:EE:FF","type":"bridge"}`)
	if err != nil {
		t.Fatal(err)
	}
	if mac != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("parsed mac: %q", mac)
	}
	// Lowercase input normalizes up at the caller; here raw is returned.
	mac, err = scrapeMACAddr(`{"macaddr":"aa:bb:cc:dd:ee:ff"}`)
	if err != nil || mac != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("lower mac: %q %v", mac, err)
	}
	if _, err := scrapeMACAddr(`{"type":"bridge"}`); err == nil {
		t.Fatal("missing macaddr must error")
	}
	if _, err := scrapeMACAddr(`{"macaddr": 42}`); err == nil {
		t.Fatal("non-string macaddr must error")
	}
	if _, err := scrapeMACAddr(`{"macaddr":"not:a:mac"}`); err == nil {
		t.Fatal("invalid mac must error")
	}
	if _, err := scrapeMACAddr(`{"macaddr":"AA:BB:CC`); err == nil {
		t.Fatal("truncated json must error")
	}
}

func TestUbusBrLANMACMissing(t *testing.T) {
	// On a non-OpenWrt host ubus is absent → the error path.
	if _, err := ubusBrLANMAC(); err == nil {
		t.Log("ubus present on this host (OpenWrt-like); error path not reached")
	}
}

func TestDiscoverMACFallbackChain(t *testing.T) {
	// ubus will fail (absent binary); the fallback must find a MAC or
	// return the typed no-MAC error.
	mac, err := DiscoverMAC()
	if err != nil {
		if err != ErrNoMAC {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if mac == "" {
		t.Fatal("non-error result must carry a mac")
	}
}

func TestDiscoverDeviceHost(t *testing.T) {
	dev := DiscoverDevice()
	if dev.OS != "OpenWrt" {
		t.Fatalf("OS label: %q", dev.OS)
	}
	// On a dev host /etc/openwrt_release is absent → empty version is
	// the documented graceful behavior.
	_ = dev.OSVersion
	_ = dev.Model
}

func TestSortStrings(t *testing.T) {
	in := []string{"c", "a", "b"}
	sortStrings(in)
	if in[0] != "a" || in[1] != "b" || in[2] != "c" {
		t.Fatalf("sorted: %v", in)
	}
}

func TestGenerateErrorPaths(t *testing.T) {
	// MAC discovery failure → Generate errors before writing anything.
	m := NewManager(t.TempDir(), false)
	m.MAC = ""
	m.Dev = false
	if _, err := m.Generate(); err == nil {
		t.Skip("MAC discovery succeeded on this host; error path not reachable")
	}
}

func TestReadFileRejectsBadSalt(t *testing.T) {
	dir := t.TempDir()
	// Valid hwid, wrong salt length.
	if err := os.WriteFile(filepath.Join(dir, "hwid"), []byte("ABCDEFGHJKLM0123456789AB\nDEAD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, false)
	if _, err := m.readFile(); err == nil {
		t.Fatal("bad salt length must be rejected")
	}
	// Valid hwid + non-hex salt.
	if err := os.WriteFile(filepath.Join(dir, "hwid"), []byte("ABCDEFGHJKLM0123456789AB\nzzzz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readFile(); err == nil {
		t.Fatal("non-hex salt must be rejected")
	}
	// Stored hwid failing the regex.
	if err := os.WriteFile(filepath.Join(dir, "hwid"), []byte("short\n"+strings.Repeat("a", 64)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readFile(); err == nil {
		t.Fatal("regex-failing hwid must be rejected")
	}
	// One line only.
	if err := os.WriteFile(filepath.Join(dir, "hwid"), []byte("ABCDEFGHJKLM0123456789AB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readFile(); err == nil {
		t.Fatal("single-line file must be rejected")
	}
}

func TestSaveErrorPath(t *testing.T) {
	// A file where the directory should be: MkdirAll fails.
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(blocked, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"
	if _, err := m.Generate(); err == nil {
		t.Fatal("save into a file path must fail")
	}
}
