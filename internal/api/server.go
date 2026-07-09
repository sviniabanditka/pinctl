// v0.2 adds the device/pipeline REST API described in docs/10-api.md on
// top of the v0.1 raw-pin endpoints in api.go/handlers.go/modes.go, which
// this file leaves untouched. Server holds the extra dependencies those
// endpoints need: a persistent store, the engine's Manager (for hot-swap
// enable/disable), and the DeviceIO adapter the engine drives pipelines
// through.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/sviniabanditka/pinctl/internal/board"
	"github.com/sviniabanditka/pinctl/internal/boardio"
	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
	"github.com/sviniabanditka/pinctl/internal/store"
)

// defaultBoardID is used when a device is created with an empty boardId.
// v0.2 is single-board (docs/12-roadmap.md), so there is no boards
// collection to validate boardId against yet — any non-empty value is
// accepted verbatim, and an empty one defaults to this well-known id.
const defaultBoardID = "brd_local"

// Server holds the dependencies the v0.2 device/pipeline handlers need,
// plus (v0.3) the WS hub and the board port/id the live publishers report
// status against, plus (v0.4) the optional multi-board Manager.
type Server struct {
	sess    Session
	port    string
	store   *store.Store
	mgr     *engine.Manager
	io      engine.DeviceIO
	hub     *Hub
	handler http.Handler

	// boardMgr is nil in single-session dev mode (cmd/pinctl -port), in
	// which case boards.go's handlers fall back to synthesizing a single
	// board entry from sess/port. Set via SetBoardManager once cmd/pinctl
	// has constructed the multi-board Manager (docs/12-roadmap.md v0.4).
	boardMgr *board.Manager

	// bus is nil unless SetDeviceBus was called. When nil,
	// StartPublishers constructs and runs its own *devicebus.Bus
	// internally, scoped to its own lifecycle — the behaviour every
	// existing caller (and test) gets for free. When set, cmd/pinctl owns
	// constructing and running the bus (so internal/mqtt, docs/15-mqtt-
	// bridge.md, can share it too); StartPublishers only subscribes.
	bus *devicebus.Bus
}

// SetDeviceBus wires an externally-owned *devicebus.Bus into the Server,
// used by the "devices" WS topic instead of the internally-constructed one
// StartPublishers would otherwise build. The caller is responsible for
// running bus.Run under its own lifecycle/context — StartPublishers never
// runs a bus it didn't construct itself. Safe to call once, before
// StartPublishers; not safe to call concurrently with in-flight requests.
func (s *Server) SetDeviceBus(b *devicebus.Bus) { s.bus = b }

// SetBoardManager wires the multi-board Manager into the Server, enabling
// the real /api/v1/boards endpoints (list/rescan/rename) and per-board
// unknown_board validation on device create. Safe to call once, before the
// server starts handling requests; not safe to call concurrently with
// in-flight requests.
func (s *Server) SetBoardManager(mgr *board.Manager) { s.boardMgr = mgr }

// ServeHTTP makes *Server itself an http.Handler (the full /api/v1 +
// static-UI mux built by NewServer), so callers such as cmd/pinctl can hold
// onto the concrete *Server (for StartPublishers/CloseWS) and still hand it
// straight to http.Server.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// CloseWS sends every currently-connected WebSocket client a clean close
// frame and tears its connection down, per docs/10-api.md's graceful
// shutdown requirement. Safe to call once publishers are stopped and no new
// broadcasts are expected.
func (s *Server) CloseWS() { s.hub.closeAll() }

