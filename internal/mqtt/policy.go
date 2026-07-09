package mqtt

import (
	"math"
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// publishState is what the bridge remembers about the last state publish
// for one device, used to evaluate the deadband/rate policy for the next
// candidate value.
type publishState struct {
	value engine.Value
	at    time.Time
	valid bool // false until the first publish
}

// shouldPublish decides whether cur is worth publishing for a device with
// the given role, per docs/15-mqtt-bridge.md "Publishing policy: deadband
// and rate":
//
//   - the value changed by at least analog_deadband raw counts for
//     analog-in, or at all for booleans/pwm-out, and at least
//     min_publish_interval has passed since the last publish, or
//   - at most max_publish_interval has passed regardless of change (a
//     heartbeat).
//
// The very first observation for a device always publishes (there is
// nothing to compare against, and no retained value would exist yet).
func shouldPublish(prev publishState, role model.DeviceRole, cur engine.Value, now time.Time, cfg config.MQTT) bool {
	if !prev.valid {
		return true
	}
	since := now.Sub(prev.at)
	if since >= time.Duration(cfg.MaxPublishInterval) {
		return true // heartbeat, regardless of change
	}
	if !valueChanged(role, prev.value, cur, cfg.AnalogDeadband) {
		return false
	}
	return since >= time.Duration(cfg.MinPublishInterval)
}

// valueChanged reports whether cur differs enough from prev to count as a
// change for role: any difference for the boolean roles, at least deadband
// raw counts for analog-in. pwm-out (also numeric) uses "any change" like
// the booleans — the deadband exists to tame ADC noise on an *input*, and
// pwm-out's value only ever moves because something explicitly commanded
// it to.
func valueChanged(role model.DeviceRole, prev, cur engine.Value, deadband int) bool {
	if role == model.RoleAnalogIn {
		return math.Abs(cur.Float-prev.Float) >= float64(deadband)
	}
	return !prev.Equal(cur)
}
