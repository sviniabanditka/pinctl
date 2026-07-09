package expr

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// ---- reference tree-walking evaluator, used only in tests to cross-check
// the compiled Program against a naive independent implementation. ----

func refEval(t *testing.T, n *astNode, vals map[string]Value) Value {
	t.Helper()
	v, err := refEvalErr(n, vals)
	if err != nil {
		t.Fatalf("refEval: unexpected error: %v", err)
	}
	return v
}

func refEvalErr(n *astNode, vals map[string]Value) (Value, error) {
	switch n.kind {
	case kNum:
		return FloatValue(n.numVal), nil
	case kBool:
		return BoolValue(n.boolVal), nil
	case kIdent:
		return vals[n.name], nil
	case kNot:
		v, err := refEvalErr(n.a, vals)
		if err != nil {
			return Value{}, err
		}
		return BoolValue(!v.Bool), nil
	case kNeg:
		v, err := refEvalErr(n.a, vals)
		if err != nil {
			return Value{}, err
		}
		return FloatValue(-v.Float), nil
	case kBinary:
		a, err := refEvalErr(n.a, vals)
		if err != nil {
			return Value{}, err
		}
		b, err := refEvalErr(n.b, vals)
		if err != nil {
			return Value{}, err
		}
		switch n.op {
		case opOr:
			return BoolValue(a.Bool || b.Bool), nil
		case opAnd:
			return BoolValue(a.Bool && b.Bool), nil
		case opEq:
			if a.Type == TypeBool {
				return BoolValue(a.Bool == b.Bool), nil
			}
			return BoolValue(a.Float == b.Float), nil
		case opNeq:
			if a.Type == TypeBool {
				return BoolValue(a.Bool != b.Bool), nil
			}
			return BoolValue(a.Float != b.Float), nil
		case opLt:
			return BoolValue(a.Float < b.Float), nil
		case opLe:
			return BoolValue(a.Float <= b.Float), nil
		case opGt:
			return BoolValue(a.Float > b.Float), nil
		case opGe:
			return BoolValue(a.Float >= b.Float), nil
		case opAdd:
			return FloatValue(a.Float + b.Float), nil
		case opSub:
			return FloatValue(a.Float - b.Float), nil
		case opMul:
			return FloatValue(a.Float * b.Float), nil
		case opDiv:
			if b.Float == 0 {
				return Value{}, ErrDivByZero
			}
			return FloatValue(a.Float / b.Float), nil
		case opMod:
			if b.Float == 0 {
				return Value{}, ErrDivByZero
			}
			return FloatValue(math.Mod(a.Float, b.Float)), nil
		}
	case kCall:
		args := make([]float64, len(n.args))
		for i, an := range n.args {
			v, err := refEvalErr(an, vals)
			if err != nil {
				return Value{}, err
			}
			args[i] = v.Float
		}
		switch n.name {
		case "min":
			return FloatValue(math.Min(args[0], args[1])), nil
		case "max":
			return FloatValue(math.Max(args[0], args[1])), nil
		case "abs":
			return FloatValue(math.Abs(args[0])), nil
		case "clamp":
			f := args[0]
			if f < args[1] {
				f = args[1]
			}
			if f > args[2] {
				f = args[2]
			}
			return FloatValue(f), nil
		case "floor":
			return FloatValue(math.Floor(args[0])), nil
		case "ceil":
			return FloatValue(math.Ceil(args[0])), nil
		case "round":
			return FloatValue(math.Round(args[0])), nil
		case "sqrt":
			if args[0] < 0 {
				return Value{}, ErrSqrtDomain
			}
			return FloatValue(math.Sqrt(args[0])), nil
		}
	}
	panic("refEvalErr: unhandled node")
}

func mustParse(t *testing.T, src string) *Expr {
	t.Helper()
	e, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): unexpected error: %v", src, err)
	}
	return e
}

func compileWithEnv(t *testing.T, e *Expr, env map[string]Type) (*Program, []string) {
	t.Helper()
	order := make([]string, 0, len(env))
	types := make([]Type, 0, len(env))
	// deterministic order
	for _, name := range e.Idents() {
		if _, ok := env[name]; ok {
			order = append(order, name)
			types = append(types, env[name])
		}
	}
	p, err := e.Compile(order, types)
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}
	return p, order
}

// ---- precedence / associativity ----

