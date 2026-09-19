// Command wellboard is the WellBoard daemon entry point.
//
// Phase 0: skeleton + /api/v1/health. Phases 1-2: store, generator,
// nikki adapter, subscriptions/HWID/scheduler (library level). Phase 3
// wires the REST API surface (sources/servers/groups/routes/templates/
// lan-devices/settings + profile preview) on top of the store.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wellboard/wellboard/internal/api"
	"github.com/wellboard/wellboard/internal/lan"
	"github.com/wellboard/wellboard/internal/model"
	"github.com/wellboard/wellboard/internal/store"
	"github.com/wellboard/wellboard/internal/templates"
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
)

func main() {
	flag.Parse()

	dev := *devFlag
	stateDir := *stateFlag
	tplDir := *templatesFlag
	if dev {
		if stateDir == "" {
			stateDir = "dev"
		}
		if tplDir == "" {
			tplDir = "templates"
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", api.HealthHandler(version))
	srv.Register(mux)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	mode := ""
	if dev {
		mode = " (dev mode)"
	}
	log.Printf("wellboard %s listening on %s%s (state=%s templates=%s)",
		version, addr, mode, stateDir, tplDir)

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen and serve: %v", err)
		}
	}()

	// Shut down gracefully on SIGINT/SIGTERM (procd sends SIGTERM on stop).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Printf("wellboard %s shutting down", version)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// staticLeases implements api.SetStater: on the router it delegates to
// lan.SetStatic (UCI); in dev mode it returns the lan.ErrDevStub
// sentinel so the API answers "simulated" with the documented commands.
type staticLeases struct{ dev bool }

func (s *staticLeases) SetStatic(dev model.LANDevice) error {
	return lan.SetStatic(dev, s.dev)
}
