package nikki

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// RouterAdapter is the production Adapter: it drives the nikki package
// that is ALREADY installed and running on the owner's router.
//
// Division of labour (the reason this type exists — the previous
// non-dev path returned a DryRunAdapter that only simulated):
//
//   - WellBoard owns one artefact: a profile file under
//     ProfilesDir (/etc/nikki/profiles). It writes new profiles with
//     its own generated names and never rewrites a profile it did not
//     create.
//   - nikki keeps owning the runtime: the selected profile lives in
//     UCI (nikki.config.profile). Activation is that one option plus a
//     service restart; mihomo's mixin and run/config.yaml are never
//     touched by WellBoard (read-only, coexistence invariant).
//   - Health is the mihomo external-controller /version probe
//     discovered from run/config.yaml; the apply flow gives it 15 s
//     (apply.HealthTimeout) and auto-rolls back to LastGood.
//
// LastGood is seeded at Detect time from the profile UCI currently
// selects, so the FIRST apply by WellBoard rolls back to the owner's
// own working profile rather than to "nothing".
type RouterAdapter struct {
	// ProfilesDir is where WellBoard writes generated profiles.
	ProfilesDir string
	// RunDir is nikki's runtime directory (read-only for WellBoard).
	RunDir string
	// MihomoBin is the core binary used for `-t` validation.
	MihomoBin string
	// UCIBin is the uci(1) executable.
	UCIBin string
	// InitScript is the nikki procd init script.
	InitScript string
	// ControllerAddr / APISecret override the coordinates discovered
	// from run/config.yaml (empty = use what Detect found).
	ControllerAddr string
	APISecret      string
	// Log receives one line per action (nil = silent).
	Log func(format string, args ...any)
	// Run executes an external command; nil = exec.CommandContext.
	// Injected by tests.
	Run CommandRunner
	// HealthPoll is the delay between API probes in Health.
	HealthPoll time.Duration
	// OpkgControlPath is the opkg control file probed for the nikki
	// version on 23.05/24.10; ApkDBPath is the apk-tools database probed
	// on 25.12+. Fields (not constants) so tests can use fixtures.
	OpkgControlPath string
	ApkDBPath       string

	mu          sync.Mutex
	lastGood    string
	lastGoodSet bool
}

// CommandRunner runs a command and returns its combined output.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Default paths of the nikki package on OpenWrt.
const (
	DefaultProfilesDir = "/etc/nikki/profiles"
	DefaultRunDir      = "/etc/nikki/run"
	DefaultMihomoBin   = "/usr/bin/mihomo"
	DefaultUCIBin      = "uci"
	DefaultInitScript  = "/etc/init.d/nikki"
	// DefaultOpkgControl is the opkg database entry (23.05/24.10) and
	// DefaultApkDB the apk-tools database (25.12+).
	DefaultOpkgControl = "/usr/lib/opkg/info/nikki.control"
	DefaultApkDB       = "/lib/apk/db/installed"
)

// NewRouterAdapter returns an adapter with production defaults.
func NewRouterAdapter() *RouterAdapter {
	return &RouterAdapter{
		ProfilesDir:     DefaultProfilesDir,
		RunDir:          DefaultRunDir,
		MihomoBin:       DefaultMihomoBin,
		UCIBin:          DefaultUCIBin,
		InitScript:      DefaultInitScript,
		OpkgControlPath: DefaultOpkgControl,
		ApkDBPath:       DefaultApkDB,
		HealthPoll:      300 * time.Millisecond,
	}
}

func (d *RouterAdapter) logf(format string, args ...any) {
	if d.Log != nil {
		d.Log(format, args...)
	}
}

