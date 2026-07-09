package engine

import (
	"sync"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// Manager owns zero or more running pipeline schedulers (runners), one per
// enabled pipeline, per docs/07-pipeline-engine.md "Execution model": a
// disabled pipeline has no goroutine and consumes no CPU.
type Manager struct {
	clock         Clock
	tickerFactory TickerFactory

	mu sync.Mutex
	// runners holds one entry per pipeline that has ever been started and
	// not since explicitly Stop-ed or hot-swapped away. This includes
	// runners that auto-disabled themselves after repeated tick failures
	// (StateError/StateDisabled) — they are deliberately kept here, not
	// removed, so Status(id) keeps reporting their last state instead of
	// falling back to "no runner" (StateDisabled with no error, docs/07's
	// "stopped"). They are only dropped by an explicit Stop(id) or by
	// Start(id) hot-swapping in a fresh runner for the same id.
	runners map[string]*runner

	// hubs holds one snapshotHub per pipeline ID that has ever been
	// started or subscribed to, and lives for the Manager's whole lifetime
	// (not torn down on Stop/hot-swap) so a WS client can Subscribe before,
	// during, or across a pipeline's enable/disable/reload cycles without
	// losing its subscription. An entry is only pruned by Remove(id) (a
	// pipeline being permanently deleted, not just disabled) or by
	// StopAll (process shutdown) — see those methods.
	hubs map[string]*snapshotHub
}

// Option configures a Manager at construction time.
type Option func(*Manager)

// WithClock overrides the Clock used by every runner started by this
// Manager. Production code doesn't need this (real time is the default);
// tests use it for a fake, advanceable clock.
func WithClock(c Clock) Option {
	return func(m *Manager) { m.clock = c }
}

// WithTickerFactory overrides the TickerFactory used by every runner
// started by this Manager. Tests use it to inject a manually-driven fake
// Ticker instead of a real time.Ticker, per docs/07-pipeline-engine.md
// "Determinism and testing hooks".
func WithTickerFactory(tf TickerFactory) Option {
	return func(m *Manager) { m.tickerFactory = tf }
}

// NewManager builds a Manager. With no options, it runs on the real clock
// and real time.Ticker.
func NewManager(opts ...Option) *Manager {
	m := &Manager{
		clock:         realClock{},
		tickerFactory: NewRealTicker,
		runners:       make(map[string]*runner),
		hubs:          make(map[string]*snapshotHub),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Start compiles p and, if it is enabled, runs it. If a runner for p.ID is
// already running, it is stopped first and replaced — hot-swap is exactly
// Stop+Start, and per docs/07-pipeline-engine.md "Hot-reload", node state
// is reset (a fresh runner has fresh node instances and a fresh
// write-dedup cache), never migrated from the old graph.
//
// If p.Enabled is false, any existing runner for p.ID is stopped and no
// new one is started (Status then reports StateDisabled with no error).
//
// A compile/validation failure leaves any previously-running graph for
// p.ID untouched and returns the error — per docs/07-pipeline-engine.md
// "Atomicity of the whole operation", Start either fully succeeds or fully
// fails.
func (m *Manager) Start(p model.Pipeline, devices map[string]model.Device, io DeviceIO) error {
	if !p.Enabled {
		m.Stop(p.ID)
		return nil
	}

	g, _, err := Compile(p, devices, io)
	if err != nil {
		return err
	}

	r := newRunner(p.ID, g, devices, io, m.clock, m.tickerFactory, m.hub(p.ID))

	m.mu.Lock()
	old := m.runners[p.ID]
	m.runners[p.ID] = r
	m.mu.Unlock()

	if old != nil {
		old.stop()
	}
	r.start()
	return nil
}

// Stop stops the runner for id, if any, waiting for its goroutine to exit
// cleanly at the next tick boundary. Stopping an id with no runner is a
// no-op. Stop deliberately leaves id's hub (if any) in Manager.hubs — Stop
// is also what a pipeline disable and Start's hot-swap-to-disabled path use
// (internal/api/pipelines.go's handleDisablePipeline, and Start below), and
// per the hubs field doc a WS client must be able to stay subscribed across
// a pipeline's enable/disable/reload cycles. Use Remove for the "this
// pipeline is never coming back" case (pipeline deletion).
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	r, ok := m.runners[id]
	if ok {
		delete(m.runners, id)
	}
	m.mu.Unlock()
	if ok {
		r.stop()
	}
}

// Remove stops the runner for id (like Stop) and additionally prunes and
// closes id's snapshotHub, if one was ever created — for use when a
// pipeline is being permanently deleted (internal/api/pipelines.go's
// handleDeletePipeline), not merely disabled. Closing the hub closes every
// current subscriber's channel so its relay goroutine exits cleanly
// (internal/api/ws.go's relaySnapshots sees ok==false and returns) instead
// of leaking the hub (and its subscriber map) for the lifetime of the
// process. A subsequent Subscribe(id) — e.g. a pipeline later re-created
// with the same id — transparently gets a fresh hub via Manager.hub.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	r, ok := m.runners[id]
	if ok {
		delete(m.runners, id)
	}
	h, hubOK := m.hubs[id]
	if hubOK {
		delete(m.hubs, id)
	}
	m.mu.Unlock()
	if ok {
		r.stop()
	}
	if hubOK {
		h.close()
	}
}

// StopAll stops every running pipeline and prunes+closes every hub, e.g. on
// process shutdown — unlike Stop, there is no "might come back" case here.
func (m *Manager) StopAll() {
	m.mu.Lock()
	runners := make([]*runner, 0, len(m.runners))
	for id, r := range m.runners {
		runners = append(runners, r)
		delete(m.runners, id)
	}
	hubs := make([]*snapshotHub, 0, len(m.hubs))
	for id, h := range m.hubs {
		hubs = append(hubs, h)
		delete(m.hubs, id)
	}
	m.mu.Unlock()
	for _, r := range runners {
		r.stop()
	}
	for _, h := range hubs {
		h.close()
	}
}

// Status returns the current status of pipeline id, and whether a runner
// exists for it at all (false if it was never started or has since been
// stopped).
func (m *Manager) Status(id string) (Status, bool) {
	m.mu.Lock()
	r, ok := m.runners[id]
	m.mu.Unlock()
	if !ok {
		return Status{}, false
	}
	return r.getStatus(), true
}

// hub returns the snapshotHub for pipeline id, creating it on first use.
// Called both from Start (so a runner always has somewhere to publish to)
// and from Subscribe (so a client can subscribe to a pipeline that hasn't
// started running yet, e.g. still disabled).
func (m *Manager) hub(id string) *snapshotHub {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.hubs[id]
	if !ok {
		h = newSnapshotHub()
		m.hubs[id] = h
	}
	return h
}

// Subscribe registers a listener for pipeline id's per-tick TickSnapshots
// (docs/07-pipeline-engine.md "Debug and observability hooks"). It never
// fails, even if id doesn't (yet) name a known pipeline or isn't currently
// running — the returned channel simply receives nothing until a runner
// for id exists and ticks. Callers that need to distinguish "unknown
// pipeline" (e.g. the WS layer's subscribe error per docs/10-api.md) check
// pipeline existence against the store themselves before calling Subscribe.
// The returned unsubscribe func is idempotent and safe to call more than
// once.
func (m *Manager) Subscribe(id string) (<-chan *TickSnapshot, func()) {
	return m.hub(id).subscribe()
}
