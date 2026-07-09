package engine

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// ---- threshold-hysteresis ----

type thresholdHysteresisConfig struct {
	OnLevel  *float64 `json:"onLevel"`
	OffLevel *float64 `json:"offLevel"`
	Initial  *bool    `json:"initial"`
}

type thresholdHysteresisType struct{}

func (thresholdHysteresisType) Name() string { return "threshold-hysteresis" }

func (thresholdHysteresisType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	if _, _, _, err := parseHysteresisConfig(raw); err != nil {
		return PortSpec{}, err
	}
	return PortSpec{
		Inputs:  []Port{{Name: "in", Type: TypeFloat64, Required: true}},
		Outputs: []Port{{Name: "out", Type: TypeBool}},
	}, nil
}

func parseHysteresisConfig(raw json.RawMessage) (onLevel, offLevel float64, initial bool, err error) {
	var cfg thresholdHysteresisConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return 0, 0, false, fmt.Errorf("threshold-hysteresis: config: %w", err)
	}
	onLevel = 600
	if cfg.OnLevel != nil {
		onLevel = *cfg.OnLevel
	}
	offLevel = 400
	if cfg.OffLevel != nil {
		offLevel = *cfg.OffLevel
	}
	if offLevel > onLevel {
		return 0, 0, false, fmt.Errorf("threshold-hysteresis: config.offLevel (%v) must be <= config.onLevel (%v)", offLevel, onLevel)
	}
	if cfg.Initial != nil {
		initial = *cfg.Initial
	}
	return onLevel, offLevel, initial, nil
}

func (thresholdHysteresisType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	onLevel, offLevel, initial, err := parseHysteresisConfig(raw)
	if err != nil {
		return nil, err
	}
	return &thresholdHysteresisNode{onLevel: onLevel, offLevel: offLevel, initial: initial}, nil
}

// thresholdHysteresisNode is a Schmitt-trigger comparator, per
// docs/08-node-catalog.md "threshold-hysteresis": above onLevel -> true,
// below offLevel -> false, in between (inclusive of both bounds) -> holds
// its previous state.
type thresholdHysteresisNode struct {
	onLevel, offLevel float64
	initial           bool
	state             bool
}

func (n *thresholdHysteresisNode) Init(Clock) error {
	n.state = n.initial
	return nil
}

func (n *thresholdHysteresisNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	v := in[0].Float
	if n.onLevel == n.offLevel {
		// docs/08-node-catalog.md: "onLevel == offLevel degenerates to a
		// plain >= comparator" — no hysteresis band, so no hold state.
		n.state = v >= n.onLevel
		return []Value{BoolValue(n.state)}, nil
	}
	switch {
	case v > n.onLevel:
		n.state = true
	case v < n.offLevel:
		n.state = false
	}
	return []Value{BoolValue(n.state)}, nil
}

func (*thresholdHysteresisNode) Close() error { return nil }

// ---- scale ----

type scaleConfig struct {
	InMin  *float64 `json:"inMin"`
	InMax  *float64 `json:"inMax"`
	OutMin *float64 `json:"outMin"`
	OutMax *float64 `json:"outMax"`
	Clamp  *bool    `json:"clamp"`
}

type scaleType struct{}

func (scaleType) Name() string { return "scale" }

func (scaleType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	if _, err := parseScaleConfig(raw); err != nil {
		return PortSpec{}, err
	}
	return PortSpec{
		Inputs:  []Port{{Name: "in", Type: TypeFloat64, Required: true}},
		Outputs: []Port{{Name: "out", Type: TypeFloat64}},
	}, nil
}

type parsedScaleConfig struct {
	inMin, inMax, outMin, outMax float64
	clamp                        bool
}

func parseScaleConfig(raw json.RawMessage) (parsedScaleConfig, error) {
	var cfg scaleConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return parsedScaleConfig{}, fmt.Errorf("scale: config: %w", err)
	}
	sc := parsedScaleConfig{inMin: 0, inMax: 1023, outMin: 0, outMax: 1, clamp: true}
	if cfg.InMin != nil {
		sc.inMin = *cfg.InMin
	}
	if cfg.InMax != nil {
		sc.inMax = *cfg.InMax
	}
	if cfg.OutMin != nil {
		sc.outMin = *cfg.OutMin
	}
	if cfg.OutMax != nil {
		sc.outMax = *cfg.OutMax
	}
	if cfg.Clamp != nil {
		sc.clamp = *cfg.Clamp
	}
	if sc.inMin == sc.inMax {
		return parsedScaleConfig{}, fmt.Errorf("scale: config.inMax must differ from config.inMin")
	}
	return sc, nil
}

