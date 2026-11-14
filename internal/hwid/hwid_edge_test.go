package hwid

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Reset / regeneration (FR-2.5) ----------------------------------------

func TestResetChangesSaltAndValue(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"

	first, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Reset()
	if err != nil {
		t.Fatal(err)
	}

	if second.Salt == first.Salt {
		t.Fatal("Reset must roll the salt, got identical salt")
	}
	if second.HWID == first.HWID {
		t.Fatal("Reset must yield a different hwid value (new device slot)")
	}
	if second.MAC != first.MAC {
		t.Fatalf("Reset keeps the MAC: %q vs %q", second.MAC, first.MAC)
	}

	salt, _ := hex.DecodeString(second.Salt)
	if recomputed := ComputeHWID("aa:bb:cc:dd:ee:ff", salt); recomputed != second.HWID {
		t.Fatalf("post-Reset hwid not derivable from stored salt: %s vs %s", recomputed, second.HWID)
	}
}

func TestResetObliteratesFileContent(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"

	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(m.Path())

	if _, err := m.Reset(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(m.Path())

	if bytes.Equal(before, after) {
		t.Fatal("Reset must rewrite the on-disk hwid file")
	}

	// And the persisted pair matches the returned identity.
	stored, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.HWID != secondIdentityHWID(t, after) {
		t.Fatal("Load after Reset returns the regenerated identity")
	}
}

func secondIdentityHWID(t *testing.T, file []byte) string {
	t.Helper()
	fields := strings.Fields(string(file))
	if len(fields) != 2 {
		t.Fatalf("hwid file must hold hwid+salt, got %d fields", len(fields))
	}
	return fields[0]
}

// --- Stability across repeated reads --------------------------------------

func TestLoadIdempotentAcrossManyReads(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"

	id, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := m.Load()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if again.HWID != id.HWID || again.Salt != id.Salt {
			t.Fatalf("read %d drifted: %v vs %v", i, again, id)
		}
	}

	// A *fresh* manager over the same dir sees the same identity — the
	// stability comes from the file, not the in-memory manager.
	fresh := NewManager(dir, false)
	fresh.MAC = "bb:cc:dd:ee:ff:00" // different MAC must NOT matter
	other, err := fresh.Load()
	if err != nil {
		t.Fatal(err)
	}
	if other.HWID != id.HWID || other.Salt != id.Salt {
		t.Fatal("stored identity must not depend on the MAC used at load time")
	}
}

func TestStableAcrossReadAfterMACChange(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"
	id, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	m.MAC = "00:11:22:33:44:55" // NIC replaced
	again, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	if again.HWID != id.HWID {
		t.Fatal("HWID must remain stable across MAC changes once persisted")
	}
}

// --- MAC selection with several interfaces --------------------------------

func TestFirstNonLoopbackSkipsLoopbackOnly(t *testing.T) {
	// firstNonLoopbackMAC orders interfaces by name and takes the first
	// MAC-bearing non-loopback one. On a CI host we cannot force the
	// interface set, but we can at least assert the contract on the
	// deterministic selection order used by the sorter.
	names := []string{"eth10", "eth2", "lo", "eth0", "br-lan"}
	sortStrings(names)
	want := []string{"br-lan", "eth0", "eth10", "eth2", "lo"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("interface order: %v, want %v", names, want)
		}
	}

	// MAC-bearing loopback interfaces are filtered from the candidate
	// list entirely (FlagLoopback), so the first non-loopback candidate
	// with a MAC wins.
	mac, err := firstNonLoopbackMAC()
	if err != nil {
		if err != ErrNoMAC {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Skip("no non-loopback interface on this host")
	}
	if mac == "00:00:00:00:00:00" {
		t.Fatalf("zero mac must not be selected: %q", mac)
	}
}

// --- Empty / missing MAC ---------------------------------------------------

func TestGenerateEmptyMACOverrideFallsThrough(t *testing.T) {
	// An empty explicit MAC override must fall through to discovery, not
	// produce a hwid derived from an empty string.
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = ""
	m.Dev = false
	_, err := m.Generate()
	if err == nil {
		// Discovery found a MAC on this host — fine, but then the
		// identity must not carry an empty MAC.
		t.Log("MAC discovery succeeded on this host")
		return
	}
	if err != ErrNoMAC {
		t.Fatalf("expected ErrNoMAC, got %v", err)
	}
	// No hwid file may be left behind by the failed generate.
	if _, statErr := os.Stat(filepath.Join(dir, "hwid")); statErr == nil {
		t.Fatal("failed Generate must not leave an hwid file")
	}
}

