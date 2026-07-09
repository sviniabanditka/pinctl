package engine

import (
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
)

// maxConsecutiveFailures is the number of consecutive failed ticks after
// which a pipeline is auto-disabled, per docs/07-pipeline-engine.md
// "Runtime error handling".
const maxConsecutiveFailures = 3

// Status is a runner's observable state at a point in time.
type Status struct {
	State               State
	Tick                uint64
	ConsecutiveFailures int
	LastErrorNode       string
	LastError           error
	OverrunCount        uint64
	Warnings            []Warning
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
			failedNode, evalErr, nodeVals, logEntries := r.runTick(TickContext{Tick: tickNum, Now: now}, buildSnapshot)
			dur := r.clock.Now().Sub(start)

			if dur > r.tickPeriod {
				// Overrun: don't try to catch up, skip the next boundary
				// instead, per docs/07-pipeline-engine.md "Overrun
				// handling".
				skipNext = true
			}

			if evalErr == nil && buildSnapshot {
				// Publish is itself a no-op if the last subscriber
				// unsubscribed between the check above and here; still
				// cheap (one atomic load) either way.
				r.hub.publish(&TickSnapshot{
					PipelineID: r.id,
					Tick:       tickNum,
					Duration:   dur,
					Nodes:      nodeVals,
					Log:        logEntries,
				})
			}

			if evalErr != nil {
				consecutiveFail++
				disable := consecutiveFail >= maxConsecutiveFailures
				r.setStatus(func(s *Status) {
					s.Tick = tickNum
					s.ConsecutiveFailures = consecutiveFail
					s.LastErrorNode = failedNode
					s.LastError = evalErr
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
				continue
			}

			consecutiveFail = 0
			r.setStatus(func(s *Status) {
				s.Tick = tickNum
				s.ConsecutiveFailures = 0
				s.State = StateRunning
			})
		}
	}
}

// runTick runs one full tick: snapshot+evaluate in topological order, then
// flush deduped/last-write-wins sink writes. On a node eval error, it
// abandons the tick immediately — no further nodes evaluate and no writes
// queued so far are flushed — per docs/07-pipeline-engine.md "Runtime
// error handling".
//
// When buildSnapshot is false (the common case: nobody is subscribed to
// this pipeline's debug stream), nodeVals is always nil and no extra
// allocation happens beyond what evaluation already does — per
// docs/07-pipeline-engine.md "Debug and observability hooks": "the engine
// skips building the snapshot's serialized form entirely" when unsubscribed.
func (r *runner) runTick(ctx TickContext, buildSnapshot bool) (failedNode string, err error, nodeVals map[string]Value, logEntries []LogEntry) {
	outputs := make(map[string][]Value, len(r.graph.Order))
	writeIntents := make(map[string]Value, len(r.graph.Order))
	if buildSnapshot {
		nodeVals = make(map[string]Value, len(r.graph.Order))
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
			failedNode = current
			err = fmt.Errorf("node %s panicked: %v", current, p)
			nodeVals, logEntries = nil, nil
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
			return id, fmt.Errorf("node %s: %w", id, evalErr), nil, nil
		}
		outputs[id] = out

		// Eval always runs (above), so a log node's internal onChangeOnly
		// state stays correct across a subscribe/unsubscribe cycle; only
		// collecting the entry into this tick's snapshot is gated on
		// buildSnapshot, per docs/07-pipeline-engine.md's zero-cost-when-
		// off invariant (see logSink's doc comment in node.go).
		if buildSnapshot {
			addSnapshotValues(nodeVals, n, out)
			if ls, ok := n.impl.(logSink); ok {
				if entry, logged := ls.LogEntry(); logged {
					logEntries = append(logEntries, entry)
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
			// A dropped write due to an unreachable device is a degraded
			// tick, not an evaluation error (docs/07-pipeline-engine.md
			// "Board disconnected mid-run") — it does not count toward
			// the consecutive-failure counter. v0.2 does not yet surface
			// a distinct "degraded" status field; the write is simply
			// not recorded as delivered, so the next tick retries it.
			continue
		}
		r.lastWritten[deviceID] = v
	}

	return "", nil, nodeVals, logEntries
}
