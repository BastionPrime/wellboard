// Package nikki integrates WellBoard with the nikki OpenWrt package that
// manages the mihomo core (initial TZ 5.5, docs/DECISIONS.md N1-N7).
//
// The Adapter interface is the transport-agnostic contract: WellBoard
// writes a profile, validates it with the real mihomo binary, activates it
// via UCI, and watches service health. DryRunAdapter implements the same
// contract for development hosts without OpenWrt/nikki (initial TZ 5.10);
// with a real mihomo binary + transport options it ACTUALLY runs a local
// mihomo process (Phase 5 FR-6 e2e: "kill mihomo → auto-rollback").
package nikki

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
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

// DryRunAdapter is the development-mode Adapter (initial TZ 5.10):
// profiles go to a temp directory and validation runs a local mihomo
// binary if one is available; otherwise validation is a no-op success
// (logged by the caller).
//
// Phase 5: when MihomoBin exists AND Transport is configured, Activate
// (re)starts a REAL local mihomo with the profile plus the transport
// options (mixed-port, external-controller — the parts nikki's mixin
// owns on the router), Health polls the external-controller /version,
// and LastGood tracks the last profile that passed activation AND
// health. Killing the process makes Health fail → the apply flow
// auto-rolls back (FR-6.4 e2e scenario).
type DryRunAdapter struct {
	// Dir is where profiles are written (created on demand).
	Dir string
	// MihomoBin is the local mihomo binary path (e.g. ./bin/mihomo);
	// may be missing, in which case Validate is skipped.
	MihomoBin string
	// Log receives one line per simulated action (dev diagnostics).
	Log func(format string, args ...any)
	// Transport holds the dev-stand mihomo transport options appended
	// to the profile on Activate (mixed-port + external-controller,
	// the TZ §5.10 dev model). Empty = Activate stays simulated.
	Transport TransportOptions

	// lastGood is the last profile that activated AND passed health.
	lastGood    string
	lastGoodSet bool

	// proc is the running local mihomo (nil when simulated/off).
	// procLog is its log file path (diagnostics).
	mu       sync.Mutex
	proc     *exec.Cmd
	procDir  string
	procPath string
	procLog  string
}

// TransportOptions are the mihomo transport lines the dev stand adds
// on top of the generated profile (nikki mixin's role on the router).
type TransportOptions struct {
	// MixedPort is the local SOCKS/HTTP port (e.g. 17890).
	MixedPort int
	// ControllerAddr is the external-controller listen address
	// (e.g. "127.0.0.1:19090").
	ControllerAddr string
	// APISecret is the external-controller bearer secret ("" = none).
	APISecret string
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

// realMode reports whether Activate/Health run a real local mihomo.
func (d *DryRunAdapter) realMode() bool {
	return d.MihomoBin != "" && d.Transport.ControllerAddr != ""
}

// runnable reports whether the configured mihomo binary exists.
func (d *DryRunAdapter) runnable() bool {
	if d.MihomoBin == "" {
		return false
	}
	_, err := os.Stat(d.MihomoBin)
	return err == nil
}

// Detect reports the dry-run layout and the local mihomo version, if any.
func (d *DryRunAdapter) Detect() (Info, error) {
	info := Info{
		ProfilesDir: d.Dir,
		RunDir:      d.Dir,
	}
	if d.realMode() {
		info.APIAddr = "http://" + d.Transport.ControllerAddr
		info.APISecret = d.Transport.APISecret
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
	if !d.runnable() {
		d.logf("dry-run: no mihomo binary, skipping validation of %s", path)
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

// transportConfig renders the dev-stand transport preamble that is
// prepended to the profile (mirrors nikki's mixin role, N3).
func (d *DryRunAdapter) transportConfig() string {
	var b []byte
	b = append(b, "mixed-port: "+strconv.Itoa(d.Transport.MixedPort)+"\n"...)
	b = append(b, "external-controller: "+d.Transport.ControllerAddr+"\n"...)
	if d.Transport.APISecret != "" {
		b = append(b, "secret: "+d.Transport.APISecret+"\n"...)
	}
	b = append(b, "log-level: info\n"...)
	return string(b)
}

// runConfigPath returns the runnable config location for name: a
// copy of the profile plus the transport preamble, written into the
// profile dir as <name>.run.yaml. Copying (not mutating) keeps the
// pristine profile on disk for validate/history (FR-6.3).
func (d *DryRunAdapter) runConfigPath(name string) (string, error) {
	profilePath := filepath.Join(d.Dir, sanitizeName(name)+".yaml")
	profile, err := os.ReadFile(profilePath)
	if err != nil {
		return "", fmt.Errorf("dry-run activate: read profile %s: %w", profilePath, err)
	}
	runPath := filepath.Join(d.Dir, sanitizeName(name)+".run.yaml")
	full := []byte(d.transportConfig())
	full = append(full, profile...)
	if err := os.WriteFile(runPath, full, 0o644); err != nil {
		return "", fmt.Errorf("dry-run activate: write run config: %w", err)
	}
	return runPath, nil
}

// stop kills the running local mihomo, if any. Idempotent.
func (d *DryRunAdapter) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked()
}

// stopLocked kills proc; caller holds d.mu.
func (d *DryRunAdapter) stopLocked() {
	if d.proc == nil {
		return
	}
	_ = d.proc.Process.Kill()
	_, _ = d.proc.Process.Wait()
	d.logf("dry-run: stopped local mihomo (pid was %d, config %s)", d.proc.Process.Pid, d.procPath)
	d.proc = nil
}

// Activate selects the profile. In real mode it stops any previous
// local mihomo and starts a new one with the profile + transport
// preamble; failure to START returns an error (apply flow aborts
// before health). In simulated mode it just records the name.
func (d *DryRunAdapter) Activate(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.realMode() || !d.runnable() {
		// The apply flow calls MarkHealthy after the health check passes,
		// so LastGood tracks actually-healthy profiles in real mode.
		// Simulated mode: last activated name is good.
		d.lastGood = name
		d.lastGoodSet = true
		d.logf("dry-run: activate %s (simulated)", name)
		return nil
	}

	runPath, err := d.runConfigPath(name)
	if err != nil {
		return err
	}
	d.stopLocked()
	// Resolve the binary to an absolute path: cmd.Dir is set to the
	// profile dir, so a relative MihomoBin would not resolve.
	bin := d.MihomoBin
	if !filepath.IsAbs(bin) {
		if abs, err := filepath.Abs(bin); err == nil {
			bin = abs
		}
	}
	cmd := exec.Command(bin, "-d", d.Dir, "-f", runPath)
	// Redirect output into a log file next to the profile so a failed
	// start can be diagnosed (the API surfaces the path on error).
	logPath := filepath.Join(d.Dir, sanitizeName(name)+".mihomo.log")
	logF, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("dry-run activate: create log: %w", err)
	}
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.Dir = d.Dir
	if err := cmd.Start(); err != nil {
		logF.Close()
		return fmt.Errorf("dry-run activate: start mihomo: %w", err)
	}
	// Reap the child when it exits so it does not linger as a zombie.
	go func() { _ = cmd.Wait() }()
	d.proc = cmd
	d.procDir = d.Dir
	d.procPath = runPath
	d.procLog = logPath
	d.logf("dry-run: activated %s (mihomo pid %d, api %s, log %s)",
		name, cmd.Process.Pid, d.Transport.ControllerAddr, logPath)
	return nil
}

// MihomoLogPath returns the log file of the currently running local
// mihomo (dev diagnostics / logs screen; "" when simulated).
func (d *DryRunAdapter) MihomoLogPath() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.proc == nil {
		return ""
	}
	return d.procLog
}

// RunningPID returns the pid of the local mihomo, 0 when none.
func (d *DryRunAdapter) RunningPID() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.proc == nil {
		return 0
	}
	return d.proc.Process.Pid
}