// NewServer builds the full /api/v1 handler: the v0.1 raw-pin endpoints
// (unchanged, see api.go), the v0.2 device/pipeline CRUD, enable/disable,
// and validate endpoints, the v0.3 WebSocket endpoint (ws.go) from
// docs/10-api.md, and (non-/api/* paths) the embedded UI (static.go). port
// is the serial port path reported by GET /api/v1/board, same as
// NewHandler.
func NewServer(sess Session, port string, st *store.Store, mgr *engine.Manager, io engine.DeviceIO) *Server {
	mux := http.NewServeMux()

	// v0.1 raw-pin endpoints, verbatim from NewHandler.
	mux.HandleFunc("GET /api/v1/board", handleGetBoard(sess, port))
	mux.HandleFunc("POST /api/v1/pins/{pin}/mode", handleSetPinMode(sess))
	mux.HandleFunc("GET /api/v1/pins/{pin}", handleReadPin(sess))
	mux.HandleFunc("POST /api/v1/pins/{pin}", handleWritePin(sess))

	s := &Server{sess: sess, port: port, store: st, mgr: mgr, io: io, hub: newHub()}

	mux.HandleFunc("GET /api/v1/boards", s.handleListBoards)
	mux.HandleFunc("POST /api/v1/boards/rescan", s.handleRescanBoards)
	mux.HandleFunc("PATCH /api/v1/boards/{id}", s.handleUpdateBoard)

	mux.HandleFunc("GET /api/v1/devices", s.handleListDevices)
	mux.HandleFunc("POST /api/v1/devices", s.handleCreateDevice)
	mux.HandleFunc("GET /api/v1/devices/{id}", s.handleGetDevice)
	mux.HandleFunc("PATCH /api/v1/devices/{id}", s.handleUpdateDevice)
	mux.HandleFunc("DELETE /api/v1/devices/{id}", s.handleDeleteDevice)
	mux.HandleFunc("POST /api/v1/devices/{id}/write", s.handleWriteDevice)

	mux.HandleFunc("GET /api/v1/pipelines", s.handleListPipelines)
	mux.HandleFunc("POST /api/v1/pipelines", s.handleCreatePipeline)
	mux.HandleFunc("POST /api/v1/pipelines/validate", s.handleValidatePipeline)
	mux.HandleFunc("GET /api/v1/pipelines/{id}", s.handleGetPipeline)
	mux.HandleFunc("PUT /api/v1/pipelines/{id}", s.handleUpdatePipeline)
	mux.HandleFunc("DELETE /api/v1/pipelines/{id}", s.handleDeletePipeline)
	mux.HandleFunc("POST /api/v1/pipelines/{id}/enable", s.handleEnablePipeline)
	mux.HandleFunc("POST /api/v1/pipelines/{id}/disable", s.handleDisablePipeline)

	mux.HandleFunc("GET /api/v1/ws", s.handleWS)

	// Low-priority catch-all: any /api/* path not matched by one of the
	// specific patterns above lands here (Go's ServeMux prefers the more
	// specific pattern whenever one matches), turning it into the
	// docs/10-api.md JSON 404 envelope rather than net/http's default
	// plain-text 404 or (via wrapWithStatic below) the SPA fallback.
	mux.HandleFunc("/api/", handleAPINotFound)

	// Everything else: non-/api/* paths are served from the embedded UI
	// (static.go), with a history-mode fallback to index.html.
	s.handler = logRequests(wrapWithStatic(mux))
	return s
}

// decodeJSONBody decodes r's body into dst, capping it at maxBytes and
// rejecting malformed JSON. It does not reject unknown fields — API
// request bodies are deliberately lenient, unlike engine node config
// (decodeStrict).
func decodeJSONBody(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeBadRequest(w, "invalid JSON body")
		return false
	}
	return true
}

// writeStoreError maps a store package sentinel error to the docs/10-api.md
// status code and error envelope.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrPinInUse):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, store.ErrDeviceInUse):
		writeError(w, http.StatusConflict, "device_in_use", err.Error())
	case errors.Is(err, store.ErrNameEmpty):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

// devicesMap snapshots the store's devices as the map shape internal/engine
// needs for Validate/Compile.
func (s *Server) devicesMap() map[string]model.Device {
	list := s.store.ListDevices()
	m := make(map[string]model.Device, len(list))
	for _, d := range list {
		m[d.ID] = d
	}
	return m
}

// applyPinModes best-effort applies pin modes for devices via boardio,
// resolving each device's session by its own BoardID when a multi-board
// Manager is wired in (v0.4) — a single call may span several boards. A
// failure (e.g. the board is offline) is logged, not returned to the HTTP
// caller — device metadata is store-backed and must remain creatable/
// editable while a board is offline; the mode gets (re)applied on that
// board's next successful (re)connect via Manager's OnReady hook.
func (s *Server) applyPinModes(devices ...model.Device) {
	if s.boardMgr == nil {
		if err := boardio.ApplyPinModes(s.sess, devices); err != nil {
			log.Printf("api: applying pin modes: %v", err)
		}
		return
	}
	for _, d := range devices {
		sess, err := s.boardMgr.Session(d.BoardID)
		if err != nil {
			continue // board offline: applied on its next reconnect instead
		}
		if err := boardio.ApplyPinModes(sess, []model.Device{d}); err != nil {
			log.Printf("api: applying pin mode for device %s: %v", d.ID, err)
		}
	}
}
