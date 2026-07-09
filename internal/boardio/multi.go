package boardio

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sviniabanditka/pinctl/internal/board"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// SessionLookup resolves a board ID to its live Session, or an error if the
// board is unknown/offline — the shape *board.Manager.Session already has.
type SessionLookup func(boardID string) (Session, error)

// MultiIO implements engine.DeviceIO by routing each call to the Session
// for d.BoardID (docs/12-roadmap.md v0.4 multi-board support), instead of
// IO's single fixed Session. An unknown/offline board surfaces as an error
// from lookup, which the engine already treats as a failed read/write for
// that tick (docs/03-architecture.md's per-pipeline degrade-not-crash
// behavior) — no engine changes needed.
type MultiIO struct {
	lookup SessionLookup
}

// NewMulti builds a MultiIO that resolves sessions via lookup.
func NewMulti(lookup SessionLookup) *MultiIO {
	return &MultiIO{lookup: lookup}
}

// NewManagerIO builds a MultiIO backed directly by a *board.Manager.
func NewManagerIO(mgr *board.Manager) *MultiIO {
	return NewMulti(func(boardID string) (Session, error) {
		sess, err := mgr.Session(boardID)
		if err != nil {
			return nil, err
		}
		return sess, nil
	})
}

func (m *MultiIO) ReadDevice(d model.Device) (engine.Value, error) {
	sess, err := m.lookup(d.BoardID)
	if err != nil {
		return engine.Value{}, fmt.Errorf("boardio: device %s: %w", d.ID, err)
	}
	return New(sess).ReadDevice(d)
}

func (m *MultiIO) WriteDevice(d model.Device, v engine.Value) error {
	sess, err := m.lookup(d.BoardID)
	if err != nil {
		return fmt.Errorf("boardio: device %s: %w", d.ID, err)
	}
	return New(sess).WriteDevice(d, v)
}

// ApplySafeValues writes each output device's configured safeValue
// (digital-out/pwm-out with options.onDisconnect == "safe-value") to sess,
// per docs/09-data-model.md's onDisconnect semantics: "write safeValue
// immediately on reconnect, before any pipeline write for this device is
// applied that tick." Devices with onDisconnect == "hold" (or unset,
// defaulting to "safe-value" per the doc — callers pass the resolved
// option) are left alone; normal pipeline-driven writes resume on the next
// tick regardless.
//
// Errors for individual devices are collected and joined, not
// all-or-nothing, matching ApplyPinModes' policy — one misconfigured
// device's safe-value write failing shouldn't skip the rest.
func ApplySafeValues(sess Session, devices []model.Device) error {
	var errs []error
	for _, d := range devices {
		if d.Options.OnDisconnect != "hold" {
			// Default (unset) and explicit "safe-value" both apply the
			// safe value; "hold" is the only opt-out.
			if err := applySafeValue(sess, d); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func applySafeValue(sess Session, d model.Device) error {
	switch d.Role {
	case model.RoleDigitalOut:
		v, err := decodeBoolOption(d.Options.SafeValue)
		if err != nil {
			return fmt.Errorf("boardio: device %s: safeValue: %w", d.ID, err)
		}
		if err := sess.DigitalWrite(d.Pin, v); err != nil {
			return fmt.Errorf("boardio: device %s (pin %d): applying safe value: %w", d.ID, d.Pin, err)
		}
	case model.RolePWMOut:
		f, err := decodeFloatOption(d.Options.SafeValue)
		if err != nil {
			return fmt.Errorf("boardio: device %s: safeValue: %w", d.ID, err)
		}
		if f < 0 {
			f = 0
		}
		if f > 255 {
			f = 255
		}
		if err := sess.AnalogWrite(d.Pin, byte(f)); err != nil {
			return fmt.Errorf("boardio: device %s (pin %d): applying safe value: %w", d.ID, d.Pin, err)
		}
	default:
		// Not an output role: nothing to do (analog-in/digital-in have no
		// onDisconnect option per docs/09-data-model.md).
	}
	return nil
}

func decodeBoolOption(raw *json.RawMessage) (bool, error) {
	if raw == nil {
		return false, nil // default false per docs/09
	}
	var v bool
	if err := json.Unmarshal(*raw, &v); err != nil {
		return false, err
	}
	return v, nil
}

func decodeFloatOption(raw *json.RawMessage) (float64, error) {
	if raw == nil {
		return 0, nil // default 0 per docs/09
	}
	var v float64
	if err := json.Unmarshal(*raw, &v); err != nil {
		return 0, err
	}
	return v, nil
}
