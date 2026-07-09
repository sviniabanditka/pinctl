// Package devicebus polls every readable device through the runtime exactly
// once and fans the resulting change events out to any number of
// subscribers, per docs/15-mqtt-bridge.md's "one poll, many readers"
// architecture: the WebSocket hub (internal/api) and the MQTT bridge
// (internal/mqtt) both read the same stream instead of each polling the
// board independently.
//
// Firmata never reports an output pin's value back, so polling alone only
// ever covers the readable roles (digital-in, analog-in). Output state
// (digital-out, pwm-out) becomes observable the other way around: every
// writer — the pipeline engine, the REST manual write, the MQTT command
// handler — writes through an engine.DeviceIO wrapped with WrapWriter,
// which calls NoteWrite on success. NoteWrite feeds the exact same Update
// stream and change-detection as the poll loop, so a subscriber sees a
// device's value change no matter which side of the Reader/writer split
// caused it.
//
// The bus does change-detection (an Update is published only when a
// device's value differs from its previous known value) but no publishing
// policy on top of that — deadbanding, rate limiting, retained-state
// semantics and the like belong to whichever subscriber needs them
// (docs/15's "Publishing policy: deadband and rate" is MQTT-bridge-specific
// and does not belong here).
package devicebus

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// Reader is what the bus needs from the runtime to read a device's current
// value. engine.DeviceIO satisfies it.
type Reader interface {
	ReadDevice(d model.Device) (engine.Value, error)
}

// Lister is what the bus needs from the store to know which devices exist.
// *store.Store satisfies it.
type Lister interface {
	ListDevices() []model.Device
}

// Update is one device's value change, delivered to every subscriber
// current at the moment of the poll that detected it.
type Update struct {
	Device model.Device
	Value  engine.Value
	At     time.Time
}

// subscriberBufSize is the per-subscriber channel buffer. A subscriber that
// falls behind has its oldest buffered Update dropped rather than blocking
// the poll loop — the board poll must never stall behind a slow consumer.
const subscriberBufSize = 64

type subscriber struct {
	ch chan Update
}

// Bus polls every readable device (digital-in, analog-in) at interval,
// publishing an Update to every current subscriber whenever a device's
// value changes since the previous poll. It is safe for concurrent use:
// Subscribe/unsubscribe may be called at any time, including concurrently
// with Run's poll loop.
type Bus struct {
	r        Reader
	l        Lister
	interval time.Duration

	mu      sync.Mutex
	subs    map[int]*subscriber
	nextID  int
	last    map[string]Update
	stopped bool

	dropped atomic.Int64
}

// New builds a Bus that reads through r, lists devices through l, and polls
// at interval. It does nothing until Run is called.
func New(r Reader, l Lister, interval time.Duration) *Bus {
	return &Bus{
		r:        r,
		l:        l,
		interval: interval,
		subs:     make(map[int]*subscriber),
		last:     make(map[string]Update),
	}
}

// Run polls until ctx is done, then closes every subscriber channel exactly
// once and returns. Call it from exactly one goroutine.
func (b *Bus) Run(ctx context.Context) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			b.stopAll()
			return
		case <-ticker.C:
			b.poll()
		}
	}
}

// poll reads every readable device once and publishes an Update for each
// one whose value changed since the previous poll. A read error (board
// offline, pin not yet reporting) skips that device for this tick, same as
// the poller this replaces.
func (b *Bus) poll() {
	for _, d := range b.l.ListDevices() {
		if d.Role != model.RoleDigitalIn && d.Role != model.RoleAnalogIn {
			continue // not readable; nothing to publish
		}
		v, err := b.r.ReadDevice(d)
		if err != nil {
			continue
		}
		b.publishIfChanged(d, v)
	}
}

// publishIfChanged records v as device d's latest value and delivers an
// Update to every subscriber, but only if v differs from the last value
// recorded for d. Delivery happens under b.mu so it can never race a
// concurrent Subscribe/unsubscribe closing a channel mid-send.
func (b *Bus) publishIfChanged(d model.Device, v engine.Value) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if prev, ok := b.last[d.ID]; ok && prev.Value.Equal(v) {
		return
	}
	u := Update{Device: d, Value: v, At: time.Now().UTC()}
	b.last[d.ID] = u
	for _, s := range b.subs {
		b.deliverLocked(s, u)
	}
}

