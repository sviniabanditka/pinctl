package mqtt

import (
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

func testCfg() config.MQTT {
	return config.MQTT{
		AnalogDeadband:     4,
		MinPublishInterval: config.Duration(time.Second),
		MaxPublishInterval: config.Duration(60 * time.Second),
	}
}

func TestShouldPublish_FirstObservationAlwaysPublishes(t *testing.T) {
	cfg := testCfg()
	now := time.Now()
	got := shouldPublish(publishState{}, model.RoleAnalogIn, engine.FloatValue(500), now, cfg)
	if !got {
		t.Fatal("first observation should always publish")
	}
}

func TestShouldPublish_BoolPublishesOnEveryEdge(t *testing.T) {
	cfg := testCfg()
	base := time.Now()
	prev := publishState{value: engine.BoolValue(false), at: base, valid: true}

	// Enough time has passed since the last publish.
	now := base.Add(2 * time.Second)
	if !shouldPublish(prev, model.RoleDigitalIn, engine.BoolValue(true), now, cfg) {
		t.Error("expected a bool edge to publish")
	}
	if shouldPublish(prev, model.RoleDigitalIn, engine.BoolValue(false), now, cfg) {
		t.Error("expected no change to not publish (below max interval)")
	}
}

func TestShouldPublish_MinPublishIntervalSuppresses(t *testing.T) {
	cfg := testCfg()
	base := time.Now()
	prev := publishState{value: engine.BoolValue(false), at: base, valid: true}

	// Only 100ms passed, well under the 1s floor.
	now := base.Add(100 * time.Millisecond)
	if shouldPublish(prev, model.RoleDigitalIn, engine.BoolValue(true), now, cfg) {
		t.Error("expected min_publish_interval to suppress a too-soon publish")
	}
}

func TestShouldPublish_AnalogDeadband(t *testing.T) {
	cfg := testCfg() // deadband 4
	base := time.Now()
	prev := publishState{value: engine.FloatValue(500), at: base, valid: true}
	now := base.Add(2 * time.Second) // past min_publish_interval

	// A 3-count wobble should not publish.
	if shouldPublish(prev, model.RoleAnalogIn, engine.FloatValue(503), now, cfg) {
		t.Error("a 3-count wobble under a deadband of 4 should not publish")
	}
	// A 5-count step should publish.
	if !shouldPublish(prev, model.RoleAnalogIn, engine.FloatValue(505), now, cfg) {
		t.Error("a 5-count step over a deadband of 4 should publish")
	}
}

func TestShouldPublish_MaxPublishIntervalForcesHeartbeat(t *testing.T) {
	cfg := testCfg()
	base := time.Now()
	prev := publishState{value: engine.FloatValue(500), at: base, valid: true}

	// No change at all, but max_publish_interval has elapsed.
	now := base.Add(61 * time.Second)
	if !shouldPublish(prev, model.RoleAnalogIn, engine.FloatValue(500), now, cfg) {
		t.Error("expected a heartbeat publish once max_publish_interval elapses, even with no change")
	}
}
