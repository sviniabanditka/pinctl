package engine

import (
	"testing"
	"time"
)

// TestThresholdHysteresis_BandCrossing exercises docs/08-node-catalog.md
// "threshold-hysteresis": crossing above onLevel turns it on, crossing
// below offLevel turns it off, and the band in between (inclusive) holds
// the previous state — in both directions.
func TestThresholdHysteresis_BandCrossing(t *testing.T) {
	n := mustNode(t, thresholdHysteresisType{}, map[string]any{
		"onLevel": 650, "offLevel": 450, "initial": false,
	}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))

	seq := []struct {
		in   float64
		want bool
	}{
		{300, false}, // below band, initial false -> stays false
		{450, false}, // at offLevel (band, inclusive) -> holds false
		{500, false}, // inside band -> holds
		{650, false}, // at onLevel (band, inclusive) -> holds
		{651, true},  // above onLevel -> turns on
		{700, true},  // stays on
		{500, true},  // back inside band -> holds true
		{450, true},  // at offLevel -> holds (band inclusive)
		{449, false}, // below offLevel -> turns off
		{300, false}, // stays off
	}
	for i, step := range seq {
		out, err := n.Eval(TickContext{}, []Value{FloatValue(step.in)})
		mustNotErr(t, err)
		if out[0].Bool != step.want {
			t.Fatalf("step %d: in=%v out=%v, want %v", i, step.in, out[0].Bool, step.want)
		}
	}
}

// TestThresholdHysteresis_DegenerateEqualLevels covers docs/08-node-catalog.md
// "threshold-hysteresis": "onLevel == offLevel degenerates to a plain >=
// comparator" — no hold state, since the hysteresis band is empty.
func TestThresholdHysteresis_DegenerateEqualLevels(t *testing.T) {
	n := mustNode(t, thresholdHysteresisType{}, map[string]any{
		"onLevel": 500, "offLevel": 500, "initial": false,
	}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))

	seq := []struct {
		in   float64
		want bool
	}{
		{300, false}, // below level -> false
		{500, true},  // at level -> true (plain >=, no hold)
		{499, false}, // below level again -> false immediately (no hysteresis)
		{501, true},  // above level -> true
	}
	for i, step := range seq {
		out, err := n.Eval(TickContext{}, []Value{FloatValue(step.in)})
		mustNotErr(t, err)
		if out[0].Bool != step.want {
			t.Fatalf("step %d: in=%v out=%v, want %v", i, step.in, out[0].Bool, step.want)
		}
	}
}

func TestThresholdHysteresis_OffAboveOnIsValidationError(t *testing.T) {
	_, err := thresholdHysteresisType{}.Ports(rawJSON(t, map[string]any{"onLevel": 400, "offLevel": 600}), nil)
	if err == nil {
		t.Fatal("expected validation error for offLevel > onLevel")
	}
}

// TestScale_LinearAndClamp covers docs/08-node-catalog.md "scale": the
// linear formula, clamp on/off, and an inverted output range.
func TestScale_LinearAndClamp(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		in   float64
		want float64
	}{
		{
			name: "default raw analog to 0..1, midpoint",
			cfg:  map[string]any{},
			in:   511.5,
			want: 0.5,
		},
		{
			name: "clamp true, above inMax clamps to outMax",
			cfg:  map[string]any{"inMin": 0, "inMax": 1023, "outMin": 0, "outMax": 1, "clamp": true},
			in:   2000,
			want: 1,
		},
		{
			name: "clamp true, below inMin clamps to outMin",
			cfg:  map[string]any{"inMin": 0, "inMax": 1023, "outMin": 0, "outMax": 1, "clamp": true},
			in:   -100,
			want: 0,
		},
		{
			name: "clamp false extrapolates beyond inMax",
			cfg:  map[string]any{"inMin": 0, "inMax": 1023, "outMin": 0, "outMax": 1, "clamp": false},
			in:   2046,
			want: 2,
		},
		{
			name: "inverted output range (outMin > outMax)",
			cfg:  map[string]any{"inMin": 0, "inMax": 100, "outMin": 100, "outMax": 0, "clamp": true},
			in:   25,
			want: 75,
		},
		{
			name: "inverted range, clamp still uses min/max of the two bounds",
			cfg:  map[string]any{"inMin": 0, "inMax": 100, "outMin": 100, "outMax": 0, "clamp": true},
			in:   1000,
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := mustNode(t, scaleType{}, tt.cfg, NodeDeps{})
			mustNotErr(t, n.Init(newFakeClock()))
			out, err := n.Eval(TickContext{}, []Value{FloatValue(tt.in)})
			mustNotErr(t, err)
			if diff := out[0].Float - tt.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("scale(%v) = %v, want %v", tt.in, out[0].Float, tt.want)
			}
		})
	}
}

