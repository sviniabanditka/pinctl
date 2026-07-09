package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// TestLDRTurnsOnLEDBelowThreshold is the README's canonical LDR->LED
// scenario (docs/13-testing.md "Engine simulation tests"): device-read
// (analog LDR) -> compare (op=lt, threshold=300) -> device-write (LED).
// Analog 200 (< 300) turns the LED on, analog 800 (>= 300) turns it off.
// It also exercises write dedup: repeating the same analog reading must
// not re-issue the device write.
func TestLDRTurnsOnLEDBelowThreshold(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", Role: model.RoleAnalogIn},
		model.Device{ID: "led", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:      "ldr-led",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "ldr"}),
			node(t, "n2", "compare", map[string]any{"op": "lt", "threshold": 300}),
			node(t, "n3", "device-write", map[string]any{"deviceId": "led"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.in"),
			edge("n2.out", "n3.in"),
		},
	}

	io := newFakeIO()
	io.SetRead("ldr", FloatValue(900)) // dark, LED should stay off initially

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	// tick 1: 900 -> not lt 300 -> LED off. No incoming edge yet so no
	// write should have happened for "off" since off is the zero value
	// and dedup only prevents *repeat* writes -- the very first write
	// still lands.
	mt.Fire(time.Unix(1, 0))
	waitForTick(t, mgr, p.ID, 1)
	if v, ok := io.LastWrite("led"); !ok || v.Bool != false {
		t.Fatalf("tick 1: led write = %+v, ok=%v, want false", v, ok)
	}
	if got := io.WriteCount("led"); got != 1 {
		t.Fatalf("tick 1: write count = %d, want 1", got)
	}

	// tick 2: still 900 -> still off -> dedup, no new write.
	mt.Fire(time.Unix(2, 0))
	waitForTick(t, mgr, p.ID, 2)
	if got := io.WriteCount("led"); got != 1 {
		t.Fatalf("tick 2 (unchanged): write count = %d, want 1 (deduped)", got)
	}

	// tick 3: 200 -> lt 300 -> LED on.
	io.SetRead("ldr", FloatValue(200))
	mt.Fire(time.Unix(3, 0))
	waitForTick(t, mgr, p.ID, 3)
	if v, ok := io.LastWrite("led"); !ok || v.Bool != true {
		t.Fatalf("tick 3: led write = %+v, ok=%v, want true", v, ok)
	}
	if got := io.WriteCount("led"); got != 2 {
		t.Fatalf("tick 3: write count = %d, want 2", got)
	}

	// tick 4: still 200 -> still on -> dedup.
	mt.Fire(time.Unix(4, 0))
	waitForTick(t, mgr, p.ID, 4)
	if got := io.WriteCount("led"); got != 2 {
		t.Fatalf("tick 4 (unchanged): write count = %d, want 2 (deduped)", got)
	}

	// tick 5: back to 800 -> LED off again -> new write.
	io.SetRead("ldr", FloatValue(800))
	mt.Fire(time.Unix(5, 0))
	waitForTick(t, mgr, p.ID, 5)
	if v, ok := io.LastWrite("led"); !ok || v.Bool != false {
		t.Fatalf("tick 5: led write = %+v, ok=%v, want false", v, ok)
	}
	if got := io.WriteCount("led"); got != 3 {
		t.Fatalf("tick 5: write count = %d, want 3", got)
	}

	st, ok := mgr.Status(p.ID)
	if !ok || st.State != StateRunning {
		t.Fatalf("status = %+v, ok=%v, want running", st, ok)
	}
}

