package sampling

import (
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

func testPolicy() Policy {
	return Policy{
		Deadband:    4,
		MinInterval: time.Second,
		MaxInterval: 60 * time.Second,
	}
}

func TestDecide_FirstObservationAlwaysEmits(t *testing.T) {
	p := testPolicy()
	now := time.Now()
	if !p.Decide(State{}, model.RoleAnalogIn, engine.FloatValue(500), now) {
		t.Fatal("first observation should always emit")
	}
}

func TestDecide_BoolEmitsOnEveryEdge(t *testing.T) {
	p := testPolicy()
	base := time.Now()
	prev := State{Value: engine.BoolValue(false), At: base, Valid: true}

	// Enough time has passed since the last emission.
	now := base.Add(2 * time.Second)
	if !p.Decide(prev, model.RoleDigitalIn, engine.BoolValue(true), now) {
		t.Error("expected a bool edge to emit")
	}
	if p.Decide(prev, model.RoleDigitalIn, engine.BoolValue(false), now) {
		t.Error("expected no change to not emit (below max interval)")
	}
}

func TestDecide_MinIntervalSuppresses(t *testing.T) {
	p := testPolicy()
	base := time.Now()
	prev := State{Value: engine.BoolValue(false), At: base, Valid: true}

	// Only 100ms passed, well under the 1s floor.
	now := base.Add(100 * time.Millisecond)
	if p.Decide(prev, model.RoleDigitalIn, engine.BoolValue(true), now) {
		t.Error("expected min_interval to suppress a too-soon emission")
	}
}

func TestDecide_AnalogDeadband(t *testing.T) {
	p := testPolicy() // deadband 4
	base := time.Now()
	prev := State{Value: engine.FloatValue(500), At: base, Valid: true}
	now := base.Add(2 * time.Second) // past min_interval

	// A 3-count wobble should not emit.
	if p.Decide(prev, model.RoleAnalogIn, engine.FloatValue(503), now) {
		t.Error("a 3-count wobble under a deadband of 4 should not emit")
	}
	// A 5-count step should emit.
	if !p.Decide(prev, model.RoleAnalogIn, engine.FloatValue(505), now) {
		t.Error("a 5-count step over a deadband of 4 should emit")
	}
}

func TestDecide_MaxIntervalForcesHeartbeat(t *testing.T) {
	p := testPolicy()
	base := time.Now()
	prev := State{Value: engine.FloatValue(500), At: base, Valid: true}

	// No change at all, but max_interval has elapsed.
	now := base.Add(61 * time.Second)
	if !p.Decide(prev, model.RoleAnalogIn, engine.FloatValue(500), now) {
		t.Error("expected a heartbeat emission once max_interval elapses, even with no change")
	}
}

func TestValueChanged_AnalogUsesDeadbandOtherRolesDoNot(t *testing.T) {
	if valueChanged(model.RoleAnalogIn, engine.FloatValue(100), engine.FloatValue(103), 4) {
		t.Error("analog: 3 < deadband 4 should not count as changed")
	}
	if !valueChanged(model.RoleAnalogIn, engine.FloatValue(100), engine.FloatValue(104), 4) {
		t.Error("analog: 4 >= deadband 4 should count as changed")
	}
	if !valueChanged(model.RoleDigitalIn, engine.BoolValue(false), engine.BoolValue(true), 4) {
		t.Error("digital-in: any edge should count as changed")
	}
	if valueChanged(model.RoleDigitalOut, engine.BoolValue(true), engine.BoolValue(true), 4) {
		t.Error("digital-out: identical value should not count as changed")
	}
	if !valueChanged(model.RolePWMOut, engine.FloatValue(10), engine.FloatValue(11), 4) {
		t.Error("pwm-out: any change (even under the analog deadband) should count as changed")
	}
}
