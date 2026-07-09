package expr

import (
	"errors"
	"fmt"
	"math"
)

// Error is returned by Parse, Check and Compile. Offset is a byte offset
// into the source (or -1 when not applicable, e.g. for the "expression too
// long" guardrail, which fires before any position can be attributed).
type Error struct {
	Offset int
	Msg    string
}

func (e *Error) Error() string {
	if e.Offset < 0 {
		return e.Msg
	}
	return fmt.Sprintf("%s at offset %d", e.Msg, e.Offset)
}

func errAt(offset int, format string, a ...any) *Error {
	return &Error{Offset: offset, Msg: fmt.Sprintf(format, a...)}
}

func errNoOffset(format string, a ...any) *Error {
	return &Error{Offset: -1, Msg: fmt.Sprintf(format, a...)}
}

// ErrDivByZero is returned by Program.Eval for division and modulo by zero.
// It is a sentinel usable with errors.Is.
var ErrDivByZero = errors.New("expr: division by zero")

// ErrSqrtDomain is returned by Program.Eval when sqrt is applied to a
// negative number.
var ErrSqrtDomain = errors.New("expr: sqrt of negative number")

// ErrArgCount is returned by Program.Eval when the number of supplied
// inputs does not match the compiled slot count.
var ErrArgCount = errors.New("expr: wrong number of inputs")

// ErrNotFinite is returned by Program.Eval when a float64 result is not
// finite (±Inf or NaN). Only the final result is checked, not intermediate
// values; see the comment on Program.Eval for why.
var ErrNotFinite = errors.New("expr: result is not finite")

// ErrArgType is returned by Program.Eval when a supplied input's Value.Type
// does not match the type the identifier was compiled with.
var ErrArgType = errors.New("expr: input type mismatch")

// argTypeErr wraps ErrArgType with a lazily-built message naming the
// offending identifier and the expected/actual types. Only constructed on
// the error path, so it costs nothing on the happy path.
type argTypeErr struct {
	name      string
	want, got Type
}

func (e *argTypeErr) Error() string {
	return fmt.Sprintf("expr: input %q: expected %s, got %s", e.name, e.want, e.got)
}

func (e *argTypeErr) Unwrap() error { return ErrArgType }

func argTypeError(name string, want, got Type) error {
	return &argTypeErr{name: name, want: want, got: got}
}

// notFiniteErr wraps ErrNotFinite with a lazily-built message naming the
// specific non-finite value. Only constructed on the error path.
type notFiniteErr struct {
	f float64
}

func (e *notFiniteErr) Error() string {
	switch {
	case math.IsNaN(e.f):
		return "expr: result is not finite (NaN)"
	case math.IsInf(e.f, 1):
		return "expr: result is not finite (+Inf)"
	default:
		return "expr: result is not finite (-Inf)"
	}
}

func (e *notFiniteErr) Unwrap() error { return ErrNotFinite }

func notFiniteError(f float64) error {
	return &notFiniteErr{f: f}
}
