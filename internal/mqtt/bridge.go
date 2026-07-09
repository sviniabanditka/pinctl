package mqtt

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/devicebus"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// tickInterval is how often Run polls board availability and checks for
// heartbeat-due devices. It is not configurable: it just needs to be
// small relative to the documented min_publish_interval default (1s) and
// max_publish_interval default (60s) so both stay accurate to about a
// second, which is what docs/15's "poll BoardStatus on the same cadence
// as the heartbeat" calls for.
const tickInterval = time.Second

// malformedLogWindow is docs/15-mqtt-bridge.md's "rejected and logged
// once per device per minute" for malformed/rejected command payloads.
const malformedLogWindow = time.Minute

// Bridge is pinctl's MQTT bridge (docs/15-mqtt-bridge.md). Construct with
// New, then run it with Run until ctx is cancelled.
type Bridge struct {
	cfg     config.MQTT
	version string
	deps    Deps
	clock   Clock

	stopped atomic.Bool

	mu       sync.Mutex
	client   Client
	devices  map[string]model.Device // last known device set, keyed by ID
	pubState map[string]publishState // per-device last-published value/time
	avail    map[string]bool         // per-device last-published availability
	lastLog  map[string]time.Time    // per-device malformed/rejected-command log rate limit
}

// New validates cfg and deps and returns a Bridge ready for Run. It does
// no I/O — no connection, no goroutine — per docs/15's "Disabled means
// absent" and the mirror-image expectation for an enabled-but-not-yet-run
// bridge.
func New(cfg config.MQTT, version string, deps Deps) (*Bridge, error) {
	if deps.Store == nil || deps.Bus == nil || deps.IO == nil || deps.Boards == nil {
		return nil, fmt.Errorf("mqtt: Deps.Store, Bus, IO and Boards are all required")
	}
	if cfg.BaseTopic == "" {
		return nil, fmt.Errorf("mqtt: base_topic must not be empty")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("mqtt: client_id must not be empty")
	}
	if strings.TrimSpace(cfg.Broker) == "" {
		return nil, fmt.Errorf("mqtt: broker is required")
	}
	u, err := url.Parse(cfg.Broker)
	if err != nil {
		return nil, fmt.Errorf("mqtt: parsing broker url: %w", err)
	}
	switch u.Scheme {
	case "tcp", "tls", "ssl", "ws", "wss":
	default:
		return nil, fmt.Errorf("mqtt: broker scheme %q is not supported (want one of tcp, tls, ssl, ws, wss)", u.Scheme)
	}
	if cfg.AnalogDeadband < 0 {
		return nil, fmt.Errorf("mqtt: analog_deadband must be >= 0")
	}
	if cfg.MinPublishInterval <= 0 {
		return nil, fmt.Errorf("mqtt: min_publish_interval must be positive")
	}
	if cfg.MaxPublishInterval <= 0 {
		return nil, fmt.Errorf("mqtt: max_publish_interval must be positive")
	}

	clock := deps.Clock
	if clock == nil {
		clock = realClock{}
	}

	return &Bridge{
		cfg:      cfg,
		version:  version,
		deps:     deps,
		clock:    clock,
		devices:  make(map[string]model.Device),
		pubState: make(map[string]publishState),
		avail:    make(map[string]bool),
		lastLog:  make(map[string]time.Time),
	}, nil
}

// Run connects and serves until ctx is done. It owns two logical loops —
// the devicebus subscription and the availability/heartbeat ticker — both
// stopped by ctx, per docs/15's "Architecture": "one goroutine for its
// command handler and one for its publish loop, both stopped by the
// context". Command handling here is driven by the MQTT client library's
// own callback goroutine (onCommand) rather than a loop Run owns directly,
// since Client.Subscribe already runs callbacks off of paho's read loop;
// Run's own loop is the "publish loop" (devicebus + ticker).
func (b *Bridge) Run(ctx context.Context) error {
	factory := b.deps.NewClient
	if factory == nil {
		factory = NewPahoClient
	}

	client, err := factory(b.cfg, b.onConnect)
	if err != nil {
		return fmt.Errorf("mqtt: connecting: %w", err)
	}
	b.mu.Lock()
	b.client = client
	b.mu.Unlock()

	defer func() {
		b.stopped.Store(true)
		client.Disconnect()
	}()

	busCh, unsubscribe := b.deps.Bus.Subscribe()
	defer unsubscribe()

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-busCh:
			if !ok {
				busCh = nil
				continue
			}
			b.handleBusUpdate(u)
		case <-ticker.C:
			now := b.clock.Now()
			b.syncAvailability(false)
			b.tick(now)
		}
	}
}

