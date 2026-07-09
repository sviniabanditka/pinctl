package engine

import (
	"encoding/json"
	"fmt"

	"github.com/sviniabanditka/pinctl/internal/expr"
	"github.com/sviniabanditka/pinctl/internal/model"
)

// maxExpressionInputs caps the number of declared inputs on an "expression"
// node, mirroring delay's maxDelayTicks-style sane cap on user-controlled
// sizing: a config-driven port count with no cap could otherwise be used to
// blow up port/edge bookkeeping in validate().
const maxExpressionInputs = 32

// ---- expression ----

type expressionInputConfig struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type expressionConfig struct {
	Expression string                  `json:"expression"`
	Inputs     []expressionInputConfig `json:"inputs"`
	OutputType string                  `json:"outputType"`
}

type expressionType struct{}

func (expressionType) Name() string { return "expression" }

// exprPortType maps the JSON type string used in both config.inputs[].type
// and config.outputType to the engine's PortType and internal/expr's Type.
func exprPortType(s string) (PortType, expr.Type, bool) {
	switch s {
	case "bool":
		return TypeBool, expr.TypeBool, true
	case "float64":
		return TypeFloat64, expr.TypeFloat, true
	default:
		return 0, 0, false
	}
}

// parsedExpressionConfig is the fully-validated result of decoding an
// "expression" node's config, shared between Ports (static pipeline
// validation, per docs/07-pipeline-engine.md) and NewInstance (which
// additionally compiles parsed into a runnable expr.Program — the "compile
// time, not per tick" step).
type parsedExpressionConfig struct {
	ports       []Port // input ports, in declared order (not sorted)
	names       []string
	types       []expr.Type
	outputType  PortType
	outExprType expr.Type
	parsed      *expr.Expr
	prog        *expr.Program
}

func parseExpressionConfig(raw json.RawMessage) (parsedExpressionConfig, error) {
	var cfg expressionConfig
	if err := decodeStrict(raw, &cfg); err != nil {
		return parsedExpressionConfig{}, fmt.Errorf("expression: config: %w", err)
	}
	if cfg.Expression == "" {
		return parsedExpressionConfig{}, fmt.Errorf("expression: config.expression must not be empty")
	}
	if len(cfg.Inputs) > maxExpressionInputs {
		return parsedExpressionConfig{}, fmt.Errorf("expression: config.inputs has %d entries, exceeds the max of %d", len(cfg.Inputs), maxExpressionInputs)
	}

	seen := make(map[string]bool, len(cfg.Inputs))
	ports := make([]Port, 0, len(cfg.Inputs))
	names := make([]string, 0, len(cfg.Inputs))
	types := make([]expr.Type, 0, len(cfg.Inputs))
	env := make(map[string]expr.Type, len(cfg.Inputs))

	for _, in := range cfg.Inputs {
		if in.Name == "" {
			return parsedExpressionConfig{}, fmt.Errorf("expression: config.inputs entry has an empty name")
		}
		if seen[in.Name] {
			return parsedExpressionConfig{}, fmt.Errorf("expression: config.inputs has a duplicate name %q", in.Name)
		}
		seen[in.Name] = true
		pt, et, ok := exprPortType(in.Type)
		if !ok {
			return parsedExpressionConfig{}, fmt.Errorf("expression: config.inputs[%q].type must be \"float64\" or \"bool\", got %q", in.Name, in.Type)
		}
		ports = append(ports, Port{Name: in.Name, Type: pt, Required: true})
		names = append(names, in.Name)
		types = append(types, et)
		env[in.Name] = et
	}

	outPt, outEt, ok := exprPortType(cfg.OutputType)
	if !ok {
		return parsedExpressionConfig{}, fmt.Errorf("expression: config.outputType must be \"float64\" or \"bool\", got %q", cfg.OutputType)
	}

	parsed, err := expr.Parse(cfg.Expression)
	if err != nil {
		return parsedExpressionConfig{}, fmt.Errorf("expression: config.expression: %w", err)
	}

	idents := parsed.Idents()
	for _, id := range idents {
		if !seen[id] {
			return parsedExpressionConfig{}, fmt.Errorf("expression: config.expression references undeclared identifier %q", id)
		}
	}
	// Reject declared-but-unused inputs too: a dangling required input port
	// (the expression never reads it) would otherwise sit in PortSpec.Inputs
	// as a required port with no way for the expression to consume it,
	// which just relocates the problem to a confusing "required input has
	// no incoming edge" validation error instead of a clear one here.
	usedIdent := make(map[string]bool, len(idents))
	for _, id := range idents {
		usedIdent[id] = true
	}
	for _, name := range names {
		if !usedIdent[name] {
			return parsedExpressionConfig{}, fmt.Errorf("expression: config.inputs[%q] is declared but not used in config.expression", name)
		}
	}

	resultType, err := parsed.Check(env)
	if err != nil {
		return parsedExpressionConfig{}, fmt.Errorf("expression: config.expression: %w", err)
	}
	if resultType != outEt {
		return parsedExpressionConfig{}, fmt.Errorf("expression: config.expression yields %s but config.outputType is %s", resultType, outPt)
	}

	// Compile here rather than only in NewInstance: constant folding rejects
	// expressions whose identifier-free subexpressions are undefined or
	// overflow (`a / 0`, `a + 1e300 * 1e300`), and Ports() is what the
	// dry-run validate endpoint calls. Compiling in both paths would let
	// those errors escape validation and surface only at enable time.
	prog, err := parsed.Compile(names, types)
	if err != nil {
		return parsedExpressionConfig{}, fmt.Errorf("expression: config.expression: %w", err)
	}

	return parsedExpressionConfig{
		ports:       ports,
		names:       names,
		types:       types,
		outputType:  outPt,
		outExprType: outEt,
		parsed:      parsed,
		prog:        prog,
	}, nil
}

