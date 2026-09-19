// Command wellboard is the WellBoard daemon entry point.
//
// Phase 0: skeleton + /api/v1/health. Phases 1-2: store, generator,
// nikki adapter, subscriptions/HWID/scheduler (library level). Phase 3
// wires the REST API surface (sources/servers/groups/routes/templates/
// lan-devices/settings + profile preview) on top of the store. Phase 4
// adds the embedded SPA. Phase 5 adds the apply/rollback flow, app
// logs, diagnostics and the metacubexd monitoring surface.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/wellboard/wellboard/internal/api"
	"github.com/wellboard/wellboard/internal/applog"
	"github.com/wellboard/wellboard/internal/apply"
	"github.com/wellboard/wellboard/internal/lan"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/nikki"
	"github.com/wellboard/wellboard/internal/store"
	"github.com/wellboard/wellboard/internal/templates"
	"github.com/wellboard/wellboard/web"
)

// version is the reported application version. It defaults to "dev" and is
// meant to be overridden at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// defaultPort is the WellBoard UI port (customer decision Q6, initial TZ 2).
const defaultPort = "8090"

// flags / env for dev mode (initial TZ 5.10).
var (
	devFlag       = flag.Bool("dev", false, "enable development mode")
	stateFlag     = flag.String("state", "", "state directory (default: /etc/wellboard; dev: ./dev)")
	templatesFlag = flag.String("templates", "", "templates directory (default: ./templates in dev)")
	leasesFlag    = flag.String("leases", "", "DHCP leases file override (dev fixtures)")
	mihomoBinFlag = flag.String("mihomo", "", "local mihomo binary for the dry-run stand (default: ./bin/mihomo in dev)")
)

