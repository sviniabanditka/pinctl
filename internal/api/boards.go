// Boards endpoints, per docs/10-api.md's "Boards" section: GET
// /api/v1/boards (list with live status), POST /api/v1/boards/rescan
// (trigger a fresh discovery pass), PATCH /api/v1/boards/{id} (rename).
//
// When the Server was built without a multi-board Manager (SetBoardManager
// never called — cmd/pinctl's dev-mode -port bypasses discovery entirely,
// docs/12-roadmap.md), these handlers fall back to synthesizing a single
// board entry from the legacy sess/port fields, so the endpoints still
// answer something sensible rather than 404ing outright.
package api

import (
	"context"
	"log"
	"net/http"

	"github.com/sviniabanditka/pinctl/internal/board"
)

type identityOut struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

type capabilitiesOut struct {
	DigitalPins []int `json:"digitalPins"`
	AnalogPins  []int `json:"analogPins"`
}

type boardOut struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Identity     identityOut      `json:"identity"`
	PortHint     string           `json:"portHint,omitempty"`
	Status       string           `json:"status"`
	Port         *string          `json:"port"`
	Capabilities *capabilitiesOut `json:"capabilities"`
}

func splitCapabilities(caps []board.PinCapability) capabilitiesOut {
	var out capabilitiesOut
	for _, pc := range caps {
		if pc.HasAnalogChannel {
			out.AnalogPins = append(out.AnalogPins, pc.AnalogChannel)
		}
		out.DigitalPins = append(out.DigitalPins, pc.Pin)
	}
	return out
}

func toBoardOut(b board.BoardInfo) boardOut {
	out := boardOut{
		ID:       b.ID,
		Name:     b.Name,
		Identity: identityOut{Kind: b.Identity.Kind, Key: b.Identity.Key},
		PortHint: b.PortHint,
		Status:   string(b.Status),
	}
	if b.Status == board.StatusConnected {
		p := b.Port
		out.Port = &p
		caps := splitCapabilities(b.Capabilities)
		out.Capabilities = &caps
	}
	return out
}

// boardsSnapshot returns the current board list: the real multi-board
// Manager's view when wired in, otherwise a single-entry fallback derived
// from the legacy sess/port fields (see package doc comment above).
func (s *Server) boardsSnapshot() []boardOut {
	if s.boardMgr != nil {
		infos := s.boardMgr.Boards()
		out := make([]boardOut, len(infos))
		for i, b := range infos {
			out[i] = toBoardOut(b)
		}
		return out
	}

	status := "disconnected"
	var port *string
	var caps *capabilitiesOut
	if s.sess.Alive() {
		status = "connected"
		p := s.port
		port = &p
		c := splitCapabilities(s.sess.Capabilities())
		caps = &c
	}
	return []boardOut{{
		ID:           defaultBoardID,
		Name:         "Arduino (single-board mode)",
		PortHint:     s.port,
		Status:       status,
		Port:         port,
		Capabilities: caps,
	}}
}

func (s *Server) handleListBoards(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"boards": s.boardsSnapshot()})
}

// handleRescanBoards triggers an immediate discovery pass, per docs/10-api.md:
// "Returns 202 Accepted with no body — the scan is async; clients observe
// results via GET /boards or the boards WebSocket topic." A no-op (still
// 202) when no multi-board Manager is wired in.
func (s *Server) handleRescanBoards(w http.ResponseWriter, r *http.Request) {
	if s.boardMgr != nil {
		go func() {
			if err := s.boardMgr.Scan(context.Background()); err != nil {
				log.Printf("api: rescan: %v", err)
			}
		}()
	}
	w.WriteHeader(http.StatusAccepted)
}

type boardPatchRequest struct {
	Name *string `json:"name"`
}

// handleUpdateBoard renames a board (only editable field, per docs/10-api.md).
func (s *Server) handleUpdateBoard(w http.ResponseWriter, r *http.Request) {
	if s.boardMgr == nil {
		writeError(w, http.StatusNotFound, "not_found", "no boards collection in single-board mode")
		return
	}

	id := r.PathValue("id")
	var req boardPatchRequest
	if !decodeJSONBody(w, r, maxDeviceBodyBytes, &req) {
		return
	}
	if req.Name == nil || *req.Name == "" {
		writeBadRequest(w, "name is required")
		return
	}

	if _, err := s.boardMgr.RenameBoard(id, *req.Name); err != nil {
		writeStoreError(w, err)
		return
	}

	for _, b := range s.boardMgr.Boards() {
		if b.ID == id {
			writeJSON(w, http.StatusOK, toBoardOut(b))
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "board "+id+" does not exist")
}
