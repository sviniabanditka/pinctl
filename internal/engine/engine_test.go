package engine

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// ---- test-only extra node type, registered for structural tests that
// need a node whose input and output share a type (none of the v0.2
// catalog nodes do: device-read/constant have no input, device-write has
// no output, compare's input is float64 but its output is bool). Without
// this, a graph cycle is structurally impossible to construct out of the
// real v0.2 node set, since edge type-checking alone would reject it
// before the cycle check ever runs.

func init() {
	register(passthroughType{})
}

type passthroughType struct{}

func (passthroughType) Name() string { return "test-passthrough" }

func (passthroughType) Ports(json.RawMessage, map[string]model.Device) (PortSpec, error) {
	return PortSpec{
		Inputs:  []Port{{Name: "in", Type: TypeFloat64, Required: true}},
		Outputs: []Port{{Name: "out", Type: TypeFloat64}},
	}, nil
}

func (passthroughType) NewInstance(json.RawMessage, NodeDeps) (Node, error) {
	return &passthroughNode{}, nil
}

type passthroughNode struct{}

func (*passthroughNode) Init(Clock) error { return nil }
func (*passthroughNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	return []Value{in[0]}, nil
}
func (*passthroughNode) Close() error { return nil }

// ---- fake DeviceIO ----

type fakeIO struct {
	mu         sync.Mutex
	reads      map[string]Value
	readErr    map[string]error
	writes     map[string]Value
	writeCount map[string]int
}

func newFakeIO() *fakeIO {
	return &fakeIO{
		reads:      map[string]Value{},
		readErr:    map[string]error{},
		writes:     map[string]Value{},
		writeCount: map[string]int{},
	}
}

func (f *fakeIO) SetRead(deviceID string, v Value) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[deviceID] = v
}

func (f *fakeIO) SetReadErr(deviceID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readErr[deviceID] = err
}

func (f *fakeIO) ReadDevice(d model.Device) (Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.readErr[d.ID]; err != nil {
		return Value{}, err
	}
	return f.reads[d.ID], nil
}

func (f *fakeIO) WriteDevice(d model.Device, v Value) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[d.ID] = v
	f.writeCount[d.ID]++
	return nil
}

func (f *fakeIO) LastWrite(deviceID string) (Value, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.writes[deviceID]
	return v, ok
}

func (f *fakeIO) WriteCount(deviceID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writeCount[deviceID]
}

// ---- fake Clock ----

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// ---- fake Ticker, manually fired by tests ----

type manualTicker struct {
	ch chan time.Time
}

func newManualTicker() *manualTicker { return &manualTicker{ch: make(chan time.Time)} }

func (m *manualTicker) C() <-chan time.Time { return m.ch }
func (m *manualTicker) Stop()               {}

// Fire sends t on the ticker's channel, blocking until the scheduler
// goroutine receives it (i.e. starts processing the tick).
func (m *manualTicker) Fire(t time.Time) { m.ch <- t }

// manualTickerFactory hands out manualTickers and publishes each one on a
// channel the test reads from, so the test can drive exactly the runner(s)
// it started.
func manualTickerFactory() (TickerFactory, <-chan *manualTicker) {
	out := make(chan *manualTicker, 8)
	return func(time.Duration) Ticker {
		mt := newManualTicker()
		out <- mt
		return mt
	}, out
}

// waitForTick polls Status until pipeline id has processed tick >= want,
// or fails the test after a generous timeout. This is synchronization
// (waiting for a goroutine we just unblocked to finish its work), not a
// simulation of tick timing — tick timing itself is fully controlled by
// manualTicker.Fire.
func waitForTick(t *testing.T, mgr *Manager, id string, want uint64) Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, ok := mgr.Status(id)
		if ok && st.Tick >= want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipeline %s did not reach tick %d in time (status=%+v, ok=%v)", id, want, st, ok)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitForState polls Status until pipeline id reaches want state, or fails
// after a timeout. Same rationale as waitForTick.
func waitForState(t *testing.T, mgr *Manager, id string, want State) Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, ok := mgr.Status(id)
		if ok && st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipeline %s did not reach state %s in time (status=%+v, ok=%v)", id, want, st, ok)
		}
		time.Sleep(time.Millisecond)
	}
}

func rawJSON(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return b
}

func mustNotErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func devicesByID(devs ...model.Device) map[string]model.Device {
	m := make(map[string]model.Device, len(devs))
	for _, d := range devs {
		m[d.ID] = d
	}
	return m
}

func edge(from, to string) model.Edge { return model.Edge{From: from, To: to} }

func node(t *testing.T, id, typ string, cfg map[string]any) model.Node {
	return model.Node{ID: id, Type: typ, Config: rawJSON(t, cfg)}
}