func (expressionType) Ports(raw json.RawMessage, _ map[string]model.Device) (PortSpec, error) {
	pc, err := parseExpressionConfig(raw)
	if err != nil {
		return PortSpec{}, err
	}
	return PortSpec{
		Inputs:  pc.ports,
		Outputs: []Port{{Name: "out", Type: pc.outputType}},
	}, nil
}

func (expressionType) NewInstance(raw json.RawMessage, _ NodeDeps) (Node, error) {
	pc, err := parseExpressionConfig(raw)
	if err != nil {
		return nil, err
	}
	if pc.prog.ResultType() != pc.outExprType {
		return nil, fmt.Errorf("expression: config.expression yields %s but config.outputType is %s", pc.prog.ResultType(), pc.outputType)
	}
	return &expressionNode{
		prog: pc.prog,
		// Allocated once here (NewInstance, compile time) and reused every
		// tick in Eval, so steady-state evaluation is zero-alloc.
		buf: make([]expr.Value, len(pc.ports)),
	}, nil
}

// expressionNode evaluates a compiled internal/expr.Program against its
// input ports every tick, per docs/08-node-catalog.md "expression".
// Stateless beyond the compiled program and its reused input buffer.
type expressionNode struct {
	prog   *expr.Program
	buf    []expr.Value // len == number of declared inputs, reused every tick
	outBuf [1]Value     // backs the returned output slice, reused every tick
}

func (*expressionNode) Init(Clock) error { return nil }

func (n *expressionNode) Eval(_ TickContext, in []Value) ([]Value, error) {
	if len(in) != len(n.buf) {
		return nil, fmt.Errorf("expression: wrong number of inputs: got %d, want %d", len(in), len(n.buf))
	}
	for i, v := range in {
		n.buf[i] = engineValueToExpr(v)
	}
	result, err := n.prog.Eval(n.buf)
	if err != nil {
		return nil, fmt.Errorf("expression: %w", err)
	}
	n.outBuf[0] = exprValueToEngine(result)
	return n.outBuf[:], nil
}

func (*expressionNode) Close() error { return nil }

// engineValueToExpr converts an engine.Value to the internal/expr.Value it
// carries, selecting the field by v.Type exactly as Value.Equal does.
func engineValueToExpr(v Value) expr.Value {
	if v.Type == TypeBool {
		return expr.BoolValue(v.Bool)
	}
	return expr.FloatValue(v.Float)
}

// exprValueToEngine is engineValueToExpr's inverse.
func exprValueToEngine(v expr.Value) Value {
	if v.Type == expr.TypeBool {
		return BoolValue(v.Bool)
	}
	return FloatValue(v.Float)
}
