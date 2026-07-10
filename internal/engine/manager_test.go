package engine

import (
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// TestStopPrunesHubClosesSubscribers verifies the fix for the snapshotHub
// leak: Manager.Remove(id) (the pipeline-deletion path, unlike Stop which
// must preserve the hub across mere disable/hot-swap per the hubs field
// doc) removes id's entry from Manager.hubs and closes every live
// subscriber channel, so a relay loop reading from it (like
// internal/api/ws.go's relaySnapshots) sees the channel close and can exit
// cleanly instead of blocking forever.
func TestStopPrunesHubClosesSubscribers(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", Role: model.RoleAnalogIn},
		model.Device{ID: "led", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:      "prune-me",
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
	io.SetRead("ldr", FloatValue(200))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	ch, unsub := mgr.Subscribe(p.ID)
	defer unsub()

	mt.Fire(time.Unix(1, 0))
	waitForTick(t, mgr, p.ID, 1)

	select {
	case snap := <-ch:
		if snap.PipelineID != p.ID {
			t.Fatalf("snapshot pipelineID = %q, want %q", snap.PipelineID, p.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first snapshot")
	}

	// A relay goroutine forwarding ch, exactly like
	// internal/api/ws.go's relaySnapshots.
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		for range ch {
		}
	}()

	mgr.mu.Lock()
	_, hubPresent := mgr.hubs[p.ID]
	mgr.mu.Unlock()
	if !hubPresent {
		t.Fatal("hub entry missing before Remove")
	}

	mgr.Remove(p.ID)

	mgr.mu.Lock()
	_, hubStillThere := mgr.hubs[p.ID]
	mgr.mu.Unlock()
	if hubStillThere {
		t.Fatal("hub entry still present in Manager.hubs after Remove")
	}

	select {
	case <-relayDone:
	case <-time.After(2 * time.Second):
		t.Fatal("relay goroutine did not exit after Remove closed the hub")
	}

	if _, ok := mgr.Status(p.ID); ok {
		t.Fatal("status still present after Remove")
	}
}

// TestManager_DeviceWrite_Servo runs a full constant -> device-write
// pipeline against a servo device end to end (through Manager, not just
// Validate), confirming the write actually reaches DeviceIO.WriteDevice
// with the constant's float64 value.
func TestManager_DeviceWrite_Servo(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "arm", Role: model.RoleServo},
	)
	p := model.Pipeline{
		ID:      "servo-write",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "constant", map[string]any{"kind": "float", "value": 90}),
			node(t, "n2", "device-write", map[string]any{"deviceId": "arm"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.in"),
		},
	}

	io := newFakeIO()
	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	mt.Fire(time.Unix(1, 0))
	waitForTick(t, mgr, p.ID, 1)

	io.mu.Lock()
	v, ok := io.writes["arm"]
	io.mu.Unlock()
	if !ok {
		t.Fatal("expected a write to device arm")
	}
	if v.Type != TypeFloat64 || v.Float != 90 {
		t.Errorf("write = %+v, want float64 90", v)
	}
}

// TestStopPreservesHubAcrossDisable verifies Stop (unlike Remove) leaves
// the hub in place, since Stop is also what disabling a pipeline uses
// (internal/api/pipelines.go's handleDisablePipeline) and a WS subscriber
// must not lose its subscription across an enable/disable cycle.
func TestStopPreservesHubAcrossDisable(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "ldr", Role: model.RoleAnalogIn},
		model.Device{ID: "led", Role: model.RoleDigitalOut},
	)
	p := model.Pipeline{
		ID:      "disable-me",
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
	io.SetRead("ldr", FloatValue(200))

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	<-tickers

	_, unsub := mgr.Subscribe(p.ID)
	defer unsub()

	mgr.Stop(p.ID) // disable path: runner stops, hub must survive

	mgr.mu.Lock()
	_, hubThere := mgr.hubs[p.ID]
	mgr.mu.Unlock()
	if !hubThere {
		t.Fatal("hub entry pruned by Stop; Stop must preserve it across disable")
	}
}
