package simulate

import (
	"encoding/json"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

func rawJSON(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return b
}

func node(t *testing.T, id, typ string, cfg map[string]any) model.Node {
	return model.Node{ID: id, Type: typ, Config: rawJSON(t, cfg)}
}

func edge(from, to string) model.Edge { return model.Edge{From: from, To: to} }

func devicesByID(devs ...model.Device) map[string]model.Device {
	m := make(map[string]model.Device, len(devs))
	for _, d := range devs {
		m[d.ID] = d
	}
	return m
}

// ldrLedPipeline is the README's example pipeline: device-read(ldr) ->
// expression "a < 500" -> device-write(led).
func ldrLedPipeline() model.Pipeline {
	return model.Pipeline{
		ID:     "sim-ldr-led",
		TickMs: 100,
		Nodes: []model.Node{
			{ID: "n1", Type: "device-read", Config: json.RawMessage(`{"deviceId":"ldr"}`)},
			{ID: "n2", Type: "expression", Config: json.RawMessage(`{
				"expression":"a < 500",
				"inputs":[{"name":"a","type":"float64"}],
				"outputType":"bool"
			}`)},
			{ID: "n3", Type: "device-write", Config: json.RawMessage(`{"deviceId":"led"}`)},
		},
		Edges: []model.Edge{
			{From: "n1.out", To: "n2.a"},
			{From: "n2.out", To: "n3.in"},
		},
	}
}

func ldrLedDevices() map[string]model.Device {
	return devicesByID(
		model.Device{ID: "ldr", Role: model.RoleAnalogIn},
		model.Device{ID: "led", Role: model.RoleDigitalOut},
	)
}

func TestRun_LDRtoLED_SeedFalseThenScriptTrue(t *testing.T) {
	devices := ldrLedDevices()
	req := Request{
		Pipeline: ldrLedPipeline(),
		Steps:    10,
		Inputs:   map[string]float64{"ldr": 800},
		Script:   []ScriptEntry{{Tick: 5, DeviceID: "ldr", Value: 120}},
	}

	res, err := Run(req, devices)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected run error: %+v", res.Error)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", res.Warnings)
	}
	if len(res.Frames) != 10 {
		t.Fatalf("len(Frames) = %d, want 10", len(res.Frames))
	}

	// Node output (n2's expression result) reflects "800 < 500" every tick
	// regardless of the write dedup below: false for ticks 1-4, and true
	// from tick 5 onward — applyScript(tick) runs before that tick's
	// evaluation, so the scripted value (120) is what device-read sees
	// starting at tick 5 itself (the observed boundary: visible starting at
	// its own tick, not the tick after).
	for tick := 1; tick <= 4; tick++ {
		got, _ := res.Frames[tick-1].Nodes["n2"].(bool)
		if got {
			t.Fatalf("tick %d: n2 output = true, want false", tick)
		}
	}
	for tick := 5; tick <= 10; tick++ {
		got, _ := res.Frames[tick-1].Nodes["n2"].(bool)
		if !got {
			t.Fatalf("tick %d: n2 output = false, want true", tick)
		}
	}

	// Only two device-write flushes ever happen (only-on-change dedup):
	// tick 1 (false, the first write ever) and tick 5 (flips to true).
	// Every other tick's runTick queues the same value again but the
	// engine's lastWritten dedup means WriteDevice isn't called, so
	// Frame.Writes has no "led" key for those ticks.
	if got, ok := res.Frames[0].Writes["led"]; !ok || got != false {
		t.Fatalf("tick 1: led write = (%v, ok=%v), want (false, true)", got, ok)
	}
	if _, ok := res.Frames[1].Writes["led"]; ok {
		t.Fatalf("tick 2: expected no write (unchanged), got %+v", res.Frames[1].Writes)
	}
	if _, ok := res.Frames[3].Writes["led"]; ok {
		t.Fatalf("tick 4: expected no write (unchanged), got %+v", res.Frames[3].Writes)
	}
	if got, ok := res.Frames[4].Writes["led"]; !ok || got != true {
		t.Fatalf("tick 5: led write = (%v, ok=%v), want (true, true)", got, ok)
	}
	if _, ok := res.Frames[5].Writes["led"]; ok {
		t.Fatalf("tick 6: expected no write (unchanged), got %+v", res.Frames[5].Writes)
	}
}

