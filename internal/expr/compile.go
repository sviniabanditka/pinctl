package expr

import "math"

// evalFunc is the type of a compiled node's evaluator. Compiled programs are
// trees of these closures, built once at Compile time; Eval only calls
// through them, allocating nothing.
type evalFunc func(in []Value) (Value, error)

// Program is a compiled, slot-indexed form of an Expr.
type Program struct {
	fn         evalFunc
	resultType Type
	nSlots     int
	slotNames  []string
	slotTypes  []Type
}

// ResultType returns the compiled expression's output type.
func (p *Program) ResultType() Type { return p.resultType }

// Eval evaluates the program against positional inputs. len(in) must equal
// the length of the order slice passed to Compile, and each in[i].Type must
// match the type the identifier at that slot was compiled with. Eval only
// returns an error for runtime-undefined results (division by zero, modulo
// by zero, sqrt of a negative number), a non-finite float64 result, a
// mismatched input count, or a mismatched input type; it does not allocate
// on the happy path.
//
// Non-finite results (±Inf, NaN) are checked only on the final result, not
// on every intermediate operation: per-op checks would cost the hot path on
// every arithmetic node, and IEEE 754 semantics for intermediate NaN/Inf
// values already behave predictably (e.g. any comparison against NaN is
// false, Inf - Inf is NaN, propagating up to the final result where it is
// caught).
func (p *Program) Eval(in []Value) (Value, error) {
	if len(in) != p.nSlots {
		return Value{}, ErrArgCount
	}
	for i := range in {
		if in[i].Type != p.slotTypes[i] {
			return Value{}, argTypeError(p.slotNames[i], p.slotTypes[i], in[i].Type)
		}
	}
	v, err := p.fn(in)
	if err != nil {
		return Value{}, err
	}
	if p.resultType == TypeFloat && !finite(v.Float) {
		return Value{}, notFiniteError(v.Float)
	}
	return v, nil
}

// finite reports whether f is neither ±Inf nor NaN.
func finite(f float64) bool {
	return !math.IsInf(f, 0) && !math.IsNaN(f)
}

// Compile type-checks e against the environment built from order/types and
// compiles it into a Program. order[i] names the identifier whose value is
// supplied positionally in Eval's in[i].
func (e *Expr) Compile(order []string, types []Type) (*Program, error) {
	if len(order) != len(types) {
		return nil, errNoOffset("expr: order and types must have the same length (%d != %d)", len(order), len(types))
	}
	env := make(map[string]Type, len(order))
	idx := make(map[string]int, len(order))
	for i, name := range order {
		if _, dup := env[name]; dup {
			return nil, errNoOffset("expr: duplicate identifier %q in order", name)
		}
		env[name] = types[i]
		idx[name] = i
	}
	fn, typ, _, err := compileNode(e.root, env, idx)
	if err != nil {
		return nil, err
	}
	slotNames := append([]string(nil), order...)
	slotTypes := append([]Type(nil), types...)
	return &Program{fn: fn, resultType: typ, nSlots: len(order), slotNames: slotNames, slotTypes: slotTypes}, nil
}