// TestAutoDisableAfterThreeConsecutiveFailures verifies
// docs/07-pipeline-engine.md's "Runtime error handling": a node eval error
// abandons that tick but doesn't stop the scheduler; three consecutive
// failed ticks auto-disable the pipeline.
func TestAutoDisableAfterThreeConsecutiveFailures(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "sw", Role: model.RoleDigitalIn},
		model.Device{ID: "led2", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:      "faulty",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "sw"}),
			node(t, "n2", "device-write", map[string]any{"deviceId": "led2"}),
		},
		Edges: []model.Edge{edge("n1.out", "n2.in")},
	}

	io := newFakeIO()
	io.SetReadErr("sw", errors.New("board offline"))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	for i := 1; i <= 3; i++ {
		mt.Fire(time.Unix(int64(i), 0))
	}
	st := waitForState(t, mgr, p.ID, StateDisabled)
	if st.ConsecutiveFailures != 3 {
		t.Fatalf("ConsecutiveFailures = %d, want 3", st.ConsecutiveFailures)
	}
	if st.LastErrorNode != "n1" {
		t.Fatalf("LastErrorNode = %q, want n1", st.LastErrorNode)
	}
	if st.LastError == nil {
		t.Fatal("LastError = nil, want non-nil")
	}
	// No writes should ever have landed: every tick failed at n1, before
	// n2 (the sink) ever evaluated.
	if got := io.WriteCount("led2"); got != 0 {
		t.Fatalf("write count = %d, want 0 (tick abandoned before the sink)", got)
	}
}

// TestCleanStop verifies Manager.Stop returns only once the scheduler
// goroutine has actually exited (no goroutine leak), and that it can be
// called at any point without racing the tick loop.
func TestCleanStop(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", Role: model.RoleAnalogIn},
		model.Device{ID: "led", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:      "stop-me",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "ldr"}),
			node(t, "n2", "compare", map[string]any{"op": "lt", "threshold": 300}),
			node(t, "n3", "device-write", map[string]any{"deviceId": "led"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.in"),
			edge("n2.out", "n3.in"),
		},
	}

	io := newFakeIO()
	io.SetRead("ldr", FloatValue(500))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	mt.Fire(time.Unix(1, 0))
	waitForTick(t, mgr, p.ID, 1)

	done := make(chan struct{})
	go func() {
		mgr.Stop(p.ID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return in time")
	}

	if _, ok := mgr.Status(p.ID); ok {
		t.Fatal("status still present after Stop")
	}

	// A further Fire on the now-defunct ticker must not be attempted
	// (nothing would ever receive it) — this is exactly why Stop is only
	// safe to call, from the test's perspective, once no more Fire calls
	// are pending. Calling Stop again must still be a safe no-op.
	mgr.Stop(p.ID)
}

// TestHotSwapResetsState verifies Start on an already-running pipeline id
// stops the old runner and starts a fresh one (hot-swap = Stop+Start),
// resetting the write-dedup cache per docs/07-pipeline-engine.md
// "Hot-reload" (node state: reset, not preserved).
func TestHotSwapResetsState(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", Role: model.RoleAnalogIn},
		model.Device{ID: "led", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:      "hotswap",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "ldr"}),
			node(t, "n2", "compare", map[string]any{"op": "lt", "threshold": 300}),
			node(t, "n3", "device-write", map[string]any{"deviceId": "led"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.in"),
			edge("n2.out", "n3.in"),
		},
	}

	io := newFakeIO()
	io.SetRead("ldr", FloatValue(200)) // lt 300 -> LED on

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt1 := <-tickers
	mt1.Fire(time.Unix(1, 0))
	waitForTick(t, mgr, p.ID, 1)
	if got := io.WriteCount("led"); got != 1 {
		t.Fatalf("write count = %d, want 1", got)
	}

	// Reload the same pipeline (unchanged) while the LDR is still 200: a
	// fresh runner has a fresh write-dedup cache, so the first tick of the
	// new graph writes again even though the value (true) hasn't changed
	// from the old graph's perspective.
	mustNotErr(t, mgr.Start(p, devices, io))
	mt2 := <-tickers
	mt2.Fire(time.Unix(2, 0))
	waitForTick(t, mgr, p.ID, 1) // fresh runner, tick counter restarts at 1
	if got := io.WriteCount("led"); got != 2 {
		t.Fatalf("write count after hot-swap = %d, want 2 (dedup cache reset)", got)
	}
}
