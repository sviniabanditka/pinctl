package mqtt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// encodeState renders v as the bare-scalar payload docs/15's "Payloads"
// table specifies: ON/OFF for the two boolean roles, an integer for the
// two numeric roles.
func encodeState(role model.DeviceRole, v engine.Value) []byte {
	switch role {
	case model.RoleDigitalIn, model.RoleDigitalOut:
		if v.Bool {
			return []byte("ON")
		}
		return []byte("OFF")
	default: // analog-in, pwm-out
		return []byte(strconv.FormatFloat(v.Float, 'f', 0, 64))
	}
}

// decodeCommand parses and range-checks payload against role, per
// docs/15's "Payloads" table ("Anything else on a set topic is rejected").
// Only the two writable roles (digital-out, pwm-out) are ever called with
// a real command; the caller rejects input roles before reaching here.
func decodeCommand(role model.DeviceRole, payload []byte) (engine.Value, error) {
	s := strings.TrimSpace(string(payload))
	switch role {
	case model.RoleDigitalOut:
		switch s {
		case "ON":
			return engine.BoolValue(true), nil
		case "OFF":
			return engine.BoolValue(false), nil
		default:
			return engine.Value{}, fmt.Errorf("mqtt: digital-out command must be ON or OFF, got %q", s)
		}
	case model.RolePWMOut:
		n, err := strconv.Atoi(s)
		if err != nil {
			return engine.Value{}, fmt.Errorf("mqtt: pwm-out command must be an integer 0-255, got %q", s)
		}
		if n < 0 || n > 255 {
			return engine.Value{}, fmt.Errorf("mqtt: pwm-out command %d out of range 0-255", n)
		}
		return engine.FloatValue(float64(n)), nil
	default:
		return engine.Value{}, fmt.Errorf("mqtt: role %q does not accept commands", role)
	}
}

// zeroValue is a writable-role device's value before any command or
// options.initial has been observed.
func zeroValue(role model.DeviceRole) engine.Value {
	if role == model.RolePWMOut {
		return engine.FloatValue(0)
	}
	return engine.BoolValue(false)
}

// initialOutputValue is the best notion the bridge has of a writable
// device's starting state before it has ever been written: its
// options.initial if one parses cleanly for its role, else the role's zero
// value. Once a device has been written at least once — by the engine, a
// REST manual write, or an MQTT command — its real value is observable
// through devicebus (every writer goes through a devicebus.WrapWriter, see
// cmd/pinctl and docs/15-mqtt-bridge.md's "Architecture"), and
// republishSnapshot prefers that. This is only the fallback for a device
// that has never been written since pinctl started (Firmata reports
// nothing for outputs, so there is no other source of truth for that
// case).
func initialOutputValue(d model.Device) engine.Value {
	if d.Options.Initial == nil {
		return zeroValue(d.Role)
	}
	switch d.Role {
	case model.RoleDigitalOut:
		var b bool
		if err := json.Unmarshal(*d.Options.Initial, &b); err == nil {
			return engine.BoolValue(b)
		}
	case model.RolePWMOut:
		var f float64
		if err := json.Unmarshal(*d.Options.Initial, &f); err == nil {
			return engine.FloatValue(f)
		}
	}
	return zeroValue(d.Role)
}
