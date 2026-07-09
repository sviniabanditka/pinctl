// Package expr implements a small, dependency-free, total expression
// evaluator over typed inputs (float64 and bool). It is not a scripting
// language: there are no variables, assignment, loops or user-defined
// functions -- every expression is a pure function of its inputs.
//
// Typical usage:
//
//	e, err := expr.Parse("a > 0.5 && !b")
//	// ... type-check / discover inputs via e.Idents() ...
//	p, err := e.Compile([]string{"a", "b"}, []expr.Type{expr.TypeFloat, expr.TypeBool})
//	v, err := p.Eval([]expr.Value{expr.FloatValue(0.7), expr.BoolValue(false)})
package expr

// Type is the type of an expr value.
type Type int

const (
	TypeFloat Type = iota
	TypeBool
)

// String returns the Go-flavoured name of the type, matching the
// conventions used elsewhere in pinctl (engine.Value uses "float64"/"bool").
func (t Type) String() string {
	switch t {
	case TypeFloat:
		return "float64"
	case TypeBool:
		return "bool"
	default:
		return "unknown"
	}
}

// Value is expr's own value representation. The engine package is
// responsible for converting to/from its own Value type; expr does not
// know about engine.
type Value struct {
	Bool  bool
	Float float64
	Type  Type
}

// BoolValue constructs a bool Value.
func BoolValue(b bool) Value {
	return Value{Bool: b, Type: TypeBool}
}

// FloatValue constructs a float64 Value.
func FloatValue(f float64) Value {
	return Value{Float: f, Type: TypeFloat}
}
