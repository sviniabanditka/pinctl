package mqtt

import (
	"context"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// testHarness bundles a Bridge with its fakes for the common case.
type testHarness struct {
	bridge *Bridge
	client *fakeClient
	store  *fakeStore
	bus    *fakeBus
	io     *fakeIO
	boards *fakeBoardStatus
	clock  *fakeClock
}

func newHarness(t *testing.T, cfg config.MQTT) *testHarness {
	t.Helper()
	h := &testHarness{
		client: &fakeClient{},
		store:  newFakeStore(),
		bus:    newFakeBus(),
		io:     newFakeIO(),
		boards: newFakeBoardStatus(),
		clock:  newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	}
	if cfg.Broker == "" {
		cfg.Broker = "tcp://broker:1883"
	}
	if cfg.ClientID == "" {
		cfg.ClientID = "pinctl-test"
	}
	if cfg.BaseTopic == "" {
		cfg.BaseTopic = "pinctl"
	}
	if cfg.MinPublishInterval == 0 {
		cfg.MinPublishInterval = config.Duration(time.Second)
	}
	if cfg.MaxPublishInterval == 0 {
		cfg.MaxPublishInterval = config.Duration(60 * time.Second)
	}

	deps := Deps{
		Store:  h.store,
		Bus:    h.bus,
		IO:     h.io,
		Boards: h.boards,
		Clock:  h.clock,
		NewClient: func(config.MQTT, func(Client)) (Client, error) {
			return h.client, nil
		},
	}

	b, err := New(cfg, "v0.5.0", deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.bridge = b
	// Mirrors devicebus.WrapWriter: in production Deps.IO is always wrapped
	// so a successful write lands back on the same bus the bridge
	// subscribes to. See fakeIO's doc comment.
	h.io.bus = h.bus
	return h
}

// runBridge starts h.bridge.Run in the background, waits for it to reach
// its select loop, then simulates a connect — the shape every test that
// needs a live devicebus round-trip (a command's resulting state flowing
// back through the bus, per bridge.go's onCommand) shares. Returns a func
// that cancels and waits for Run to return.
func runBridge(t *testing.T, h *testHarness) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.bridge.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	h.bridge.onConnect(h.client)
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after cancel")
		}
	}
}