// deliverLocked sends u on s.ch, dropping the single oldest buffered Update
// (and counting it) instead of blocking if s.ch is full. Caller must hold
// b.mu — that's what makes this safe against a concurrent unsubscribe
// closing s.ch out from under a send.
func (b *Bus) deliverLocked(s *subscriber, u Update) {
	for {
		select {
		case s.ch <- u:
			return
		default:
		}
		select {
		case <-s.ch:
			b.dropped.Add(1)
		default:
			// Buffer was drained by the receiving goroutine between the two
			// selects above; retry the send.
		}
	}
}

// stopAll closes and forgets every current subscriber, and marks the bus
// stopped so a later Subscribe doesn't register a subscriber that will
// never be delivered to or closed.
func (b *Bus) stopAll() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopped = true
	for id, s := range b.subs {
		close(s.ch)
		delete(b.subs, id)
	}
}

// Subscribe registers a new subscriber and returns its update channel and
// an idempotent unsubscribe func — calling it more than once, or after Run
// has already closed the channel, is a safe no-op. If Run has already
// returned, Subscribe hands back an already-closed channel and a no-op
// unsubscribe rather than registering a subscriber nothing will ever
// service.
func (b *Bus) Subscribe() (<-chan Update, func()) {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		ch := make(chan Update)
		close(ch)
		return ch, func() {}
	}

	id := b.nextID
	b.nextID++
	s := &subscriber{ch: make(chan Update, subscriberBufSize)}
	b.subs[id] = s
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		s, ok := b.subs[id]
		if !ok {
			return // already unsubscribed, or Run already closed everything
		}
		delete(b.subs, id)
		close(s.ch)
	}
	return s.ch, unsubscribe
}

// Snapshot returns the last known Update for every device the bus has
// observed a value for, keyed by device ID. Useful for a subscriber that
// needs the current state immediately on start instead of waiting for the
// next change (e.g. MQTT's retained state republish on reconnect,
// docs/15-mqtt-bridge.md).
func (b *Bus) Snapshot() map[string]Update {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make(map[string]Update, len(b.last))
	for k, v := range b.last {
		out[k] = v
	}
	return out
}

// Dropped returns the total number of Updates dropped so far because a
// subscriber's buffer was full — a slow-subscriber counter, not an error
// condition by itself.
func (b *Bus) Dropped() int64 { return b.dropped.Load() }

// NoteWrite records v as device d's latest known value and fans an Update
// out to every current subscriber, using the exact same change-detection
// (no Update if v matches the previously recorded value) and drop-oldest
// delivery policy as the poll loop's publishIfChanged. It is how output
// device state — digital-out, pwm-out — becomes observable at all: Firmata
// never reports an output pin back, so poll() only ever sees the readable
// roles, and without a call like this nothing outside the writer would
// ever learn a pipeline (or anyone else) changed one.
//
// Safe to call from any goroutine, concurrently with Run's poll loop and
// with itself — typically via WrapWriter, once per successful write,
// including from the engine's own pipeline goroutines.
func (b *Bus) NoteWrite(d model.Device, v engine.Value) {
	b.publishIfChanged(d, v)
}

// writerIO decorates an engine.DeviceIO so every device it successfully
// writes is also recorded on a Bus, via NoteWrite. See WrapWriter.
type writerIO struct {
	io engine.DeviceIO
	b  *Bus
}

// WrapWriter returns an engine.DeviceIO wrapping io: ReadDevice passes
// straight through, and WriteDevice calls through to io and, only if that
// write succeeds, calls b.NoteWrite(d, v) — this is how output device
// state becomes observable outside the writer itself (docs/15-mqtt-
// bridge.md's Home Assistant switches and the WS "devices" topic alike).
//
// Wrap once, at startup, before handing the result to anything that writes
// devices — the engine manager, the REST manual-write handler, the MQTT
// command handler — so every writer is observed at exactly the point its
// write actually succeeded, regardless of which of them made it.
func WrapWriter(io engine.DeviceIO, b *Bus) engine.DeviceIO {
	return writerIO{io: io, b: b}
}

func (w writerIO) ReadDevice(d model.Device) (engine.Value, error) {
	return w.io.ReadDevice(d)
}

func (w writerIO) WriteDevice(d model.Device, v engine.Value) error {
	if err := w.io.WriteDevice(d, v); err != nil {
		return err
	}
	w.b.NoteWrite(d, v)
	return nil
}
