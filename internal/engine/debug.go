package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

// TickSnapshot is a per-tick debug snapshot for one pipeline, delivered to
// Manager.Subscribe listeners, per docs/07-pipeline-engine.md "Debug and
// observability hooks" and the pipeline:{id} WS topic in docs/10-api.md.
type TickSnapshot struct {
	PipelineID string
	Tick       uint64
	Duration   time.Duration

	// Nodes holds this tick's output value per node/edge: keyed by nodeId
	// for a node with exactly one output port (every v0.2 non-sink node
	// type), or "nodeId.port" for a node with more than one output port.
	// Sink nodes (zero output ports, e.g. device-write) are omitted — there
	// is no edge value to show for them.
	Nodes map[string]Value

	// Log holds this tick's log entries, one per "log" node that decided
	// to log this tick (per its onChangeOnly config), in evaluation order.
	// Empty/nil on a tick with nothing to log or when nobody is subscribed
	// (see logSink.LogEntry's doc comment) — never built just to stay
	// empty, per the same zero-cost-when-off invariant as Nodes.
	Log []LogEntry
}

// LogEntry is one "log" node's recorded value for a tick, per
// docs/08-node-catalog.md's log node and the WS wire shape in
// docs/10-api.md ({nodeId, label, value, timestamp}).
type LogEntry struct {
	NodeID    string
	Label     string
	Value     Value
	Timestamp time.Time
}

// snapshotChanBuf is small: this is a live "debugging while the tab is
// open" stream, not a durable log — a slow consumer is expected to miss
// ticks (drop-oldest, see snapshotHub.publish), not backlog forever.
const snapshotChanBuf = 16

// snapshotHub fans one pipeline's per-tick snapshots out to zero or more
// Manager.Subscribe listeners. The scheduler's hot path only ever calls
// subscribed(), a single atomic load — per docs/07-pipeline-engine.md,
// "When no one is subscribed to a pipeline, the engine skips building the
// snapshot's serialized form entirely... to keep the steady-state cost of
// debugging support near zero."
type snapshotHub struct {
	count atomic.Int32

	mu     sync.Mutex
	subs   map[int]chan *TickSnapshot
	next   int
	closed bool
}

func newSnapshotHub() *snapshotHub {
	return &snapshotHub{subs: make(map[int]chan *TickSnapshot)}
}

// subscribed reports, with a single atomic load, whether any subscriber is
// currently attached.
func (h *snapshotHub) subscribed() bool { return h.count.Load() > 0 }

// subscribe registers a new listener and returns its channel plus an
// idempotent unsubscribe func. If the hub has already been closed (its
// pipeline was Stop-ed and pruned from Manager.hubs), subscribe returns a
// channel that is immediately closed, so a caller who raced Subscribe
// against Stop still gets well-defined "no more snapshots ever" behavior
// instead of hanging on a channel nothing will publish to.
func (h *snapshotHub) subscribe() (<-chan *TickSnapshot, func()) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		ch := make(chan *TickSnapshot)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan *TickSnapshot, snapshotChanBuf)
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()
	h.count.Add(1)

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
			h.count.Add(-1)
		})
	}
	return ch, unsubscribe
}

// close detaches the hub: every current subscriber's channel is closed (so
// its relay loop sees ok==false and exits cleanly, per internal/api/ws.go's
// relaySnapshots) and any future subscribe call gets an already-closed
// channel rather than being silently registered against a hub nobody will
// ever publish to again. Called from Manager.Stop/StopAll when the hub
// entry is pruned from Manager.hubs, per docs/07-pipeline-engine.md.
func (h *snapshotHub) close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	subs := h.subs
	h.subs = make(map[int]chan *TickSnapshot)
	h.mu.Unlock()

	for _, ch := range subs {
		close(ch)
	}
	h.count.Store(0)
}

// publish delivers snap to every subscriber, non-blocking: a subscriber
// channel that's already full has its oldest queued snapshot dropped to
// make room, rather than the tick stalling on a slow browser tab — per
// docs/07-pipeline-engine.md "Delivery".
func (h *snapshotHub) publish(snap *TickSnapshot) {
	if !h.subscribed() {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- snap:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
			default:
			}
		}
	}
}

// addSnapshotValues records n's this-tick output(s) into dst, keyed per
// TickSnapshot.Nodes' convention (see its doc comment). No-op for a
// zero-output (sink) node.
func addSnapshotValues(dst map[string]Value, ni *nodeInstance, out []Value) {
	switch len(ni.outputs) {
	case 0:
		return
	case 1:
		dst[ni.id] = out[0]
	default:
		for i, p := range ni.outputs {
			dst[ni.id+"."+p.Name] = out[i]
		}
	}
}
