// Package simulate implements docs/18-simulation.md: running a pipeline
// graph against a fake clock, fake ticker and fake DeviceIO instead of real
// hardware, so an editor can answer "what does this graph do?" without
// enabling it on live hardware.
//
// Principle (docs/18 "Principles", #1): simulation runs the real
// engine.Manager/runner, never a second evaluator. internal/engine already
// takes an injected Clock, TickerFactory and DeviceIO — this package is
// exactly those three, faked, driving one throwaway *engine.Manager per
// Run call. A pipeline compiled here never reaches internal/board: the
// DeviceIO given to Manager.Start is simIO, which never touches a pin.
package simulate

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// MaxSteps is the hard cap on Request.Steps, per docs/18-simulation.md
// ("`steps` is capped (2000)").
const MaxSteps = 2000

// epoch is the fixed, arbitrary instant used when Request.StartTime is the
// zero value, per docs/18-simulation.md "Time": "default: an arbitrary
// fixed epoch, so runs are reproducible".
var epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// waitTickTimeout bounds how long Run waits for a fired tick to be
// reflected in the runner's Status before giving up. The fake clock/ticker
// never block on real I/O, so a tick completes in microseconds; this exists
// only as a defensive backstop against a future engine bug turning into a
// hung request instead of a hung test.
const waitTickTimeout = 30 * time.Second

// ScriptEntry changes a device's simulated input value at a given tick,
// per docs/18-simulation.md's `script` field.
type ScriptEntry struct {
	Tick     int
	DeviceID string
	Value    float64
}

// Request is one simulation run's full input: a pipeline body (need not
// exist in the store; Enabled is ignored — see Run), a tick count, a start
// instant, seed values for every device the graph reads, and a script of
// mid-run input changes.
type Request struct {
	Pipeline  model.Pipeline
	Steps     int
	StartTime time.Time
	Inputs    map[string]float64
	Script    []ScriptEntry
}

// Warning is a non-fatal finding about the request, e.g. a device the graph
// reads that Inputs never seeded (code "unseeded_input").
type Warning struct {
	Code     string
	DeviceID string
}

// RunError describes a node evaluation error that ended a run early — the
// same kind of failure that would auto-disable a live pipeline after
// several consecutive ticks, but here reported immediately, since a
// simulated run's whole point is showing the user why their graph misbehaves.
type RunError struct {
	Tick    int
	NodeID  string
	Message string
}

// Frame is one tick's observable output: every node's value (same shape as
// the pipeline:{id} WS debug snapshot), every device write that would have
// reached a pin, and any log node output.
type Frame struct {
	Tick   int
	At     time.Time
	Nodes  map[string]any
	Writes map[string]any
	Log    []engine.LogEntry
}

// Result is a full simulation run's output.
type Result struct {
	TickMs   int
	Warnings []Warning
	Frames   []Frame

	// Error is set when a node errored mid-run (e.g. an "expression" node's
	// division by zero). Frames still holds every tick captured before the
	// failure. A set Error is not itself a Go error — Run still returns nil
	// for it, per docs/18-simulation.md: "That is more useful than a 500,
	// because seeing the last good frame is how you find out why."
	Error *RunError
}

// StepsExceedsCapError is returned by Run when Request.Steps > MaxSteps.
type StepsExceedsCapError struct{ Steps int }

func (e *StepsExceedsCapError) Error() string {
	return fmt.Sprintf("simulate: steps %d exceeds the cap of %d", e.Steps, MaxSteps)
}

// UnknownDeviceError is returned by Run when Inputs or Script names a
// deviceId absent from the devices map Run was given (i.e. the store
// doesn't know it) — docs/18-simulation.md: "A device id the store doesn't
// know is a 400."
type UnknownDeviceError struct{ DeviceID string }

func (e *UnknownDeviceError) Error() string {
	return fmt.Sprintf("simulate: unknown device %q", e.DeviceID)
}

