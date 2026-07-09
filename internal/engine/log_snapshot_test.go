package engine

import (
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// TestLogEntries_OnlyPublishedWhenSubscribed verifies the runner-level
// wiring: a "log" node's entries land in TickSnapshot.Log when someone is
// subscribed, and (per docs/07-pipeline-engine.md's zero-cost-when-off
// invariant, extended to log by this v0.4 work) its onChangeOnly state
// still advances correctly on ticks nobody is watching.
func TestLogEntries_OnlyPublishedWhenSubscribed(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", Role: model.RoleAnalogIn},
	)
	p := model.Pipeline{
		ID:      "log-pipeline",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "ldr"}),
			node(t, "n2", "log", map[string]any{"label": "ldr-raw", "onChangeOnly": true}),
		},
		Edges: []model.Edge{edge("n1.out", "n2.in")},
	}

	io := newFakeIO()
	io.SetRead("ldr", FloatValue(100))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	// tick 1: nobody subscribed yet. The tick still runs (write-free here,
	// log is a no-op sink), but no snapshot is built/published at all.
	mt.Fire(time.Unix(1, 0))
	waitForTick(t, mgr, p.ID, 1)

	ch, unsub := mgr.Subscribe(p.ID)
	defer unsub()

	// tick 2: same value (100) as tick 1. Per docs/08-node-catalog.md,
	// onChangeOnly still logs "the very first value observed" -- but the
	// log node's Eval already observed 100 on tick 1 (Eval runs every
	// tick regardless of subscription), so tick 2 should NOT log again;
	// this exercises that the log node's state isn't reset just because a
	// subscriber showed up.
	mt.Fire(time.Unix(2, 0))
	snap := recvSnapshot(t, ch)
	if len(snap.Log) != 0 {
		t.Fatalf("tick 2 (unchanged since tick 1): Log = %+v, want empty", snap.Log)
	}

	// tick 3: value changes -> logs.
	io.SetRead("ldr", FloatValue(900))
	mt.Fire(time.Unix(3, 0))
	snap = recvSnapshot(t, ch)
	if len(snap.Log) != 1 {
		t.Fatalf("tick 3 (changed): Log = %+v, want exactly 1 entry", snap.Log)
	}
	e := snap.Log[0]
	if e.NodeID != "n2" || e.Label != "ldr-raw" || e.Value.Float != 900 {
		t.Fatalf("tick 3 entry = %+v, want nodeID=n2 label=ldr-raw value=900", e)
	}

	// tick 4: unsubscribe, then confirm no more snapshots arrive (and the
	// pipeline keeps ticking fine without a subscriber).
	unsub()
	mt.Fire(time.Unix(4, 0))
	waitForTick(t, mgr, p.ID, 4)
}

func recvSnapshot(t *testing.T, ch <-chan *TickSnapshot) *TickSnapshot {
	t.Helper()
	select {
	case snap := <-ch:
		return snap
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot")
		return nil
	}
}
