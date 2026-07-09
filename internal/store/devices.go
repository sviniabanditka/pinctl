package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// ListDevices returns all known devices, ordered by ID.
func (s *Store) ListDevices() []model.Device {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]model.Device, 0, len(s.devices))
	for _, d := range s.devices {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// GetDevice returns the device with the given ID, or ErrNotFound.
func (s *Store) GetDevice(id string) (model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.devices[id]
	if !ok {
		return model.Device{}, fmt.Errorf("store: device %s: %w", id, ErrNotFound)
	}
	return d, nil
}

// CreateDevice assigns a new ID and timestamps to d, validates it, and
// persists devices.json. The stored (and returned) device is what the
// caller should treat as authoritative — d.ID/CreatedAt/UpdatedAt are
// overwritten.
func (s *Store) CreateDevice(d model.Device) (model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if d.Name == "" {
		return model.Device{}, ErrNameEmpty
	}
	if s.pinInUseLocked(d.BoardID, d.Pin, "") {
		return model.Device{}, fmt.Errorf("store: board %s pin %d: %w", d.BoardID, d.Pin, ErrPinInUse)
	}

	now := time.Now().UTC()
	d.ID = model.NewID("dev")
	d.CreatedAt = now
	d.UpdatedAt = now

	s.devices[d.ID] = d
	if err := s.saveDevicesLocked(); err != nil {
		delete(s.devices, d.ID)
		return model.Device{}, err
	}
	return d, nil
}

// UpdateDevice replaces the mutable fields (Name, BoardID, Pin, Role,
// Options) of the device with the given ID, preserving its ID and
// CreatedAt, bumping UpdatedAt, and persisting devices.json.
func (s *Store) UpdateDevice(id string, d model.Device) (model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.devices[id]
	if !ok {
		return model.Device{}, fmt.Errorf("store: device %s: %w", id, ErrNotFound)
	}
	if d.Name == "" {
		return model.Device{}, ErrNameEmpty
	}
	if s.pinInUseLocked(d.BoardID, d.Pin, id) {
		return model.Device{}, fmt.Errorf("store: board %s pin %d: %w", d.BoardID, d.Pin, ErrPinInUse)
	}

	updated := d
	updated.ID = existing.ID
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = time.Now().UTC()

	prev := s.devices[id]
	s.devices[id] = updated
	if err := s.saveDevicesLocked(); err != nil {
		s.devices[id] = prev
		return model.Device{}, err
	}
	return updated, nil
}

// DeleteDevice removes the device with the given ID and persists
// devices.json. Per docs/09-data-model.md, a device cannot be deleted while
// any pipeline's nodes still reference it — that returns ErrDeviceInUse
// (409 Conflict at the API layer) instead of deleting.
func (s *Store) DeleteDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.devices[id]
	if !ok {
		return fmt.Errorf("store: device %s: %w", id, ErrNotFound)
	}
	if s.deviceReferencedByPipelineLocked(id) {
		return fmt.Errorf("store: device %s: %w", id, ErrDeviceInUse)
	}

	delete(s.devices, id)
	if err := s.saveDevicesLocked(); err != nil {
		s.devices[id] = d
		return err
	}
	return nil
}

// deviceReferencedByPipelineLocked reports whether any in-memory pipeline
// has a node whose config references deviceID. Node.Config is opaque
// per-node-type JSON (internal/model), so this only recognizes the
// convention every device-referencing node type shares: a top-level
// "deviceId" string field. Caller must hold s.mu.
func (s *Store) deviceReferencedByPipelineLocked(deviceID string) bool {
	type nodeDeviceRef struct {
		DeviceID string `json:"deviceId"`
	}
	for _, p := range s.pipelines {
		for _, n := range p.Nodes {
			var ref nodeDeviceRef
			if err := json.Unmarshal(n.Config, &ref); err != nil {
				continue
			}
			if ref.DeviceID == deviceID {
				return true
			}
		}
	}
	return false
}

// pinInUseLocked reports whether some device other than excludeID already
// binds (boardID, pin). Caller must hold s.mu.
func (s *Store) pinInUseLocked(boardID string, pin int, excludeID string) bool {
	for _, d := range s.devices {
		if d.ID == excludeID {
			continue
		}
		if d.BoardID == boardID && d.Pin == pin {
			return true
		}
	}
	return false
}