func TestRun_Determinism(t *testing.T) {
	devices := ldrLedDevices()
	req := Request{
		Pipeline: ldrLedPipeline(),
		Steps:    50,
		Inputs:   map[string]float64{"ldr": 800},
		Script:   []ScriptEntry{{Tick: 20, DeviceID: "ldr", Value: 120}},
	}

	res1, err := Run(req, devices)
	if err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	res2, err := Run(req, devices)
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}

	j1, err := json.Marshal(res1.Frames)
	if err != nil {
		t.Fatalf("marshal frames #1: %v", err)
	}
	j2, err := json.Marshal(res2.Frames)
	if err != nil {
		t.Fatalf("marshal frames #2: %v", err)
	}
	if string(j1) != string(j2) {
		t.Fatalf("frames differ between runs:\n#1: %s\n#2: %s", j1, j2)
	}
	if !reflect.DeepEqual(res1.Frames, res2.Frames) {
		t.Fatal("reflect.DeepEqual(res1.Frames, res2.Frames) = false")
	}
}

// TestRun_DelayResolvesAtExactTick is docs/18-simulation.md's headline
// claim: "a delay of ten minutes resolves in six hundred steps of a 1s
// tick". The observed boundary: delay's tick-indexed FIFO queue (len ==
// ticks) means the value enqueued on tick 1 is what Eval returns on tick
// 1+ticks — i.e. the very first tick (tick 1) whose input differs from the
// node's own configured "initial" shows up at tick 1+600 = 601, not 600.
func TestRun_DelayResolvesAtExactTick(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "sw", Role: model.RoleDigitalIn},
		model.Device{ID: "out", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:     "sim-delay",
		TickMs: 1000, // 1s tick
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "sw"}),
			node(t, "n2", "delay", map[string]any{"kind": "bool", "initial": false, "delayMs": 600000}), // 10 minutes
			node(t, "n3", "device-write", map[string]any{"deviceId": "out"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.in"),
			edge("n2.out", "n3.in"),
		},
	}

	req := Request{
		Pipeline: p,
		Steps:    602,
		Inputs:   map[string]float64{"sw": 1}, // true from tick 1 onward
	}

	res, err := Run(req, devices)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected run error: %+v", res.Error)
	}
	if len(res.Frames) != 602 {
		t.Fatalf("len(Frames) = %d, want 602", len(res.Frames))
	}

	for _, tick := range []int{1, 100, 500, 600} {
		got, _ := res.Frames[tick-1].Nodes["n2"].(bool)
		if got {
			t.Fatalf("tick %d: n2 output = true, want false (delay not yet elapsed)", tick)
		}
	}
	got601, _ := res.Frames[600].Nodes["n2"].(bool)
	if !got601 {
		t.Fatal("tick 601: n2 output = false, want true (delay of 600 ticks elapsed)")
	}
}

func TestRun_UnseededInputWarning(t *testing.T) {
	devices := ldrLedDevices()
	req := Request{
		Pipeline: ldrLedPipeline(),
		Steps:    3,
		// Inputs deliberately omits "ldr".
	}

	res, err := Run(req, devices)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("len(Warnings) = %d, want 1: %+v", len(res.Warnings), res.Warnings)
	}
	w := res.Warnings[0]
	if w.Code != "unseeded_input" || w.DeviceID != "ldr" {
		t.Fatalf("Warnings[0] = %+v, want {unseeded_input ldr}", w)
	}

	// The type's zero (float64 0) means 0 < 500 is true -> LED on from
	// tick 1.
	got, _ := res.Frames[0].Writes["led"].(bool)
	if !got {
		t.Fatalf("tick 1: led write = %v, want true (zero-seeded ldr reads as 0)", res.Frames[0].Writes["led"])
	}
}

func TestRun_TimerPulsesOnScheduledTicks(t *testing.T) {
	devices := map[string]model.Device{}
	p := model.Pipeline{
		ID:     "sim-timer",
		TickMs: 100,
		Nodes: []model.Node{
			node(t, "n1", "timer", map[string]any{"periodMs": 300}),
		},
	}
	req := Request{Pipeline: p, Steps: 9}

	res, err := Run(req, devices)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for tick := 1; tick <= 9; tick++ {
		got, _ := res.Frames[tick-1].Nodes["n1"].(bool)
		want := tick%3 == 0
		if got != want {
			t.Fatalf("tick %d: timer output = %v, want %v", tick, got, want)
		}
	}
}

