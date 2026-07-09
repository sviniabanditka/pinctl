package devicebus

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// fakeReader is a controllable Reader: SetRead installs the value the next
// poll should observe for a device; ReadDevice returns errNotFound-like
// behaviour (zero value) for anything not set, matching how a real
// engine.DeviceIO fake behaves in this codebase's tests.
type fakeReader struct {
	mu    sync.Mutex
	vals  map[string]engine.Value
	err   map[string]bool
	reads int
}

func newFakeReader() *fakeReader {
	return &fakeReader{vals: map[string]engine.Value{}, err: map[string]bool{}}
}

func (f *fakeReader) SetRead(id string, v engine.Value) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[id] = v
}

func (f *fakeReader) SetErr(id string, fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err[id] = fail
}

func (f *fakeReader) ReadDevice(d model.Device) (engine.Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err[d.ID] {
		return engine.Value{}, errBoardOffline
	}
	return f.vals[d.ID], nil
}

type staticErr string

func (e staticErr) Error() string { return string(e) }

const errBoardOffline = staticErr("board offline")

// fakeLister is a fixed device list.
type fakeLister struct {
	devices []model.Device
}

func (f *fakeLister) ListDevices() []model.Device { return f.devices }

func testDevice(id string, role model.DeviceRole) model.Device {
	return model.Device{ID: id, BoardID: "brd_local", Pin: 1, Role: role}
}

// waitFor polls cond until it's true or fails the test after timeout.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBus_TwoSubscribersBothSeeUpdate(t *testing.T) {
	r := newFakeReader()
	l := &fakeLister{devices: []model.Device{testDevice("dev_1", model.RoleAnalogIn)}}
	b := New(r, l, 2*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	ch1, unsub1 := b.Subscribe()
	defer unsub1()
	ch2, unsub2 := b.Subscribe()
	defer unsub2()

	r.SetRead("dev_1", engine.FloatValue(42))

	u1 := requireUpdate(t, ch1, time.Second)
	u2 := requireUpdate(t, ch2, time.Second)

	if u1.Device.ID != "dev_1" || !u1.Value.Equal(engine.FloatValue(42)) {
		t.Errorf("sub1 update = %+v, want dev_1=42", u1)
	}
	if u2.Device.ID != "dev_1" || !u2.Value.Equal(engine.FloatValue(42)) {
		t.Errorf("sub2 update = %+v, want dev_1=42", u2)
	}
}

func requireUpdate(t *testing.T, ch <-chan Update, timeout time.Duration) Update {
	t.Helper()
	select {
	case u, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed before an update arrived")
		}
		return u
	case <-time.After(timeout):
		t.Fatalf("no update within %s", timeout)
	}
	return Update{}
}

func TestBus_UnsubscribeStopsDeliveryAndIsSafeTwice(t *testing.T) {
	r := newFakeReader()
	l := &fakeLister{devices: []model.Device{testDevice("dev_1", model.RoleAnalogIn)}}
	b := New(r, l, 2*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	ch, unsub := b.Subscribe()

	r.SetRead("dev_1", engine.FloatValue(1))
	requireUpdate(t, ch, time.Second)

	unsub()
	unsub() // must not panic

	// Channel must be closed, and no further sends should arrive (a send
	// on a closed channel would panic the bus's poll goroutine, which
	// would surface as a test failure via -race/panic).
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("expected channel closed, got a value")
		}
	case <-time.After(time.Second):
		t.Fatalf("channel was not closed by unsubscribe")
	}

	r.SetRead("dev_1", engine.FloatValue(2))
	time.Sleep(20 * time.Millisecond) // let a few more polls happen; must not panic
}

