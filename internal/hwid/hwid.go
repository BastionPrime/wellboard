// Package hwid generates and stores the persistent hardware ID sent with
// every subscription request (initial TZ FR-2, 5.6).
//
// Algorithm (FR-2.1): HWID = HEX_UPPER(SHA256(primary_lan_mac ||
// random_salt_32B))[0:32]. Hex was chosen because the panel-side regex
// ^[a-zA-Z0-9=-]{10,64}$ (docs/DECISIONS.md R4) forbids base64 symbols
// (+, /, _). The salt is generated once and stored next to the HWID, so
// regeneration is reproducible for audits, but Reset() makes a fresh salt
// and therefore a fresh HWID (a new device slot on the panel, FR-2.5).
//
// Storage (FR-2.2): <dir>/hwid as a two-line file "hwid\nsalt" (both hex,
// 0600, dir 0700). The packaging phase adds the path to
// /lib/upgrade/keep.d/wellboard so the ID survives sysupgrade.
//
// MAC discovery (FR-2.3 / 5.6): on the router the primary MAC comes from
// `ubus call network.device status {"name":"br-lan"}`; the fallback is the
// first non-loopback interface. In dev mode WELLBOARD_DEV_MAC or a
// wellboard-dev-mac file next to the state pins the MAC instead.
package hwid

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ValidPattern is the panel-side HWID format (docs/DECISIONS.md R4,
// Remnawave extract-hwid-headers.util.ts:13-33).
var ValidPattern = regexp.MustCompile(`^[a-zA-Z0-9=-]{10,64}$`)

// ErrNoMAC is returned when no LAN MAC could be discovered at all.
var ErrNoMAC = errors.New("hwid: no primary LAN MAC address found")

// saltLen is the random salt length in bytes (FR-2.1: "random_salt_32B").
const saltLen = 32

// HWIDLen is the number of hex characters kept from the SHA-256 digest
// (FR-2.1: "[0:32]").
const HWIDLen = 32

// Device holds the device identity facts sent alongside the HWID
// (FR-2.3: x-device-os / x-ver-os / x-device-model).
type Device struct {
	// MAC is the primary LAN MAC in "aa:bb:cc:dd:ee:ff" form.
	MAC string
	// OS is the platform name ("OpenWrt" on the router).
	OS string
	// OSVersion is the firmware version from /etc/openwrt_release.
	OSVersion string
	// Model is the board name from /etc/board.json.
	Model string
}

// DevMACEnv is the environment variable that pins the MAC in dev mode
// (initial TZ 5.10 / 5.6).
const DevMACEnv = "WELLBOARD_DEV_MAC"

// devMACFile is the state-dir file that pins the MAC in dev mode.
const devMACFile = "wellboard-dev-mac"

// DiscoverMAC finds the primary LAN MAC: ubus br-lan status first, then the
// first non-loopback interface (initial TZ 5.6).
func DiscoverMAC() (string, error) {
	if mac, err := ubusBrLANMAC(); err == nil && mac != "" {
		return mac, nil
	}
	if mac, err := firstNonLoopbackMAC(); err == nil && mac != "" {
		return mac, nil
	}
	return "", ErrNoMAC
}

// ubusBrLANMAC calls `ubus call network.device status {"name":"br-lan"}`
// and extracts macaddr (Present only on OpenWrt; failure is not an error
// for this fallback chain).
func ubusBrLANMAC() (string, error) {
	out, err := exec.Command("ubus", "call", "network.device", "status",
		`{"name":"br-lan"}`).Output()
	if err != nil {
		return "", err
	}
	s := string(out)
	mac, err := scrapeMACAddr(s)
	if err != nil {
		return "", err
	}
	return strings.ToLower(mac), nil
}

// scrapeMACAddr pulls the "macaddr" string value out of a ubus JSON
// reply. Exposed for tests; cheap on purpose (no JSON import needed for
// one field).
func scrapeMACAddr(s string) (string, error) {
	i := strings.Index(s, `"macaddr"`)
	if i < 0 {
		return "", fmt.Errorf("no macaddr in ubus output")
	}
	rest := s[i:]
	c := strings.Index(rest, ":")
	if c < 0 {
		return "", fmt.Errorf("malformed ubus output")
	}
	j := strings.Index(rest[c:], `"`)
	if j < 0 {
		return "", fmt.Errorf("malformed ubus output")
	}
	j += c
	k := strings.Index(rest[j+1:], `"`)
	if k < 0 {
		return "", fmt.Errorf("malformed ubus output")
	}
	mac := rest[j+1 : j+1+k]
	if _, err := net.ParseMAC(mac); err != nil {
		return "", fmt.Errorf("bad macaddr %q", mac)
	}
	return mac, nil
}

// firstNonLoopbackMAC returns the MAC of the first non-loopback interface,
// ordered by name for determinism.
func firstNonLoopbackMAC() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(ifaces))
	byName := map[string]net.Interface{}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		names = append(names, ifi.Name)
		byName[ifi.Name] = ifi
	}
	sortStrings(names)
	for _, name := range names {
		ifi := byName[name]
		mac := ifi.HardwareAddr.String()
		if mac == "" {
			continue
		}
		if _, err := net.ParseMAC(mac); err != nil {
			continue
		}
		return strings.ToLower(mac), nil
	}
	return "", ErrNoMAC
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Manager loads and stores the HWID pair below Dir.
type Manager struct {
	// Dir is the state directory (e.g. /etc/wellboard or a dev dir).
	Dir string
	// Prod enforces 0700/0600 permissions (NFR-2.3).
	Prod bool
	// Dev relaxes MAC discovery to the dev overrides (env / state file).
	Dev bool
	// MAC overrides MAC discovery when non-empty (used in dev and tests).
	MAC string
}

