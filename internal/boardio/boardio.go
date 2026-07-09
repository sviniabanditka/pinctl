// Package boardio adapts a single internal/board.Session to
// internal/engine.DeviceIO, mapping model.Device roles onto the Firmata
// pin operations a Session exposes. It is the "thin, mechanical wrapper"
// engine.DeviceIO's doc comment anticipates: one method per direction,
// keyed by the model.Device the engine already resolved at compile time.
package boardio

import (
	"errors"
	"fmt"

	"github.com/sviniabanditka/pinctl/internal/board"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// Session is the subset of *board.Session boardio needs. Declaring it here
// (rather than depending on the concrete type) lets tests exercise IO and
// ApplyPinModes against a fake, same pattern as internal/api's Session
// interface. Any *board.Session, or anything satisfying internal/api's
// (wider) Session interface, satisfies this one too.
type Session interface {
	Capabilities() []board.PinCapability
	SetPinMode(pin int, mode board.PinMode) error
	DigitalWrite(pin int, high bool) error
	AnalogWrite(pin int, value byte) error
	DigitalRead(pin int) (bool, error)
	AnalogRead(channel int) (int, error)
}

// IO implements engine.DeviceIO over a Session.
type IO struct {
	sess Session
}

// New builds an IO adapter over sess.
func New(sess Session) *IO {
	return &IO{sess: sess}
}

// ReadDevice implements engine.DeviceIO. analog-in reads raw 0-1023 (per
// docs/08-node-catalog.md) via the pin's analog channel; digital-in reads
// the cached digital value. Any other role is a programming error (the
// engine only builds device-read nodes against readable roles) and returns
// an error rather than panicking.
func (io *IO) ReadDevice(d model.Device) (engine.Value, error) {
	switch d.Role {
	case model.RoleAnalogIn:
		ch, ok := analogChannel(io.sess, d.Pin)
		if !ok {
			return engine.Value{}, fmt.Errorf("boardio: device %s: pin %d has no analog channel", d.ID, d.Pin)
		}
		v, err := io.sess.AnalogRead(ch)
		if err != nil {
			return engine.Value{}, err
		}
		return engine.FloatValue(float64(v)), nil
	case model.RoleDigitalIn:
		v, err := io.sess.DigitalRead(d.Pin)
		if err != nil {
			return engine.Value{}, err
		}
		return engine.BoolValue(v), nil
	default:
		return engine.Value{}, fmt.Errorf("boardio: device %s: role %q is not readable", d.ID, d.Role)
	}
}

// WriteDevice implements engine.DeviceIO. digital-out writes a bool;
// pwm-out clamps v.Float to 0-255 (belt-and-suspenders — device-write
// already clamps per docs/08-node-catalog.md, but boardio doesn't assume
// its only caller does) and writes a byte duty cycle. A write error (e.g.
// a dead session) is passed straight through — the engine's runner already
// treats a WriteDevice error as a dropped write, not a fatal tick error.
func (io *IO) WriteDevice(d model.Device, v engine.Value) error {
	switch d.Role {
	case model.RoleDigitalOut:
		return io.sess.DigitalWrite(d.Pin, v.Bool)
	case model.RolePWMOut:
		f := v.Float
		if f < 0 {
			f = 0
		}
		if f > 255 {
			f = 255
		}
		return io.sess.AnalogWrite(d.Pin, byte(f))
	default:
		return fmt.Errorf("boardio: device %s: role %q is not writable", d.ID, d.Role)
	}
}

// analogChannel looks up pin's analog channel from the session's reported
// capabilities.
func analogChannel(sess Session, pin int) (int, bool) {
	for _, pc := range sess.Capabilities() {
		if pc.Pin == pin {
			return pc.AnalogChannel, pc.HasAnalogChannel
		}
	}
	return 0, false
}

// modeFor maps a device's role (and, for digital-in, its pullup option) to
// the Firmata pin mode it needs to be set to.
func modeFor(d model.Device) (board.PinMode, error) {
	switch d.Role {
	case model.RoleAnalogIn:
		return board.ModeAnalog, nil
	case model.RoleDigitalIn:
		if d.Options.Pullup {
			return board.ModePullup, nil
		}
		return board.ModeInput, nil
	case model.RoleDigitalOut:
		return board.ModeOutput, nil
	case model.RolePWMOut:
		return board.ModePWM, nil
	default:
		return 0, fmt.Errorf("boardio: device %s: unknown role %q", d.ID, d.Role)
	}
}

// ApplyPinModes sets the Firmata pin mode for every device in devices, per
// its role, on sess. Called at startup (for every persisted device) and
// again on device create/update (for just that one device) so a device's
// pin is always in the mode its role implies before anything tries to read
// or write it.
//
// One device's mode failing to apply (unknown pin, unsupported mode, dead
// session) does not stop the rest from being attempted — errors are
// collected and joined, not treated as all-or-nothing, since a single
// misconfigured device shouldn't leave every other device unconfigured.
func ApplyPinModes(sess Session, devices []model.Device) error {
	var errs []error
	for _, d := range devices {
		mode, err := modeFor(d)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := sess.SetPinMode(d.Pin, mode); err != nil {
			errs = append(errs, fmt.Errorf("boardio: device %s (pin %d): %w", d.ID, d.Pin, err))
		}
	}
	return errors.Join(errs...)
}