// compileNode type-checks n and builds its evaluator, folding constant
// subtrees along the way. isConst reports whether n's value does not
// depend on any input slot.
func compileNode(n *astNode, env map[string]Type, idx map[string]int) (fn evalFunc, typ Type, isConst bool, err error) {
	switch n.kind {
	case kNum:
		v := FloatValue(n.numVal)
		return func(in []Value) (Value, error) { return v, nil }, TypeFloat, true, nil

	case kBool:
		v := BoolValue(n.boolVal)
		return func(in []Value) (Value, error) { return v, nil }, TypeBool, true, nil

	case kIdent:
		t, ok := env[n.name]
		if !ok {
			return nil, 0, false, errAt(n.pos, "undefined identifier %q", n.name)
		}
		i := idx[n.name]
		return func(in []Value) (Value, error) { return in[i], nil }, t, false, nil

	case kNot:
		af, at, ac, err := compileNode(n.a, env, idx)
		if err != nil {
			return nil, 0, false, err
		}
		if at != TypeBool {
			return nil, 0, false, errAt(n.pos, "operator ! requires bool, got %s", at)
		}
		fn := func(in []Value) (Value, error) {
			v, err := af(in)
			if err != nil {
				return Value{}, err
			}
			return BoolValue(!v.Bool), nil
		}
		fn, err = foldConst(fn, ac, TypeBool, n.pos)
		if err != nil {
			return nil, 0, false, err
		}
		return fn, TypeBool, ac, nil

	case kNeg:
		af, at, ac, err := compileNode(n.a, env, idx)
		if err != nil {
			return nil, 0, false, err
		}
		if at != TypeFloat {
			return nil, 0, false, errAt(n.pos, "unary - requires float64, got %s", at)
		}
		fn := func(in []Value) (Value, error) {
			v, err := af(in)
			if err != nil {
				return Value{}, err
			}
			return FloatValue(-v.Float), nil
		}
		fn, err = foldConst(fn, ac, TypeFloat, n.pos)
		if err != nil {
			return nil, 0, false, err
		}
		return fn, TypeFloat, ac, nil

	case kBinary:
		return compileBinary(n, env, idx)

	case kCall:
		return compileCall(n, env, idx)

	default:
		return nil, 0, false, errAt(n.pos, "internal error: unknown node kind")
	}
}

func compileBinary(n *astNode, env map[string]Type, idx map[string]int) (evalFunc, Type, bool, error) {
	af, at, ac, err := compileNode(n.a, env, idx)
	if err != nil {
		return nil, 0, false, err
	}
	bf, bt, bc, err := compileNode(n.b, env, idx)
	if err != nil {
		return nil, 0, false, err
	}
	isConst := ac && bc

	var fn evalFunc
	var typ Type

	switch n.op {
	case opOr:
		if at != TypeBool || bt != TypeBool {
			return nil, 0, false, errAt(n.pos, "operator %s requires bool operands, got %s and %s", n.op, at, bt)
		}
		typ = TypeBool
		fn = func(in []Value) (Value, error) {
			va, err := af(in)
			if err != nil {
				return Value{}, err
			}
			if va.Bool {
				return BoolValue(true), nil
			}
			vb, err := bf(in)
			if err != nil {
				return Value{}, err
			}
			return BoolValue(vb.Bool), nil
		}
	case opAnd:
		if at != TypeBool || bt != TypeBool {
			return nil, 0, false, errAt(n.pos, "operator %s requires bool operands, got %s and %s", n.op, at, bt)
		}
		typ = TypeBool
		fn = func(in []Value) (Value, error) {
			va, err := af(in)
			if err != nil {
				return Value{}, err
			}
			if !va.Bool {
				return BoolValue(false), nil
			}
			vb, err := bf(in)
			if err != nil {
				return Value{}, err
			}
			return BoolValue(vb.Bool), nil
		}
	case opEq, opNeq:
		if at != bt {
			return nil, 0, false, errAt(n.pos, "cannot compare %s with %s", at, bt)
		}
		typ = TypeBool
		neg := n.op == opNeq
		if at == TypeBool {
			fn = func(in []Value) (Value, error) {
				va, err := af(in)
				if err != nil {
					return Value{}, err
				}
				vb, err := bf(in)
				if err != nil {
					return Value{}, err
				}
				eq := va.Bool == vb.Bool
				return BoolValue(eq != neg), nil
			}
		} else {
			fn = func(in []Value) (Value, error) {
				va, err := af(in)
				if err != nil {
					return Value{}, err
				}
				vb, err := bf(in)
				if err != nil {
					return Value{}, err
				}
				eq := va.Float == vb.Float
				return BoolValue(eq != neg), nil
			}
		}
	case opLt, opLe, opGt, opGe:
		if at != TypeFloat || bt != TypeFloat {
			return nil, 0, false, errAt(n.pos, "operator %s requires float64 operands, got %s and %s", n.op, at, bt)
		}
		typ = TypeBool
		op := n.op
		fn = func(in []Value) (Value, error) {
			va, err := af(in)
			if err != nil {
				return Value{}, err
			}
			vb, err := bf(in)
			if err != nil {
				return Value{}, err
			}
			var r bool
			switch op {
			case opLt:
				r = va.Float < vb.Float
			case opLe:
				r = va.Float <= vb.Float
			case opGt:
				r = va.Float > vb.Float
			case opGe:
				r = va.Float >= vb.Float
			}
			return BoolValue(r), nil
		}
	case opAdd, opSub, opMul, opDiv, opMod:
		if at != TypeFloat || bt != TypeFloat {
			return nil, 0, false, errAt(n.pos, "operator %s requires float64 operands, got %s and %s", n.op, at, bt)
		}
		// A constant zero divisor can never be anything but an error, so
		// reject it at compile time (i.e. at pipeline validate/save) rather
		// than letting every tick fail at runtime. Whole-constant folding
		// below can't catch `a / 0`: the dividend keeps the node non-const.
		if n.op == opDiv || n.op == opMod {
			if v, ok := constFloat(bf, bc); ok && v == 0 {
				return nil, 0, false, errAt(n.pos, "division by zero")
			}
		}
		typ = TypeFloat
		op := n.op
		fn = func(in []Value) (Value, error) {
			va, err := af(in)
			if err != nil {
				return Value{}, err
			}
			vb, err := bf(in)
			if err != nil {
				return Value{}, err
			}
			switch op {
			case opAdd:
				return FloatValue(va.Float + vb.Float), nil
			case opSub:
				return FloatValue(va.Float - vb.Float), nil
			case opMul:
				return FloatValue(va.Float * vb.Float), nil
			case opDiv:
				if vb.Float == 0 {
					return Value{}, ErrDivByZero
				}
				return FloatValue(va.Float / vb.Float), nil
			case opMod:
				if vb.Float == 0 {
					return Value{}, ErrDivByZero
				}
				return FloatValue(math.Mod(va.Float, vb.Float)), nil
			}
			return Value{}, nil
		}
	default:
		return nil, 0, false, errAt(n.pos, "internal error: unknown operator")
	}

	fn, err = foldConst(fn, isConst, typ, n.pos)
	if err != nil {
		return nil, 0, false, err
	}
	return fn, typ, isConst, nil
}