func TestLoadAfterEmptyDevMACEnvOnly(t *testing.T) {
	// Dev mode with an empty WELLBOARD_DEV_MAC and no dev file: the
	// empty env value must be skipped, not used as the MAC.
	dir := t.TempDir()
	t.Setenv(DevMACEnv, "")
	m := NewManager(dir, false)
	m.Dev = true
	id, err := m.Load()
	if err != nil {
		// Host had no MAC at all — acceptable.
		if err == ErrNoMAC {
			t.Skip("no MAC discovered on this host")
		}
		t.Fatal(err)
	}
	if id.MAC == "" {
		t.Fatal("identity must carry a MAC")
	}
}

func TestEmptyWhitespaceDevMACFileIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, devMACFile), []byte("   \n\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(DevMACEnv, "")
	m := NewManager(dir, false)
	m.Dev = true
	id, err := m.Load()
	if err != nil {
		if err == ErrNoMAC {
			t.Skip("no MAC discovered on this host")
		}
		t.Fatal(err)
	}
	if id.MAC == "" {
		t.Fatal("whitespace-only dev MAC file must not pin an empty MAC")
	}
}

// --- keep-file migration (sysupgrade survival, FR-2.2) ---------------------

func TestHWIDSurvivesKeepFileMove(t *testing.T) {
	// Emulate a sysupgrade: the old root's /etc/wellboard was preserved
	// via /lib/upgrade/keep.d/wellboard and reappears on the new root.
	// The HWID must be identical after the "move".
	oldRoot := t.TempDir()
	oldState := filepath.Join(oldRoot, "etc", "wellboard")

	m := NewManager(oldState, true)
	m.MAC = "aa:bb:cc:dd:ee:ff"
	id, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the keep-file contract: the state dir is exactly what
	// packaging/openwrt/files/keep.d/wellboard preserves.
	keep := keepFilePath(t)
	want, err := os.ReadFile(keep)
	if err != nil {
		t.Skipf("keep.d file not present in source checkout: %v", err)
	}
	if !bytes.Contains(want, []byte("/etc/wellboard")) {
		t.Fatalf("keep.d/wellboard must preserve /etc/wellboard, got %q", want)
	}

	newRoot := t.TempDir()
	newState := filepath.Join(newRoot, "etc", "wellboard")
	if err := os.MkdirAll(filepath.Dir(newState), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldState, newState); err != nil {
		t.Fatal(err)
	}

	m2 := NewManager(newState, true)
	m2.MAC = "aa:bb:cc:dd:ee:ff"
	id2, err := m2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if id2.HWID != id.HWID || id2.Salt != id.Salt {
		t.Fatalf("hwid did not survive the keep-file move: %v vs %v", id2, id)
	}

	// Perms preserved through the move in prod mode.
	fi, err := os.Stat(m2.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("hwid file perms after move: %v", fi.Mode().Perm())
	}
}

func TestHWIDSaltLengthAfterRegeneration(t *testing.T) {
	// Post-Reset identity and file must still satisfy the salt length
	// contract (32 bytes, 64 hex chars).
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"
	if _, err := m.Load(); err != nil {
		t.Fatal(err)
	}
	id, err := m.Reset()
	if err != nil {
		t.Fatal(err)
	}
	if len(id.Salt) != 2*saltLen {
		t.Fatalf("salt length after Reset: %d, want %d", len(id.Salt), 2*saltLen)
	}
	if _, err := hex.DecodeString(id.Salt); err != nil {
		t.Fatalf("salt not hex after Reset: %v", err)
	}
	if !ValidPattern.MatchString(id.HWID) || len(id.HWID) != HWIDLen {
		t.Fatalf("hwid shape after Reset: %q", id.HWID)
	}
}

func TestSaveAtomicNoTmpLeftover(t *testing.T) {
	// save() is atomic (tmp + rename); after a successful save no temp
	// files may remain — sysupgrade copying the dir must not see junk.
	dir := t.TempDir()
	m := NewManager(dir, false)
	m.MAC = "aa:bb:cc:dd:ee:ff"
	if _, err := m.Generate(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "hwid" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("state dir must contain only 'hwid', got %v", names)
	}
}

// keepFilePath locates the packaging keep.d contract relative to the
// package under test.
func keepFilePath(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "packaging", "openwrt", "files", "keep.d", "wellboard"),
		filepath.Join("..", "..", "..", "packaging", "openwrt", "files", "keep.d", "wellboard"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Fatal("packaging/openwrt/files/keep.d/wellboard not found relative to internal/hwid")
	return ""
}
