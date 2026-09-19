// Package nikki integrates WellBoard with the nikki OpenWrt package that
// manages the mihomo core (initial TZ 5.5, docs/DECISIONS.md N1-N7).
//
// The Adapter interface is the transport-agnostic contract: WellBoard
// writes a profile, validates it with the real mihomo binary, activates it
// via UCI, and watches service health. DryRunAdapter implements the same
// contract for development hosts without OpenWrt/nikki (initial TZ 5.10).
package nikki

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Info describes the nikki installation discovered on the device.
type Info struct {
	// NikkiVersion is the installed nikki package version ("" if unknown).
	NikkiVersion string
	// MihomoVersion is the mihomo core binary version.
	MihomoVersion string
	// ProfilesDir holds profile files (verified: /etc/nikki/profiles).
	ProfilesDir string
	// RunDir is nikki's runtime dir (verified: /etc/nikki/run).
	RunDir string
	// APIAddr is the mihomo external-controller address (nikki mixin).
	APIAddr string
	// APISecret is the mihomo API secret (nikki mixin, read-only).
	APISecret string
}

// Adapter is the nikki integration contract (initial TZ 5.5).
type Adapter interface {
	// Detect reports nikki/mihomo versions, paths and API coordinates.
	Detect() (Info, error)
	// WriteProfile stores the profile bytes under the given name and
	// returns its file path.
	WriteProfile(name string, yamlData []byte) (path string, err error)
	// Validate runs `mihomo -t -f path` and reports config errors.
	Validate(path string) error
	// Activate selects the profile in UCI and restarts the service.
	Activate(name string) error
	// Health checks that the mihomo API answers within timeout and the
	// service is running.
	Health(timeout time.Duration) error
	// LastGood reports the most recent profile known to have applied
	// successfully (for rollback, FR-6.3/6.4).
	LastGood() (name string, ok bool)
}

// DryRunAdapter is the development-mode Adapter (initial TZ 5.10): profiles
// go to a temp directory and validation runs a local mihomo binary if one
// is available; otherwise validation is a no-op success (logged by the
// caller). Activate/Health are simulated; LastGood tracks the last
// successfully validated profile name.
type DryRunAdapter struct {
	// Dir is where profiles are written (created on demand).
	Dir string
	// MihomoBin is the local mihomo binary path (e.g. ./bin/mihomo); may
	// be missing, in which case Validate is skipped.
	MihomoBin string
	// Log receives one line per simulated action (dev diagnostics).
	Log func(format string, args ...any)

	lastGood    string
	lastGoodSet bool
}

// NewDryRunAdapter returns a DryRunAdapter writing into dir.
func NewDryRunAdapter(dir, mihomoBin string) *DryRunAdapter {
	return &DryRunAdapter{Dir: dir, MihomoBin: mihomoBin}
}

func (d *DryRunAdapter) logf(format string, args ...any) {
	if d.Log != nil {
		d.Log(format, args...)
	}
}

// Detect reports the dry-run layout and the local mihomo version, if any.
func (d *DryRunAdapter) Detect() (Info, error) {
	info := Info{
		ProfilesDir: d.Dir,
		RunDir:      d.Dir,
	}
	if ver, err := d.mihomoVersion(); err == nil {
		info.MihomoVersion = ver
	} else {
		info.MihomoVersion = "(unavailable: " + err.Error() + ")"
	}
	return info, nil
}

// mihomoVersion runs `<bin> -v` and extracts the version line.
func (d *DryRunAdapter) mihomoVersion() (string, error) {
	if d.MihomoBin == "" {
		return "", fmt.Errorf("mihomo binary not configured")
	}
	if _, err := os.Stat(d.MihomoBin); err != nil {
		return "", fmt.Errorf("mihomo binary %s: %w", d.MihomoBin, err)
	}
	out, err := exec.Command(d.MihomoBin, "-v").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("mihomo -v: %w: %s", err, firstLine(out))
	}
	return firstLine(out), nil
}

// WriteProfile stores the profile bytes as <dir>/<name>.yaml.
func (d *DryRunAdapter) WriteProfile(name string, yamlData []byte) (string, error) {
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create dry-run dir: %w", err)
	}
	path := filepath.Join(d.Dir, sanitizeName(name)+".yaml")
	if err := os.WriteFile(path, yamlData, 0o644); err != nil {
		return "", fmt.Errorf("write dry-run profile: %w", err)
	}
	d.logf("dry-run: wrote profile %s (%d bytes)", path, len(yamlData))
	return path, nil
}

// Validate runs `mihomo -t -f <path>` when the local binary exists.
// Without a binary it returns nil (nothing to validate against) and logs
// the skip.
func (d *DryRunAdapter) Validate(path string) error {
	if d.MihomoBin == "" {
		d.logf("dry-run: no mihomo binary, skipping validation of %s", path)
		return nil
	}
	if _, err := os.Stat(d.MihomoBin); err != nil {
		d.logf("dry-run: mihomo binary missing (%v), skipping validation of %s", err, path)
		return nil
	}
	// -d points the home dir at the profile's dir so relative geodata /
	// provider paths (./providers/…) resolve like they do under nikki.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.MihomoBin, "-t", "-d", filepath.Dir(path), "-f", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mihomo -t %s: %w: %s", path, err, firstLine(out))
	}
	d.logf("dry-run: mihomo -t OK for %s", path)
	return nil
}

// Activate simulates activation: in dry-run there is no UCI or service.
func (d *DryRunAdapter) Activate(name string) error {
	d.lastGood = name
	d.lastGoodSet = true
	d.logf("dry-run: activate %s (simulated)", name)
	return nil
}

// Health simulates an always-healthy service in dry-run.
func (d *DryRunAdapter) Health(timeout time.Duration) error {
	d.logf("dry-run: health check (simulated ok, timeout %s)", timeout)
	return nil
}

// LastGood reports the last activated profile name.
func (d *DryRunAdapter) LastGood() (string, bool) {
	return d.lastGood, d.lastGoodSet
}

// sanitizeName keeps profile names filesystem-safe.
func sanitizeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "profile"
	}
	return string(out)
}

// firstLine trims command output to its first line for error messages.
func firstLine(b []byte) string {
	s := string(b)
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