func TestPrecedenceAndAssociativity(t *testing.T) {
	cases := []struct {
		src  string
		want Value
	}{
		{"1+2*3", FloatValue(7)},
		{"(1+2)*3", FloatValue(9)},
		{"2*3+1", FloatValue(7)},
		{"10-3-2", FloatValue(5)}, // left-assoc: (10-3)-2
		{"2*3%4", FloatValue(2)},  // (2*3) % 4 = 6%4=2
		{"1<2 == true", BoolValue(true)},
		{"!true && false", BoolValue(false)},
		{"!(true && false)", BoolValue(true)},
		{"true || false && false", BoolValue(true)}, // && binds tighter than ||
		{"-2+3", FloatValue(1)},
		{"-(2+3)", FloatValue(-5)},
		{"1==1 && 2==2", BoolValue(true)},
	}
	for _, c := range cases {
		e := mustParse(t, c.src)
		p, err := e.Compile(nil, nil)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.src, err)
		}
		got, err := p.Eval(nil)
		if err != nil {
			t.Fatalf("Eval(%q): %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("%q = %+v, want %+v", c.src, got, c.want)
		}
	}
}

func TestIdentExpressions(t *testing.T) {
	e := mustParse(t, "a<1 == b")
	env := map[string]Type{"a": TypeFloat, "b": TypeBool}
	p, order := compileWithEnv(t, e, env)
	in := make([]Value, len(order))
	for i, name := range order {
		switch name {
		case "a":
			in[i] = FloatValue(0.5)
		case "b":
			in[i] = BoolValue(true)
		}
	}
	got, err := p.Eval(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != BoolValue(true) {
		t.Errorf("got %+v, want true", got)
	}
}

// ---- builtins ----

func TestBuiltins(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"min(3,5)", 3},
		{"max(3,5)", 5},
		{"abs(-4)", 4},
		{"clamp(5,0,3)", 3},
		{"clamp(-1,0,3)", 0},
		{"clamp(2,0,3)", 2},
		{"floor(1.9)", 1},
		{"ceil(1.1)", 2},
		{"round(1.5)", 2},
		{"sqrt(9)", 3},
	}
	for _, c := range cases {
		e := mustParse(t, c.src)
		p, err := e.Compile(nil, nil)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.src, err)
		}
		got, err := p.Eval(nil)
		if err != nil {
			t.Fatalf("Eval(%q): %v", c.src, err)
		}
		if got.Float != c.want {
			t.Errorf("%q = %v, want %v", c.src, got.Float, c.want)
		}
	}
}

func TestBuiltinArityAndTypeErrors(t *testing.T) {
	cases := []string{
		"min(1)",
		"min(1,2,3)",
		"abs(1,2)",
		"sqrt()",
		"clamp(1,2)",
	}
	for _, src := range cases {
		e := mustParse(t, src)
		if _, err := e.Compile(nil, nil); err == nil {
			t.Errorf("Compile(%q): expected error, got nil", src)
		}
	}

	// bool arg where float expected
	e := mustParse(t, "sqrt(a)")
	_, err := e.Compile([]string{"a"}, []Type{TypeBool})
	if err == nil {
		t.Fatal("expected error for bool arg to sqrt")
	}
}

// TestDivModByZero covers the fully-constant case: "1/0" and "1%0" have no
// identifiers, so they are eagerly folded at Compile time, and per the
// documented policy (see foldConst) a constant subexpression that is
// undefined must be a Compile error, not deferred to Eval.
func TestDivModByZero(t *testing.T) {
	cases := []string{"1/0", "1%0"}
	for _, src := range cases {
		e := mustParse(t, src)
		_, err := e.Compile(nil, nil)
		if err == nil {
			t.Fatalf("Compile(%q): expected error, got nil", src)
		}
		if !strings.Contains(err.Error(), "division by zero") {
			t.Errorf("Compile(%q): err = %v, want it to mention division by zero", src, err)
		}
	}
}

func TestDivModByZeroWithIdent(t *testing.T) {
	e := mustParse(t, "1/a")
	p, err := e.Compile([]string{"a"}, []Type{TypeFloat})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval([]Value{FloatValue(0)})
	if !errors.Is(err, ErrDivByZero) {
		t.Errorf("err = %v, want ErrDivByZero", err)
	}
}

