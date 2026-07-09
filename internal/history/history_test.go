package history

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// testCfg is a history config tuned for fast, deterministic tests: no
// deadband/interval suppression by default, so every distinct Update is
// recorded, and a short flush interval so batching tests don't need to
// wait long.
func testCfg() config.History {
	return config.History{
		Enabled:       true,
		Retention:     config.Duration(168 * time.Hour),
		Deadband:      0,
		MinInterval:   0,
		MaxInterval:   config.Duration(time.Hour),
		FlushInterval: config.Duration(20 * time.Millisecond),
	}
}

// openTestStore opens a fresh Store backed by a temp-file database (not
// :memory:, per the task's "a real temp-file DB" requirement for the
// record->flush->query round trip).
func openTestStore(t *testing.T, cfg config.History) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := Open(path, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// fakeBus is a history.Bus whose single subscriber channel the test
// controls directly.
type fakeBus struct {
	ch chan devicebus.Update
}

func newFakeBus() *fakeBus {
	return &fakeBus{ch: make(chan devicebus.Update, 256)}
}

func (b *fakeBus) Subscribe() (<-chan devicebus.Update, func()) {
	return b.ch, func() {}
}

// fakeDevices is a DeviceLister backed by an in-memory set, safe for
// concurrent use since Record's goroutine calls GetDevice concurrently
// with a test goroutine mutating it (device-deleted-mid-stream tests).
type fakeDevices struct {
	mu      sync.Mutex
	devices map[string]model.Device
}

func newFakeDevices(devices ...model.Device) *fakeDevices {
	f := &fakeDevices{devices: make(map[string]model.Device)}
	for _, d := range devices {
		f.devices[d.ID] = d
	}
	return f
}

func (f *fakeDevices) GetDevice(id string) (model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devices[id]
	if !ok {
		return model.Device{}, fmt.Errorf("no such device %s", id)
	}
	return d, nil
}

func (f *fakeDevices) delete(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.devices, id)
}

func testDevice(id string, role model.DeviceRole) model.Device {
	return model.Device{ID: id, Name: id, BoardID: "brd_1", Pin: 1, Role: role}
}

func update(d model.Device, v engine.Value, at time.Time) devicebus.Update {
	return devicebus.Update{Device: d, Value: v, At: at}
}

// countRows returns the number of samples rows currently in s's database
// for deviceID, bypassing Query's limit/windowing — used to observe raw
// flush behaviour directly.
func countRows(t *testing.T, s *Store, deviceID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM samples WHERE device_id = ?`, deviceID).Scan(&n); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	return n
}

// waitFor polls cond every 5ms until it returns true or timeout elapses,
// failing the test on timeout.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
