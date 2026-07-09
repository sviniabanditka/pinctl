// Command pinctl is the v0.1 walking-skeleton binary: open one serial port
// to a StandardFirmata board and serve the raw-pin REST API over it. See
// docs/12-roadmap.md's "v0.1 — walking skeleton" for scope.
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

	"github.com/sviniabanditka/pinctl/internal/api"
	"github.com/sviniabanditka/pinctl/internal/board"
)

func main() {
	port := flag.String("port", "", "serial port the board is connected to, e.g. /dev/ttyACM0 (required)")
	listen := flag.String("listen", ":8422", "HTTP listen address")
	flag.Parse()

	if *port == "" {
		log.Fatal("pinctl: -port is required, e.g. -port=/dev/ttyACM0")
	}

	log.Printf("pinctl: opening board on %s", *port)
	sess, err := board.Open(*port)
	if err != nil {
		log.Fatalf("pinctl: opening board: %v", err)
	}
	defer sess.Close()

	name, major, minor := sess.Firmware()
	log.Printf("pinctl: connected: firmware %q %d.%d, %d pins reported", name, major, minor, len(sess.Capabilities()))

	srv := &http.Server{
		Addr:    *listen,
		Handler: api.NewHandler(sess, *port),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("pinctl: http shutdown: %v", err)
		}
	}
}