// Run simulates req.Pipeline against devices (resolved by the caller from
// the store, for role/pin metadata only — see internal/api's handler) for
// req.Steps ticks and returns the recorded frames.
//
// Run drives the real engine: it builds one throwaway *engine.Manager per
// call, wired to a fake Clock and a fake TickerFactory that only ever
// advance when Run itself fires them, plus a DeviceIO (simIO) that reads
// req.Inputs/req.Script and records writes instead of touching hardware.
// Steps are processed one at a time, synchronously: Run fires a tick and
// blocks until the runner's Status confirms it, so the whole run completes
// with no leaked goroutines and no dependency on wall-clock time (a huge
// tickMs/Steps combination still runs in milliseconds).
//
// A validation failure (unknown node type, dangling edge, cycle, ...)
// surfaces as the same engine.ValidationErrors POST /pipelines/validate
// returns, since it's produced by the same engine.Validate pass (via
// engine.Compile, called internally by engine.Manager.Start). Steps over
// MaxSteps or a deviceId Inputs/Script names that devices doesn't contain
// are reported as *StepsExceedsCapError / *UnknownDeviceError. A node
// erroring mid-run is not a Go error: it's recorded in Result.Error, with
// every frame up to that point still returned.
func Run(req Request, devices map[string]model.Device) (Result, error) {
	if req.Steps < 0 {
		return Result{}, fmt.Errorf("simulate: steps must be >= 0, got %d", req.Steps)
	}
	if req.Steps > MaxSteps {
		return Result{}, &StepsExceedsCapError{Steps: req.Steps}
	}
	for devID := range req.Inputs {
		if _, ok := devices[devID]; !ok {
			return Result{}, &UnknownDeviceError{DeviceID: devID}
		}
	}
	for _, se := range req.Script {
		if _, ok := devices[se.DeviceID]; !ok {
			return Result{}, &UnknownDeviceError{DeviceID: se.DeviceID}
		}
	}

	startTime := req.StartTime
	if startTime.IsZero() {
		startTime = epoch
	}

	// Enabled is ignored, per docs/18-simulation.md ("Its `enabled` flag is
	// ignored"): Run always starts it regardless of what the request body
	// said, exactly like engine.Manager.Start would for a saved, enabled
	// pipeline.
	p := req.Pipeline
	p.Enabled = true

	tickMs := engine.EffectiveTickMs(p)
	tickPeriod := time.Duration(tickMs) * time.Millisecond

	warnings := unseededWarnings(p, req.Inputs)
	io := newSimIO(devices, req.Inputs, req.Script)
	clk := newFakeClock(startTime)
	tf, tickers := newManualTickerFactory()

	mgr := engine.NewManager(engine.WithClock(clk), engine.WithTickerFactory(tf))
	defer mgr.StopAll() // never leaves a goroutine running past Run's return

	ch, unsub := mgr.Subscribe(p.ID)
	defer unsub()

	if err := mgr.Start(p, devices, io); err != nil {
		return Result{}, err
	}

	var mt *manualTicker
	select {
	case mt = <-tickers:
	case <-time.After(waitTickTimeout):
		return Result{}, fmt.Errorf("simulate: runner never started")
	}

	result := Result{TickMs: tickMs, Warnings: warnings, Frames: make([]Frame, 0, req.Steps)}

	for i := 1; i <= req.Steps; i++ {
		io.applyScript(i)
		now := startTime.Add(time.Duration(i) * tickPeriod)
		clk.set(now)
		mt.Fire(now)

		state, failedNode, tickErr, err := waitTick(mgr, p.ID, uint64(i))
		if err != nil {
			return result, err
		}
		if state == engine.StateError || state == engine.StateDisabled {
			msg := ""
			if tickErr != nil {
				msg = tickErr.Error()
			}
			result.Error = &RunError{Tick: i, NodeID: failedNode, Message: msg}
			return result, nil
		}

		snap := <-ch
		result.Frames = append(result.Frames, Frame{
			Tick:   i,
			At:     now,
			Nodes:  valuesToAny(snap.Nodes),
			Writes: io.drainWrites(),
			Log:    snap.Log,
		})
	}

	return result, nil
}