// waitForState polls until topic's last published payload equals want, or
// fails the test after timeout — used where a publish now happens
// asynchronously via the devicebus round-trip instead of inline in the
// handler under test.
func waitForState(t *testing.T, h *testHarness, topic, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if msg, ok := h.client.find(topic); ok && string(msg.payload) == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("no publish to %s with payload %q within %s", topic, want, timeout)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func testDevice(id, boardID string, pin int, role model.DeviceRole) model.Device {
	return model.Device{ID: id, Name: "dev " + id, BoardID: boardID, Pin: pin, Role: role}
}

// ---- LWT / birth ----

func TestBridge_BirthOnConnect(t *testing.T) {
	h := newHarness(t, config.MQTT{DiscoveryPrefix: "homeassistant"})
	h.bridge.onConnect(h.client)

	msg, ok := h.client.find(statusTopic("pinctl"))
	if !ok {
		t.Fatal("no message published to status topic")
	}
	if string(msg.payload) != "online" || !msg.retained || msg.qos != 1 {
		t.Errorf("status publish = %+v, want online/retained/qos1", msg)
	}
}

// ---- Commands ----

func TestBridge_CommandDigitalOutWritesTrue(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_1", "brd_1", 5, model.RoleDigitalOut)
	h.store.putDevice(d)
	stop := runBridge(t, h)
	defer stop()

	// onConnect already published dev_1's initial (OFF) state at the fixed
	// fake-clock instant; advance past min_publish_interval so the
	// command's resulting bus update isn't rate-limited away by the same
	// deadband/rate policy every other change goes through.
	h.clock.Advance(2 * time.Second)
	h.client.deliver(setTopic("pinctl", "dev_1"), []byte("ON"))

	writes := h.io.writesFor("dev_1")
	if len(writes) != 1 || !writes[0].Bool {
		t.Fatalf("writes = %v, want one BoolValue(true)", writes)
	}
	// The command no longer publishes state directly (bridge.go's onCommand
	// doc comment): the write lands on the bus via fakeIO's WrapWriter
	// stand-in, and Run's handleBusUpdate publishes it from there — same
	// path a pipeline-driven write takes.
	waitForState(t, h, stateTopic("pinctl", "dev_1"), "ON", 2*time.Second)
}

func TestBridge_CommandPWMOutWrites200(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_2", "brd_1", 6, model.RolePWMOut)
	h.store.putDevice(d)
	stop := runBridge(t, h)
	defer stop()

	h.clock.Advance(2 * time.Second)
	h.client.deliver(setTopic("pinctl", "dev_2"), []byte("200"))

	writes := h.io.writesFor("dev_2")
	if len(writes) != 1 || writes[0].Float != 200 {
		t.Fatalf("writes = %v, want one FloatValue(200)", writes)
	}
	waitForState(t, h, stateTopic("pinctl", "dev_2"), "200", 2*time.Second)
}

func TestBridge_CommandServoWrites90(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_servo", "brd_1", 11, model.RoleServo)
	h.store.putDevice(d)
	stop := runBridge(t, h)
	defer stop()

	h.clock.Advance(2 * time.Second)
	h.client.deliver(setTopic("pinctl", "dev_servo"), []byte("90"))

	writes := h.io.writesFor("dev_servo")
	if len(writes) != 1 || writes[0].Float != 90 {
		t.Fatalf("writes = %v, want one FloatValue(90)", writes)
	}
	waitForState(t, h, stateTopic("pinctl", "dev_servo"), "90", 2*time.Second)
}

func TestBridge_CommandServo181Rejected(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_servo2", "brd_1", 12, model.RoleServo)
	h.store.putDevice(d)
	h.bridge.onConnect(h.client)

	h.client.deliver(setTopic("pinctl", "dev_servo2"), []byte("181"))

	if writes := h.io.writesFor("dev_servo2"); len(writes) != 0 {
		t.Fatalf("writes = %v, want none for out-of-range payload", writes)
	}
}

func TestBridge_CommandServoNonNumericRejected(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_servo3", "brd_1", 13, model.RoleServo)
	h.store.putDevice(d)
	h.bridge.onConnect(h.client)

	h.client.deliver(setTopic("pinctl", "dev_servo3"), []byte("abc"))

	if writes := h.io.writesFor("dev_servo3"); len(writes) != 0 {
		t.Fatalf("writes = %v, want none for a malformed payload", writes)
	}
}

func TestBridge_CommandServoRefusedWhenPipelineDrivesDevice(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_servo4", "brd_1", 14, model.RoleServo)
	h.store.putDevice(d)
	h.store.setConflict("dev_servo4", "pl_1", "My Pipeline")
	h.bridge.onConnect(h.client)

	h.bridge.mu.Lock()
	h.bridge.pubState["dev_servo4"] = publishState{value: engine.FloatValue(45), at: h.clock.Now(), valid: true}
	h.bridge.mu.Unlock()

	before := h.client.countTopic(stateTopic("pinctl", "dev_servo4"))
	h.client.deliver(setTopic("pinctl", "dev_servo4"), []byte("90"))
	after := h.client.countTopic(stateTopic("pinctl", "dev_servo4"))

	if writes := h.io.writesFor("dev_servo4"); len(writes) != 0 {
		t.Fatalf("writes = %v, want none: device is pipeline-driven", writes)
	}
	if after <= before {
		t.Fatal("expected the device's current state to be republished on refusal")
	}
	msg, _ := h.client.find(stateTopic("pinctl", "dev_servo4"))
	if string(msg.payload) != "45" {
		t.Errorf("republished state = %q, want 45 (the pre-command value, not 90)", msg.payload)
	}
}

func TestBridge_CommandPWMOut300Rejected(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_3", "brd_1", 7, model.RolePWMOut)
	h.store.putDevice(d)
	h.bridge.onConnect(h.client)

	h.client.deliver(setTopic("pinctl", "dev_3"), []byte("300"))

	if writes := h.io.writesFor("dev_3"); len(writes) != 0 {
		t.Fatalf("writes = %v, want none for out-of-range payload", writes)
	}
}

func TestBridge_CommandAnalogInRejected(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_4", "brd_1", 0, model.RoleAnalogIn)
	h.store.putDevice(d)
	h.bridge.onConnect(h.client)

	h.client.deliver(setTopic("pinctl", "dev_4"), []byte("ON"))

	if writes := h.io.writesFor("dev_4"); len(writes) != 0 {
		t.Fatalf("writes = %v, want none for an input-role command", writes)
	}
}

func TestBridge_CommandRefusedWhenPipelineDrivesDevice(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_5", "brd_1", 8, model.RoleDigitalOut)
	h.store.putDevice(d)
	h.store.setConflict("dev_5", "pl_1", "My Pipeline")
	h.bridge.onConnect(h.client)

	// Seed a known current state so we can assert it gets republished.
	h.bridge.mu.Lock()
	h.bridge.pubState["dev_5"] = publishState{value: engine.BoolValue(true), at: h.clock.Now(), valid: true}
	h.bridge.mu.Unlock()

	before := h.client.countTopic(stateTopic("pinctl", "dev_5"))
	h.client.deliver(setTopic("pinctl", "dev_5"), []byte("OFF"))
	after := h.client.countTopic(stateTopic("pinctl", "dev_5"))

	if writes := h.io.writesFor("dev_5"); len(writes) != 0 {
		t.Fatalf("writes = %v, want none: device is pipeline-driven", writes)
	}
	if after <= before {
		t.Fatal("expected the device's current state to be republished on refusal")
	}
	msg, _ := h.client.find(stateTopic("pinctl", "dev_5"))
	if string(msg.payload) != "ON" {
		t.Errorf("republished state = %q, want ON (the pre-command value, not OFF)", msg.payload)
	}
}

func TestBridge_MalformedCommandRateLimited(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_6", "brd_1", 9, model.RolePWMOut)
	h.store.putDevice(d)
	h.bridge.onConnect(h.client)

	// logRateLimited itself is what's under test here (log.Printf can't
	// easily be asserted on without capturing global log output), so drive
	// it directly and check the internal rate-limit bookkeeping.
	calls := 0
	logFn := func() { calls++ }

	h.bridge.mu.Lock()
	h.bridge.lastLog = make(map[string]time.Time)
	h.bridge.mu.Unlock()

	for i := 0; i < 3; i++ {
		h.bridge.logRateLimited("dev_6", "malformed %d", i)
		logFn()
	}
	h.bridge.mu.Lock()
	_, logged := h.bridge.lastLog["dev_6"]
	h.bridge.mu.Unlock()
	if !logged {
		t.Fatal("expected dev_6 to have a recorded log time after the first call")
	}

	// Advance past the rate-limit window: the next call should log again
	// (we can't observe log.Printf directly, but we can observe the
	// recorded timestamp advancing).
	before := h.bridge.lastLog["dev_6"]
	h.clock.Advance(2 * time.Minute)
	h.bridge.logRateLimited("dev_6", "malformed again")
	h.bridge.mu.Lock()
	after := h.bridge.lastLog["dev_6"]
	h.bridge.mu.Unlock()
	if !after.After(before) {
		t.Fatal("expected the rate-limit timestamp to advance after the window elapsed")
	}
}

func TestBridge_MalformedCommandDoesNotWrite(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_7", "brd_1", 10, model.RolePWMOut)
	h.store.putDevice(d)
	h.bridge.onConnect(h.client)

	h.client.deliver(setTopic("pinctl", "dev_7"), []byte("not-a-number"))

	if writes := h.io.writesFor("dev_7"); len(writes) != 0 {
		t.Fatalf("writes = %v, want none for a malformed payload", writes)
	}
}

func TestBridge_CommandUnknownDeviceDropped(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	h.bridge.onConnect(h.client)

	// Should not panic and should write nothing.
	h.client.deliver(setTopic("pinctl", "dev_ghost"), []byte("ON"))
}

// ---- Availability ----

func TestBridge_AvailabilityFlipsOnlyOwnDevicesOnBoardOffline(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d1 := testDevice("dev_a", "brd_1", 1, model.RoleDigitalIn)
	d2 := testDevice("dev_b", "brd_2", 2, model.RoleDigitalIn)
	h.store.putDevice(d1)
	h.store.putDevice(d2)
	h.boards.setConnected("brd_1", true)
	h.boards.setConnected("brd_2", true)
	h.bridge.onConnect(h.client)

	// Both online after connect.
	m1, _ := h.client.find(availableTopic("pinctl", "dev_a"))
	m2, _ := h.client.find(availableTopic("pinctl", "dev_b"))
	if string(m1.payload) != "online" || string(m2.payload) != "online" {
		t.Fatalf("expected both devices online initially, got %q / %q", m1.payload, m2.payload)
	}

	h.boards.setConnected("brd_1", false)
	h.bridge.syncAvailability(false)

	m1, _ = h.client.find(availableTopic("pinctl", "dev_a"))
	m2, _ = h.client.find(availableTopic("pinctl", "dev_b"))
	if string(m1.payload) != "offline" {
		t.Errorf("dev_a available = %q, want offline", m1.payload)
	}
	if string(m2.payload) != "online" {
		t.Errorf("dev_b available = %q, want still online", m2.payload)
	}
}

// ---- Reconnect ----

func TestBridge_ReconnectRepublishesEverything(t *testing.T) {
	h := newHarness(t, config.MQTT{DiscoveryPrefix: "homeassistant"})
	d := testDevice("dev_r", "brd_1", 3, model.RoleAnalogIn)
	h.store.putDevice(d)
	h.store.putBoard(model.Board{ID: "brd_1", Name: "Board 1"})
	h.boards.setConnected("brd_1", true)
	h.bus.setSnapshot(devicebus.Update{Device: d, Value: engine.FloatValue(512), At: h.clock.Now()})

	h.bridge.onConnect(h.client) // first connect

	discoTopic := discoveryTopic("homeassistant", "sensor", "dev_r")
	firstDiscovery := h.client.countTopic(discoTopic)
	firstAvail := h.client.countTopic(availableTopic("pinctl", "dev_r"))
	firstState := h.client.countTopic(stateTopic("pinctl", "dev_r"))
	if firstDiscovery == 0 || firstAvail == 0 || firstState == 0 {
		t.Fatalf("first connect: discovery=%d avail=%d state=%d, want all > 0", firstDiscovery, firstAvail, firstState)
	}

	h.bridge.onConnect(h.client) // simulated reconnect

	if got := h.client.countTopic(discoTopic); got <= firstDiscovery {
		t.Errorf("discovery count after reconnect = %d, want > %d", got, firstDiscovery)
	}
	if got := h.client.countTopic(availableTopic("pinctl", "dev_r")); got <= firstAvail {
		t.Errorf("availability count after reconnect = %d, want > %d", got, firstAvail)
	}
	if got := h.client.countTopic(stateTopic("pinctl", "dev_r")); got <= firstState {
		t.Errorf("state count after reconnect = %d, want > %d", got, firstState)
	}
}

// ---- Cancel-clean Run ----

func TestBridge_RunReturnsAndDisconnectsOnCancel(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- h.bridge.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}

	if !h.client.isDisconnected() {
		t.Error("expected Disconnect to have been called")
	}

	// No publish should land after Run has returned.
	before := len(h.client.snapshot())
	h.bridge.publish("pinctl/late/state", 0, true, []byte("should not appear"))
	after := len(h.client.snapshot())
	if after != before {
		t.Error("publish after Run returned was not suppressed")
	}
}