func (scaleType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	sc, err := parseScaleConfig(raw)
	if err != nil {
		return nil, err
	}
	return &scaleNode{sc: sc}, nil
}

// scaleNode linearly maps in from [inMin, inMax] to [outMin, outMax], per
// docs/08-node-catalog.md "scale". Stateless.
type scaleNode struct {
	sc parsedScaleConfig
}

func (*scaleNode) Init(Clock) error { return nil }

func (n *scaleNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	sc := n.sc
	out := sc.outMin + (in[0].Float-sc.inMin)*(sc.outMax-sc.outMin)/(sc.inMax-sc.inMin)
	if sc.clamp {
		lo, hi := sc.outMin, sc.outMax
		if lo > hi {
			lo, hi = hi, lo
		}
		out = clamp(out, lo, hi)
	}
	return []Value{FloatValue(out)}, nil
}

func (*scaleNode) Close() error { return nil }

// ---- and / or ----

type logicConfig struct {
	InputCount int `json:"inputCount"`
}

func parseLogicConfig(nodeTypeName string, raw json.RawMessage) (int, error) {
	var cfg logicConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return 0, fmt.Errorf("%s: config: %w", nodeTypeName, err)
	}
	n := cfg.InputCount
	if n == 0 {
		n = 2
	}
	if n < 2 {
		return 0, fmt.Errorf("%s: config.inputCount must be >= 2, got %d", nodeTypeName, n)
	}
	return n, nil
}

func logicInputPorts(n int) []Port {
	ports := make([]Port, n)
	for i := range ports {
		ports[i] = Port{Name: fmt.Sprintf("in%d", i+1), Type: TypeBool, Required: true}
	}
	return ports
}

type andType struct{}

func (andType) Name() string { return "and" }

func (andType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	n, err := parseLogicConfig("and", raw)
	if err != nil {
		return PortSpec{}, err
	}
	return PortSpec{Inputs: logicInputPorts(n), Outputs: []Port{{Name: "out", Type: TypeBool}}}, nil
}

func (andType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	if _, err := parseLogicConfig("and", raw); err != nil {
		return nil, err
	}
	return &andNode{}, nil
}

type andNode struct{}

func (*andNode) Init(Clock) error { return nil }
func (*andNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	out := true
	for _, v := range in {
		if !v.Bool {
			out = false
			break
		}
	}
	return []Value{BoolValue(out)}, nil
}
func (*andNode) Close() error { return nil }

type orType struct{}

func (orType) Name() string { return "or" }

func (orType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	n, err := parseLogicConfig("or", raw)
	if err != nil {
		return PortSpec{}, err
	}
	return PortSpec{Inputs: logicInputPorts(n), Outputs: []Port{{Name: "out", Type: TypeBool}}}, nil
}

func (orType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	if _, err := parseLogicConfig("or", raw); err != nil {
		return nil, err
	}
	return &orNode{}, nil
}

type orNode struct{}

func (*orNode) Init(Clock) error { return nil }
func (*orNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	out := false
	for _, v := range in {
		if v.Bool {
			out = true
			break
		}
	}
	return []Value{BoolValue(out)}, nil
}
func (*orNode) Close() error { return nil }

// ---- not ----

type notType struct{}

func (notType) Name() string { return "not" }

func (notType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	if err := decodeStrict(raw, &struct{}{}); err != nil {
		return PortSpec{}, fmt.Errorf("not: config: %w", err)
	}
	return PortSpec{
		Inputs:  []Port{{Name: "in", Type: TypeBool, Required: true}},
		Outputs: []Port{{Name: "out", Type: TypeBool}},
	}, nil
}

func (notType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	if err := decodeStrict(raw, &struct{}{}); err != nil {
		return nil, fmt.Errorf("not: config: %w", err)
	}
	return &notNode{}, nil
}

type notNode struct{}

func (*notNode) Init(Clock) error { return nil }
func (*notNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	return []Value{BoolValue(!in[0].Bool)}, nil
}
func (*notNode) Close() error { return nil }

// ---- delay / debounce shared "kind" config parsing ----

// kindConfig is the shared shape of delay/debounce's config.kind +
// config.initial, per docs/08-node-catalog.md: both are "polymorphic over
// kind (bool or float)" (docs/07-pipeline-engine.md "Type checking on edge
// connect"), with their declared port type fixed by config.kind.
type kindConfig struct {
	Kind    string          `json:"kind"`
	Initial json.RawMessage `json:"initial"`
}

