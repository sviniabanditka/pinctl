package api

import (
	"encoding/json"
	"net/http"

	"github.com/sviniabanditka/pinctl/internal/model"
)

const maxDeviceBodyBytes = 1 << 16 // devices.json bodies are small; generous headroom over the ~200B example in docs/10.

func validRole(r model.DeviceRole) bool {
	switch r {
	case model.RoleDigitalIn, model.RoleDigitalOut, model.RoleAnalogIn, model.RolePWMOut:
		return true
	}
	return false
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"devices": s.store.ListDevices()})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDevice(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type deviceCreateRequest struct {
	Name    string              `json:"name"`
	BoardID string              `json:"boardId"`
	Pin     int                 `json:"pin"`
	Role    model.DeviceRole    `json:"role"`
	Options model.DeviceOptions `json:"options"`
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceCreateRequest
	if !decodeJSONBody(w, r, maxDeviceBodyBytes, &req) {
		return
	}
	if !validRole(req.Role) {
		writeBadRequest(w, "role must be one of digital-in, digital-out, analog-in, pwm-out")
		return
	}
	boardID := req.BoardID
	if boardID == "" {
		boardID = defaultBoardID
	}

	created, err := s.store.CreateDevice(model.Device{
		Name:    req.Name,
		BoardID: boardID,
		Pin:     req.Pin,
		Role:    req.Role,
		Options: req.Options,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.applyPinModes(created)
	writeJSON(w, http.StatusCreated, created)
}

// devicePatchRequest covers the editable device fields (name, pin,
// options). Role/BoardID are decoded as raw JSON only to detect whether the
// caller tried to set them at all — docs/10-api.md: "PATCH requests that
// include either field return 400", since both are immutable after create.
type devicePatchRequest struct {
	Name    *string              `json:"name"`
	Pin     *int                 `json:"pin"`
	Options *model.DeviceOptions `json:"options"`
	Role    json.RawMessage      `json:"role"`
	BoardID json.RawMessage      `json:"boardId"`
}

func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req devicePatchRequest
	if !decodeJSONBody(w, r, maxDeviceBodyBytes, &req) {
		return
	}
	if req.Role != nil || req.BoardID != nil {
		writeBadRequest(w, "role and boardId are immutable after creation")
		return
	}

	existing, err := s.store.GetDevice(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Pin != nil {
		existing.Pin = *req.Pin
	}
	if req.Options != nil {
		existing.Options = *req.Options
	}

	updated, err := s.store.UpdateDevice(id, existing)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.applyPinModes(updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteDevice(r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