func TestScale_InMinEqualsInMaxIsValidationError(t *testing.T) {
	_, err := scaleType{}.Ports(rawJSON(t, map[string]any{"inMin": 5, "inMax": 5}), nil)
	if err == nil {
		t.Fatal("expected validation error for inMin == inMax")
	}
}

// TestAndOr_TruthTable covers docs/08-node-catalog.md "and"/"or" over 3
// inputs.
func TestAndOr_TruthTable(t *testing.T) {
	and := mustNode(t, andType{}, map[string]any{"inputCount": 3}, NodeDeps{})
	or := mustNode(t, orType{}, map[string]any{"inputCount": 3}, NodeDeps{})
	mustNotErr(t, and.Init(newFakeClock()))
	mustNotErr(t, or.Init(newFakeClock()))

	tests := []struct {
		in      [3]bool
		wantAnd bool
		wantOr  bool
	}{
		{[3]bool{true, true, true}, true, true},
		{[3]bool{true, true, false}, false, true},
		{[3]bool{false, false, false}, false, false},
		{[3]bool{true, false, false}, false, true},
	}
	for _, tt := range tests {
		in := []Value{BoolValue(tt.in[0]), BoolValue(tt.in[1]), BoolValue(tt.in[2])}
		aOut, err := and.Eval(TickContext{}, in)
		mustNotErr(t, err)
		if aOut[0].Bool != tt.wantAnd {
			t.Fatalf("and(%v) = %v, want %v", tt.in, aOut[0].Bool, tt.wantAnd)
		}
		oOut, err := or.Eval(TickContext{}, in)
		mustNotErr(t, err)
		if oOut[0].Bool != tt.wantOr {
			t.Fatalf("or(%v) = %v, want %v", tt.in, oOut[0].Bool, tt.wantOr)
		}
	}
}

func TestAndOr_InputCountBelowTwoIsValidationError(t *testing.T) {
	if _, err := (andType{}).Ports(rawJSON(t, map[string]any{"inputCount": 1}), nil); err == nil {
		t.Fatal("expected validation error for inputCount < 2 (and)")
	}
	if _, err := (orType{}).Ports(rawJSON(t, map[string]any{"inputCount": 1}), nil); err == nil {
		t.Fatal("expected validation error for inputCount < 2 (or)")
	}
}

func TestNot(t *testing.T) {
	n := mustNode(t, notType{}, map[string]any{}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))
	out, err := n.Eval(TickContext{}, []Value{BoolValue(true)})
	mustNotErr(t, err)
	if out[0].Bool != false {
		t.Fatalf("not(true) = %v, want false", out[0].Bool)
	}
	out, err = n.Eval(TickContext{}, []Value{BoolValue(false)})
	mustNotErr(t, err)
	if out[0].Bool != true {
		t.Fatalf("not(false) = %v, want true", out[0].Bool)
	}
}