// TestSqrtNegative covers the constant case ("sqrt(-1)" has no identifiers,
// so it is folded eagerly at Compile time); per the same "constant errors
// fold to Compile errors" policy as division by zero, this must fail
// Compile, not Eval.
func TestSqrtNegative(t *testing.T) {
	e := mustParse(t, "sqrt(-1)")
	_, err := e.Compile(nil, nil)
	if err == nil {
		t.Fatal("Compile(\"sqrt(-1)\"): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sqrt of negative number") {
		t.Errorf("err = %v, want it to mention sqrt of negative number", err)
	}
}

// TestSqrtNegativeWithIdent covers the non-constant case: sqrt of a
// negative value that only becomes known at Eval time (behind an
// identifier) must still surface as an Eval-time ErrSqrtDomain, unaffected
// by the constant-folding Compile-error policy above.
func TestSqrtNegativeWithIdent(t *testing.T) {
	e := mustParse(t, "sqrt(a)")
	p, err := e.Compile([]string{"a"}, []Type{TypeFloat})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval([]Value{FloatValue(-1)})
	if !errors.Is(err, ErrSqrtDomain) {
		t.Errorf("err = %v, want ErrSqrtDomain", err)
	}
}

// ---- unknown identifier / type mismatch ----

func TestUnknownIdentifier(t *testing.T) {
	e := mustParse(t, "a + 1")
	_, err := e.Check(map[string]Type{})
	if err == nil {
		t.Fatal("expected error for unknown identifier")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should mention identifier: %v", err)
	}
}

func TestTypeMismatchMessages(t *testing.T) {
	e := mustParse(t, "a == b")
	_, err := e.Check(map[string]Type{"a": TypeBool, "b": TypeFloat})
	if err == nil {
		t.Fatal("expected error")
	}
	want := "cannot compare bool with float64"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

func TestCheckArithmeticOnBool(t *testing.T) {
	e := mustParse(t, "a + 1")
	_, err := e.Check(map[string]Type{"a": TypeBool})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---- guardrails ----

func TestGuardrailTooLong(t *testing.T) {
	src := strings.Repeat("1+", 600) + "1" // way over 1024 bytes
	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected error for too-long expression")
	}
	want := "expr: expression too long"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want prefix %q", err.Error(), want)
	}
}

func TestGuardrailNestingDepth(t *testing.T) {
	// 100 levels of parens around a single literal -> exceeds depth 64.
	src := strings.Repeat("(", 100) + "1" + strings.Repeat(")", 100)
	if len(src) > maxSrcBytes {
		t.Fatalf("test src too long: %d", len(src))
	}
	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected error for excessive nesting depth")
	}
}

func TestGuardrailTooManyIdents(t *testing.T) {
	names := make([]string, 33)
	for i := range names {
		names[i] = "v" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	src := strings.Join(names, "+")
	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected error for too many identifiers")
	}
}

