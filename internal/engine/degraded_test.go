package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// errSentinel stands in for a board.Err* cause (board.ErrBoardOffline,
// board.ErrSessionDead, board.ErrNoCachedValue) without internal/engine's
// tests needing to import internal/board — all this package cares about is
// that the wrapping *UnreachableError satisfies errors.Is(err,
// ErrDeviceUnreachable), which internal/boardio's own tests already cover
// for the three real board errors.
var errSentinel = errors.New("fake board: unreachable")

func unreachableErr(deviceID, boardID string) error {
	return &UnreachableError{DeviceID: deviceID, BoardID: boardID, Err: errSentinel}
}

// TestDegradedRead_PausesAndRecovers covers docs/07-pipeline-engine.md
// "Degraded ticks": a pipeline whose device-read hits ErrDeviceUnreachable
// goes to StatePaused (not StateError/StateDisabled), never auto-disables
// no matter how many consecutive ticks stay degraded, and returns to
// StateRunning (Detail cleared) the moment the device is reachable again.
func TestDegradedRead_PausesAndRecovers(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", BoardID: "brd_1", Role: model.RoleAnalogIn},
		model.Device{ID: "led", BoardID: "brd_1", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:      "degraded-read",
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
	io.SetReadErr("ldr", unreachableErr("ldr", "brd_1"))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	// Fire well past maxConsecutiveFailures worth of ticks: a run of
	// degraded ticks must never auto-disable the pipeline.
	for i := 1; i <= maxConsecutiveFailures+2; i++ {
		mt.Fire(time.Unix(int64(i), 0))
	}
	st := waitForTick(t, mgr, p.ID, uint64(maxConsecutiveFailures+2))
	if st.State != StatePaused {
		t.Fatalf("State = %q, want %q", st.State, StatePaused)
	}
	if st.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0 (degraded ticks must not increment it)", st.ConsecutiveFailures)
	}
	if st.Detail == "" {
		t.Fatal("Detail is empty, want a reason naming the unreachable device/board")
	}
	if got, want := st.Detail, "ldr"; !strings.Contains(got, want) {
		t.Errorf("Detail = %q, want it to mention device %q", got, want)
	}
	if got, want := st.Detail, "brd_1"; !strings.Contains(got, want) {
		t.Errorf("Detail = %q, want it to mention board %q", got, want)
	}
	// No write should ever have landed: every degraded tick abandoned
	// evaluation at the unreachable read, before n3 (the sink) ever ran.
	if got := io.WriteCount("led"); got != 0 {
		t.Fatalf("write count = %d, want 0 (tick skipped past the unreachable read)", got)
	}

	// The board comes back: the very next tick should return to running
	// and clear Detail.
	io.SetReadErr("ldr", nil)
	io.SetRead("ldr", FloatValue(200)) // lt 300 -> LED on
	mt.Fire(time.Unix(int64(maxConsecutiveFailures+3), 0))
	st = waitForState(t, mgr, p.ID, StateRunning)
	if st.Detail != "" {
		t.Errorf("Detail = %q after recovery, want empty", st.Detail)
	}
	if v, ok := io.LastWrite("led"); !ok || v.Bool != true {
		t.Fatalf("led write after recovery = %+v, ok=%v, want true", v, ok)
	}
}