func TestRun_DivisionByZeroEndsRunEarly(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "sw", Role: model.RoleAnalogIn},
		model.Device{ID: "out", Role: model.RolePWMOut},
	)
	p := model.Pipeline{
		ID:     "sim-div-zero",
		TickMs: 20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "sw"}),
			node(t, "n2", "expression", map[string]any{
				"expression": "1.0 / a",
				"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
				"outputType": "float64",
			}),
			node(t, "n3", "device-write", map[string]any{"deviceId": "out"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.a"),
			edge("n2.out", "n3.in"),
		},
	}

	req := Request{
		Pipeline: p,
		Steps:    10,
		Inputs:   map[string]float64{"sw": 0}, // a == 0 every tick -> always errors
	}

	res, err := Run(req, devices)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Error == nil {
		t.Fatal("Result.Error = nil, want set")
	}
	if res.Error.Tick != 1 {
		t.Fatalf("Error.Tick = %d, want 1 (ends on the very first failing tick, not after 3 like a live pipeline auto-disable)", res.Error.Tick)
	}
	if res.Error.NodeID != "n2" {
		t.Fatalf("Error.NodeID = %q, want n2", res.Error.NodeID)
	}
	if len(res.Frames) != 0 {
		t.Fatalf("len(Frames) = %d, want 0 (fails on tick 1, no good frame before it)", len(res.Frames))
	}
}

// TestRun_DivisionByZeroReturnsFramesBeforeFailure checks the "frames up to
// that point" half of the contract with a run that succeeds for a while
// first.
func TestRun_DivisionByZeroReturnsFramesBeforeFailure(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "sw", Role: model.RoleAnalogIn},
		model.Device{ID: "out", Role: model.RolePWMOut},
	)
	p := model.Pipeline{
		ID:     "sim-div-zero-2",
		TickMs: 20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "sw"}),
			node(t, "n2", "expression", map[string]any{
				"expression": "1.0 / a",
				"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
				"outputType": "float64",
			}),
			node(t, "n3", "device-write", map[string]any{"deviceId": "out"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.a"),
			edge("n2.out", "n3.in"),
		},
	}

	req := Request{
		Pipeline: p,
		Steps:    5,
		Inputs:   map[string]float64{"sw": 2},
		Script:   []ScriptEntry{{Tick: 3, DeviceID: "sw", Value: 0}},
	}

	res, err := Run(req, devices)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Error == nil {
		t.Fatal("Result.Error = nil, want set")
	}
	if res.Error.Tick != 3 {
		t.Fatalf("Error.Tick = %d, want 3", res.Error.Tick)
	}
	if len(res.Frames) != 2 {
		t.Fatalf("len(Frames) = %d, want 2 (ticks 1-2 succeeded)", len(res.Frames))
	}
}

func TestRun_StepsOverCapRejected(t *testing.T) {
	_, err := Run(Request{Pipeline: ldrLedPipeline(), Steps: MaxSteps + 1}, ldrLedDevices())
	if err == nil {
		t.Fatal("expected an error for Steps over the cap, got nil")
	}
	if _, ok := err.(*StepsExceedsCapError); !ok {
		t.Fatalf("err = %T (%v), want *StepsExceedsCapError", err, err)
	}
}

func TestRun_UnknownDeviceRejected(t *testing.T) {
	_, err := Run(Request{
		Pipeline: ldrLedPipeline(),
		Steps:    1,
		Inputs:   map[string]float64{"does-not-exist": 1},
	}, ldrLedDevices())
	if err == nil {
		t.Fatal("expected an error for an unknown device id, got nil")
	}
	if _, ok := err.(*UnknownDeviceError); !ok {
		t.Fatalf("err = %T (%v), want *UnknownDeviceError", err, err)
	}
}

// TestRun_HugeTickMsDoesNotSleep asserts the fake clock/ticker never touch
// real time: a huge Steps*tickMs combination (in simulated time, over 30
// years) still completes the whole run in milliseconds of wall time.
func TestRun_HugeTickMsDoesNotSleep(t *testing.T) {
	p := ldrLedPipeline()
	p.TickMs = 1 << 30 // absurdly large tick period in wall-clock terms
	req := Request{
		Pipeline: p,
		Steps:    MaxSteps,
		Inputs:   map[string]float64{"ldr": 800},
	}

	start := time.Now()
	res, err := Run(req, ldrLedDevices())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Frames) != MaxSteps {
		t.Fatalf("len(Frames) = %d, want %d", len(res.Frames), MaxSteps)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took %s, want well under a second (fake clock/ticker must never sleep on simulated time)", elapsed)
	}
}

func TestRun_NoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	for i := 0; i < 20; i++ {
		if _, err := Run(Request{
			Pipeline: ldrLedPipeline(),
			Steps:    20,
			Inputs:   map[string]float64{"ldr": 800},
		}, ldrLedDevices()); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	// Give any straggler goroutine a moment to actually exit before
	// counting — Manager.StopAll already waits for each runner's doneCh,
	// so this is just settling scheduler noise, not synchronization.
	deadline := time.Now().Add(2 * time.Second)
	for {
		after := runtime.NumGoroutine()
		if after <= before+2 { // small slack for test/runtime bookkeeping goroutines
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: before=%d after=%d", before, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
