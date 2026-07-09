package engine

import (
	"encoding/json"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// DeviceIO is what the engine needs from the board layer: read a device's
// current value, and write a device value, addressed by model.Device
// (which already carries the board/pin/role a v0.1 board.Session needs to
// resolve). An adapter over board.Session (mapping role -> DigitalRead/
// AnalogRead/DigitalWrite/AnalogWrite by pin) is intentionally not built
// here — that's the job of whoever wires internal/board into the engine —
// but the interface is shaped so that adapter is a thin, mechanical
// wrapper: one method per direction, keyed by the device the pipeline
// already resolved at compile time.
type DeviceIO interface {
	// ReadDevice returns the current value of d (bool for digital-in,
	// float64 raw 0-1023 for analog-in). Called once per tick per
	// device-read node, per docs/07-pipeline-engine.md's input
	// snapshotting.
	ReadDevice(d model.Device) (Value, error)

	// WriteDevice writes v to d (bool for digital-out, float64 0-255 for
	// pwm-out). Called at most once per tick per device, after the
	// engine's own dedup/last-write-wins logic (docs/07-pipeline-engine.md
	// "Input snapshotting and output writes") — WriteDevice itself does
	// not need to re-implement dedup.
	WriteDevice(d model.Device, v Value) error
}

// Clock abstracts time.Now so timing-dependent node/scheduler code never
// calls time.Now() directly, per docs/07-pipeline-engine.md "Determinism
// and testing hooks". None of the v0.2 node set is timing-dependent, but
// Node.Init and the scheduler both thread it through so later node types
// (delay, debounce, timer) can rely on it without an interface change.
type Clock interface {
	Now() time.Time
}

// TickContext carries per-tick metadata into Node.Eval.
type TickContext struct {
	Tick uint64
	Now  time.Time
}

// NodeDeps is injected into NodeType.NewInstance once per node per graph
// compile. Devices/IO let device-read/device-write resolve and touch a
// device; ConnectedInputs tells a node instance, by declared input port
// name, whether that port has an incoming edge — needed for ports that
// fall back to a config default when unconnected (e.g. compare.threshold,
// the one documented exception in docs/08-node-catalog.md). TickMs is the
// owning pipeline's effective tick period (default-resolved, never 0),
// needed by nodes whose config is specified in wall-clock ms but whose
// behavior is implemented tick-indexed (delay's "rounded down to the
// nearest multiple of tickMs", docs/08-node-catalog.md). NodeID is this
// node's own id, needed by nodes whose behavior defaults from their own
// id (log's label).
type NodeDeps struct {
	Devices         map[string]model.Device
	IO              DeviceIO
	ConnectedInputs map[string]bool
	TickMs          int
	NodeID          string
}

// NodeType is implemented once per catalog entry and registered in the
// package-level type registry keyed by the JSON "type" string.
type NodeType interface {
	// Name is the JSON "type" discriminator, e.g. "device-read".
	Name() string

	// Ports describes this node's input/output ports and their static
	// types, given its raw JSON config and the pipeline's device set (only
	// device-read/device-write consult devices, to resolve output/input
	// type from the bound device's role).
	//
	// Deviation from docs/07-pipeline-engine.md's sketch (`Ports(cfg
	// NodeConfig)`): that signature can't resolve device-role-dependent
	// port typing on its own, since NodeConfig there is only decoded JSON.
	// Ports here takes the raw config (so it can decode it however it
	// needs) plus the device map instead.
	Ports(rawConfig json.RawMessage, devices map[string]model.Device) (PortSpec, error)

	// NewInstance decodes raw config into a typed config struct and
	// returns a fresh Node ready to evaluate. Called once per node per
	// graph compile, never per tick.
	NewInstance(rawConfig json.RawMessage, deps NodeDeps) (Node, error)
}

// Node is the per-instance evaluator, one allocated per node per compiled
// graph.
type Node interface {
	// Init is called once, right after NewInstance, before the first tick.
	Init(clk Clock) error

	// Eval runs once per tick, in topological order. in holds one Value
	// per declared input port (ordered per PortSpec.Inputs); it returns
	// one Value per declared output port (ordered per PortSpec.Outputs).
	Eval(ctx TickContext, in []Value) ([]Value, error)

	// Close releases any resources on pipeline stop/reload. All v0.2 node
	// types no-op.
	Close() error
}

// sinkNode is an additional narrow interface implemented only by
// device-write: since a sink node declares zero output ports (per
// docs/08-node-catalog.md), the scheduler can't learn "what to write"
// from Eval's return value alone. SinkValue reports the device to write
// to and the (already validated/clamped) value for this tick, read by the
// runner right after a successful Eval call.
type sinkNode interface {
	SinkValue() (deviceID string, v Value)
}

// logSink is implemented only by the log node: since it declares zero
// output ports, the scheduler can't learn "what (if anything) to log this
// tick" from Eval's return value alone. LogEntry is called once per tick,
// right after a successful Eval, regardless of whether anyone is
// subscribed to this pipeline's debug stream — this keeps the node's
// internal onChangeOnly state correct across a subscribe/unsubscribe
// cycle (docs/08-node-catalog.md "onChangeOnly: true still logs the very
// first value observed after the pipeline is enabled"). Whether the
// returned entry is actually collected into a TickSnapshot is the
// runner's decision, gated on subscription, per docs/07-pipeline-engine.md
// "the engine skips building the snapshot's serialized form entirely"
// when nobody is subscribed.
type logSink interface {
	LogEntry() (entry LogEntry, ok bool)
}
