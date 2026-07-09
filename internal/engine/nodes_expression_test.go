package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// TestExpression_LDRCompareEquivalence covers docs/08-node-catalog.md
// "expression": a single expression node folding the README's LDR->LED
// compare (op=lt, threshold=300) into `a < 300`, matching compare's
// behavior exactly.
func TestExpression_LDRCompareEquivalence(t *testing.T) {
	n := mustNode(t, expressionType{}, map[string]any{
		"expression": "a < 300",
		"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
		"outputType": "bool",
	}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))

	tests := []struct {
		in   float64
		want bool
	}{
		{900, false},
		{300, false},
		{299.999, true},
		{200, true},
	}
	for _, tt := range tests {
		out, err := n.Eval(TickContext{}, []Value{FloatValue(tt.in)})
		mustNotErr(t, err)
		if out[0].Bool != tt.want {
			t.Fatalf("a=%v: out=%v, want %v", tt.in, out[0].Bool, tt.want)
		}
	}
}

// TestExpression_BoolLogic covers `a && !b` over bool inputs, port order
// matching declaration order (a then b).
func TestExpression_BoolLogic(t *testing.T) {
	n := mustNode(t, expressionType{}, map[string]any{
		"expression": "a && !b",
		"inputs": []map[string]any{
			{"name": "a", "type": "bool"},
			{"name": "b", "type": "bool"},
		},
		"outputType": "bool",
	}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))

	tests := []struct {
		a, b, want bool
	}{
		{true, false, true},
		{true, true, false},
		{false, false, false},
		{false, true, false},
	}
	for _, tt := range tests {
		out, err := n.Eval(TickContext{}, []Value{BoolValue(tt.a), BoolValue(tt.b)})
		mustNotErr(t, err)
		if out[0].Bool != tt.want {
			t.Fatalf("a=%v b=%v: out=%v, want %v", tt.a, tt.b, out[0].Bool, tt.want)
		}
	}
}

// TestExpression_Arithmetic covers `(a*0.5) + b` over mixed float inputs.
func TestExpression_Arithmetic(t *testing.T) {
	n := mustNode(t, expressionType{}, map[string]any{
		"expression": "(a*0.5) + b",
		"inputs": []map[string]any{
			{"name": "a", "type": "float64"},
			{"name": "b", "type": "float64"},
		},
		"outputType": "float64",
	}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))

	out, err := n.Eval(TickContext{}, []Value{FloatValue(10), FloatValue(3)})
	mustNotErr(t, err)
	if out[0].Float != 8 {
		t.Fatalf("(10*0.5)+3 = %v, want 8", out[0].Float)
	}
}

// TestExpression_PortOrderMatchesDeclaration verifies input ports are
// exposed in config.inputs declaration order, not sorted — per
// docs/08-node-catalog.md "expression": "IN THE DECLARED ORDER (not
// sorted)".
func TestExpression_PortOrderMatchesDeclaration(t *testing.T) {
	ps, err := expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "z + a + m",
		"inputs": []map[string]any{
			{"name": "z", "type": "float64"},
			{"name": "a", "type": "float64"},
			{"name": "m", "type": "float64"},
		},
		"outputType": "float64",
	}), nil)
	mustNotErr(t, err)
	want := []string{"z", "a", "m"}
	if len(ps.Inputs) != len(want) {
		t.Fatalf("got %d input ports, want %d", len(ps.Inputs), len(want))
	}
	for i, name := range want {
		if ps.Inputs[i].Name != name {
			t.Fatalf("input port %d = %q, want %q", i, ps.Inputs[i].Name, name)
		}
	}
}

// ---- config validation errors ----

func TestExpression_UndeclaredIdentIsValidationError(t *testing.T) {
	_, err := expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "a < b",
		"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
		"outputType": "bool",
	}), nil)
	if err == nil {
		t.Fatal("expected validation error for undeclared identifier b")
	}
}