// NewManager returns a Manager for dir.
func NewManager(dir string, prod bool) *Manager {
	return &Manager{Dir: dir, Prod: prod}
}

// Identity is a loaded (or freshly generated) HWID record.
type Identity struct {
	// HWID is the 32-char upper-hex identifier.
	HWID string
	// Salt is the 64-char lower-hex salt.
	Salt string
	// MAC is the MAC the HWID was derived from.
	MAC string
}

// Path returns the hwid file location (FR-2.2).
func (m *Manager) Path() string {
	return filepath.Join(m.Dir, "hwid")
}

// Load returns the stored identity, generating and persisting a new one on
// first use (FR-2.1: generated once, persistently). The dev MAC overrides
// (env WELLBOARD_DEV_MAC, <dir>/wellboard-dev-mac) apply only when Dev is
// set; production always uses real MAC discovery.
func (m *Manager) Load() (*Identity, error) {
	if id, err := m.readFile(); err == nil {
		return id, nil
	}
	return m.Generate()
}

// Generate makes a NEW identity: fresh 32-byte salt, HWID derived from the
// discovered (or overridden) MAC, persisted atomically. It does not reuse a
// stored salt — reproducibility of Load comes from the file, not from a
// fixed salt.
func (m *Manager) Generate() (*Identity, error) {
	mac, err := m.mac()
	if err != nil {
		return nil, err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("hwid: read random salt: %w", err)
	}
	id := &Identity{
		HWID: computeHWID(mac, salt),
		Salt: hex.EncodeToString(salt),
		MAC:  mac,
	}
	if err := m.save(id); err != nil {
		return nil, err
	}
	return id, nil
}

// Reset regenerates the identity with a fresh salt (FR-2.5: "Generate
// again" — takes a new device slot on the panel).
func (m *Manager) Reset() (*Identity, error) {
	return m.Generate()
}

// computeHWID implements HEX_UPPER(SHA256(mac || salt))[0:32].
func computeHWID(mac string, salt []byte) string {
	norm := strings.ToLower(strings.TrimSpace(mac))
	sum := sha256.Sum256(append([]byte(norm), salt...))
	return strings.ToUpper(hex.EncodeToString(sum[:]))[:HWIDLen]
}

// ComputeHWID is the exported pure form of the FR-2.1 algorithm — same MAC
// and salt always yield the same HWID (stability is what the panel sees).
func ComputeHWID(mac string, salt []byte) string {
	return computeHWID(mac, salt)
}

