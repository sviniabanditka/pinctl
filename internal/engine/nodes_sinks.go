package engine

import (
	"encoding/json"
	"fmt"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// ---- log ----

type logConfig struct {
	Label        *string `json:"label"`
	OnChangeOnly *bool   `json:"onChangeOnly"`
}

type logType struct{}

func (logType) Name() string { return "log" }

func (logType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	if err := validateLogConfig(raw); err != nil {
		return PortSpec{}, err
	}
	// AnyType: log "accepts either type, logs whichever arrives" per
	// docs/08-node-catalog.md — see Port.AnyType's doc comment in
	// value.go for why this needs a dedicated escape hatch from the
	// otherwise-strict edge type-checking. Type is set to TypeBool as a
	// placeholder only; it is never consulted for a port with AnyType set.
	return PortSpec{
		Inputs: []Port{{Name: "in", Type: TypeBool, Required: true, AnyType: true}},
	}, nil
}

func validateLogConfig(raw json.RawMessage) error {
	var cfg logConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return fmt.Errorf("log: config: %w", err)
	}
	return nil
}

func (logType) NewInstance(raw json.RawMessage, deps NodeDeps) (Node, error) {
	var cfg logConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return nil, fmt.Errorf("log: config: %w", err)
	}
	label := deps.NodeID
	if cfg.Label != nil {
		label = *cfg.Label
	}
	onChangeOnly := true
	if cfg.OnChangeOnly != nil {
		onChangeOnly = *cfg.OnChangeOnly
	}
	return &logNode{nodeID: deps.NodeID, label: label, onChangeOnly: onChangeOnly}, nil
}

// logNode writes its input value to the pipeline's log stream, per
// docs/08-node-catalog.md "log". It declares zero output ports and never
// touches a device — purely observational, "has no effect on pipeline
// behavior" — so it implements logSink instead of sinkNode. Eval always
// runs (it's a normal graph node in topological order) and updates the
// onChangeOnly state every tick regardless of whether anyone is
// subscribed to the debug stream; only whether the runner bothers to
// collect LogEntry()'s result into a TickSnapshot is gated on
// subscription (see logSink's doc comment in node.go).
type logNode struct {
	nodeID       string
	label        string
	onChangeOnly bool

	hasLast bool
	last    Value

	pending   LogEntry
	pendingOK bool
}

func (*logNode) Init(Clock) error { return nil }

func (n *logNode) Eval(ctx TickContext, in []Value) ([]Value, error) {
	v := in[0]
	shouldLog := true
	if n.onChangeOnly && n.hasLast && n.last.Equal(v) {
		shouldLog = false
	}
	if shouldLog {
		n.pending = LogEntry{NodeID: n.nodeID, Label: n.label, Value: v, Timestamp: ctx.Now}
		n.pendingOK = true
		n.last = v
		n.hasLast = true
	} else {
		n.pendingOK = false
	}
	return nil, nil
}

func (*logNode) Close() error { return nil }

// LogEntry implements logSink.
func (n *logNode) LogEntry() (LogEntry, bool) {
	return n.pending, n.pendingOK
}
