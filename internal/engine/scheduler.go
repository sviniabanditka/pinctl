package engine

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// State is a runner's coarse lifecycle state, surfaced via Manager.Status.
type State string

const (
	StateRunning  State = "running"
	StateDisabled State = "disabled"
	StateError    State = "error"

	// StatePaused is a runner whose tick loop is alive and ticking, but
	// whose most recent tick was degraded — a read or write hit
	// ErrDeviceUnreachable, per docs/07-pipeline-engine.md "Degraded
	// ticks". This is deliberately distinct from StateError: hardware
	// being unreachable is not the pipeline's fault, so it never counts
	// toward maxConsecutiveFailures and never auto-disables.
	StatePaused State = "paused"
)

// maxConsecutiveFailures is the number of consecutive failed ticks after
// which a pipeline is auto-disabled, per docs/07-pipeline-engine.md
// "Runtime error handling". A degraded (ErrDeviceUnreachable) tick never
// counts toward this — see runTick and run's tickResult handling.
const maxConsecutiveFailures = 3

// Status is a runner's observable state at a point in time.
type Status struct {
	State               State
	Tick                uint64
	ConsecutiveFailures int
	LastErrorNode       string
	LastError           error
	OverrunCount        uint64

	// DroppedWrites is a cumulative count of device-write sink writes
	// dropped because their target device was unreachable
	// (ErrDeviceUnreachable), across the runner's whole lifetime — never
	// reset, same pattern as OverrunCount.
	DroppedWrites uint64

	// Detail is a short human-readable reason, populated when State is
	// StatePaused (e.g. "device dev_9zq3v7ab4c is unreachable (board
	// brd_4kx8n2wq1r)") and cleared whenever a tick completes cleanly
	// (no unreachability, no error) — see run's tickResult handling.
	Detail string

	Warnings []Warning
}

// runner drives one enabled pipeline's tick loop, one goroutine per
// runner, per docs/07-pipeline-engine.md "Execution model".
type runner struct {
	id      string
	graph   *Graph
	io      DeviceIO
	devices map[string]model.Device

	tickPeriod    time.Duration
	clock         Clock
	tickerFactory TickerFactory
	hub           *snapshotHub

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once

	// lastWritten dedups device writes across ticks (only write on
	// change), per docs/07-pipeline-engine.md "Input snapshotting and
	// output writes". Only ever touched from the runner's own goroutine.
	lastWritten map[string]Value

	statusMu sync.Mutex
	status   Status
}

func newRunner(id string, g *Graph, devices map[string]model.Device, io DeviceIO, clock Clock, tf TickerFactory, hub *snapshotHub) *runner {
	if hub == nil {
		hub = newSnapshotHub() // defensive: keeps runner usable standalone in tests that skip the Manager
	}
	return &runner{
		id:            id,
		graph:         g,
		io:            io,
		devices:       devices,
		tickPeriod:    time.Duration(g.TickMs) * time.Millisecond,
		clock:         clock,
		tickerFactory: tf,
		hub:           hub,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
		lastWritten:   make(map[string]Value),
		status:        Status{State: StateRunning, Warnings: g.Warnings},
	}
}

func (r *runner) start() {
	go r.run()
}

// stop signals the scheduler to stop at the next tick boundary and waits
// for the goroutine to exit. Safe to call more than once.
func (r *runner) stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	<-r.doneCh
}

func (r *runner) getStatus() Status {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	s := r.status
	s.Warnings = append([]Warning(nil), r.status.Warnings...)
	return s
}

func (r *runner) setStatus(mutate func(*Status)) {
	r.statusMu.Lock()
	mutate(&r.status)
	r.statusMu.Unlock()
}