func TestExpression_UnusedDeclaredInputIsValidationError(t *testing.T) {
	_, err := expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "a < 300",
		"inputs": []map[string]any{
			{"name": "a", "type": "float64"},
			{"name": "b", "type": "float64"},
		},
		"outputType": "bool",
	}), nil)
	if err == nil {
		t.Fatal("expected validation error for unused declared input b")
	}
}

func TestExpression_TypeMismatchVsOutputTypeIsValidationError(t *testing.T) {
	_, err := expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "a < 300",
		"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
		"outputType": "float64",
	}), nil)
	if err == nil {
		t.Fatal("expected validation error: expression yields bool but outputType is float64")
	}
}

func TestExpression_DuplicateInputNamesIsValidationError(t *testing.T) {
	_, err := expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "a < b",
		"inputs": []map[string]any{
			{"name": "a", "type": "float64"},
			{"name": "a", "type": "float64"},
		},
		"outputType": "bool",
	}), nil)
	if err == nil {
		t.Fatal("expected validation error for duplicate input name")
	}
}

func TestExpression_BadTypeStringIsValidationError(t *testing.T) {
	_, err := expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "a < 300",
		"inputs":     []map[string]any{{"name": "a", "type": "int"}},
		"outputType": "bool",
	}), nil)
	if err == nil {
		t.Fatal("expected validation error for bad input type string")
	}

	_, err = expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "a",
		"inputs":     []map[string]any{{"name": "a", "type": "bool"}},
		"outputType": "int",
	}), nil)
	if err == nil {
		t.Fatal("expected validation error for bad outputType string")
	}
}

func TestExpression_EmptyExpressionIsValidationError(t *testing.T) {
	_, err := expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "",
		"outputType": "bool",
	}), nil)
	if err == nil {
		t.Fatal("expected validation error for empty expression")
	}
}

func TestExpression_TooManyInputsIsValidationError(t *testing.T) {
	inputs := make([]map[string]any, maxExpressionInputs+1)
	for i := range inputs {
		inputs[i] = map[string]any{"name": "x" + string(rune('a'+i)), "type": "float64"}
	}
	_, err := expressionType{}.Ports(rawJSON(t, map[string]any{
		"expression": "1",
		"inputs":     inputs,
		"outputType": "float64",
	}), nil)
	if err == nil {
		t.Fatal("expected validation error for > 32 inputs")
	}
}

// TestExpression_ZeroAllocEval asserts steady-state Eval allocates nothing,
// per the frozen internal/expr.Program.Eval contract ("zero-alloc") and the
// expressionNode.buf being allocated once in NewInstance.
func TestExpression_ZeroAllocEval(t *testing.T) {
	n := mustNode(t, expressionType{}, map[string]any{
		"expression": "(a*0.5) + b",
		"inputs": []map[string]any{
			{"name": "a", "type": "float64"},
			{"name": "b", "type": "float64"},
		},
		"outputType": "float64",
	}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))

	in := []Value{FloatValue(10), FloatValue(3)}
	allocs := testing.AllocsPerRun(1000, func() {
		if _, err := n.Eval(TickContext{}, in); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Eval allocs/op = %v, want 0", allocs)
	}
}

// TestExpression_TooManyInputsNoPanic covers the length mismatch between
// Eval's in []Value and the node's reused buf: a caller passing more values
// than the node has declared inputs must return an error, not panic
// indexing n.buf, mirroring internal/expr.Program.Eval's ErrArgCount style.
func TestExpression_TooManyInputsNoPanic(t *testing.T) {
	n := mustNode(t, expressionType{}, map[string]any{
		"expression": "a < 300",
		"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
		"outputType": "bool",
	}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))

	_, err := n.Eval(TickContext{}, []Value{FloatValue(1), FloatValue(2)})
	if err == nil {
		t.Fatal("expected error for too many inputs, got nil")
	}
}