// Dev-stand transport (TZ §5.10: DryRun + local mihomo): mixed-port,
// external-controller port and optional secret from env so parallel
// e2e runs do not collide (MIHOMO_MIXED_PORT / MIHOMO_CONTROLLER_PORT
// / MIHOMO_API_SECRET; defaults 17890/19090/empty).

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	flag.Parse()

	dev := *devFlag
	stateDir := *stateFlag
	tplDir := *templatesFlag
	mihomoBin := *mihomoBinFlag
	if dev {
		if stateDir == "" {
			stateDir = "dev"
		}
		if tplDir == "" {
			tplDir = "templates"
		}
		if mihomoBin == "" {
			mihomoBin = "bin/mihomo"
		}
	} else {
		if stateDir == "" {
			stateDir = "/etc/wellboard"
		}
		if tplDir == "" {
			tplDir = "/usr/share/wellboard/templates"
		}
	}

	port := os.Getenv("WELLBOARD_PORT")
	if port == "" {
		port = defaultPort
	}
	addr := ":" + port

	stStore := store.New(stateDir, !dev)

	// App log (FR-9.2): capture every log line into a bounded buffer +
	// mirror file; GET /api/v1/logs serves the tail.
	appLogFile := filepath.Join(stateDir, "wellboard.log")
	appLog := applog.New(appLogFile, 0)
	log.SetOutput(appLog) // ALL log output lands in the buffer + file
	defer appLog.Close()

	// Template catalog (FR-5): fatal in prod (the package is broken),
	// warning-only in dev (the dir may be absent in a bare checkout).
	catalog, err := templates.Load(tplDir)
	if err != nil {
		if dev {
			log.Printf("warning: templates: %v (template API disabled)", err)
		} else {
			log.Fatalf("templates: %v", err)
		}
	}

	srv := api.NewServer(stStore, catalog)
	srv.SetLog(func(format string, args ...any) { log.Printf(format, args...) })

	// LAN devices (FR-4.4): lease file + ubus fallback; dev mode uses the
	// fixture/override without ubus and a static-lease stub.
	lanReader := &lan.Reader{
		LeaseFile: *leasesFlag,
	}
	if !dev {
		lanReader.UbusSocket = lan.UbusSocket
	}
	static := &staticLeases{dev: dev}
	srv.SetLAN(lanReader, static)
	api.SetDevStubSentinel(lan.ErrDevStub)

	// Phase 5: the nikki adapter (dry-run stand: real local mihomo).
	// Transport ports come from env (e2e parallelism); on the router
	// the real Adapter reads the coordinates from UCI (phase 6).
	adapter := newAdapter(dev, stateDir, mihomoBin, log.Printf)
	var apiAddr, apiSecret string
	if info, err := adapter.Detect(); err == nil {
		apiAddr, apiSecret = info.APIAddr, info.APISecret
	}
	if a, s, ok := transportFromEnv(); ok {
		apiAddr, apiSecret = a, s
	}

	applyDir := filepath.Join(stateDir, "apply")
	applyer := apply.New(adapter, stStore, catalogProviders(catalog), applyDir)
	srv.SetMonitor(api.MonitorConfig{
		Adapter:         adapter,
		Applyer:         applyer,
		Logs:            appLog,
		MihomoAPIAddr:   apiAddr,
		MihomoAPISecret: apiSecret,
		UIDir:           uiDir(dev),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", api.HealthHandler(version))
	srv.Register(mux)
	// SPA (phase 4): the embedded Vue bundle is served at / in dev mode
	// (web/embed.go, DECISIONS D13). In prod the UI is served from LuCI
	// (phase 6); serving it unconditionally is harmless and keeps the
	// binary self-contained for testing.
	mux.Handle("/", web.SPAHandler())

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	mode := ""
	if dev {
		mode = " (dev mode)"
	}
	log.Printf("wellboard %s listening on %s%s (state=%s templates=%s)", version, addr, mode, stateDir, tplDir)
	if apiAddr != "" {
		log.Printf("monitoring: mihomo API %s (proxied at /api/mihomo/*), metacubexd at /ui/metacubexd/", apiAddr)
	}

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen and serve: %v", err)
		}
	}()

	// Shut down gracefully on SIGINT/SIGTERM (procd sends SIGTERM on
	// stop). The local dry-run mihomo is stopped too.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Printf("wellboard %s shutting down", version)
	if dry, ok := adapter.(*nikki.DryRunAdapter); ok {
		dry.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// newAdapter builds the nikki integration for this host: the real
// UCI/procd adapter on the router (phase 6) and the DryRunAdapter
// elsewhere. Dev stand: local mihomo binary + transport from env.
func newAdapter(dev bool, stateDir, mihomoBin string, logf func(string, ...any)) nikki.Adapter {
	if !dev {
		// Router mode lands with phase 6 packaging; until then the
		// daemon would not run on a router anyway.
		return nikki.NewDryRunAdapter("/etc/nikki/profiles", "/usr/bin/mihomo")
	}
	d := nikki.NewDryRunAdapter(filepath.Join(stateDir, "nikki-profiles"), mihomoBin)
	d.Log = logf
	if m, c, s, ok := devTransport(); ok {
		d.Transport = nikki.TransportOptions{MixedPort: m, ControllerAddr: c, APISecret: s}
	}
	return d
}

// devTransport parses the dev-stand transport env (ports + secret).
func devTransport() (mixed int, controller, secret string, ok bool) {
	mixedPort := envOrDefault("MIHOMO_MIXED_PORT", "17890")
	controllerPort := envOrDefault("MIHOMO_CONTROLLER_PORT", "19090")
	m, err := strconv.Atoi(mixedPort)
	if err != nil || m <= 0 || m > 65535 {
		return 0, "", "", false
	}
	cp, err := strconv.Atoi(controllerPort)
	if err != nil || cp <= 0 || cp > 65535 {
		return 0, "", "", false
	}
	return m, "127.0.0.1:" + controllerPort, os.Getenv("MIHOMO_API_SECRET"), true
}

// transportFromEnv returns the API coordinates derived from the env
// transport (dev stand). ok=false when unset/invalid.
func transportFromEnv() (addr, secret string, ok bool) {
	_, c, s, ok := devTransport()
	if !ok {
		return "", "", false
	}
	return c, s, true
}

// catalogProviders flattens the catalog into the provider-name set the
// apply flow validates against (nil when the catalog is unavailable).
func catalogProviders(catalog *templates.Catalog) map[string]bool {
	if catalog == nil {
		return nil
	}
	m := make(map[string]bool, len(catalog.Templates))
	for _, tpl := range catalog.Templates {
		for _, p := range tpl.Providers {
			m[p] = true
		}
	}
	return m
}

// uiDir returns the metacubexd dist directory (DECISIONS D18:
// gitignored, fetched by scripts/fetch-metacubexd.sh).
func uiDir(dev bool) string {
	if dev {
		if _, err := os.Stat("ui/metacubexd/index.html"); err == nil {
			return "ui/metacubexd"
		}
	}
	// Fallback: alongside the state (deployments may place it there).
	if _, err := os.Stat("/usr/share/wellboard/ui/metacubexd/index.html"); err == nil {
		return "/usr/share/wellboard/ui/metacubexd"
	}
	return ""
}

// staticLeases implements api.SetStater: on the router it delegates to
// lan.SetStatic (UCI); in dev mode it returns the lan.ErrDevStub
// sentinel so the API answers "simulated" with the documented commands.
type staticLeases struct{ dev bool }

func (s *staticLeases) SetStatic(dev model.LANDevice) error {
	return lan.SetStatic(dev, s.dev)
}