// run executes a command through the injected runner (or the real
// process runner when none is set).
func (d *RouterAdapter) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if d.Run != nil {
		return d.Run(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// configPath is the nikki runtime config WellBoard only ever reads.
func (d *RouterAdapter) configPath() string {
	return filepath.Join(d.RunDir, "config.yaml")
}

// profilePath returns the on-disk location of a profile name.
func (d *RouterAdapter) profilePath(name string) string {
	return filepath.Join(d.ProfilesDir, sanitizeName(name)+".yaml")
}

// Detect reports the installed nikki/mihomo versions, paths and the
// mihomo API coordinates, and seeds LastGood from the currently
// selected profile so a failed first apply can roll back to it.
func (d *RouterAdapter) Detect() (Info, error) {
	info := Info{ProfilesDir: d.ProfilesDir, RunDir: d.RunDir}
	fi, err := os.Stat(d.ProfilesDir)
	if err != nil || !fi.IsDir() {
		return info, fmt.Errorf("nikki: profiles dir %s is not a directory (is nikki installed?)", d.ProfilesDir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if out, err := d.run(ctx, d.MihomoBin, "-v"); err == nil {
		info.MihomoVersion = firstLine(out)
	}
	info.NikkiVersion = d.nikkiVersion(ctx)

	// The profile UCI selects right now is the rollback target for the
	// first WellBoard apply — the owner's working configuration.
	if out, err := d.run(ctx, d.UCIBin, "-q", "get", "nikki.config.profile"); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			if _, statErr := os.Stat(d.profilePath(name)); statErr == nil {
				d.mu.Lock()
				d.lastGood, d.lastGoodSet = name, true
				d.mu.Unlock()
				d.logf("nikki: current profile %q recorded as rollback target", name)
			}
		}
	}

	// External-controller coordinates live in the runtime config
	// (read-only). Without them Health falls back to the service probe.
	if data, err := os.ReadFile(d.configPath()); err == nil {
		var doc struct {
			ExternalController string `yaml:"external-controller"`
			Secret             string `yaml:"secret"`
		}
		if yaml.Unmarshal(data, &doc) == nil {
			info.APIAddr, info.APISecret = doc.ExternalController, doc.Secret
		}
	}
	if d.ControllerAddr != "" {
		info.APIAddr = d.ControllerAddr
	}
	if d.APISecret != "" {
		info.APISecret = d.APISecret
	}
	return info, nil
}

// nikkiVersion reads the installed nikki package version: opkg's
// database on 23.05/24.10, the apk-tools database on 25.12+ (OpenWrt
// switched package managers there), "" when neither is readable — the
// UI shows "unknown" rather than failing Detect.
func (d *RouterAdapter) nikkiVersion(ctx context.Context) string {
	if out, err := d.run(ctx, "opkg", "list-installed", "nikki"); err == nil {
		// "nikki - 1.2.3" style line
		fields := strings.Fields(string(out))
		if len(fields) >= 4 {
			return fields[3]
		}
		if len(fields) >= 3 {
			return fields[len(fields)-1]
		}
	}
	if data, err := os.ReadFile(d.opkgControlPath()); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Version:"); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	if data, err := os.ReadFile(d.apkDBPath()); err == nil {
		if v := versionFromApkDB(string(data), "nikki"); v != "" {
			return v
		}
	}
	return ""
}

// opkgControlPath / apkDBPath fall back to the production defaults so
// adapters built by hand (tests) do not need every field set.
func (d *RouterAdapter) opkgControlPath() string {
	if d.OpkgControlPath != "" {
		return d.OpkgControlPath
	}
	return DefaultOpkgControl
}

func (d *RouterAdapter) apkDBPath() string {
	if d.ApkDBPath != "" {
		return d.ApkDBPath
	}
	return DefaultApkDB
}

// versionFromApkDB returns the version of the record whose package name
// is name ("" when the package is absent). The apk installed database
// stores one record per package: "P:<name>", "V:<version>", … blocks.
func versionFromApkDB(db, name string) string {
	current := ""
	for _, line := range strings.Split(db, "\n") {
		switch {
		case strings.HasPrefix(line, "P:"):
			current = strings.TrimSpace(strings.TrimPrefix(line, "P:"))
		case strings.HasPrefix(line, "V:"):
			if current == name {
				return strings.TrimSpace(strings.TrimPrefix(line, "V:"))
			}
		}
	}
	return ""
}

// WriteProfile stores the generated profile under ProfilesDir with an
// atomic rename. Existing files are not consulted: WellBoard only ever
// writes names it minted itself (wellboard-<timestamp>-<seq>), so the
// owner's profiles are never overwritten.
func (d *RouterAdapter) WriteProfile(name string, yamlData []byte) (string, error) {
	if err := os.MkdirAll(d.ProfilesDir, 0o755); err != nil {
		return "", fmt.Errorf("router: create profiles dir: %w", err)
	}
	path := d.profilePath(name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, yamlData, 0o644); err != nil {
		return "", fmt.Errorf("router: write profile: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("router: commit profile: %w", err)
	}
	d.logf("nikki: wrote profile %s (%d bytes)", path, len(yamlData))
	return path, nil
}

// Validate runs `mihomo -t -d <profiles dir> -f <path>`: the same
// check the core performs at start, against the exact bytes that would
// run.
func (d *RouterAdapter) Validate(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := d.run(ctx, d.MihomoBin, "-t", "-d", d.ProfilesDir, "-f", path)
	if err != nil {
		return fmt.Errorf("mihomo -t %s: %w: %s", path, err, firstLine(out))
	}
	d.logf("nikki: mihomo -t OK for %s", path)
	return nil
}

// Activate selects the profile in UCI and restarts nikki. Only
// nikki.config.profile changes: the mixin and the runtime config stay
// byte-identical (coexistence invariant).
func (d *RouterAdapter) Activate(name string) error {
	name = sanitizeName(name)
	if _, err := os.Stat(d.profilePath(name)); err != nil {
		return fmt.Errorf("router activate: profile %q is not in %s: %w", name, d.ProfilesDir, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if out, err := d.run(ctx, d.UCIBin, "set", "nikki.config.profile="+name); err != nil {
		return fmt.Errorf("router activate: uci set profile: %w: %s", err, firstLine(out))
	}
	if out, err := d.run(ctx, d.UCIBin, "commit", "nikki"); err != nil {
		return fmt.Errorf("router activate: uci commit: %w: %s", err, firstLine(out))
	}
	if out, err := d.run(ctx, d.InitScript, "restart"); err != nil {
		return fmt.Errorf("router activate: restart nikki: %w: %s", err, firstLine(out))
	}
	d.logf("nikki: activated %s (uci profile + service restart)", name)
	return nil
}

// Health waits (up to timeout) for the core to answer after a restart.
// With external-controller coordinates the check is the API /version
// probe; without them it falls back to the init-script status.
func (d *RouterAdapter) Health(timeout time.Duration) error {
	if d.apiAddr() != "" {
		return d.apiHealth(timeout)
	}
	return d.serviceHealth(timeout)
}

// apiAddr resolves the external-controller address (field wins over
// the discovered config).
func (d *RouterAdapter) apiAddr() string {
	if d.ControllerAddr != "" {
		return d.ControllerAddr
	}
	if data, err := os.ReadFile(d.configPath()); err == nil {
		var doc struct {
			ExternalController string `yaml:"external-controller"`
		}
		if yaml.Unmarshal(data, &doc) == nil {
			return doc.ExternalController
		}
	}
	return ""
}

// apiSecret resolves the API secret from the runtime config.
func (d *RouterAdapter) apiSecret() string {
	if d.APISecret != "" {
		return d.APISecret
	}
	if data, err := os.ReadFile(d.configPath()); err == nil {
		var doc struct {
			Secret string `yaml:"secret"`
		}
		if yaml.Unmarshal(data, &doc) == nil {
			return doc.Secret
		}
	}
	return ""
}

// apiHealth polls GET /version on the external controller. Fresh
// connections per attempt (keep-alive would pin a stale socket).
func (d *RouterAdapter) apiHealth(timeout time.Duration) error {
	addr := d.apiAddr()
	secret := d.apiSecret()
	url := "http://" + addr + "/version"
	deadline := time.Now().Add(timeout)
	poll := d.HealthPoll
	if poll <= 0 {
		poll = 300 * time.Millisecond
	}
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("mihomo API /version: status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(poll)
	}
	return fmt.Errorf("mihomo API health check failed within %s: %w", timeout, lastErr)
}

// serviceHealth is the fallback probe when the runtime config carries
// no external-controller: the init script reports the service state.
func (d *RouterAdapter) serviceHealth(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	poll := d.HealthPoll
	if poll <= 0 {
		poll = 300 * time.Millisecond
	}
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := d.run(ctx, d.InitScript, "status")
		cancel()
		if err == nil && !strings.Contains(strings.ToLower(string(out)), "not running") {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("nikki status reports not running")
		}
		time.Sleep(poll)
	}
	return fmt.Errorf("nikki service health check failed within %s: %w", timeout, lastErr)
}

// LastGood reports the profile a failed apply rolls back to.
func (d *RouterAdapter) LastGood() (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastGood, d.lastGoodSet
}

// MarkHealthy promotes a profile to the rollback target after the
// apply flow confirmed health (FR-6.4).
func (d *RouterAdapter) MarkHealthy(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastGood = sanitizeName(name)
	d.lastGoodSet = true
}
