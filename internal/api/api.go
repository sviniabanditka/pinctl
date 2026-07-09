// Package api implements the v0.1 walking-skeleton HTTP layer over a single
// board.Session: read/write a pin directly, no device model, no pipeline,
// no persistence (see docs/12-roadmap.md's "v0.1 — walking skeleton").
//
// ponytail: v0.1 raw-pin API, replaced by device-level API (docs/10) in v0.2
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/sviniabanditka/pinctl/internal/board"
)

// Session is the subset of *board.Session the API handlers use. Declaring
// it here (rather than depending on the concrete type) lets tests exercise
// the handlers with a fake, per docs/13-testing.md's fake-board pattern.
type Session interface {
	Alive() bool
	Firmware() (name string, major, minor int)
	ProtocolVersion() (major, minor int)
	Capabilities() []board.PinCapability
	SetPinMode(pin int, mode board.PinMode) error
	DigitalWrite(pin int, high bool) error
	AnalogWrite(pin int, value byte) error
	DigitalRead(pin int) (bool, error)
	AnalogRead(channel int) (int, error)
}

// NewHandler builds the /api/v1 HTTP handler over sess. port is the serial
// port path the session was opened against, reported back verbatim in
// GET /api/v1/board (Session itself has no such accessor, since v0.1's
// board.Session is deliberately single-connection and doesn't remember it).
func NewHandler(sess Session, port string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/board", handleGetBoard(sess, port))
	mux.HandleFunc("POST /api/v1/pins/{pin}/mode", handleSetPinMode(sess))
	mux.HandleFunc("GET /api/v1/pins/{pin}", handleReadPin(sess))
	mux.HandleFunc("POST /api/v1/pins/{pin}", handleWritePin(sess))
	return logRequests(mux)
}

// logRequests is the one logging middleware the package uses.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func pinFromPath(r *http.Request) (int, bool) {
	pin, err := strconv.Atoi(r.PathValue("pin"))
	if err != nil || pin < 0 || pin > 127 {
		return 0, false
	}
	return pin, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errorResponse is the docs/10-api.md error envelope.
type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: errorBody{Code: code, Message: message}})
}

func writeBadRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "bad_request", message)
}

// writeBoardError maps a board package sentinel error (or a wrapped one) to
// the appropriate status code and error envelope.
func writeBoardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, board.ErrUnknownPin):
		writeError(w, http.StatusNotFound, "unknown_pin", err.Error())
	case errors.Is(err, board.ErrUnsupportedMode):
		writeError(w, http.StatusConflict, "unsupported_mode", err.Error())
	case errors.Is(err, board.ErrSessionDead):
		writeError(w, http.StatusServiceUnavailable, "session_dead", err.Error())
	case errors.Is(err, board.ErrNoCachedValue):
		writeError(w, http.StatusNotFound, "no_cached_value", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
