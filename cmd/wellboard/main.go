// Command wellboard is the WellBoard daemon entry point.
//
// Phase 0 scope: a minimal HTTP server exposing /api/v1/health so that the
// skeleton builds, runs, and is covered by CI. Real functionality lands in
// Phases 1-7 (see docs/initial-tz.md, section 7).
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
)

// version is the reported application version. It defaults to "dev" and is
// meant to be overridden at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// defaultPort is the WellBoard UI port (customer decision Q6, initial TZ 2).
const defaultPort = "8090"

func main() {
	// --dev switches to development mode (initial TZ 5.10): relaxed checks
	// and local state paths. Phase 0 only parses and logs it; the actual
	// DryRun behavior lands with the nikki adapter in Phase 1.
	dev := flag.Bool("dev", false, "enable development mode")
	flag.Parse()

	// WELLBOARD_PORT overrides the listen port.
	port := os.Getenv("WELLBOARD_PORT")
	if port == "" {
		port = defaultPort
	}
	addr := ":" + port

	mux := http.NewServeMux()
	mux.Handle("/api/v1/health", api.HealthHandler(version))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	mode := ""
	if *dev {
		mode = " (dev mode)"
	}
	log.Printf("wellboard %s listening on %s%s", version, addr, mode)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
