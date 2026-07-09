// Package sampling implements the deadband/rate/heartbeat policy that
// decides whether a device value is worth recording, shared by the MQTT
// bridge's publish policy (docs/15-mqtt-bridge.md "Publishing policy:
// deadband and rate") and pinctl's value history recorder (docs/16-
// history.md "What gets recorded") — both need the same shape of answer
// to the same question ("is this new value worth emitting, given the last
// one we emitted?"), so it lives in exactly one place.
package sampling

import (
	"math"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// Policy is the deadband/rate/heartbeat rule: a value is emitted when it
// has moved by at least Deadband and at least MinInterval has passed since
// the last emission, or unconditionally once MaxInterval has passed (a
// heartbeat).
type Policy struct {
	// Deadband is the minimum raw-counts change that counts as movement
	// for analog-in devices; every other role treats any change as
	// movement regardless of Deadband.
	Deadband float64
	// MinInterval is the floor between two emissions.
	MinInterval time.Duration
	// MaxInterval is the heartbeat ceiling: emit even with no movement
	// once this much time has passed since the last emission.
	MaxInterval time.Duration
}

// State is what a Decide caller remembers about the last value it emitted
// for one device, so the next candidate can be evaluated against it.
type State struct {
	Value engine.Value
	At    time.Time
	Valid bool // false until the first observation has been emitted
}

// Decide reports whether cur should be emitted for a device with the given
// role, given prev (the last emitted State) and now.
//
// The very first observation for a device always emits — prev.Valid is
// false, so there is nothing to compare against and no retained value
// would exist yet either way. Once MaxInterval has elapsed since the last
// emission, cur emits regardless of whether it changed at all (the
// heartbeat, so a flat line reads as "measured and unchanged" rather than
// "nothing was watching"). Otherwise cur emits only if it changed by at
// least the policy's deadband (analog-in) or at all (every other role,
// including booleans) *and* at least MinInterval has passed since the
// last emission.
func (p Policy) Decide(prev State, role model.DeviceRole, cur engine.Value, now time.Time) bool {
	if !prev.Valid {
		return true
	}
	since := now.Sub(prev.At)
	if since >= p.MaxInterval {
		return true // heartbeat, regardless of change
	}
	if !valueChanged(role, prev.Value, cur, p.Deadband) {
		return false
	}
	return since >= p.MinInterval
}

// valueChanged reports whether cur differs enough from prev to count as a
// change for role: at least deadband raw counts for analog-in, any
// difference at all for every other role. pwm-out (also numeric) falls
// into "any change" like the booleans — the deadband exists to tame ADC
// noise on an *input*, and an output's value only ever moves because
// something explicitly commanded it to.
func valueChanged(role model.DeviceRole, prev, cur engine.Value, deadband float64) bool {
	if role == model.RoleAnalogIn {
		return math.Abs(cur.Float-prev.Float) >= deadband
	}
	return !prev.Equal(cur)
}
