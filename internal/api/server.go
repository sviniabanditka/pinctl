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

	"github.com/sviniabanditka/pinctl/internal/boardio"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
	"github.com/sviniabanditka/pinctl/internal/store"
)

// defaultBoardID is used when a device is created with an empty boardId.
// v0.2 is single-board (docs/12-roadmap.md), so there is no boards
// collection to validate boardId against yet — any non-empty value is
// accepted verbatim, and an empty one defaults to this well-known id.
const defaultBoardID = "brd_local"

// Server holds the dependencies the v0.2 device/pipeline handlers need.
type Server struct {
	sess  Session
	store *store.Store
	mgr   *engine.Manager
	io    engine.DeviceIO
}

// NewServer builds the full /api/v1 handler: the v0.1 raw-pin endpoints
// (unchanged, see api.go) plus the v0.2 device/pipeline CRUD, enable/
// disable, and validate endpoints from docs/10-api.md. port is the serial
// port path reported by GET /api/v1/board, same as NewHandler.
func NewServer(sess Session, port string, st *store.Store, mgr *engine.Manager, io engine.DeviceIO) http.Handler {
	mux := http.NewServeMux()

	// v0.1 raw-pin endpoints, verbatim from NewHandler.
	mux.HandleFunc("GET /api/v1/board", handleGetBoard(sess, port))
	mux.HandleFunc("POST /api/v1/pins/{pin}/mode", handleSetPinMode(sess))
	mux.HandleFunc("GET /api/v1/pins/{pin}", handleReadPin(sess))
	mux.HandleFunc("POST /api/v1/pins/{pin}", handleWritePin(sess))

	s := &Server{sess: sess, store: st, mgr: mgr, io: io}

	mux.HandleFunc("GET /api/v1/devices", s.handleListDevices)
	mux.HandleFunc("POST /api/v1/devices", s.handleCreateDevice)
	mux.HandleFunc("GET /api/v1/devices/{id}", s.handleGetDevice)
	mux.HandleFunc("PATCH /api/v1/devices/{id}", s.handleUpdateDevice)
	mux.HandleFunc("DELETE /api/v1/devices/{id}", s.handleDeleteDevice)

	mux.HandleFunc("GET /api/v1/pipelines", s.handleListPipelines)
	mux.HandleFunc("POST /api/v1/pipelines", s.handleCreatePipeline)
	mux.HandleFunc("POST /api/v1/pipelines/validate", s.handleValidatePipeline)
	mux.HandleFunc("GET /api/v1/pipelines/{id}", s.handleGetPipeline)
	mux.HandleFunc("PUT /api/v1/pipelines/{id}", s.handleUpdatePipeline)
	mux.HandleFunc("DELETE /api/v1/pipelines/{id}", s.handleDeletePipeline)
	mux.HandleFunc("POST /api/v1/pipelines/{id}/enable", s.handleEnablePipeline)
	mux.HandleFunc("POST /api/v1/pipelines/{id}/disable", s.handleDisablePipeline)

	return logRequests(mux)
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

// applyPinModes best-effort applies pin modes for devices via boardio. A
// failure (e.g. the board is dead) is logged, not returned to the HTTP
// caller — device metadata is store-backed and must remain creatable/
// editable while the board is offline; the mode gets (re)applied on the
// next successful ApplyPinModes call (e.g. process restart).
func (s *Server) applyPinModes(devices ...model.Device) {
	if err := boardio.ApplyPinModes(s.sess, devices); err != nil {
		log.Printf("api: applying pin modes: %v", err)
	}
}
