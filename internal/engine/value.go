// Package engine is the tick-based DAG pipeline evaluator described in
// docs/07-pipeline-engine.md. It compiles a model.Pipeline into an
// executable graph, runs one scheduler goroutine per enabled pipeline, and
// mediates all device I/O through the DeviceIO interface so it never
// depends on internal/board directly.
package engine

import "fmt"

// PortType is the static type of a port or edge value. Only two exist, per
// docs/07-pipeline-engine.md "Value types on edges".
type PortType int

const (
	TypeBool PortType = iota
	TypeFloat64
)

func (t PortType) String() string {
	switch t {
	case TypeBool:
		return "bool"
	case TypeFloat64:
		return "float64"
	default:
		return fmt.Sprintf("PortType(%d)", int(t))
	}
}

// Value is either a bool or a float64; exactly one of the two fields is
// meaningful, selected by Type (docs/07-pipeline-engine.md "Node lifecycle
// and interface").
type Value struct {
	Type  PortType
	Bool  bool
	Float float64
}

// BoolValue returns a Value of type TypeBool.
func BoolValue(b bool) Value { return Value{Type: TypeBool, Bool: b} }

// FloatValue returns a Value of type TypeFloat64.
func FloatValue(f float64) Value { return Value{Type: TypeFloat64, Float: f} }

// Equal reports whether v and o carry the same type and value, used for the
// tick engine's write-dedup logic (only write on change).
func (v Value) Equal(o Value) bool {
	if v.Type != o.Type {
		return false
	}
	if v.Type == TypeBool {
		return v.Bool == o.Bool
	}
	return v.Float == o.Float
}

// Port is a named, typed slot on a node (docs/08-node-catalog.md
// "Conventions used below").
type Port struct {
	Name     string
	Type     PortType
	Required bool // only meaningful for inputs; ignored for outputs

	// AnyType marks an input port that accepts either bool or float64,
	// logging/forwarding whichever type actually arrives rather than
	// requiring the connected edge's source type to match a fixed
	// declaration. Only "log" uses this today (docs/08-node-catalog.md:
	// "in — bool or float64 ... accepts either type, logs whichever
	// arrives"). validate()'s edge type-check skips the exact-type-match
	// rule for a port with AnyType set; Type is still populated (a
	// placeholder) so every other code path that assumes every Port has a
	// concrete Type keeps working.
	AnyType bool
}

// PortSpec describes a node instance's input and output ports, resolved
// from its config (and, for device-read/device-write, the referenced
// device's role).
type PortSpec struct {
	Inputs  []Port
	Outputs []Port
}
