package expr

// Check type-checks the expression against env (identifier name -> type)
// and returns the expression's result type.
func (e *Expr) Check(env map[string]Type) (Type, error) {
	return typeOf(e.root, env)
}

func typeOf(n *astNode, env map[string]Type) (Type, error) {
	switch n.kind {
	case kNum:
		return TypeFloat, nil
	case kBool:
		return TypeBool, nil
	case kIdent:
		t, ok := env[n.name]
		if !ok {
			return 0, errAt(n.pos, "undefined identifier %q", n.name)
		}
		return t, nil
	case kNot:
		t, err := typeOf(n.a, env)
		if err != nil {
			return 0, err
		}
		if t != TypeBool {
			return 0, errAt(n.pos, "operator ! requires bool, got %s", t)
		}
		return TypeBool, nil
	case kNeg:
		t, err := typeOf(n.a, env)
		if err != nil {
			return 0, err
		}
		if t != TypeFloat {
			return 0, errAt(n.pos, "unary - requires float64, got %s", t)
		}
		return TypeFloat, nil
	case kBinary:
		return typeOfBinary(n, env)
	case kCall:
		return typeOfCall(n, env)
	default:
		return 0, errAt(n.pos, "internal error: unknown node kind")
	}
}

func typeOfBinary(n *astNode, env map[string]Type) (Type, error) {
	at, err := typeOf(n.a, env)
	if err != nil {
		return 0, err
	}
	bt, err := typeOf(n.b, env)
	if err != nil {
		return 0, err
	}
	switch n.op {
	case opOr, opAnd:
		if at != TypeBool || bt != TypeBool {
			return 0, errAt(n.pos, "operator %s requires bool operands, got %s and %s", n.op, at, bt)
		}
		return TypeBool, nil
	case opEq, opNeq:
		if at != bt {
			return 0, errAt(n.pos, "cannot compare %s with %s", at, bt)
		}
		return TypeBool, nil
	case opLt, opLe, opGt, opGe:
		if at != TypeFloat || bt != TypeFloat {
			return 0, errAt(n.pos, "operator %s requires float64 operands, got %s and %s", n.op, at, bt)
		}
		return TypeBool, nil
	case opAdd, opSub, opMul, opDiv, opMod:
		if at != TypeFloat || bt != TypeFloat {
			return 0, errAt(n.pos, "operator %s requires float64 operands, got %s and %s", n.op, at, bt)
		}
		return TypeFloat, nil
	default:
		return 0, errAt(n.pos, "internal error: unknown operator")
	}
}

func typeOfCall(n *astNode, env map[string]Type) (Type, error) {
	arity, ok := builtinArity[n.name]
	if !ok {
		return 0, errAt(n.pos, "unknown function %q", n.name)
	}
	if len(n.args) != arity {
		return 0, errAt(n.pos, "%s: expected %d args, got %d", n.name, arity, len(n.args))
	}
	for i, a := range n.args {
		t, err := typeOf(a, env)
		if err != nil {
			return 0, err
		}
		if t != TypeFloat {
			return 0, errAt(a.pos, "argument %d of %s must be float64, got %s", i+1, n.name, t)
		}
	}
	return TypeFloat, nil
}