// DevicesChanged re-diffs the bridge's cached device set against the
// store and publishes discovery configs for new/changed devices and an
// empty retained payload for departed ones (docs/15's "Home Assistant
// discovery": "published on connect and whenever a device is created,
// renamed, or deleted"). The API layer calls it after any device
// mutation. It is cheap (one ListDevices call plus a map diff) and
// idempotent — calling it twice with no intervening store change
// republishes nothing.
func (b *Bridge) DevicesChanged() {
	now := b.clock.Now()
	fresh := b.deps.Store.ListDevices()
	freshByID := make(map[string]model.Device, len(fresh))
	for _, d := range fresh {
		freshByID[d.ID] = d
	}

	b.mu.Lock()
	old := b.devices
	b.mu.Unlock()

	for id, d := range old {
		if _, ok := freshByID[id]; !ok {
			b.depublishDevice(d)
		}
	}

	for id, d := range freshByID {
		prev, existed := old[id]
		if existed && devicesEqualForDiscovery(prev, d) {
			continue
		}
		b.publishDeviceDiscovery(d)
		if existed {
			continue
		}
		// A brand-new device: seed and publish its initial availability
		// and (for a writable role) its initial state, same as onConnect
		// does for every device on a fresh connect.
		online := b.deps.Boards.Connected(d.BoardID)
		b.mu.Lock()
		b.avail[d.ID] = online
		b.mu.Unlock()
		b.publish(availableTopic(b.cfg.BaseTopic, d.ID), 1, true, availabilityPayload(online))

		if isWritableRole(d.Role) {
			v := initialOutputValue(d)
			b.mu.Lock()
			b.pubState[d.ID] = publishState{value: v, at: now, valid: true}
			b.mu.Unlock()
			b.publish(stateTopic(b.cfg.BaseTopic, d.ID), 0, true, encodeState(d.Role, v))
		}
	}

	b.mu.Lock()
	b.devices = freshByID
	b.mu.Unlock()
}

// onConnect runs on every (re)connect, including the very first one:
// republish discovery, availability and every device's current state
// (docs/15's "Failure modes" table). It also (re)subscribes to the
// command filter, since a broker that never saw us before (or dropped our
// session) has no subscription for us otherwise.
func (b *Bridge) onConnect(c Client) {
	b.mu.Lock()
	b.client = c
	b.mu.Unlock()

	if err := c.Subscribe(commandFilter(b.cfg.BaseTopic), 1, b.onCommand); err != nil {
		log.Printf("mqtt: subscribing to commands: %v", err)
	}

	b.publish(statusTopic(b.cfg.BaseTopic), 1, true, []byte("online"))

	devices := b.deps.Store.ListDevices()
	byID := make(map[string]model.Device, len(devices))
	for _, d := range devices {
		byID[d.ID] = d
	}
	b.mu.Lock()
	b.devices = byID
	b.mu.Unlock()

	for _, d := range devices {
		b.publishDeviceDiscovery(d)
	}

	now := b.clock.Now()
	b.syncAvailability(true) // force: republish every device's availability on (re)connect, per docs/15
	b.republishSnapshot(devices, now)
}

// republishSnapshot publishes every device's current state. The devicebus
// snapshot now covers both directions — readable devices from polling,
// writable devices once WrapWriter has observed their first write — so it
// is preferred for both. A writable device the bus has no entry for yet
// (never written since pinctl started, or since its last restart) falls
// back to options.initial via payload.go's initialOutputValue.
func (b *Bridge) republishSnapshot(devices []model.Device, now time.Time) {
	snap := b.deps.Bus.Snapshot()

	b.mu.Lock()
	for id, u := range snap {
		b.pubState[id] = publishState{value: u.Value, at: now, valid: true}
	}
	for _, d := range devices {
		if !isWritableRole(d.Role) {
			continue
		}
		if st, ok := b.pubState[d.ID]; ok {
			st.at = now
			b.pubState[d.ID] = st
		} else {
			b.pubState[d.ID] = publishState{value: initialOutputValue(d), at: now, valid: true}
		}
	}
	roleByID := make(map[string]model.DeviceRole, len(devices))
	for _, d := range devices {
		roleByID[d.ID] = d.Role
	}
	toPublish := make(map[string]publishState, len(b.pubState))
	for id, st := range b.pubState {
		toPublish[id] = st
	}
	b.mu.Unlock()

	for id, st := range toPublish {
		role, ok := roleByID[id]
		if !ok {
			continue // stale pubState entry for a device that no longer exists
		}
		b.publish(stateTopic(b.cfg.BaseTopic, id), 0, true, encodeState(role, st.value))
	}
}

