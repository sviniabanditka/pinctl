package expr

import "strconv"

type tokKind uint8

const (
	tEOF tokKind = iota
	tNum
	tIdent
	tTrue
	tFalse
	tLParen
	tRParen
	tComma
	tPlus
	tMinus
	tStar
	tSlash
	tPercent
	tNot
	tAnd
	tOr
	tEq
	tNeq
	tLt
	tLe
	tGt
	tGe
)

type token struct {
	kind tokKind
	pos  int
	text string
	num  float64
}

func tokDesc(t token) string {
	if t.kind == tEOF {
		return "end of input"
	}
	return "'" + t.text + "'"
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// lex tokenizes src. Returns a *Error on any lexical error.
func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	n := len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{tLParen, i, "(", 0})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, i, ")", 0})
			i++
		case c == ',':
			toks = append(toks, token{tComma, i, ",", 0})
			i++
		case c == '+':
			toks = append(toks, token{tPlus, i, "+", 0})
			i++
		case c == '-':
			toks = append(toks, token{tMinus, i, "-", 0})
			i++
		case c == '*':
			toks = append(toks, token{tStar, i, "*", 0})
			i++
		case c == '/':
			toks = append(toks, token{tSlash, i, "/", 0})
			i++
		case c == '%':
			toks = append(toks, token{tPercent, i, "%", 0})
			i++
		case c == '!':
			if i+1 < n && src[i+1] == '=' {
				toks = append(toks, token{tNeq, i, "!=", 0})
				i += 2
			} else {
				toks = append(toks, token{tNot, i, "!", 0})
				i++
			}
		case c == '&':
			if i+1 < n && src[i+1] == '&' {
				toks = append(toks, token{tAnd, i, "&&", 0})
				i += 2
			} else {
				return nil, errAt(i, "unexpected '&'")
			}
		case c == '|':
			if i+1 < n && src[i+1] == '|' {
				toks = append(toks, token{tOr, i, "||", 0})
				i += 2
			} else {
				return nil, errAt(i, "unexpected '|'")
			}
		case c == '=':
			if i+1 < n && src[i+1] == '=' {
				toks = append(toks, token{tEq, i, "==", 0})
				i += 2
			} else {
				return nil, errAt(i, "unexpected '='")
			}
		case c == '<':
			if i+1 < n && src[i+1] == '=' {
				toks = append(toks, token{tLe, i, "<=", 0})
				i += 2
			} else {
				toks = append(toks, token{tLt, i, "<", 0})
				i++
			}
		case c == '>':
			if i+1 < n && src[i+1] == '=' {
				toks = append(toks, token{tGe, i, ">=", 0})
				i += 2
			} else {
				toks = append(toks, token{tGt, i, ">", 0})
				i++
			}
		case isDigit(c) || c == '.':
			start := i
			if c == '.' {
				if i+1 >= n || !isDigit(src[i+1]) {
					return nil, errAt(i, "unexpected '.'")
				}
			}
			for i < n && isDigit(src[i]) {
				i++
			}
			if i < n && src[i] == '.' {
				i++
				for i < n && isDigit(src[i]) {
					i++
				}
			}
			if i < n && (src[i] == 'e' || src[i] == 'E') {
				j := i + 1
				if j < n && (src[j] == '+' || src[j] == '-') {
					j++
				}
				if j < n && isDigit(src[j]) {
					i = j
					for i < n && isDigit(src[i]) {
						i++
					}
				}
			}
			text := src[start:i]
			f, perr := strconv.ParseFloat(text, 64)
			if perr != nil {
				return nil, errAt(start, "invalid number %q", text)
			}
			toks = append(toks, token{tNum, start, text, f})
		case isIdentStart(c):
			start := i
			i++
			for i < n && isIdentPart(src[i]) {
				i++
			}
			text := src[start:i]
			switch text {
			case "true":
				toks = append(toks, token{tTrue, start, text, 0})
			case "false":
				toks = append(toks, token{tFalse, start, text, 0})
			default:
				toks = append(toks, token{tIdent, start, text, 0})
			}
		default:
			return nil, errAt(i, "unexpected character %q", string(c))
		}
	}
	toks = append(toks, token{tEOF, n, "", 0})
	return toks, nil
}