func TestBridge_RunConsumesBusUpdates(t *testing.T) {
	h := newHarness(t, config.MQTT{})
	d := testDevice("dev_live", "brd_1", 2, model.RoleDigitalIn)
	h.store.putDevice(d)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.bridge.Run(ctx) }()

	// Give Run a moment to reach its select loop, then simulate a connect
	// and a devicebus update.
	time.Sleep(50 * time.Millisecond)
	h.bridge.onConnect(h.client)
	h.bus.send(devicebus.Update{Device: d, Value: engine.BoolValue(true), At: h.clock.Now()})

	deadline := time.After(2 * time.Second)
	for {
		if _, ok := h.client.find(stateTopic("pinctl", "dev_live")); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("state was never published for a live devicebus update")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// ---- New() validation ----

func TestNew_RejectsMissingDeps(t *testing.T) {
	_, err := New(config.MQTT{Broker: "tcp://x:1883", ClientID: "c", BaseTopic: "pinctl", MinPublishInterval: config.Duration(time.Second), MaxPublishInterval: config.Duration(time.Minute)}, "v1", Deps{})
	if err == nil {
		t.Fatal("expected an error for missing Deps")
	}
}

func TestNew_RejectsBadBrokerScheme(t *testing.T) {
	h := &testHarness{store: newFakeStore(), bus: newFakeBus(), io: newFakeIO(), boards: newFakeBoardStatus()}
	deps := Deps{Store: h.store, Bus: h.bus, IO: h.io, Boards: h.boards}
	_, err := New(config.MQTT{
		Broker: "http://x:1883", ClientID: "c", BaseTopic: "pinctl",
		MinPublishInterval: config.Duration(time.Second), MaxPublishInterval: config.Duration(time.Minute),
	}, "v1", deps)
	if err == nil {
		t.Fatal("expected an error for an unsupported broker scheme")
	}
}

func TestNew_OK(t *testing.T) {
	h := &testHarness{store: newFakeStore(), bus: newFakeBus(), io: newFakeIO(), boards: newFakeBoardStatus()}
	deps := Deps{Store: h.store, Bus: h.bus, IO: h.io, Boards: h.boards}
	_, err := New(config.MQTT{
		Broker: "tcp://x:1883", ClientID: "c", BaseTopic: "pinctl",
		MinPublishInterval: config.Duration(time.Second), MaxPublishInterval: config.Duration(time.Minute),
	}, "v1", deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestNew_DoesNoIO(t *testing.T) {
	// Deps whose methods panic if called prove New performs no I/O.
	store := &panicStore{t: t}
	deps := Deps{Store: store, Bus: newFakeBus(), IO: newFakeIO(), Boards: newFakeBoardStatus()}
	_, err := New(config.MQTT{
		Broker: "tcp://x:1883", ClientID: "c", BaseTopic: "pinctl",
		MinPublishInterval: config.Duration(time.Second), MaxPublishInterval: config.Duration(time.Minute),
	}, "v1", deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

type panicStore struct{ t *testing.T }

func (p *panicStore) ListDevices() []model.Device {
	p.t.Fatal("New must not call ListDevices")
	return nil
}
func (p *panicStore) GetDevice(id string) (model.Device, error) {
	p.t.Fatal("New must not call GetDevice")
	return model.Device{}, nil
}
func (p *panicStore) ListBoards() []model.Board {
	p.t.Fatal("New must not call ListBoards")
	return nil
}
func (p *panicStore) GetBoard(id string) (model.Board, error) {
	p.t.Fatal("New must not call GetBoard")
	return model.Board{}, nil
}
func (p *panicStore) EnabledPipelineWritingDevice(deviceID string) (string, string, bool) {
	p.t.Fatal("New must not call EnabledPipelineWritingDevice")
	return "", "", false
}

// engine.DeviceIO compile-time satisfaction check.
var _ engine.DeviceIO = (*fakeIO)(nil)
