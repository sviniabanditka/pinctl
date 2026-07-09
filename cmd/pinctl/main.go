// Command pinctl is the v0.2 binary: one board.Session, a persisted device/
// pipeline store, the tick engine driving every enabled pipeline, and the
// REST API over all of it. See docs/12-roadmap.md's "v0.2 — engine +
// minimal node set" for scope.
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
	"syscall"
	"time"

	"github.com/sviniabanditka/pinctl/internal/api"
	"github.com/sviniabanditka/pinctl/internal/board"
	"github.com/sviniabanditka/pinctl/internal/boardio"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
	"github.com/sviniabanditka/pinctl/internal/store"
)

// defaultDataDir is docs/09-data-model.md's dev default (~/.pinctl/);
// production installs override it with -data=/var/lib/pinctl.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pinctl"
	}
	return filepath.Join(home, ".pinctl")
}

func main() {
	port := flag.String("port", "", "serial port the board is connected to, e.g. /dev/ttyACM0 (required)")
	listen := flag.String("listen", ":8422", "HTTP listen address")
	dataDir := flag.String("data", defaultDataDir(), "data directory for devices.json and pipelines/")
	flag.Parse()

	if *port == "" {
		log.Fatal("pinctl: -port is required, e.g. -port=/dev/ttyACM0")
	}

	log.Printf("pinctl: opening store at %s", *dataDir)
	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("pinctl: opening store: %v", err)
	}

	log.Printf("pinctl: opening board on %s", *port)
	sess, err := board.Open(*port)
	if err != nil {
		log.Fatalf("pinctl: opening board: %v", err)
	}
	defer sess.Close()

	fwName, fwMajor, fwMinor := sess.Firmware()
	log.Printf("pinctl: connected: firmware %q %d.%d, %d pins reported", fwName, fwMajor, fwMinor, len(sess.Capabilities()))

	devices := st.ListDevices()
	if err := boardio.ApplyPinModes(sess, devices); err != nil {
		log.Printf("pinctl: applying pin modes at startup: %v", err)
	}

	io := boardio.New(sess)
	mgr := engine.NewManager()

	deviceIndex := devicesByID(devices)
	for _, p := range st.ListPipelines() {
		if !p.Enabled {
			continue
		}
		if err := mgr.Start(p, deviceIndex, io); err != nil {
			log.Printf("pinctl: starting pipeline %s (%s): %v", p.ID, p.Name, err)
		}
	}

	apiServer := api.NewServer(sess, *port, st, mgr, io)
	srv := &http.Server{
		Addr:    *listen,
		Handler: apiServer,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// v0.3: boards/devices/pipelines WS topic publishers (docs/10-api.md),
	// stopped alongside the pipeline engine on shutdown below.
	stopPublishers := apiServer.StartPublishers(ctx)

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("pinctl: listening on %s", *listen)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("pinctl: http server: %v", err)
		}
	case <-ctx.Done():
		log.Print("pinctl: shutting down")

		// Stop every pipeline runner first (no pipeline is interrupted
		// mid-tick), then the WS publishers and every connected WS client
		// (docs/10-api.md: "the server closes every WebSocket cleanly...
		// as part of the graceful shutdown sequence"), then the HTTP
		// server; the board session closes via the deferred sess.Close()
		// above once main returns.
		mgr.StopAll()
		stopPublishers()
		apiServer.CloseWS()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("pinctl: http shutdown: %v", err)
		}
	}
}

// devicesByID indexes devices the way internal/engine wants them (keyed by
// ID) for Manager.Start.
func devicesByID(devices []model.Device) map[string]model.Device {
	m := make(map[string]model.Device, len(devices))
	for _, d := range devices {
		m[d.ID] = d
	}
	return m
}