func TestBus_NoChangeNoUpdate(t *testing.T) {
	r := newFakeReader()
	l := &fakeLister{devices: []model.Device{testDevice("dev_1", model.RoleAnalogIn)}}
	b := New(r, l, 2*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	r.SetRead("dev_1", engine.FloatValue(7))

	ch, unsub := b.Subscribe()
	defer unsub()

	// First update is expected (first-ever observation).
	requireUpdate(t, ch, time.Second)

	// Value stays the same for many polls: no further updates should
	// arrive.
	select {
	case u := <-ch:
		t.Fatalf("unexpected update for unchanged value: %+v", u)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestBus_FullSubscriberBufferDropsRatherThanBlocks(t *testing.T) {
	r := newFakeReader()
	l := &fakeLister{devices: []model.Device{testDevice("dev_1", model.RoleAnalogIn)}}
	b := New(r, l, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe but never drain: force many more distinct values than the
	// buffer holds so the bus must drop instead of blocking the poll loop.
	_, unsub := b.Subscribe()
	defer unsub()

	go b.Run(ctx)

	for i := 0; i < subscriberBufSize*8; i++ {
		r.SetRead("dev_1", engine.FloatValue(float64(i)))
		time.Sleep(500 * time.Microsecond)
	}

	// The poll loop must still be alive and responsive (not blocked on a
	// full subscriber channel) — prove it by observing the read count kept
	// climbing throughout, and that Dropped() is now > 0.
	waitFor(t, 5*time.Second, func() bool { return b.Dropped() > 0 })

	r.mu.Lock()
	reads := r.reads
	r.mu.Unlock()
	if reads < subscriberBufSize {
		t.Errorf("reads = %d, want the poll loop to have kept running (>= %d)", reads, subscriberBufSize)
	}
}

func TestBus_RunClosesChannelsOnCancelAndSubscribeAfterIsSane(t *testing.T) {
	r := newFakeReader()
	l := &fakeLister{devices: []model.Device{testDevice("dev_1", model.RoleAnalogIn)}}
	b := New(r, l, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	ch, unsub := b.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Run did not return after ctx cancel")
	}

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("expected channel closed after Run returned")
		}
	case <-time.After(time.Second):
		t.Fatalf("channel was not closed when Run returned")
	}

	// Subscribe after Run has returned must behave sanely: an
	// already-closed channel and a no-op unsubscribe, not a subscriber
	// that's registered but never serviced.
	ch2, unsub2 := b.Subscribe()
	select {
	case _, ok := <-ch2:
		if ok {
			t.Fatalf("expected an already-closed channel from post-Run Subscribe")
		}
	default:
		t.Fatalf("expected post-Run Subscribe's channel to be immediately closed/ready, not empty-and-open")
	}
	unsub2()
	unsub2() // safe to call twice
}

// ---- NoteWrite / WrapWriter ----

// fakeIO is a controllable engine.DeviceIO: reads are canned like
// fakeReader, writes are recorded (and can be made to fail) so WrapWriter's
// only-on-success behaviour is directly observable.
type fakeIO struct {
	mu       sync.Mutex
	reads    map[string]engine.Value
	writes   []engine.Value
	attempts int
	failErr  error
}

func newFakeIO() *fakeIO {
	return &fakeIO{reads: map[string]engine.Value{}}
}

func (f *fakeIO) ReadDevice(d model.Device) (engine.Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[d.ID], nil
}

func (f *fakeIO) WriteDevice(d model.Device, v engine.Value) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.failErr != nil {
		return f.failErr
	}
	f.writes = append(f.writes, v)
	return nil
}

func (f *fakeIO) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

func TestBus_NoteWriteFansOutAndDedups(t *testing.T) {
	l := &fakeLister{}
	b := New(newFakeIO(), l, time.Hour) // interval irrelevant: Run's poll loop is not exercised here

	ch1, unsub1 := b.Subscribe()
	defer unsub1()
	ch2, unsub2 := b.Subscribe()
	defer unsub2()

	d := testDevice("dev_out", model.RoleDigitalOut)

	b.NoteWrite(d, engine.BoolValue(true))

	u1 := requireUpdate(t, ch1, time.Second)
	u2 := requireUpdate(t, ch2, time.Second)
	if u1.Device.ID != "dev_out" || !u1.Value.Equal(engine.BoolValue(true)) {
		t.Errorf("sub1 update = %+v, want dev_out=true", u1)
	}
	if u2.Device.ID != "dev_out" || !u2.Value.Equal(engine.BoolValue(true)) {
		t.Errorf("sub2 update = %+v, want dev_out=true", u2)
	}

	// Same value again: no further Update (same change-detection as poll).
	b.NoteWrite(d, engine.BoolValue(true))
	select {
	case u := <-ch1:
		t.Fatalf("unexpected update for unchanged NoteWrite value: %+v", u)
	case <-time.After(30 * time.Millisecond):
	}

	// Snapshot reflects the last noted value.
	snap := b.Snapshot()
	if got, ok := snap["dev_out"]; !ok || !got.Value.Equal(engine.BoolValue(true)) {
		t.Errorf("Snapshot()[dev_out] = %+v, ok=%v, want true", got, ok)
	}

	// A genuinely different value does publish.
	b.NoteWrite(d, engine.BoolValue(false))
	u1 = requireUpdate(t, ch1, time.Second)
	if !u1.Value.Equal(engine.BoolValue(false)) {
		t.Errorf("update after change = %+v, want false", u1)
	}
}

func TestWrapWriter_OnlyNotesOnSuccess(t *testing.T) {
	l := &fakeLister{}
	b := New(newFakeIO(), l, time.Hour)
	io := newFakeIO()
	wrapped := WrapWriter(io, b)

	d := testDevice("dev_out", model.RoleDigitalOut)

	io.failErr = errBoardOffline
	if err := wrapped.WriteDevice(d, engine.BoolValue(true)); err == nil {
		t.Fatal("expected the underlying write's error to propagate")
	}
	if _, ok := b.Snapshot()["dev_out"]; ok {
		t.Fatal("a failing WriteDevice must not have called NoteWrite")
	}

	io.failErr = nil
	if err := wrapped.WriteDevice(d, engine.BoolValue(true)); err != nil {
		t.Fatalf("WriteDevice: %v", err)
	}
	snap, ok := b.Snapshot()["dev_out"]
	if !ok || !snap.Value.Equal(engine.BoolValue(true)) {
		t.Fatalf("a successful WriteDevice must have called NoteWrite; snapshot = %+v, ok=%v", snap, ok)
	}
	if io.attemptCount() != 2 {
		t.Fatalf("underlying write attempts = %d, want 2 (WrapWriter must call through on every attempt)", io.attemptCount())
	}
}

// ---- End-to-end: engine.Manager driving a wrapped DeviceIO ----

// manualTicker/manualTickerFactory give the end-to-end test a
// deterministically-fired tick instead of racing real wall-clock time, per
// docs/07-pipeline-engine.md "Determinism and testing hooks".
type manualTicker struct {
	ch chan time.Time
}

func (m *manualTicker) C() <-chan time.Time { return m.ch }
func (m *manualTicker) Stop()               {}

func manualTickerFactory() (engine.TickerFactory, <-chan *manualTicker) {
	tickers := make(chan *manualTicker, 8)
	return func(time.Duration) engine.Ticker {
		mt := &manualTicker{ch: make(chan time.Time, 1)}
		tickers <- mt
		return mt
	}, tickers
}

// TestWrapWriter_EngineDrivenWriteReachesBusSubscriber drives a real
// engine.Manager pipeline (constant true -> device-write) through a
// WrapWriter-wrapped DeviceIO and asserts a bus subscriber sees the
// resulting Update — proving the seam this package adds closes the gap
// described in devicebus.go's package doc: an engine-driven output write
// becomes observable outside the engine, exactly like a polled input.
func TestWrapWriter_EngineDrivenWriteReachesBusSubscriber(t *testing.T) {
	led := model.Device{ID: "led", BoardID: "brd_local", Pin: 3, Role: model.RoleDigitalOut}
	devices := map[string]model.Device{"led": led}

	valueCfg, err := json.Marshal(map[string]any{"kind": "bool", "value": true})
	if err != nil {
		t.Fatalf("marshal constant config: %v", err)
	}
	writeCfg, err := json.Marshal(map[string]any{"deviceId": "led"})
	if err != nil {
		t.Fatalf("marshal device-write config: %v", err)
	}
	p := model.Pipeline{
		ID:      "p_notewrite",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			{ID: "n1", Type: "constant", Config: valueCfg},
			{ID: "n2", Type: "device-write", Config: writeCfg},
		},
		Edges: []model.Edge{{From: "n1.out", To: "n2.in"}},
	}

	io := newFakeIO()
	l := &fakeLister{devices: []model.Device{led}}
	b := New(io, l, time.Hour) // poll loop irrelevant: only readable roles are polled, led is not
	wrapped := WrapWriter(io, b)

	ch, unsub := b.Subscribe()
	defer unsub()

	tf, tickers := manualTickerFactory()
	mgr := engine.NewManager(engine.WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	if err := mgr.Start(p, devices, wrapped); err != nil {
		t.Fatalf("mgr.Start: %v", err)
	}

	var mt *manualTicker
	select {
	case mt = <-tickers:
	case <-time.After(time.Second):
		t.Fatal("runner never created a ticker")
	}
	mt.ch <- time.Now()

	u := requireUpdate(t, ch, 2*time.Second)
	if u.Device.ID != "led" || !u.Value.Equal(engine.BoolValue(true)) {
		t.Fatalf("bus update = %+v, want led=true", u)
	}
}