func compileCall(n *astNode, env map[string]Type, idx map[string]int) (evalFunc, Type, bool, error) {
	arity, ok := builtinArity[n.name]
	if !ok {
		return nil, 0, false, errAt(n.pos, "unknown function %q", n.name)
	}
	if len(n.args) != arity {
		return nil, 0, false, errAt(n.pos, "%s: expected %d args, got %d", n.name, arity, len(n.args))
	}
	argFns := make([]evalFunc, len(n.args))
	isConst := true
	for i, a := range n.args {
		af, at, ac, err := compileNode(a, env, idx)
		if err != nil {
			return nil, 0, false, err
		}
		if at != TypeFloat {
			return nil, 0, false, errAt(a.pos, "argument %d of %s must be float64, got %s", i+1, n.name, at)
		}
		argFns[i] = af
		isConst = isConst && ac
	}

	var fn evalFunc
	switch n.name {
	case "min":
		fn = func(in []Value) (Value, error) {
			a, b, err := eval2(argFns, in)
			if err != nil {
				return Value{}, err
			}
			return FloatValue(math.Min(a, b)), nil
		}
	case "max":
		fn = func(in []Value) (Value, error) {
			a, b, err := eval2(argFns, in)
			if err != nil {
				return Value{}, err
			}
			return FloatValue(math.Max(a, b)), nil
		}
	case "abs":
		fn = func(in []Value) (Value, error) {
			v, err := argFns[0](in)
			if err != nil {
				return Value{}, err
			}
			return FloatValue(math.Abs(v.Float)), nil
		}
	case "clamp":
		fn = func(in []Value) (Value, error) {
			v, err := argFns[0](in)
			if err != nil {
				return Value{}, err
			}
			lo, err := argFns[1](in)
			if err != nil {
				return Value{}, err
			}
			hi, err := argFns[2](in)
			if err != nil {
				return Value{}, err
			}
			f := v.Float
			if f < lo.Float {
				f = lo.Float
			}
			if f > hi.Float {
				f = hi.Float
			}
			return FloatValue(f), nil
		}
	case "floor":
		fn = func(in []Value) (Value, error) {
			v, err := argFns[0](in)
			if err != nil {
				return Value{}, err
			}
			return FloatValue(math.Floor(v.Float)), nil
		}
	case "ceil":
		fn = func(in []Value) (Value, error) {
			v, err := argFns[0](in)
			if err != nil {
				return Value{}, err
			}
			return FloatValue(math.Ceil(v.Float)), nil
		}
	case "round":
		fn = func(in []Value) (Value, error) {
			v, err := argFns[0](in)
			if err != nil {
				return Value{}, err
			}
			return FloatValue(math.Round(v.Float)), nil
		}
	case "sqrt":
		fn = func(in []Value) (Value, error) {
			v, err := argFns[0](in)
			if err != nil {
				return Value{}, err
			}
			if v.Float < 0 {
				return Value{}, ErrSqrtDomain
			}
			return FloatValue(math.Sqrt(v.Float)), nil
		}
	default:
		return nil, 0, false, errAt(n.pos, "unknown function %q", n.name)
	}

	fn, foldErr := foldConst(fn, isConst, TypeFloat, n.pos)
	if foldErr != nil {
		return nil, 0, false, foldErr
	}
	return fn, TypeFloat, isConst, nil
}