func (r *runner) run() {
	ticker := r.tickerFactory(r.tickPeriod)
	defer ticker.Stop()

	var tickNum uint64
	var consecutiveFail int
	skipNext := false

	for {
		select {
		case <-r.stopCh:
			close(r.doneCh)
			return
		case now, ok := <-ticker.C():
			if !ok {
				close(r.doneCh)
				return
			}
			if skipNext {
				skipNext = false
				r.setStatus(func(s *Status) { s.OverrunCount++ })
				continue
			}

			tickNum++
			buildSnapshot := r.hub.subscribed()
			start := r.clock.Now()
			tr := r.runTick(TickContext{Tick: tickNum, Now: now}, buildSnapshot)
			dur := r.clock.Now().Sub(start)

			if dur > r.tickPeriod {
				// Overrun: don't try to catch up, skip the next boundary
				// instead, per docs/07-pipeline-engine.md "Overrun
				// handling".
				skipNext = true
			}

			if buildSnapshot && tr.nodeVals != nil {
				// nodeVals is nil whenever the tick was abandoned early
				// (a genuine node eval error, a panic, or an unreachable
				// read — see runTick), same as it always has been for the
				// error case; a degraded tick caused only by a dropped
				// write still reaches here with a full nodeVals map.
				// Publish is itself a no-op if the last subscriber
				// unsubscribed between the check above and here; still
				// cheap (one atomic load) either way.
				r.hub.publish(&TickSnapshot{
					PipelineID: r.id,
					Tick:       tickNum,
					Duration:   dur,
					Nodes:      tr.nodeVals,
					Log:        tr.logEntries,
				})
			}

			switch {
			case tr.err != nil:
				// A genuine node evaluation error (or panic) — unchanged
				// from before degraded ticks existed: counts toward
				// maxConsecutiveFailures, auto-disables at the threshold.
				consecutiveFail++
				disable := consecutiveFail >= maxConsecutiveFailures
				r.setStatus(func(s *Status) {
					s.Tick = tickNum
					s.ConsecutiveFailures = consecutiveFail
					s.LastErrorNode = tr.failedNode
					s.LastError = tr.err
					s.Detail = ""
					if disable {
						s.State = StateDisabled
					} else {
						s.State = StateError
					}
				})
				if disable {
					close(r.doneCh)
					return
				}

			case tr.degraded:
				// Hardware being unreachable is a pause, not a logic
				// error, per docs/07-pipeline-engine.md "Degraded ticks":
				// consecutiveFail is deliberately left untouched (neither
				// incremented nor reset) so a run of degraded ticks can
				// never auto-disable the pipeline, and doesn't erase a
				// genuine-failure streak that was already in progress
				// either.
				r.setStatus(func(s *Status) {
					s.Tick = tickNum
					s.State = StatePaused
					s.Detail = tr.detail
					s.DroppedWrites += tr.droppedWrites
				})

			default:
				consecutiveFail = 0
				r.setStatus(func(s *Status) {
					s.Tick = tickNum
					s.ConsecutiveFailures = 0
					s.State = StateRunning
					s.Detail = ""
				})
			}
		}
	}
}

// tickResult bundles everything runTick reports back to run() about one
// tick. A tick has three possible outcomes: clean (err == nil, degraded ==
// false), degraded (err == nil, degraded == true — an unreachable device,
// see docs/07-pipeline-engine.md "Degraded ticks"), or failed (err != nil —
// a genuine node evaluation error or panic).
type tickResult struct {
	failedNode string // set only when err != nil
	err        error  // genuine node evaluation error/panic; nil for a clean or degraded tick

	degraded      bool
	detail        string // human-readable reason, set when degraded; e.g. "device dev_9zq3v7ab4c is unreachable (board brd_4kx8n2wq1r)"
	droppedWrites uint64 // sink writes dropped this tick because their device was unreachable

	nodeVals   map[string]Value
	logEntries []LogEntry
}

// unreachableDetail renders err (which must satisfy errors.Is(err,
// ErrDeviceUnreachable)) into the short "device X is unreachable (board Y)"
// form used for Status.Detail, by errors.As-ing out the *UnreachableError
// boardio attaches. Falls back to err.Error() if, for whatever reason, no
// *UnreachableError is found in the chain (defensive — should not happen
// for anything boardio produces).
func unreachableDetail(err error) string {
	var ue *UnreachableError
	if errors.As(err, &ue) {
		return fmt.Sprintf("device %s is unreachable (board %s)", ue.DeviceID, ue.BoardID)
	}
	return err.Error()
}