// waitTick blocks until pipeline id's runner Status reports having
// processed tick (successfully or not), returning its outcome. See Run's
// doc comment: this is synchronization with a goroutine we just unblocked
// via mt.Fire, not a wait on real time — the fake clock/ticker mean the
// runner has nothing to block on, so this returns almost immediately.
func waitTick(mgr *engine.Manager, id string, tick uint64) (state engine.State, failedNode string, tickErr error, err error) {
	deadline := time.Now().Add(waitTickTimeout)
	for {
		st, ok := mgr.Status(id)
		if ok && st.Tick >= tick {
			return st.State, st.LastErrorNode, st.LastError, nil
		}
		if time.Now().After(deadline) {
			return "", "", nil, fmt.Errorf("simulate: tick %d did not complete in time", tick)
		}
		runtime.Gosched()
	}
}

// valuesToAny converts a TickSnapshot's typed Nodes map into the plain
// bool/float64 shape docs/18-simulation.md's Frame.nodes example shows —
// the same shape internal/api's WS pipeline:{id} snapshot uses.
func valuesToAny(nodes map[string]engine.Value) map[string]any {
	out := make(map[string]any, len(nodes))
	for k, v := range nodes {
		out[k] = valueToAny(v)
	}
	return out
}

func valueToAny(v engine.Value) any {
	if v.Type == engine.TypeBool {
		return v.Bool
	}
	return v.Float
}

// deviceReadCfg mirrors just the field of a "device-read" node's config
// unseededWarnings needs. It deliberately doesn't reuse engine's own
// (unexported) config type: this is a one-field read of already-validated
// JSON, not pipeline execution logic, so duplicating the shape carries no
// drift risk the way re-implementing node semantics would.
type deviceReadCfg struct {
	DeviceID string `json:"deviceId"`
}

// unseededWarnings reports, once per device (not per node), every
// "device-read" node in p whose deviceId isn't a key of inputs — per
// docs/18-simulation.md: "A device the graph reads but `inputs` omits
// starts at its type's zero, and the response says so in `warnings`".
func unseededWarnings(p model.Pipeline, inputs map[string]float64) []Warning {
	seen := make(map[string]bool)
	var ids []string
	for _, n := range p.Nodes {
		if n.Type != "device-read" {
			continue
		}
		var cfg deviceReadCfg
		if err := json.Unmarshal(n.Config, &cfg); err != nil || cfg.DeviceID == "" {
			continue // malformed config is already an engine.Validate failure
		}
		if _, ok := inputs[cfg.DeviceID]; ok {
			continue
		}
		if seen[cfg.DeviceID] {
			continue
		}
		seen[cfg.DeviceID] = true
		ids = append(ids, cfg.DeviceID)
	}
	sort.Strings(ids)
	out := make([]Warning, len(ids))
	for i, id := range ids {
		out[i] = Warning{Code: "unseeded_input", DeviceID: id}
	}
	return out
}

// ---- fake Clock ----

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

// ---- fake Ticker, driven only by Run's own tick loop ----

type manualTicker struct {
	ch chan time.Time
}

func (m *manualTicker) C() <-chan time.Time { return m.ch }
func (m *manualTicker) Stop()               {}

// Fire sends t on the ticker's channel, blocking until the runner goroutine
// receives it (i.e. starts processing the tick) — same pattern as
// internal/engine's own test helpers (manualTicker.Fire).
func (m *manualTicker) Fire(t time.Time) { m.ch <- t }

// newManualTickerFactory hands out manualTickers and publishes the one and
// only one Run ever creates (one pipeline per Run) on tickers, so Run can
// grab it once and drive it directly.
func newManualTickerFactory() (engine.TickerFactory, <-chan *manualTicker) {
	out := make(chan *manualTicker, 1)
	return func(time.Duration) engine.Ticker {
		mt := &manualTicker{ch: make(chan time.Time)}
		out <- mt
		return mt
	}, out
}