// handleBusUpdate applies the deadband/rate policy to a devicebus.Update
// and publishes it if warranted.
func (b *Bridge) handleBusUpdate(u devicebus.Update) {
	now := b.clock.Now()

	b.mu.Lock()
	prev := b.pubState[u.Device.ID]
	publish := shouldPublish(prev, u.Device.Role, u.Value, now, b.cfg)
	if publish {
		b.pubState[u.Device.ID] = publishState{value: u.Value, at: now, valid: true}
	}
	b.mu.Unlock()

	if publish {
		b.publish(stateTopic(b.cfg.BaseTopic, u.Device.ID), 0, true, encodeState(u.Device.Role, u.Value))
	}
}

// tick republishes the last-known state of any device whose
// max_publish_interval has elapsed without a change-triggered publish —
// the heartbeat docs/15 requires.
func (b *Bridge) tick(now time.Time) {
	for _, d := range b.currentDevices() {
		b.maybeHeartbeat(d, now)
	}
}

func (b *Bridge) maybeHeartbeat(d model.Device, now time.Time) {
	b.mu.Lock()
	st, ok := b.pubState[d.ID]
	if !ok || now.Sub(st.at) < time.Duration(b.cfg.MaxPublishInterval) {
		b.mu.Unlock()
		return
	}
	st.at = now
	b.pubState[d.ID] = st
	b.mu.Unlock()

	b.publish(stateTopic(b.cfg.BaseTopic, d.ID), 0, true, encodeState(d.Role, st.value))
}

// syncAvailability polls BoardStatus for every known device and publishes
// its availability topic on change, per docs/15's "Availability": "a
// board that unplugs greys out exactly its own pins".
func (b *Bridge) syncAvailability(force bool) {
	for _, d := range b.currentDevices() {
		online := b.deps.Boards.Connected(d.BoardID)

		b.mu.Lock()
		prev, known := b.avail[d.ID]
		changed := force || !known || prev != online
		if changed {
			b.avail[d.ID] = online
		}
		b.mu.Unlock()

		if changed {
			b.publish(availableTopic(b.cfg.BaseTopic, d.ID), 1, true, availabilityPayload(online))
		}
	}
}

// onCommand handles one message on <base>/<device>/set, per docs/15's
// "Commands and the pipeline-conflict rule": resolve the device, reject
// input roles, check the pipeline-conflict predicate, parse/range-check,
// write.
func (b *Bridge) onCommand(topic string, payload []byte) {
	deviceID, ok := deviceIDFromSetTopic(b.cfg.BaseTopic, topic)
	if !ok {
		return
	}

	d, err := b.deps.Store.GetDevice(deviceID)
	if err != nil {
		log.Printf("mqtt: command on %s: unknown device, dropping", topic)
		return
	}

	if d.Role != model.RoleDigitalOut && d.Role != model.RolePWMOut {
		b.logRateLimited(deviceID, "mqtt: command for %s (role %s) rejected: not a writable device", deviceID, d.Role)
		return
	}

	if pipelineID, pipelineName, conflict := b.deps.Store.EnabledPipelineWritingDevice(deviceID); conflict {
		log.Printf("mqtt: command for %s refused: driven by enabled pipeline %s (%q)", deviceID, pipelineID, pipelineName)
		b.republishState(d)
		return
	}

	v, err := decodeCommand(d.Role, payload)
	if err != nil {
		b.logRateLimited(deviceID, "mqtt: %v", err)
		return
	}

	if err := b.deps.IO.WriteDevice(d, v); err != nil {
		log.Printf("mqtt: writing %s: %v", deviceID, err)
		return
	}

	// No separate bookkeeping here: b.deps.IO is a devicebus.WrapWriter-
	// wrapped DeviceIO (cmd/pinctl wires every writer through the same one),
	// so the value just written lands back on the bus and arrives on busCh
	// like any other device change — handleBusUpdate publishes it, subject
	// to the same deadband/rate policy as a pipeline-driven write. This is
	// what lets the bridge drop tracking "writes I personally made" as a
	// distinct concept: a command-driven change and an engine-driven change
	// are now indistinguishable once they reach devicebus, which is the
	// point of routing every writer through one seam
	// (docs/15-mqtt-bridge.md's "one read path", now covering outputs too).
}