func eval2(argFns []evalFunc, in []Value) (float64, float64, error) {
	a, err := argFns[0](in)
	if err != nil {
		return 0, 0, err
	}
	b, err := argFns[1](in)
	if err != nil {
		return 0, 0, err
	}
	return a.Float, b.Float, nil
}

// foldConst evaluates fn eagerly when isConst is true (no identifiers in
// its subtree) and, if that succeeds, replaces it with a closure returning
// the precomputed value.
//
// If eager evaluation errors (e.g. a constant division by zero, or sqrt of a
// negative constant), that is reported as a Compile-time error rather than
// deferred to Eval: a constant subexpression's undefined-ness is a property
// of the source, known at Compile time, and silently deferring it would let
// a broken pipeline pass validation only to fail on its first tick.
//
// Likewise, if the folded value is a non-finite float64 (±Inf, NaN) --
// e.g. `1e300*1e300` -- Compile fails instead of baking a silently
// non-finite constant into the program.
func foldConst(fn evalFunc, isConst bool, typ Type, pos int) (evalFunc, error) {
	if !isConst {
		return fn, nil
	}
	v, err := fn(nil)
	if err != nil {
		return nil, errAt(pos, "expr: constant subexpression: %v", err)
	}
	if typ == TypeFloat && !finite(v.Float) {
		return nil, errAt(pos, "expr: constant subexpression overflows to %s", nonFiniteLabel(v.Float))
	}
	return func(in []Value) (Value, error) { return v, nil }, nil
}

// nonFiniteLabel renders a non-finite float64 the way Go's fmt would
// (+Inf/-Inf/NaN), used in Compile-time overflow error messages.
func nonFiniteLabel(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	default:
		return "-Inf"
	}
}

// constFloat evaluates fn when it is known to be identifier-free, returning
// its float value. Used to reject a constant zero divisor at compile time.
func constFloat(fn evalFunc, isConst bool) (float64, bool) {
	if !isConst {
		return 0, false
	}
	v, err := fn(nil)
	if err != nil || v.Type != TypeFloat {
		return 0, false
	}
	return v.Float, true
}