func parseKindAndInitial(nodeTypeName string, k string, initialRaw json.RawMessage) (PortType, Value, error) {
	kind := k
	if kind == "" {
		kind = "bool"
	}
	switch kind {
	case "bool":
		b := false
		if len(initialRaw) > 0 {
			if err := json.Unmarshal(initialRaw, &b); err != nil {
				return 0, Value{}, fmt.Errorf("%s: config.initial must be a bool for kind=bool: %w", nodeTypeName, err)
			}
		}
		return TypeBool, BoolValue(b), nil
	case "float":
		f := 0.0
		if len(initialRaw) > 0 {
			if err := json.Unmarshal(initialRaw, &f); err != nil {
				return 0, Value{}, fmt.Errorf("%s: config.initial must be a number for kind=float: %w", nodeTypeName, err)
			}
		}
		return TypeFloat64, FloatValue(f), nil
	default:
		return 0, Value{}, fmt.Errorf("%s: config.kind must be \"bool\" or \"float\", got %q", nodeTypeName, kind)
	}
}

// ---- delay ----

type delayConfig struct {
	kindConfig
	DelayMs *int `json:"delayMs"`
}

type delayType struct{}

func (delayType) Name() string { return "delay" }

func (delayType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	pt, _, _, err := parseDelayConfig(raw)
	if err != nil {
		return PortSpec{}, err
	}
	return PortSpec{
		Inputs:  []Port{{Name: "in", Type: pt, Required: true}},
		Outputs: []Port{{Name: "out", Type: pt}},
	}, nil
}

func parseDelayConfig(raw json.RawMessage) (pt PortType, initial Value, delayMs int, err error) {
	var cfg delayConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return 0, Value{}, 0, fmt.Errorf("delay: config: %w", err)
	}
	pt, initial, err = parseKindAndInitial("delay", cfg.Kind, cfg.Initial)
	if err != nil {
		return 0, Value{}, 0, err
	}
	delayMs = 1000
	if cfg.DelayMs != nil {
		delayMs = *cfg.DelayMs
	}
	if delayMs < 0 {
		return 0, Value{}, 0, fmt.Errorf("delay: config.delayMs must be >= 0, got %d", delayMs)
	}
	return pt, initial, delayMs, nil
}

// maxDelayTicks caps delay's tick-indexed queue length so a misconfigured
// (or malicious) config.delayMs can't allocate an unbounded queue — an
// arbitrary but sane cap (~1000 ticks/sec * 10s worth of ticks at a 1ms
// tick, or e.g. 10000 ticks at a 1s tickMs = ~2.8h of delay).
const maxDelayTicks = 10000

func (delayType) NewInstance(raw json.RawMessage, deps NodeDeps) (Node, error) {
	_, initial, delayMs, err := parseDelayConfig(raw)
	if err != nil {
		return nil, err
	}
	tickMs := deps.TickMs
	if tickMs <= 0 {
		tickMs = defaultTickMs
	}
	// "Rounded down to the nearest multiple of the pipeline's tickMs
	// internally" (docs/08-node-catalog.md), then expressed as a tick
	// count for the tick-indexed queue.
	ticks := delayMs / tickMs
	if ticks > maxDelayTicks {
		return nil, fmt.Errorf("delay: config.delayMs (%d) / tickMs (%d) = %d ticks, exceeds the max of %d ticks", delayMs, tickMs, ticks, maxDelayTicks)
	}
	return &delayNode{ticks: ticks, initial: initial, queue: make([]Value, ticks)}, nil
}

// delayNode implements delay's tick-indexed FIFO queue, per
// docs/08-node-catalog.md "delay": every tick's input is enqueued, and
// out on a given tick is whatever was enqueued `ticks` ticks earlier. A
// zero (or sub-tick) delayMs makes it a same-tick passthrough.
//
// queue is a fixed-size ring buffer (len == ticks) so steady-state Eval
// allocates nothing: head is the index of the oldest pending value, which
// is overwritten in place with the new value and head advances.
type delayNode struct {
	ticks   int
	initial Value
	queue   []Value // len == ticks
	head    int     // index of the oldest pending value
}

func (n *delayNode) Init(Clock) error {
	for i := range n.queue {
		n.queue[i] = n.initial
	}
	n.head = 0
	return nil
}

func (n *delayNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	v := in[0]
	if n.ticks <= 0 {
		return []Value{v}, nil
	}
	out := n.queue[n.head]
	n.queue[n.head] = v
	n.head++
	if n.head == n.ticks {
		n.head = 0
	}
	return []Value{out}, nil
}

func (*delayNode) Close() error { return nil }

// ---- debounce ----

type debounceConfig struct {
	kindConfig
	StableMs *int `json:"stableMs"`
}

type debounceType struct{}

func (debounceType) Name() string { return "debounce" }