// republishState publishes d's last known value again, so an HA entity
// that optimistically flipped (or a broker that already delivered the
// rejected command) snaps back to the truth.
func (b *Bridge) republishState(d model.Device) {
	b.mu.Lock()
	st, ok := b.pubState[d.ID]
	b.mu.Unlock()

	v := st.value
	if !ok {
		v = initialOutputValue(d)
	}
	b.publish(stateTopic(b.cfg.BaseTopic, d.ID), 0, true, encodeState(d.Role, v))
}

// logRateLimited logs format/args for deviceID at most once per
// malformedLogWindow, per docs/15's "rejected and logged once per device
// per minute".
func (b *Bridge) logRateLimited(deviceID, format string, args ...any) {
	now := b.clock.Now()

	b.mu.Lock()
	last, ok := b.lastLog[deviceID]
	if ok && now.Sub(last) < malformedLogWindow {
		b.mu.Unlock()
		return
	}
	b.lastLog[deviceID] = now
	b.mu.Unlock()

	log.Printf(format, args...)
}

// publish is the single choke point every outbound message goes through:
// it refuses to publish once Run has begun shutting down, satisfying "no
// publish after ctx is done".
func (b *Bridge) publish(topic string, qos byte, retained bool, payload []byte) {
	if b.stopped.Load() {
		return
	}
	b.mu.Lock()
	client := b.client
	b.mu.Unlock()
	if client == nil {
		return
	}
	if err := client.Publish(topic, qos, retained, payload); err != nil {
		log.Printf("mqtt: publishing %s: %v", topic, err)
	}
}

func (b *Bridge) publishDeviceDiscovery(d model.Device) {
	if b.cfg.DiscoveryPrefix == "" {
		return
	}
	component, ok := haComponent(d.Role)
	if !ok {
		return
	}
	board, _ := b.deps.Store.GetBoard(d.BoardID) // zero-value Board is fine if lookup fails
	firmware, _ := b.deps.Boards.Firmware(d.BoardID)

	payload, err := buildDiscoveryConfig(discoveryInput{
		Device:    d,
		BoardName: board.Name,
		Firmware:  firmware,
		BaseTopic: b.cfg.BaseTopic,
		Version:   b.version,
	})
	if err != nil {
		log.Printf("mqtt: building discovery config for %s: %v", d.ID, err)
		return
	}
	b.publish(discoveryTopic(b.cfg.DiscoveryPrefix, component, d.ID), 1, true, payload)
}

// depublishDevice publishes an empty retained payload to d's discovery
// config topic — how HA removes an entity (docs/15) — and drops the
// bridge's local state for it.
func (b *Bridge) depublishDevice(d model.Device) {
	if b.cfg.DiscoveryPrefix != "" {
		if component, ok := haComponent(d.Role); ok {
			b.publish(discoveryTopic(b.cfg.DiscoveryPrefix, component, d.ID), 1, true, []byte{})
		}
	}
	b.mu.Lock()
	delete(b.pubState, d.ID)
	delete(b.avail, d.ID)
	delete(b.lastLog, d.ID)
	b.mu.Unlock()
}

func (b *Bridge) currentDevices() []model.Device {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]model.Device, 0, len(b.devices))
	for _, d := range b.devices {
		out = append(out, d)
	}
	return out
}

func isWritableRole(role model.DeviceRole) bool {
	return role == model.RoleDigitalOut || role == model.RolePWMOut
}

func devicesEqualForDiscovery(a, b model.Device) bool {
	return a.Name == b.Name && a.BoardID == b.BoardID && a.Pin == b.Pin && a.Role == b.Role
}

func availabilityPayload(online bool) []byte {
	if online {
		return []byte("online")
	}
	return []byte("offline")
}
