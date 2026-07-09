package mqtt

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// ---- fakeClient ----

type publishedMsg struct {
	topic    string
	qos      byte
	retained bool
	payload  []byte
}

// fakeClient is the Client fake every test in this package publishes
// through instead of a real broker, per the task brief: "Do not test
// against paho or a real broker."
type fakeClient struct {
	mu           sync.Mutex
	published    []publishedMsg
	subFilter    string
	subCB        func(topic string, payload []byte)
	disconnected bool
	publishErr   error
}

func (f *fakeClient) Publish(topic string, qos byte, retained bool, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := append([]byte(nil), payload...)
	f.published = append(f.published, publishedMsg{topic, qos, retained, cp})
	return f.publishErr
}

func (f *fakeClient) Subscribe(topic string, qos byte, cb func(topic string, payload []byte)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subFilter = topic
	f.subCB = cb
	return nil
}

func (f *fakeClient) Disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnected = true
}

// deliver simulates the broker routing an inbound message matching a
// single-level "+" wildcard subscription to the callback Subscribe was
// given.
func (f *fakeClient) deliver(topic string, payload []byte) {
	f.mu.Lock()
	filter, cb := f.subFilter, f.subCB
	f.mu.Unlock()
	if cb == nil || !topicMatchesFilter(filter, topic) {
		return
	}
	cb(topic, payload)
}

func topicMatchesFilter(filter, topic string) bool {
	fp := strings.Split(filter, "/")
	tp := strings.Split(topic, "/")
	if len(fp) != len(tp) {
		return false
	}
	for i, seg := range fp {
		if seg == "+" {
			continue
		}
		if seg != tp[i] {
			return false
		}
	}
	return true
}

func (f *fakeClient) snapshot() []publishedMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]publishedMsg, len(f.published))
	copy(out, f.published)
	return out
}

func (f *fakeClient) isDisconnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.disconnected
}

// find returns the last published message to topic, if any.
func (f *fakeClient) find(topic string) (publishedMsg, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var last publishedMsg
	found := false
	for _, m := range f.published {
		if m.topic == topic {
			last, found = m, true
		}
	}
	return last, found
}

func (f *fakeClient) countTopic(topic string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.published {
		if m.topic == topic {
			n++
		}
	}
	return n
}

// ---- fakeStore ----

type fakeStore struct {
	mu        sync.Mutex
	devices   map[string]model.Device
	boards    map[string]model.Board
	conflicts map[string]conflictInfo
}

type conflictInfo struct {
	id, name string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		devices:   make(map[string]model.Device),
		boards:    make(map[string]model.Board),
		conflicts: make(map[string]conflictInfo),
	}
}

func (s *fakeStore) ListDevices() []model.Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, d)
	}
	return out
}

func (s *fakeStore) GetDevice(id string) (model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[id]
	if !ok {
		return model.Device{}, fmt.Errorf("fakeStore: device %s not found", id)
	}
	return d, nil
}

func (s *fakeStore) ListBoards() []model.Board {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Board, 0, len(s.boards))
	for _, b := range s.boards {
		out = append(out, b)
	}
	return out
}

func (s *fakeStore) GetBoard(id string) (model.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[id]
	if !ok {
		return model.Board{}, fmt.Errorf("fakeStore: board %s not found", id)
	}
	return b, nil
}

func (s *fakeStore) EnabledPipelineWritingDevice(deviceID string) (id, name string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, found := s.conflicts[deviceID]
	if !found {
		return "", "", false
	}
	return c.id, c.name, true
}

func (s *fakeStore) putDevice(d model.Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[d.ID] = d
}

func (s *fakeStore) deleteDevice(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.devices, id)
}

func (s *fakeStore) putBoard(b model.Board) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.boards[b.ID] = b
}

func (s *fakeStore) setConflict(deviceID, pipelineID, pipelineName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conflicts[deviceID] = conflictInfo{pipelineID, pipelineName}
}

// ---- fakeBus ----

type fakeBus struct {
	mu       sync.Mutex
	ch       chan devicebus.Update
	snapshot map[string]devicebus.Update
}

func newFakeBus() *fakeBus {
	return &fakeBus{
		ch:       make(chan devicebus.Update, 16),
		snapshot: make(map[string]devicebus.Update),
	}
}

func (b *fakeBus) Subscribe() (<-chan devicebus.Update, func()) {
	return b.ch, func() {}
}

func (b *fakeBus) Snapshot() map[string]devicebus.Update {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]devicebus.Update, len(b.snapshot))
	for k, v := range b.snapshot {
		out[k] = v
	}
	return out
}

func (b *fakeBus) setSnapshot(u devicebus.Update) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snapshot[u.Device.ID] = u
}

func (b *fakeBus) send(u devicebus.Update) {
	b.mu.Lock()
	b.snapshot[u.Device.ID] = u
	b.mu.Unlock()
	b.ch <- u
}

// ---- fakeIO ----

// fakeIO is the mqtt package's fake engine.DeviceIO. Its optional bus field
// mirrors devicebus.WrapWriter for tests that exercise the full
// command -> write -> devicebus -> Bridge.Run -> publish path
// (newHarness wires h.io.bus = h.bus): in production, Deps.IO is always a
// WrapWriter-wrapped DeviceIO, so a successful write always lands back on
// the bus the bridge itself subscribes to.
type fakeIO struct {
	mu     sync.Mutex
	writes map[string][]engine.Value
	err    error
	bus    *fakeBus
}

func newFakeIO() *fakeIO {
	return &fakeIO{writes: make(map[string][]engine.Value)}
}

func (f *fakeIO) ReadDevice(d model.Device) (engine.Value, error) { return engine.Value{}, nil }

func (f *fakeIO) WriteDevice(d model.Device, v engine.Value) error {
	f.mu.Lock()
	if f.err != nil {
		err := f.err
		f.mu.Unlock()
		return err
	}
	f.writes[d.ID] = append(f.writes[d.ID], v)
	bus := f.bus
	f.mu.Unlock()

	if bus != nil {
		bus.send(devicebus.Update{Device: d, Value: v, At: time.Now()})
	}
	return nil
}

func (f *fakeIO) writesFor(id string) []engine.Value {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]engine.Value, len(f.writes[id]))
	copy(out, f.writes[id])
	return out
}

// ---- fakeBoardStatus ----

type fakeBoardStatus struct {
	mu        sync.Mutex
	connected map[string]bool
	firmware  map[string]string
}

func newFakeBoardStatus() *fakeBoardStatus {
	return &fakeBoardStatus{connected: make(map[string]bool), firmware: make(map[string]string)}
}

func (f *fakeBoardStatus) Connected(boardID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected[boardID]
}

func (f *fakeBoardStatus) Firmware(boardID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.firmware[boardID]
	return name, ok
}

func (f *fakeBoardStatus) setConnected(boardID string, connected bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected[boardID] = connected
}

func (f *fakeBoardStatus) setFirmware(boardID, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.firmware[boardID] = name
}

// ---- fakeClock ----

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