// runTick runs one full tick: snapshot+evaluate in topological order, then
// flush deduped/last-write-wins sink writes.
//
// A genuine node eval error (or panic) abandons the tick immediately — no
// further nodes evaluate and no writes queued so far are flushed — per
// docs/07-pipeline-engine.md "Runtime error handling".
//
// An unreachable-device error (errors.Is(evalErr, ErrDeviceUnreachable),
// e.g. a device-read node whose board is offline) is treated the same way
// evaluation-wise — the tick is abandoned, no stale/half-computed values
// propagate to downstream nodes or writes — but is reported back as
// *degraded*, not failed: see docs/07-pipeline-engine.md "Degraded ticks"
// for why a read failure skips the rest of the tick rather than, say,
// substituting a last-known value.
//
// A device-write's flush is handled differently: an unreachable target
// device only drops *that* write and keeps flushing the rest — a device
// being away must not stop a sibling device-write to a device that's still
// there. A non-unreachable write error is a genuine tick failure, same as
// a node eval error, and stops the flush.
//
// When buildSnapshot is false (the common case: nobody is subscribed to
// this pipeline's debug stream), nodeVals is always nil and no extra
// allocation happens beyond what evaluation already does — per
// docs/07-pipeline-engine.md "Debug and observability hooks": "the engine
// skips building the snapshot's serialized form entirely" when unsubscribed.
func (r *runner) runTick(ctx TickContext, buildSnapshot bool) (tr tickResult) {
	outputs := make(map[string][]Value, len(r.graph.Order))
	writeIntents := make(map[string]Value, len(r.graph.Order))
	if buildSnapshot {
		tr.nodeVals = make(map[string]Value, len(r.graph.Order))
	}

	// A node's Eval (or a DeviceIO call) runs third-party-ish code on the
	// pipeline's own goroutine: a panic there would take the whole runtime
	// down with every other pipeline and board (NFR-REL-2 in
	// docs/02-requirements.md). Turn it into an ordinary tick error so the
	// consecutive-failure policy disables just this pipeline. `current`
	// names the node the panic came from.
	var current string
	defer func() {
		if p := recover(); p != nil {
			tr = tickResult{
				failedNode: current,
				err:        fmt.Errorf("node %s panicked: %v", current, p),
			}
			log.Printf("engine: pipeline %s: node %s panicked: %v\n%s", r.id, current, p, debug.Stack())
		}
	}()

	for _, id := range r.graph.Order {
		current = id
		n := r.graph.Nodes[id]
		in := make([]Value, len(n.inputs))
		for i := range n.inputs {
			if n.srcPortIdx[i] < 0 {
				continue // unconnected optional port; node uses its own default
			}
			in[i] = outputs[n.srcNode[i]][n.srcPortIdx[i]]
		}

		out, evalErr := n.impl.Eval(ctx, in)
		if evalErr != nil {
			if errors.Is(evalErr, ErrDeviceUnreachable) {
				// Skip the rest of the tick's evaluation (see the doc
				// comment above): no stale values reach downstream nodes,
				// no half-computed writes queue up, but this is a pause,
				// not a logic error, so it's reported degraded rather
				// than failed.
				return tickResult{degraded: true, detail: unreachableDetail(evalErr)}
			}
			return tickResult{failedNode: id, err: fmt.Errorf("node %s: %w", id, evalErr)}
		}
		outputs[id] = out

		// Eval always runs (above), so a log node's internal onChangeOnly
		// state stays correct across a subscribe/unsubscribe cycle; only
		// collecting the entry into this tick's snapshot is gated on
		// buildSnapshot, per docs/07-pipeline-engine.md's zero-cost-when-
		// off invariant (see logSink's doc comment in node.go).
		if buildSnapshot {
			addSnapshotValues(tr.nodeVals, n, out)
			if ls, ok := n.impl.(logSink); ok {
				if entry, logged := ls.LogEntry(); logged {
					tr.logEntries = append(tr.logEntries, entry)
				}
			}
		}

		if sn, ok := n.impl.(sinkNode); ok {
			deviceID, v := sn.SinkValue()
			writeIntents[deviceID] = v // last write wins: later in topo order overwrites earlier
		}
	}

	for deviceID, v := range writeIntents {
		if last, ok := r.lastWritten[deviceID]; ok && last.Equal(v) {
			continue // only write on change
		}
		dev, ok := r.devices[deviceID]
		if !ok {
			continue // shouldn't happen post-validation; be defensive, not fatal
		}
		if writeErr := r.io.WriteDevice(dev, v); writeErr != nil {
			if errors.Is(writeErr, ErrDeviceUnreachable) {
				// Drop just this write and keep flushing the rest of
				// writeIntents (docs/07-pipeline-engine.md "Degraded
				// ticks") — one device being away must not stop a
				// sibling write to a device that's still reachable. The
				// next tick retries it (lastWritten is only updated on a
				// successful write, so the dedup cache still sees this
				// as "not yet delivered").
				tr.degraded = true
				tr.detail = unreachableDetail(writeErr)
				tr.droppedWrites++
				continue
			}
			// A non-unreachable write error is unexpected (boardio maps
			// every transport-level failure to ErrDeviceUnreachable) and
			// treated as a genuine tick failure, same as a node eval
			// error — abandon the rest of the flush. nodeVals/logEntries
			// are dropped too, matching the node-eval-error path: a
			// failed tick never gets a TickSnapshot published (see run's
			// publish gate).
			return tickResult{failedNode: deviceID, err: fmt.Errorf("device-write %s: %w", deviceID, writeErr)}
		}
		r.lastWritten[deviceID] = v
	}

	return tr
}
