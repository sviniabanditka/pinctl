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

// nodeDeviceRef is the shape every device-referencing node type's config
// shares: a top-level "deviceId" string field (docs/08-node-catalog.md).
type nodeDeviceRef struct {
	DeviceID string `json:"deviceId"`
}

// nodeDeviceID extracts n's "deviceId" config field, if it has one.
// Node.Config is opaque per-node-type JSON (internal/model); a node type
// that doesn't follow the deviceId convention (or has an empty one) simply
// fails to match here — that's not an error, just "doesn't reference a
// device".
func nodeDeviceID(n model.Node) (deviceID string, ok bool) {
	var ref nodeDeviceRef
	if err := json.Unmarshal(n.Config, &ref); err != nil || ref.DeviceID == "" {
		return "", false
	}
	return ref.DeviceID, true
}

// deviceReferencedByPipelineLocked reports whether any in-memory pipeline
// has a node — of any type, enabled or not — whose config references
// deviceID. This is the broad rule DeleteDevice enforces (docs/09-data-
// model.md: "a device cannot be deleted while any pipeline still
// references it"). Contrast EnabledPipelineWritingDevice, which is
// narrower on purpose: only device-write nodes, only in enabled pipelines.
// Caller must hold s.mu.
func (s *Store) deviceReferencedByPipelineLocked(deviceID string) bool {
	for _, p := range s.pipelines {
		for _, n := range p.Nodes {
			if id, ok := nodeDeviceID(n); ok && id == deviceID {
				return true
			}
		}
	}
	return false
}

// EnabledPipelineWritingDevice reports the id and name of an enabled
// pipeline whose device-write node targets deviceID, if any. This is the
// pipeline-conflict rule shared by the manual REST write
// (POST /devices/{id}/write, docs/10-api.md) and the MQTT bridge's command
// handler (docs/15-mqtt-bridge.md "Commands and the pipeline-conflict
// rule") — implemented once here so the two surfaces can't drift apart.
//
// It deliberately differs from deviceReferencedByPipelineLocked: that one
// matches any node (including device-read) in any pipeline (enabled or
// not), because deleting a device must be blocked by every reference to
// it. This one only blocks a manual/MQTT write, so it matches only
// device-write nodes, and only within pipelines that are currently
// enabled — a device-read reference doesn't drive the pin, and a disabled
// pipeline doesn't either.
func (s *Store) EnabledPipelineWritingDevice(deviceID string) (id, name string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.pipelines {
		if !p.Enabled {
			continue
		}
		for _, n := range p.Nodes {
			if n.Type != "device-write" {
				continue
			}
			if refID, refOK := nodeDeviceID(n); refOK && refID == deviceID {
				return p.ID, p.Name, true
			}
		}
	}
	return "", "", false
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