// TestExpression_TooFewInputsNoPanic covers the symmetric under-supply
// case, which would otherwise leave n.buf partially stale rather than
// panicking, but is still rejected explicitly for the same reason.
func TestExpression_TooFewInputsNoPanic(t *testing.T) {
	n := mustNode(t, expressionType{}, map[string]any{
		"expression": "a && b",
		"inputs": []map[string]any{
			{"name": "a", "type": "bool"},
			{"name": "b", "type": "bool"},
		},
		"outputType": "bool",
	}, NodeDeps{})
	mustNotErr(t, n.Init(newFakeClock()))

	_, err := n.Eval(TickContext{}, []Value{BoolValue(true)})
	if err == nil {
		t.Fatal("expected error for too few inputs, got nil")
	}
}

// ---- runtime error / scheduler integration ----

// TestExpression_DivByZeroAutoDisablesAfterThreeFailures covers
// docs/07-pipeline-engine.md's per-tick error policy: a runtime expression
// error (division by zero) fails that tick without stopping the scheduler,
// and three consecutive failures auto-disable the pipeline, exactly like
// any other node's runtime error.
func TestExpression_DivByZeroAutoDisablesAfterThreeFailures(t *testing.T) {
	devices := devicesByID(
		model.Device{ID: "sw", Role: model.RoleAnalogIn},
		model.Device{ID: "led3", Role: model.RolePWMOut},
	)
	p := model.Pipeline{
		ID:      "expr-div-zero",
		Enabled: true,
		TickMs:  20,
		Nodes: []model.Node{
			node(t, "n1", "device-read", map[string]any{"deviceId": "sw"}),
			node(t, "n2", "expression", map[string]any{
				"expression": "1.0 / a",
				"inputs":     []map[string]any{{"name": "a", "type": "float64"}},
				"outputType": "float64",
			}),
			node(t, "n3", "device-write", map[string]any{"deviceId": "led3"}),
		},
		Edges: []model.Edge{
			edge("n1.out", "n2.a"),
			edge("n2.out", "n3.in"),
		},
	}

	io := newFakeIO()
	io.SetRead("sw", FloatValue(0)) // a == 0 every tick -> 1.0/a always errors

	clk := newFakeClock()
	tf, tickers := manualTickerFactory()
	mgr := NewManager(WithClock(clk), WithTickerFactory(tf))
	t.Cleanup(mgr.StopAll)

	mustNotErr(t, mgr.Start(p, devices, io))
	mt := <-tickers

	for i := 1; i <= 3; i++ {
		mt.Fire(time.Unix(int64(i), 0))
	}
	st := waitForState(t, mgr, p.ID, StateDisabled)
	if st.ConsecutiveFailures != 3 {
		t.Fatalf("ConsecutiveFailures = %d, want 3", st.ConsecutiveFailures)
	}
	if st.LastErrorNode != "n2" {
		t.Fatalf("LastErrorNode = %q, want n2", st.LastErrorNode)
	}
	if st.LastError == nil {
		t.Fatal("LastError = nil, want non-nil")
	}
}

// TestExpression_PortsRejectsConstantErrors guards the dry-run validate
// path: constant folding errors live in expr.Compile, so Ports() (what
// Validate calls) must compile too, or `a / 0` only fails at enable time.
func TestExpression_PortsRejectsConstantErrors(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"div by zero", "a / 0"},
		{"mod by zero", "a % 0"},
		{"constant overflow", "a + 1e300 * 1e300"},
		{"sqrt domain", "a + sqrt(0 - 1)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"expression":"` + tc.expr +
				`","inputs":[{"name":"a","type":"float64"}],"outputType":"float64"}`)
			if _, err := (expressionType{}).Ports(raw, nil); err == nil {
				t.Fatalf("Ports(%q) = nil error, want a compile error", tc.expr)
			}
			if _, err := (expressionType{}).NewInstance(raw, NodeDeps{}); err == nil {
				t.Fatalf("NewInstance(%q) = nil error, want a compile error", tc.expr)
			}
		})
	}
}
