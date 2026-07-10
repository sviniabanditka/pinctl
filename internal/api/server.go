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
	"fmt"
	"log"
	"net/http"

	"github.com/sviniabanditka/pinctl/internal/board"
	"github.com/sviniabanditka/pinctl/internal/boardio"
	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/history"
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

	// deviceWatcher is nil unless SetDeviceWatcher was called (mqtt.enabled
	// in config.yaml). See DeviceWatcher's doc comment.
	deviceWatcher DeviceWatcher

	// history is nil unless SetHistoryStore was called (history.enabled in
	// config.yaml, and the database opened successfully). When nil, the
	// GET .../history endpoint answers 503 history_disabled, and device
	// delete skips the best-effort samples cleanup.
	history HistoryStore
}

// HistoryStore is what the Server needs from internal/history's *Store:
// query the samples GET /api/v1/devices/{id}/history serves, and clean up
// a deleted device's samples. Unlike DeviceWatcher/*devicebus.Bus, this is
// internal/history's own Query/Sample/Query types used directly rather
// than mirrored locally — the handler has to speak those types anyway to
// parse query-string params and shape the JSON response, so a narrow
// mirror interface would buy nothing (there is no "internal/api must not
// depend on internal/history" architectural rule the way there is for
// internal/mqtt/internal/devicebus). Wired via SetHistoryStore so tests
// and a disabled-history instance can supply nil.
type HistoryStore interface {
	Query(deviceID string, q history.Query) ([]history.Sample, error)
	DeleteDevice(deviceID string) error
}

// SetHistoryStore wires an externally-owned HistoryStore (in practice,
// cmd/pinctl's *history.Store when history.enabled is true and the
// database opened successfully) into the Server. Safe to call once,
// before the server starts handling requests; not safe to call
// concurrently with in-flight requests.
func (s *Server) SetHistoryStore(h HistoryStore) { s.history = h }

// notifyDevicesChanged tells deviceWatcher (if any) that the device set
// changed, so it can republish/remove Home Assistant discovery configs. A
// no-op when no DeviceWatcher was wired in (mqtt.enabled: false, or a test
// server that never called SetDeviceWatcher).
func (s *Server) notifyDevicesChanged() {
	if s.deviceWatcher != nil {
		s.deviceWatcher.DevicesChanged()
	}
}

// DeviceWatcher is the narrow interface internal/mqtt's Bridge satisfies
// (DevicesChanged), kept here instead of importing internal/mqtt directly —
// internal/api must not depend on internal/mqtt (docs/15-mqtt-bridge.md's
// "Architecture": "nothing depends on internal/mqtt; deleting the package
// would leave a compiling binary with one dead config section"). Wired via
// SetDeviceWatcher; nil (the default) means no MQTT bridge is running, in
// which case the notify calls in devices.go are no-ops.
type DeviceWatcher interface {
	DevicesChanged()
}

// SetDeviceWatcher wires an externally-owned DeviceWatcher (in practice,
// cmd/pinctl's *mqtt.Bridge when mqtt.enabled is true) into the Server, so
// device create/update/delete can tell it to republish/remove Home
// Assistant discovery configs. Safe to call once, before the server starts
// handling requests; not safe to call concurrently with in-flight requests.
func (s *Server) SetDeviceWatcher(w DeviceWatcher) { s.deviceWatcher = w }

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
	mux.HandleFunc("GET /api/v1/devices/{id}/history", s.handleDeviceHistory)

	mux.HandleFunc("GET /api/v1/pipelines", s.handleListPipelines)
	mux.HandleFunc("POST /api/v1/pipelines", s.handleCreatePipeline)
	mux.HandleFunc("POST /api/v1/pipelines/validate", s.handleValidatePipeline)
	mux.HandleFunc("POST /api/v1/simulate", s.handleSimulate)
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

// connectedSessionFor resolves the live Session for boardID, mirroring
// applyPinModes' fallback (single-session dev mode when boardMgr is nil vs.
// per-board lookup once a Manager is wired in). ok is false whenever the
// board is offline/unknown (or single-session mode's sess isn't alive) —
// callers treat that as "nothing to validate against," per
// unsupportedPinModeMsg's doc comment.
func (s *Server) connectedSessionFor(boardID string) (sess Session, ok bool) {
	if s.boardMgr == nil {
		return s.sess, s.sess.Alive()
	}
	sc, err := s.boardMgr.Session(boardID)
	if err != nil {
		return nil, false
	}
	return sc, true
}

// unsupportedPinModeMsg validates candidate's (pin, role) against its
// target board's actually-reported Firmata pin capabilities, closing the
// gap where POST /devices previously accepted any pin for any role and let
// a mismatch (e.g. analog-in on pin 0/RX, servo on a pin the board can't
// drive one on) surface only as a logged ErrUnsupportedMode later, leaving
// a device that silently does nothing.
//
// When the target board is connected, an incompatible (pin, role) reports
// unsupported=true with a message naming both. When the board is offline
// or unknown, validation is skipped (unsupported=false) — device metadata
// must stay creatable/editable while hardware is away; the mismatch would
// only surface once the board reconnects and ApplyPinModes' SetPinMode call
// fails, same as today.
func (s *Server) unsupportedPinModeMsg(candidate model.Device) (msg string, unsupported bool) {
	mode, err := boardio.ModeFor(candidate)
	if err != nil {
		// Unknown role: validRole already rejected this before we get here
		// in the create handler; nothing more to check.
		return "", false
	}
	sess, ok := s.connectedSessionFor(candidate.BoardID)
	if !ok {
		return "", false
	}
	for _, pc := range sess.Capabilities() {
		if pc.Pin == candidate.Pin {
			if pc.SupportsMode(mode) {
				return "", false
			}
			break
		}
	}
	return fmt.Sprintf("pin %d does not support role %s", candidate.Pin, candidate.Role), true
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
