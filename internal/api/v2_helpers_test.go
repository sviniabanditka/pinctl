package api

import (
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
	"github.com/sviniabanditka/pinctl/internal/store"
)

// mustDecode decodes rec's JSON body into v, failing the test on error.
func mustDecode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
}

// ---- fake engine.DeviceIO, same shape as internal/engine's test fake,
// duplicated here since it's unexported there (docs/13-testing.md's
// fake-board pattern applied one layer up, at the DeviceIO boundary). ----

type fakeDeviceIO struct {
	mu         sync.Mutex
	reads      map[string]engine.Value
	writes     map[string]engine.Value
	writeCount map[string]int
}

func newFakeDeviceIO() *fakeDeviceIO {
	return &fakeDeviceIO{
		reads:      map[string]engine.Value{},
		writes:     map[string]engine.Value{},
		writeCount: map[string]int{},
	}
}

func (f *fakeDeviceIO) SetRead(deviceID string, v engine.Value) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[deviceID] = v
}

func (f *fakeDeviceIO) ReadDevice(d model.Device) (engine.Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[d.ID], nil
}

func (f *fakeDeviceIO) WriteDevice(d model.Device, v engine.Value) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[d.ID] = v
	f.writeCount[d.ID]++
	return nil
}

func (f *fakeDeviceIO) LastWrite(deviceID string) (engine.Value, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.writes[deviceID]
	return v, ok
}

// ---- fake Clock / manually-driven Ticker, same pattern as
// internal/engine's test helpers, needed here to drive Manager.Start'ed
// runners deterministically from an httptest-driven API test. ----

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

type manualTicker struct {
	ch chan time.Time
}

func (m *manualTicker) C() <-chan time.Time { return m.ch }
func (m *manualTicker) Stop()               {}

func (m *manualTicker) Fire(t time.Time) { m.ch <- t }

func manualTickerFactory() (engine.TickerFactory, <-chan *manualTicker) {
	out := make(chan *manualTicker, 8)
	return func(time.Duration) engine.Ticker {
		mt := &manualTicker{ch: make(chan time.Time)}
		out <- mt
		return mt
	}, out
}

// waitForTick polls mgr.Status until pipeline id has processed tick >=
// want, or panics after a timeout (mirrors internal/engine's test helper,
// which is unexported there).
func waitForTick(mgr *engine.Manager, id string, want uint64, timeout time.Duration) (engine.Status, bool) {
	deadline := time.Now().Add(timeout)
	for {
		st, ok := mgr.Status(id)
		if ok && st.Tick >= want {
			return st, true
		}
		if time.Now().After(deadline) {
			return st, false
		}
		time.Sleep(time.Millisecond)
	}
}

// newTestStore opens a store rooted at t.TempDir().
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st
}

// newTestServerWithBoards builds a Server (single-session fakeSession, no
// board.Manager — boardMgr stays nil) whose store already has one board
// registered per name in names, per the v0.4 device-create boardId
// existence check (internal/api/devices.go: "once a real boards collection
// exists, an unknown boardId is a 400"). Returns the boards in the same
// order as names, with their store-assigned IDs.
func newTestServerWithBoards(t *testing.T, names ...string) (*Server, []model.Board) {
	t.Helper()
	st := newTestStore(t)
	fs := newFakeSession()
	mgr := engine.NewManager()
	t.Cleanup(mgr.StopAll)
	io := newFakeDeviceIO()
	srv := NewServer(fs, "/dev/ttyACM0", st, mgr, io)

	boards := make([]model.Board, len(names))
	for i, name := range names {
		b, err := st.CreateBoard(model.Board{Name: name, Identity: model.Identity{Kind: "port-path", Key: "/dev/fake" + name}})
		if err != nil {
			t.Fatalf("CreateBoard(%s): %v", name, err)
		}
		boards[i] = b
	}
	return srv, boards
}