// TestDelay_TickIndexedQueue covers docs/08-node-catalog.md "delay": a
// bool signal delayed by a fixed number of ticks (delayMs rounded down to
// a tickMs multiple), holding `initial` until the first delayed value
// arrives, and preserving fast toggles (not filtering them).
func TestDelay_TickIndexedQueue(t *testing.T) {
	// tickMs=20, delayMs=60 -> 3 ticks of delay.
	n := mustNode(t, delayType{}, map[string]any{"kind": "bool", "delayMs": 60, "initial": false}, NodeDeps{TickMs: 20})
	mustNotErr(t, n.Init(newFakeClock()))

	inputs := []bool{true, true, false, true, false, false, false}
	// Expect: ticks 1..3 output the initial value (false); tick 4 outputs
	// tick 1's input (true); tick 5 outputs tick 2's input (true); etc.
	want := []bool{false, false, false, true, true, false, true}

	for i, in := range inputs {
		out, err := n.Eval(TickContext{}, []Value{BoolValue(in)})
		mustNotErr(t, err)
		if out[0].Bool != want[i] {
			t.Fatalf("tick %d: in=%v out=%v, want %v", i+1, in, out[0].Bool, want[i])
		}
	}
}

func TestDelay_ZeroDelayIsPassthrough(t *testing.T) {
	n := mustNode(t, delayType{}, map[string]any{"kind": "float", "delayMs": 0}, NodeDeps{TickMs: 20})
	mustNotErr(t, n.Init(newFakeClock()))
	out, err := n.Eval(TickContext{}, []Value{FloatValue(42)})
	mustNotErr(t, err)
	if out[0].Float != 42 {
		t.Fatalf("zero-delay passthrough = %v, want 42", out[0].Float)
	}
}

func TestDelay_SubTickDelayIsPassthrough(t *testing.T) {
	// delayMs=10 < tickMs=20 -> 0 ticks of delay, same-tick passthrough.
	n := mustNode(t, delayType{}, map[string]any{"kind": "bool", "delayMs": 10}, NodeDeps{TickMs: 20})
	mustNotErr(t, n.Init(newFakeClock()))
	out, err := n.Eval(TickContext{}, []Value{BoolValue(true)})
	mustNotErr(t, err)
	if !out[0].Bool {
		t.Fatal("sub-tick delay should passthrough same-tick")
	}
}

// TestDelay_DelayMsExceedingCapIsValidationError covers the sane cap on
// delay's tick-indexed queue: delayMs/tickMs beyond maxDelayTicks must be
// rejected at config time rather than allocating an unbounded queue.
func TestDelay_DelayMsExceedingCapIsValidationError(t *testing.T) {
	_, err := delayType{}.NewInstance(rawJSON(t, map[string]any{
		"kind": "bool", "delayMs": (maxDelayTicks + 1) * 20,
	}), NodeDeps{TickMs: 20})
	if err == nil {
		t.Fatal("expected validation error for delayMs/tickMs exceeding the max tick cap")
	}

	// Exactly at the cap is fine.
	_, err = delayType{}.NewInstance(rawJSON(t, map[string]any{
		"kind": "bool", "delayMs": maxDelayTicks * 20,
	}), NodeDeps{TickMs: 20})
	if err != nil {
		t.Fatalf("delayMs/tickMs at the cap should be accepted, got: %v", err)
	}
}

// TestDelay_SteadyStateNoQueueRealloc covers the fix for the per-tick queue
// realloc bug: delayNode must reuse one fixed-size ring buffer (mutated in
// place via a head index) rather than reallocating a new backing array on
// every tick via append(queue[1:], v).
func TestDelay_SteadyStateNoQueueRealloc(t *testing.T) {
	node, err := delayType{}.NewInstance(rawJSON(t, map[string]any{
		"kind": "bool", "delayMs": 60, "initial": false,
	}), NodeDeps{TickMs: 20})
	mustNotErr(t, err)
	dn := node.(*delayNode)
	mustNotErr(t, dn.Init(newFakeClock()))

	backing := &dn.queue[0]
	in := []Value{BoolValue(true)}
	for i := 0; i < 1000; i++ {
		if _, err := dn.Eval(TickContext{}, in); err != nil {
			t.Fatal(err)
		}
	}
	if &dn.queue[0] != backing {
		t.Fatal("delayNode.queue's backing array changed across ticks — steady state must not reallocate")
	}
	if len(dn.queue) != dn.ticks {
		t.Fatalf("len(queue) = %d, want %d (ticks) — ring buffer must not grow", len(dn.queue), dn.ticks)
	}
}