// mac resolves the MAC to derive from: explicit override, dev env, dev
// file, then real discovery.
func (m *Manager) mac() (string, error) {
	if m.MAC != "" {
		return strings.ToLower(m.MAC), nil
	}
	if m.Dev {
		if mac := strings.TrimSpace(os.Getenv(DevMACEnv)); mac != "" {
			return strings.ToLower(mac), nil
		}
		if data, err := os.ReadFile(filepath.Join(m.Dir, devMACFile)); err == nil {
			if mac := strings.TrimSpace(string(data)); mac != "" {
				return strings.ToLower(mac), nil
			}
		}
	}
	return DiscoverMAC()
}

// readFile loads a previously stored identity. A malformed file is an
// error (the caller falls back to Generate); a missing file is too — Load
// treats both as "generate the first one".
func (m *Manager) readFile() (*Identity, error) {
	data, err := os.ReadFile(m.Path())
	if err != nil {
		return nil, err
	}
	lines := strings.Fields(string(data))
	if len(lines) != 2 {
		return nil, fmt.Errorf("hwid: malformed hwid file %s", m.Path())
	}
	hw, salt := lines[0], lines[1]
	if !ValidPattern.MatchString(hw) {
		return nil, fmt.Errorf("hwid: stored HWID %q fails ^[a-zA-Z0-9=-]{10,64}$", hw)
	}
	if _, err := hex.DecodeString(salt); err != nil || len(salt) != 2*saltLen {
		return nil, fmt.Errorf("hwid: stored salt is not %d hex bytes", saltLen)
	}
	return &Identity{HWID: hw, Salt: salt, MAC: ""}, nil
}

// save persists "hwid\nsalt" atomically (tmp+rename) with 0600 perms in
// prod mode.
func (m *Manager) save(id *Identity) error {
	if err := os.MkdirAll(m.Dir, m.dirPerm()); err != nil {
		return fmt.Errorf("hwid: create dir: %w", err)
	}
	if m.Prod {
		// MkdirAll keeps the existing dir's mode; enforce 0700 (NFR-2.3).
		if err := os.Chmod(m.Dir, 0o700); err != nil {
			return fmt.Errorf("hwid: chmod dir: %w", err)
		}
	}
	data := []byte(id.HWID + "\n" + id.Salt + "\n")
	tmp, err := os.CreateTemp(m.Dir, ".hwid-*.tmp")
	if err != nil {
		return fmt.Errorf("hwid: create temp: %w", err)
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if m.Prod {
		if err = tmp.Chmod(0o600); err != nil {
			tmp.Close()
			return fmt.Errorf("hwid: chmod: %w", err)
		}
	}
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("hwid: write: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("hwid: sync: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("hwid: close: %w", err)
	}
	if err = os.Rename(name, m.Path()); err != nil {
		return fmt.Errorf("hwid: rename: %w", err)
	}
	return nil
}

func (m *Manager) dirPerm() os.FileMode {
	if m.Prod {
		return 0o700
	}
	return 0o755
}

// DiscoverDevice collects the FR-2.3 identity facts from the router
// filesystem (/etc/openwrt_release, /etc/board.json). Missing files yield
// empty fields — the headers are optional.
func DiscoverDevice() Device {
	dev := Device{OS: "OpenWrt"}
	if data, err := os.ReadFile("/etc/openwrt_release"); err == nil {
		dev.OSVersion = scrapeShellVar(string(data), "DISTRIB_RELEASE")
	}
	if data, err := os.ReadFile("/etc/board.json"); err == nil {
		dev.Model = scrapeJSONString(string(data), "model")
	}
	return dev
}

// scrapeShellVar pulls a VAR="value" assignment from a shell-style file.
func scrapeShellVar(s, key string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			v := strings.TrimPrefix(line, key+"=")
			v = strings.Trim(v, `"'`)
			return v
		}
	}
	return ""
}

// scrapeJSONString extracts "key":"value" without a JSON dependency.
func scrapeJSONString(s, key string) string {
	i := strings.Index(s, `"`+key+`"`)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key)+2:]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	k := strings.Index(rest[j+1:], `"`)
	if k < 0 {
		return ""
	}
	return rest[j+1 : j+1+k]
}