func (debounceType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	pt, _, _, err := parseDebounceConfig(raw)
	if err != nil {
		return PortSpec{}, err
	}
	return PortSpec{
		Inputs:  []Port{{Name: "in", Type: pt, Required: true}},
		Outputs: []Port{{Name: "out", Type: pt}},
	}, nil
}

func parseDebounceConfig(raw json.RawMessage) (pt PortType, initial Value, stableMs int, err error) {
	var cfg debounceConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return 0, Value{}, 0, fmt.Errorf("debounce: config: %w", err)
	}
	pt, initial, err = parseKindAndInitial("debounce", cfg.Kind, cfg.Initial)
	if err != nil {
		return 0, Value{}, 0, err
	}
	stableMs = 50
	if cfg.StableMs != nil {
		stableMs = *cfg.StableMs
	}
	if stableMs < 0 {
		return 0, Value{}, 0, fmt.Errorf("debounce: config.stableMs must be >= 0, got %d", stableMs)
	}
	return pt, initial, stableMs, nil
}

func (debounceType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	_, initial, stableMs, err := parseDebounceConfig(raw)
	if err != nil {
		return nil, err
	}
	return &debounceNode{stableDur: time.Duration(stableMs) * time.Millisecond, initial: initial}, nil
}

// debounceNode passes a value through only after it has held stable for
// stableDur, per docs/08-node-catalog.md "debounce". Uses the Clock handed
// to Init (never TickContext.Now) for its stability timer, per
// docs/07-pipeline-engine.md "Determinism and testing hooks".
type debounceNode struct {
	stableDur time.Duration
	initial   Value

	clk Clock

	output       Value
	candidate    Value
	candidateSet bool
	changeAt     time.Time
}

func (n *debounceNode) Init(clk Clock) error {
	n.clk = clk
	n.output = n.initial
	n.candidateSet = false
	return nil
}

func (n *debounceNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	now := n.clk.Now()
	v := in[0]
	if !n.candidateSet || !v.Equal(n.candidate) {
		// A change (or the very first observed value) restarts the
		// stability timer, discarding whatever was previously the
		// candidate — docs/08-node-catalog.md: "if in changes again
		// before stableMs elapses, the candidate value is discarded and
		// the timer restarts against the newest value".
		n.candidate = v
		n.candidateSet = true
		n.changeAt = now
	}
	if !now.Before(n.changeAt.Add(n.stableDur)) {
		n.output = n.candidate
	}
	return []Value{n.output}, nil
}

func (*debounceNode) Close() error { return nil }

// ---- edge-detect ----

type edgeDetectConfig struct {
	Edge string `json:"edge"`
}

type edgeDetectType struct{}

func (edgeDetectType) Name() string { return "edge-detect" }

func (edgeDetectType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	if _, err := parseEdgeMode(raw); err != nil {
		return PortSpec{}, err
	}
	return PortSpec{
		Inputs:  []Port{{Name: "in", Type: TypeBool, Required: true}},
		Outputs: []Port{{Name: "out", Type: TypeBool}},
	}, nil
}

func parseEdgeMode(raw json.RawMessage) (string, error) {
	var cfg edgeDetectConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return "", fmt.Errorf("edge-detect: config: %w", err)
	}
	mode := cfg.Edge
	if mode == "" {
		mode = "rising"
	}
	switch mode {
	case "rising", "falling", "both":
	default:
		return "", fmt.Errorf("edge-detect: config.edge %q not one of rising/falling/both", cfg.Edge)
	}
	return mode, nil
}

func (edgeDetectType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	mode, err := parseEdgeMode(raw)
	if err != nil {
		return nil, err
	}
	return &edgeDetectNode{mode: mode}, nil
}

// edgeDetectNode emits a one-tick pulse on the configured transition, per
// docs/08-node-catalog.md "edge-detect". No pulse fires on tick one: the
// node is primed as if the previous value equaled the first observed
// value.
type edgeDetectNode struct {
	mode    string
	prev    bool
	hasPrev bool
}

func (n *edgeDetectNode) Init(Clock) error {
	n.hasPrev = false
	return nil
}

func (n *edgeDetectNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	cur := in[0].Bool
	if !n.hasPrev {
		n.hasPrev = true
		n.prev = cur
		return []Value{BoolValue(false)}, nil
	}
	var pulse bool
	switch n.mode {
	case "rising":
		pulse = !n.prev && cur
	case "falling":
		pulse = n.prev && !cur
	case "both":
		pulse = n.prev != cur
	}
	n.prev = cur
	return []Value{BoolValue(pulse)}, nil
}

func (*edgeDetectNode) Close() error { return nil }
