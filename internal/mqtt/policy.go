package mqtt

import (
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
	"github.com/sviniabanditka/pinctl/internal/sampling"
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
// and rate". The rule itself — deadband, min-interval floor, max-interval
// heartbeat — lives in internal/sampling, shared with the value history
// recorder (docs/16-history.md); this is just the adapter from the
// bridge's cfg/publishState shapes to sampling.Policy/State.
func shouldPublish(prev publishState, role model.DeviceRole, cur engine.Value, now time.Time, cfg config.MQTT) bool {
	p := sampling.Policy{
		Deadband:    float64(cfg.AnalogDeadband),
		MinInterval: time.Duration(cfg.MinPublishInterval),
		MaxInterval: time.Duration(cfg.MaxPublishInterval),
	}
	return p.Decide(sampling.State{Value: prev.value, At: prev.at, Valid: prev.valid}, role, cur, now)
}