func TestGuardrailExactly32IdentsOK(t *testing.T) {
	names := make([]string, 32)
	for i := range names {
		names[i] = "v" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	src := strings.Join(names, "+")
	_, err := Parse(src)
	if err != nil {
		t.Fatalf("32 distinct identifiers should be allowed: %v", err)
	}
}

// ---- Idents() ----

func TestIdentsSortedDeduped(t *testing.T) {
	e := mustParse(t, "b + a + b + c + a")
	got := e.Idents()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

// ---- constant folding produces identical results ----

func TestConstantFolding(t *testing.T) {
	cases := []string{
		"2*3",
		"1+2*3-4/2",
		"min(1,2)+max(3,4)",
		"!true && false",
		"clamp(10,0,5)",
	}
	for _, src := range cases {
		e := mustParse(t, src)
		p, err := e.Compile(nil, nil)
		if err != nil {
			t.Fatalf("Compile(%q): %v", src, err)
		}
		got, err := p.Eval(nil)
		if err != nil {
			t.Fatalf("Eval(%q): %v", src, err)
		}
		want := refEval(t, e.root, nil)
		if got != want {
			t.Errorf("%q: folded = %+v, reference = %+v", src, got, want)
		}
	}
}

// ---- zero allocations on the happy path ----

func TestEvalZeroAllocs(t *testing.T) {
	e := mustParse(t, "clamp(a*2+1, 0, 10) > b && sqrt(abs(a-b)) < 5 || c")
	env := map[string]Type{"a": TypeFloat, "b": TypeFloat, "c": TypeBool}
	p, order := compileWithEnv(t, e, env)
	in := make([]Value, len(order))
	for i, name := range order {
		switch name {
		case "a":
			in[i] = FloatValue(3.5)
		case "b":
			in[i] = FloatValue(1.2)
		case "c":
			in[i] = BoolValue(false)
		}
	}
	// sanity check it evaluates without error first
	if _, err := p.Eval(in); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	allocs := testing.AllocsPerRun(1000, func() {
		_, _ = p.Eval(in)
	})
	if allocs != 0 {
		t.Errorf("AllocsPerRun = %v, want 0", allocs)
	}
}

// ---- Compile matches reference tree-walking evaluator (property-ish) ----

func TestCompileMatchesReferenceEvaluator(t *testing.T) {
	exprs := []struct {
		src string
		env map[string]Type
	}{
		{"a+b*2-c", map[string]Type{"a": TypeFloat, "b": TypeFloat, "c": TypeFloat}},
		{"(a>b) && (c || !(a==b))", map[string]Type{"a": TypeFloat, "b": TypeFloat, "c": TypeBool}},
		{"clamp(a,0,1) == clamp(b,0,1)", map[string]Type{"a": TypeFloat, "b": TypeFloat}},
		{"min(a,b) + max(a,b)", map[string]Type{"a": TypeFloat, "b": TypeFloat}},
		{"a % b", map[string]Type{"a": TypeFloat, "b": TypeFloat}},
	}
	floatSamples := []float64{-3, -0.5, 0, 0.5, 2, 7.25}
	boolSamples := []bool{true, false}

	for _, tc := range exprs {
		e := mustParse(t, tc.src)
		p, order := compileWithEnv(t, e, tc.env)

		// exhaustively combine small sample sets for each identifier
		combos := [][]Value{{}}
		for _, name := range order {
			var next [][]Value
			typ := tc.env[name]
			var samples []Value
			if typ == TypeFloat {
				for _, f := range floatSamples {
					samples = append(samples, FloatValue(f))
				}
			} else {
				for _, b := range boolSamples {
					samples = append(samples, BoolValue(b))
				}
			}
			for _, c := range combos {
				for _, s := range samples {
					nc := append(append([]Value{}, c...), s)
					next = append(next, nc)
				}
			}
			combos = next
		}

		for _, in := range combos {
			vals := make(map[string]Value, len(order))
			for i, name := range order {
				vals[name] = in[i]
			}
			refV, refErr := refEvalErr(e.root, vals)
			gotV, gotErr := p.Eval(in)
			if (refErr != nil) != (gotErr != nil) {
				t.Fatalf("%q with %v: ref err=%v, compiled err=%v", tc.src, vals, refErr, gotErr)
			}
			if refErr == nil && gotV != refV {
				t.Errorf("%q with %v: compiled=%+v, ref=%+v", tc.src, vals, gotV, refV)
			}
		}
	}
}

// ---- malformed input: no panics, all return errors ----

func TestMalformedInputsNoPanic(t *testing.T) {
	cases := []string{
		"",
		"(",
		")",
		"((",
		"1+",
		"+1",
		"1 1",
		"1..2",
		".",
		"1.2.3",
		"a b",
		"a(",
		"a)",
		"min(1,2",
		"min 1,2)",
		"min(,)",
		"min(1,)",
		"1 && 2",
		"true + 1",
		"1 == true",
		"!1",
		"-true",
		"&",
		"|",
		"=",
		"1 = 2",
		"@",
		"1e",
		"1e+",
		"foo(1,2)", // unknown function
		"min(1,2,3,4)",
		"true(1)",
		"false(1)",
		"1 & 1",
		"1 | 1",
		"\"str\"",
		"1,2",
		",",
		"(((((((((((((((((((((((((((((((((((((((((((((((((((((((((((((((((1)))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))))",
	}
	for _, src := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Parse(%q) panicked: %v", src, r)
				}
			}()
			e, err := Parse(src)
			if err != nil {
				return // expected
			}
			// Some of these parse fine syntactically but should fail on
			// Check/Compile (e.g. type errors); make sure those don't
			// panic either, and if truly valid+typed, that's fine too.
			_, cerr := e.Compile(e.Idents(), allFloat(len(e.Idents())))
			_ = cerr
		}()
	}
}

func allFloat(n int) []Type {
	t := make([]Type, n)
	for i := range t {
		t[i] = TypeFloat
	}
	return t
}

