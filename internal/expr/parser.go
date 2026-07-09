package expr

import "sort"

const (
	maxSrcBytes = 1024
	maxDepth    = 64
	maxIdents   = 32
)

// Parse parses src into an un-typechecked AST. Errors carry a byte offset
// and a human-readable message.
func Parse(src string) (*Expr, error) {
	if len(src) > maxSrcBytes {
		return nil, errNoOffset("expr: expression too long (%d bytes, max %d)", len(src), maxSrcBytes)
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, identSet: make(map[string]struct{})}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, errAt(p.cur().pos, "unexpected %s", tokDesc(p.cur()))
	}
	idents := make([]string, 0, len(p.identSet))
	for name := range p.identSet {
		idents = append(idents, name)
	}
	sort.Strings(idents)
	return &Expr{root: root, idents: idents}, nil
}

type parser struct {
	toks     []token
	pos      int
	depth    int
	identSet map[string]struct{}
}

func (p *parser) cur() token { return p.toks[p.pos] }

func (p *parser) advance() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) expect(k tokKind, desc string) (token, error) {
	if p.cur().kind != k {
		return token{}, errAt(p.cur().pos, "expected %s, got %s", desc, tokDesc(p.cur()))
	}
	return p.advance(), nil
}

func (p *parser) enterDepth(pos int) error {
	p.depth++
	if p.depth > maxDepth {
		return errAt(pos, "expr: nesting depth exceeds %d", maxDepth)
	}
	return nil
}

func (p *parser) leaveDepth() { p.depth-- }

func (p *parser) addIdent(name string, pos int) error {
	if _, ok := p.identSet[name]; ok {
		return nil
	}
	if len(p.identSet) >= maxIdents {
		return errAt(pos, "expr: too many identifiers (max %d)", maxIdents)
	}
	p.identSet[name] = struct{}{}
	return nil
}

// --- precedence climbing, low to high: || && ==/!= relational +- */% unary primary ---

func (p *parser) parseOr() (*astNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tOr {
		pos := p.cur().pos
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &astNode{kind: kBinary, op: opOr, a: left, b: right, pos: pos}
	}
	return left, nil
}

func (p *parser) parseAnd() (*astNode, error) {
	left, err := p.parseEq()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tAnd {
		pos := p.cur().pos
		p.advance()
		right, err := p.parseEq()
		if err != nil {
			return nil, err
		}
		left = &astNode{kind: kBinary, op: opAnd, a: left, b: right, pos: pos}
	}
	return left, nil
}

func (p *parser) parseEq() (*astNode, error) {
	left, err := p.parseRel()
	if err != nil {
		return nil, err
	}
	for {
		var op opKind
		switch p.cur().kind {
		case tEq:
			op = opEq
		case tNeq:
			op = opNeq
		default:
			return left, nil
		}
		pos := p.cur().pos
		p.advance()
		right, err := p.parseRel()
		if err != nil {
			return nil, err
		}
		left = &astNode{kind: kBinary, op: op, a: left, b: right, pos: pos}
	}
}

func (p *parser) parseRel() (*astNode, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		var op opKind
		switch p.cur().kind {
		case tLt:
			op = opLt
		case tLe:
			op = opLe
		case tGt:
			op = opGt
		case tGe:
			op = opGe
		default:
			return left, nil
		}
		pos := p.cur().pos
		p.advance()
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &astNode{kind: kBinary, op: op, a: left, b: right, pos: pos}
	}
}

func (p *parser) parseAdd() (*astNode, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		var op opKind
		switch p.cur().kind {
		case tPlus:
			op = opAdd
		case tMinus:
			op = opSub
		default:
			return left, nil
		}
		pos := p.cur().pos
		p.advance()
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &astNode{kind: kBinary, op: op, a: left, b: right, pos: pos}
	}
}

func (p *parser) parseMul() (*astNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		var op opKind
		switch p.cur().kind {
		case tStar:
			op = opMul
		case tSlash:
			op = opDiv
		case tPercent:
			op = opMod
		default:
			return left, nil
		}
		pos := p.cur().pos
		p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &astNode{kind: kBinary, op: op, a: left, b: right, pos: pos}
	}
}

func (p *parser) parseUnary() (*astNode, error) {
	t := p.cur()
	if t.kind == tNot || t.kind == tMinus {
		if err := p.enterDepth(t.pos); err != nil {
			return nil, err
		}
		defer p.leaveDepth()
		p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		k := kNot
		if t.kind == tMinus {
			k = kNeg
		}
		return &astNode{kind: k, a: operand, pos: t.pos}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*astNode, error) {
	t := p.cur()
	switch t.kind {
	case tNum:
		p.advance()
		return &astNode{kind: kNum, numVal: t.num, pos: t.pos}, nil
	case tTrue:
		p.advance()
		return &astNode{kind: kBool, boolVal: true, pos: t.pos}, nil
	case tFalse:
		p.advance()
		return &astNode{kind: kBool, boolVal: false, pos: t.pos}, nil
	case tIdent:
		name := t.text
		pos := t.pos
		p.advance()
		if p.cur().kind == tLParen {
			if !isBuiltin(name) {
				return nil, errAt(pos, "unknown function %q", name)
			}
			if err := p.enterDepth(pos); err != nil {
				return nil, err
			}
			defer p.leaveDepth()
			p.advance() // consume '('
			var args []*astNode
			if p.cur().kind != tRParen {
				for {
					arg, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
					if p.cur().kind == tComma {
						p.advance()
						continue
					}
					break
				}
			}
			if _, err := p.expect(tRParen, "')'"); err != nil {
				return nil, err
			}
			return &astNode{kind: kCall, name: name, args: args, pos: pos}, nil
		}
		if isBuiltin(name) {
			return nil, errAt(pos, "identifier %q is reserved (builtin name)", name)
		}
		if err := p.addIdent(name, pos); err != nil {
			return nil, err
		}
		return &astNode{kind: kIdent, name: name, pos: pos}, nil
	case tLParen:
		pos := t.pos
		if err := p.enterDepth(pos); err != nil {
			return nil, err
		}
		defer p.leaveDepth()
		p.advance()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return inner, nil
	default:
		return nil, errAt(t.pos, "unexpected %s", tokDesc(t))
	}
}