// TestDegradedWrite_CountsDroppedAndRecovers covers the write side of
// "Degraded ticks": WriteDevice returning ErrDeviceUnreachable drops that
// tick's write (not queued/retried internally — the next tick's flush just
// tries again since the dedup cache was never updated for a failed write),
// counts it in Status.DroppedWrites, pauses the pipeline, and resumes
// writing normally once the device is reachable again.
func TestDegradedWrite_CountsDroppedAndRecovers(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", BoardID: "brd_1", Role: model.RoleAnalogIn},
		model.Device{ID: "led", BoardID: "brd_2", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:      "degraded-write",
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
	io.SetRead("ldr", FloatValue(200)) // lt 300 -> LED on, every tick
	io.SetWriteErr("led", unreachableErr("led", "brd_2"))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	mt.Fire(time.Unix(1, 0))
	st := waitForTick(t, mgr, p.ID, 1)
	if st.State != StatePaused {
		t.Fatalf("tick 1: State = %q, want %q", st.State, StatePaused)
	}
	if st.DroppedWrites != 1 {
		t.Fatalf("tick 1: DroppedWrites = %d, want 1", st.DroppedWrites)
	}
	if got := io.WriteCount("led"); got != 0 {
		t.Fatalf("tick 1: write count = %d, want 0 (dropped)", got)
	}

	// The dedup cache was never updated for a dropped write, so tick 2
	// retries it — DroppedWrites keeps accumulating (cumulative counter,
	// same pattern as OverrunCount).
	mt.Fire(time.Unix(2, 0))
	st = waitForTick(t, mgr, p.ID, 2)
	if st.DroppedWrites != 2 {
		t.Fatalf("tick 2: DroppedWrites = %d, want 2 (cumulative)", st.DroppedWrites)
	}
	if st.ConsecutiveFailures != 0 {
		t.Fatalf("tick 2: ConsecutiveFailures = %d, want 0", st.ConsecutiveFailures)
	}

	// Device comes back: the write lands, status returns to running.
	io.SetWriteErr("led", nil)
	mt.Fire(time.Unix(3, 0))
	st = waitForState(t, mgr, p.ID, StateRunning)
	if st.DroppedWrites != 2 {
		t.Fatalf("after recovery: DroppedWrites = %d, want 2 (unchanged by a successful write)", st.DroppedWrites)
	}
	if v, ok := io.LastWrite("led"); !ok || v.Bool != true {
		t.Fatalf("led write after recovery = %+v, ok=%v, want true", v, ok)
	}
	if got := io.WriteCount("led"); got != 1 {
		t.Fatalf("write count after recovery = %d, want 1", got)
	}
}

// TestDegradedWrite_UnreachableDeviceDoesNotBlockSiblingWrite exercises the
// partial-writes choice documented on runTick: within one tick, a
// device-write to an unreachable device only drops *that* write — a
// sibling device-write targeting a still-reachable device in the same tick
// still lands. (This is deliberately different from the read case in
// TestDegradedRead_PausesAndRecovers, where an unreachable read abandons
// the rest of the tick's evaluation entirely — see runTick's doc comment
// for why the two phases make different calls.)
func TestDegradedWrite_UnreachableDeviceDoesNotBlockSiblingWrite(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "sensorA", BoardID: "brd_1", Role: model.RoleDigitalIn},
		model.Device{ID: "sensorB", BoardID: "brd_1", Role: model.RoleDigitalIn},
		model.Device{ID: "devA", BoardID: "brd_1", Role: model.RoleDigitalOut}, // unreachable
		model.Device{ID: "devB", BoardID: "brd_1", Role: model.RoleDigitalOut}, // reachable
	)
	p := model.Pipeline{
		ID:      "partial-writes",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "sensorA"}),
			node(t, "n2", "device-write", map[string]any{"deviceId": "devA"}),
			node(t, "n3", "device-read", map[string]any{"deviceId": "sensorB"}),
			node(t, "n4", "device-write", map[string]any{"deviceId": "devB"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.in"),
			edge("n3.out", "n4.in"),
		},
	}

	io := newFakeIO()
	io.SetRead("sensorA", BoolValue(true))
	io.SetRead("sensorB", BoolValue(true))
	io.SetWriteErr("devA", unreachableErr("devA", "brd_1"))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	mt.Fire(time.Unix(1, 0))
	st := waitForTick(t, mgr, p.ID, 1)

	if st.State != StatePaused {
		t.Fatalf("State = %q, want %q", st.State, StatePaused)
	}
	if st.DroppedWrites != 1 {
		t.Fatalf("DroppedWrites = %d, want 1", st.DroppedWrites)
	}
	if got := io.WriteCount("devA"); got != 0 {
		t.Errorf("devA write count = %d, want 0 (unreachable, dropped)", got)
	}
	if v, ok := io.LastWrite("devB"); !ok || v.Bool != true {
		t.Errorf("devB write = %+v, ok=%v, want true (reachable sibling write still lands)", v, ok)
	}
	if got := io.WriteCount("devB"); got != 1 {
		t.Errorf("devB write count = %d, want 1", got)
	}
}

// TestErrNoCachedValue_FirstTickPauses covers the third unreachable
// condition called out in the brief: a pipeline enabled immediately after
// its board connects, before the pin's first Firmata report has arrived,
// must pause rather than error — the very first tick already sees
// ErrDeviceUnreachable, with no prior "good" tick to fall back on.
func TestErrNoCachedValue_FirstTickPauses(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", BoardID: "brd_1", Role: model.RoleAnalogIn},
	)
	p := model.Pipeline{
		ID:      "no-cached-value",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "ldr"}),
		},
	}

	io := newFakeIO()
	io.SetReadErr("ldr", unreachableErr("ldr", "brd_1"))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	mt.Fire(time.Unix(1, 0))
	st := waitForTick(t, mgr, p.ID, 1)
	if st.State != StatePaused {
		t.Fatalf("first tick: State = %q, want %q", st.State, StatePaused)
	}
	if st.ConsecutiveFailures != 0 {
		t.Fatalf("first tick: ConsecutiveFailures = %d, want 0", st.ConsecutiveFailures)
	}
}