// TestDebounce_SuppressesChatter covers docs/08-node-catalog.md
// "debounce": a value must hold continuously for stableMs before it's
// reflected on out; any change restarts the timer and discards the
// previous candidate.
func TestDebounce_SuppressesChatter(t *testing.T) {
	clk := newFakeClock()
	n := mustNode(t, debounceType{}, map[string]any{"kind": "bool", "stableMs": 100, "initial": false}, NodeDeps{})
	mustNotErr(t, n.Init(clk))

	step := func(advance time.Duration, in bool, want bool, label string) {
		t.Helper()
		clk.Advance(advance)
		out, err := n.Eval(TickContext{}, []Value{BoolValue(in)})
		mustNotErr(t, err)
		if out[0].Bool != want {
			t.Fatalf("%s: out=%v, want %v", label, out[0].Bool, want)
		}
	}

	// t=0: in flips to true, timer starts; not stable yet -> still false.
	step(0, true, false, "t=0 first change")
	// t=50ms: still true, only 50ms held -> not yet stable.
	step(50*time.Millisecond, true, false, "t=50ms held")
	// t=80ms: chatter -- flips to false, discarding the true candidate and
	// restarting the timer.
	step(30*time.Millisecond, false, false, "t=80ms chatter")
	// t=150ms (70ms after the chatter): not yet 100ms since the restart.
	step(70*time.Millisecond, false, false, "t=150ms still not stable")
	// t=190ms (110ms after the restart at t=80ms): now stable at false...
	// which is also the initial value, so no visible change, but confirm
	// candidate/output tracking by continuing to true.
	step(40*time.Millisecond, true, false, "t=190ms change to true again, timer restarts")
	// t=300ms (110ms after the t=190ms restart): stable at true.
	step(110*time.Millisecond, true, true, "t=300ms stable true")
}

func TestDebounce_FloatExactEquality(t *testing.T) {
	clk := newFakeClock()
	n := mustNode(t, debounceType{}, map[string]any{"kind": "float", "stableMs": 100, "initial": 0}, NodeDeps{})
	mustNotErr(t, n.Init(clk))

	out, err := n.Eval(TickContext{}, []Value{FloatValue(5.0)})
	mustNotErr(t, err)
	if out[0].Float != 0 {
		t.Fatalf("immediately after change, out = %v, want 0 (not yet stable)", out[0].Float)
	}
	clk.Advance(150 * time.Millisecond)
	out, err = n.Eval(TickContext{}, []Value{FloatValue(5.0)})
	mustNotErr(t, err)
	if out[0].Float != 5.0 {
		t.Fatalf("after stableMs elapsed, out = %v, want 5.0", out[0].Float)
	}
}

// TestEdgeDetect_AllModes covers docs/08-node-catalog.md "edge-detect":
// rising/falling/both, one-tick-wide pulses, and no pulse on tick one.
func TestEdgeDetect_AllModes(t *testing.T) {
	tests := []struct {
		mode string
		seq  []bool
		want []bool
	}{
		{
			mode: "rising",
			seq:  []bool{false, false, true, true, false, true},
			want: []bool{false, false, true, false, false, true},
		},
		{
			mode: "falling",
			seq:  []bool{true, true, false, false, true, false},
			want: []bool{false, false, true, false, false, true},
		},
		{
			mode: "both",
			seq:  []bool{false, true, true, false, false, true},
			want: []bool{false, true, false, true, false, true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			n := mustNode(t, edgeDetectType{}, map[string]any{"edge": tt.mode}, NodeDeps{})
			mustNotErr(t, n.Init(newFakeClock()))
			for i, in := range tt.seq {
				out, err := n.Eval(TickContext{}, []Value{BoolValue(in)})
				mustNotErr(t, err)
				if out[0].Bool != tt.want[i] {
					t.Fatalf("mode=%s tick %d: in=%v out=%v, want %v", tt.mode, i+1, in, out[0].Bool, tt.want[i])
				}
			}
		})
	}
}

func TestEdgeDetect_UnknownModeIsValidationError(t *testing.T) {
	_, err := edgeDetectType{}.Ports(rawJSON(t, map[string]any{"edge": "sideways"}), nil)
	if err == nil {
		t.Fatal("expected validation error for unknown edge mode")
	}
}