// runningAlive reports whether the child is still alive (Signal 0).
func (d *DryRunAdapter) runningAlive() bool {
	if d.proc == nil {
		return false
	}
	return d.proc.Process.Signal(syscall.Signal(0)) == nil
}

// Health polls the external-controller /version within timeout:
// 200/204 answers mean the core is up (FR-6.4). In real mode a dead
// child is an immediate failure; in simulated mode health is ok.
// When the API is secret-protected, the Bearer token is sent.
func (d *DryRunAdapter) Health(timeout time.Duration) error {
	d.mu.Lock()
	proc := d.proc
	secret := d.Transport.APISecret
	api := d.Transport.ControllerAddr
	real := d.realMode() && d.runnable()
	d.mu.Unlock()

	if !real {
		d.logf("dry-run: health check (simulated ok, timeout %s)", timeout)
		return nil
	}
	if proc == nil {
		return fmt.Errorf("dry-run: no local mihomo running (activate first)")
	}
	if !d.runningAlive() {
		return fmt.Errorf("dry-run: local mihomo process is not running (crashed?)")
	}

	url := "http://" + api + "/version"
	deadline := time.Now().Add(timeout)
	// DisableKeepAlives: each poll must open a FRESH connection. With
	// keep-alive the first (failing) attempt pins a connection to
	// whatever answered first — e.g. a transient port holder — and
	// later polls keep talking to it even after the real service has
	// taken the port (verified with a bind/close probe, Phase 5).
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
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
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("mihomo API /version: status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("mihomo API health check failed within %s: %w", timeout, lastErr)
}

// LastGood reports the last profile that passed activation and health.
// In real mode a profile only becomes "good" after a successful Health
// call (set via MarkHealthy by the apply flow); in simulated mode the
// last activated name is it.
func (d *DryRunAdapter) LastGood() (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastGood, d.lastGoodSet
}

// MarkHealthy promotes the current activated profile to "last good"
// after the health check passed (called by the apply flow; FR-6.4).
// Without it LastGood would lag one apply behind in real mode.
func (d *DryRunAdapter) MarkHealthy(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastGood = name
	d.lastGoodSet = true
}

// Stop terminates the local mihomo (daemon shutdown / tests cleanup).
func (d *DryRunAdapter) Stop() { d.stop() }

// ErrNotRunning is returned by operations that require a live child.
var ErrNotRunning = errors.New("dry-run: local mihomo not running")

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
