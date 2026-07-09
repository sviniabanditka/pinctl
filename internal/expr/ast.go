package expr

// astKind identifies the shape of an astNode.
type astKind uint8

const (
	kNum astKind = iota
	kBool
	kIdent
	kNot
	kNeg
	kBinary
	kCall
)

// opKind identifies a binary operator.
type opKind uint8

const (
	opOr opKind = iota
	opAnd
	opEq
	opNeq
	opLt
	opLe
	opGt
	opGe
	opAdd
	opSub
	opMul
	opDiv
	opMod
)

func (op opKind) String() string {
	switch op {
	case opOr:
		return "||"
	case opAnd:
		return "&&"
	case opEq:
		return "=="
	case opNeq:
		return "!="
	case opLt:
		return "<"
	case opLe:
		return "<="
	case opGt:
		return ">"
	case opGe:
		return ">="
	case opAdd:
		return "+"
	case opSub:
		return "-"
	case opMul:
		return "*"
	case opDiv:
		return "/"
	case opMod:
		return "%"
	default:
		return "?"
	}
}

// astNode is a node in the parsed, un-typechecked AST.
type astNode struct {
	kind astKind
	pos  int

	numVal  float64
	boolVal bool

	name string // ident name, or builtin function name for kCall

	op   opKind     // for kBinary
	a, b *astNode   // a: operand (kNot/kNeg) or left (kBinary); b: right (kBinary)
	args []*astNode // for kCall
}

// builtinArity is the fixed whitelist of builtin functions and their
// argument counts. All builtins take float64 args and return float64.
var builtinArity = map[string]int{
	"min":   2,
	"max":   2,
	"abs":   1,
	"clamp": 3,
	"floor": 1,
	"ceil":  1,
	"round": 1,
	"sqrt":  1,
}

func isBuiltin(name string) bool {
	_, ok := builtinArity[name]
	return ok
}

// Expr is a parsed, un-typechecked AST.
type Expr struct {
	root   *astNode
	idents []string // sorted, deduped
}

// Idents returns the identifiers referenced by the expression, sorted and
// deduplicated.
func (e *Expr) Idents() []string {
	out := make([]string, len(e.idents))
	copy(out, e.idents)
	return out
}