// ---- Parse error offsets ----

func TestParseErrorOffset(t *testing.T) {
	_, err := Parse("(1+2))")
	if err == nil {
		t.Fatal("expected error")
	}
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("error is not *expr.Error: %T", err)
	}
	if perr.Offset != 5 {
		t.Errorf("Offset = %d, want 5", perr.Offset)
	}
}

// ---- Type.String() ----

func TestTypeString(t *testing.T) {
	if TypeFloat.String() != "float64" {
		t.Errorf("TypeFloat.String() = %q", TypeFloat.String())
	}
	if TypeBool.String() != "bool" {
		t.Errorf("TypeBool.String() = %q", TypeBool.String())
	}
}

// ---- non-finite results ----

// TestEvalNotFiniteInf covers a non-finite result that only arises at Eval
// time (through an identifier, so it cannot be caught by constant folding):
// 1e300*1e300 overflows to +Inf.
func TestEvalNotFiniteInf(t *testing.T) {
	e := mustParse(t, "a*a")
	p, err := e.Compile([]string{"a"}, []Type{TypeFloat})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval([]Value{FloatValue(1e300)})
	if !errors.Is(err, ErrNotFinite) {
		t.Errorf("err = %v, want ErrNotFinite", err)
	}
}

// TestEvalNotFiniteNaN covers Inf-Inf producing NaN as the final result.
func TestEvalNotFiniteNaN(t *testing.T) {
	e := mustParse(t, "a-a")
	p, err := e.Compile([]string{"a"}, []Type{TypeFloat})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval([]Value{FloatValue(math.Inf(1))})
	if !errors.Is(err, ErrNotFinite) {
		t.Errorf("err = %v, want ErrNotFinite", err)
	}
}

// TestEvalIntermediateNonFiniteCaughtOnlyAtFinalResult documents the
// intentional policy (see the comment on Program.Eval): a comparison
// against an intermediate Inf/NaN follows plain IEEE 754 semantics (always
// false) rather than being special-cased, and is not itself an error since
// the final result (a bool here) is finite by construction.
func TestEvalIntermediateNonFiniteCaughtOnlyAtFinalResult(t *testing.T) {
	e := mustParse(t, "(a*a) > 1")
	p, err := e.Compile([]string{"a"}, []Type{TypeFloat})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Eval([]Value{FloatValue(1e300)})
	if err != nil {
		t.Fatalf("Eval: unexpected error: %v", err)
	}
	if got != BoolValue(true) {
		t.Errorf("got %+v, want true (1e300*1e300 > 1)", got)
	}
}

// ---- constant folding must not swallow undefined/non-finite results ----

func TestConstantFoldingOverflowIsCompileError(t *testing.T) {
	e := mustParse(t, "1e300*1e300")
	_, err := e.Compile(nil, nil)
	if err == nil {
		t.Fatal("Compile(\"1e300*1e300\"): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "overflows") {
		t.Errorf("err = %v, want it to mention overflow", err)
	}
}

// ---- input type validation ----

func TestEvalArgTypeMismatch(t *testing.T) {
	e := mustParse(t, "a + 1")
	p, err := e.Compile([]string{"a"}, []Type{TypeFloat})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval([]Value{BoolValue(true)})
	if !errors.Is(err, ErrArgType) {
		t.Errorf("err = %v, want ErrArgType", err)
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("err = %v, want it to mention identifier %q", err, "a")
	}
}

func TestEvalArgTypeMismatchOK(t *testing.T) {
	e := mustParse(t, "a + 1")
	p, err := e.Compile([]string{"a"}, []Type{TypeFloat})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Eval([]Value{FloatValue(1)}); err != nil {
		t.Fatalf("Eval: unexpected error: %v", err)
	}
}

// ---- Eval arg count mismatch ----

func TestEvalArgCountMismatch(t *testing.T) {
	e := mustParse(t, "a+1")
	p, err := e.Compile([]string{"a"}, []Type{TypeFloat})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval(nil)
	if !errors.Is(err, ErrArgCount) {
		t.Errorf("err = %v, want ErrArgCount", err)
	}
}

// ---- reserved builtin name as identifier ----

func TestBuiltinNameReservedAsIdent(t *testing.T) {
	_, err := Parse("min + 1")
	if err == nil {
		t.Fatal("expected error using builtin name as identifier")
	}
}
