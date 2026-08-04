// Copyright (c) 2026 Meizon Inc.
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
// REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
// INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
// LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
// OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
// PERFORMANCE OF THIS SOFTWARE.

package fwqa

// The `when` expression language decides question flow (followUps, skipTo) and
// scoring (assessment rules). It is deliberately tiny and NOT a general
// expression engine: a fixed set of variables, comparison and boolean operators,
// and two set operators. There is no function call, no field access beyond the
// known names, no arbitrary code — an agent must be able to evaluate a rule
// safely and a reviewer must be able to read one.
//
// Grammar (precedence low to high):
//
//	expr    := or
//	or      := and ('||' and)*
//	and     := cmp ('&&' cmp)*
//	cmp     := '(' expr ')'
//	         | value ('==' | '!=' | '<' | '<=' | '>' | '>=') value
//	         | ident 'in' list
//	         | ident 'superset' list
//	value   := ident | number | string | bool
//	list    := '[' (string (',' string)*)? ']'
//
// Variables (resolved from EvalContext): answer, verdict (string); score, value,
// ageDays (number); evidence.count, selected.count (number); selected (list);
// attested (bool); true/false (bool literals).

import (
	"fmt"
	"strconv"
	"strings"
)

// EvalContext holds the values a `when` expression is evaluated against. Only
// the fields relevant to a question's type need be set; referencing an unset
// numeric variable evaluates it as zero, an unset string as "".
type EvalContext struct {
	Answer        string
	Verdict       string
	Score         float64
	Value         float64
	AgeDays       float64
	EvidenceCount int
	Selected      []string
	Attested      bool
}

// Eval parses and evaluates a `when` expression against ctx. A syntactically
// invalid expression is an error (surfaced at template validation), never a
// silent false.
func Eval(expr string, ctx EvalContext) (bool, error) {
	toks, err := lex(expr)
	if err != nil {
		return false, err
	}
	p := &parser{toks: toks}
	node, err := p.parseExpr()
	if err != nil {
		return false, err
	}
	if !p.atEnd() {
		return false, fmt.Errorf("fwqa: unexpected token %q in %q", p.cur().text, expr)
	}
	return node.eval(&ctx)
}

// CheckExpr reports whether an expression parses, without needing a context.
// Used by Validate to reject a malformed rule at author time.
func CheckExpr(expr string) error {
	toks, err := lex(expr)
	if err != nil {
		return err
	}
	p := &parser{toks: toks}
	if _, err := p.parseExpr(); err != nil {
		return err
	}
	if !p.atEnd() {
		return fmt.Errorf("fwqa: trailing tokens in %q", expr)
	}
	return nil
}

// --- lexer ---

type tokKind int

const (
	tEnd tokKind = iota
	tIdent
	tNumber
	tString
	tOp      // == != < <= > >=
	tAnd     // &&
	tOr      // ||
	tLParen  // (
	tRParen  // )
	tLBrack  // [
	tRBrack  // ]
	tComma   // ,
	tKeyword // in | superset
)

type token struct {
	kind tokKind
	text string
}

func lex(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, token{tLParen, "("})
			i++
		case c == ')':
			out = append(out, token{tRParen, ")"})
			i++
		case c == '[':
			out = append(out, token{tLBrack, "["})
			i++
		case c == ']':
			out = append(out, token{tRBrack, "]"})
			i++
		case c == ',':
			out = append(out, token{tComma, ","})
			i++
		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				out = append(out, token{tAnd, "&&"})
				i += 2
			} else {
				return nil, fmt.Errorf("fwqa: single '&' in expression")
			}
		case c == '|':
			if i+1 < len(s) && s[i+1] == '|' {
				out = append(out, token{tOr, "||"})
				i += 2
			} else {
				return nil, fmt.Errorf("fwqa: single '|' in expression")
			}
		case c == '=':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, token{tOp, "=="})
				i += 2
			} else {
				return nil, fmt.Errorf("fwqa: single '=' (use '==')")
			}
		case c == '!':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, token{tOp, "!="})
				i += 2
			} else {
				return nil, fmt.Errorf("fwqa: '!' must be '!='")
			}
		case c == '<' || c == '>':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, token{tOp, string(c) + "="})
				i += 2
			} else {
				out = append(out, token{tOp, string(c)})
				i++
			}
		case c == '\'' || c == '"':
			// string literal until the matching quote
			q := c
			j := i + 1
			for j < len(s) && s[j] != q {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("fwqa: unterminated string")
			}
			out = append(out, token{tString, s[i+1 : j]})
			i = j + 1
		case c >= '0' && c <= '9' || c == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			j := i + 1
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			out = append(out, token{tNumber, s[i:j]})
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			word := s[i:j]
			if word == "in" || word == "superset" {
				out = append(out, token{tKeyword, word})
			} else {
				out = append(out, token{tIdent, word})
			}
			i = j
		default:
			return nil, fmt.Errorf("fwqa: unexpected character %q", string(c))
		}
	}
	out = append(out, token{tEnd, ""})
	return out, nil
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '.'
}

// --- parser ---

type parser struct {
	toks []token
	pos  int
}

func (p *parser) cur() token  { return p.toks[p.pos] }
func (p *parser) atEnd() bool { return p.cur().kind == tEnd }
func (p *parser) advance()    { p.pos++ }

type node interface {
	eval(ctx *EvalContext) (bool, error)
}

func (p *parser) parseExpr() (node, error) { return p.parseOr() }

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tOr {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orNode{left, right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tAnd {
		p.advance()
		right, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		left = andNode{left, right}
	}
	return left, nil
}

func (p *parser) parseCmp() (node, error) {
	if p.cur().kind == tLParen {
		p.advance()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tRParen {
			return nil, fmt.Errorf("fwqa: missing ')'")
		}
		p.advance()
		return inner, nil
	}

	left, err := p.parseValue()
	if err != nil {
		return nil, err
	}

	switch p.cur().kind {
	case tKeyword: // in | superset
		kw := p.cur().text
		p.advance()
		list, err := p.parseList()
		if err != nil {
			return nil, err
		}
		return setNode{op: kw, left: left, list: list}, nil
	case tOp:
		op := p.cur().text
		p.advance()
		right, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return cmpNode{op: op, left: left, right: right}, nil
	default:
		return nil, fmt.Errorf("fwqa: expected an operator after %q", left.raw)
	}
}

func (p *parser) parseValue() (operand, error) {
	t := p.cur()
	switch t.kind {
	case tIdent, tNumber, tString:
		p.advance()
		return operand{kind: t.kind, raw: t.text}, nil
	default:
		return operand{}, fmt.Errorf("fwqa: expected a value, got %q", t.text)
	}
}

func (p *parser) parseList() ([]string, error) {
	if p.cur().kind != tLBrack {
		return nil, fmt.Errorf("fwqa: expected '[' after in/superset")
	}
	p.advance()
	var items []string
	for p.cur().kind != tRBrack {
		if p.cur().kind != tString {
			return nil, fmt.Errorf("fwqa: list items must be strings")
		}
		items = append(items, p.cur().text)
		p.advance()
		if p.cur().kind == tComma {
			p.advance()
		} else if p.cur().kind != tRBrack {
			return nil, fmt.Errorf("fwqa: expected ',' or ']' in list")
		}
	}
	p.advance() // consume ]
	return items, nil
}

// --- AST + evaluation ---

type orNode struct{ l, r node }

func (n orNode) eval(ctx *EvalContext) (bool, error) {
	lv, err := n.l.eval(ctx)
	if err != nil {
		return false, err
	}
	if lv {
		return true, nil
	}
	return n.r.eval(ctx)
}

type andNode struct{ l, r node }

func (n andNode) eval(ctx *EvalContext) (bool, error) {
	lv, err := n.l.eval(ctx)
	if err != nil {
		return false, err
	}
	if !lv {
		return false, nil
	}
	return n.r.eval(ctx)
}

// operand is a leaf: identifier, number, or string literal.
type operand struct {
	kind tokKind
	raw  string
}

type cmpNode struct {
	op          string
	left, right operand
}

func (n cmpNode) eval(ctx *EvalContext) (bool, error) {
	// Numeric comparison when either side is numeric or a numeric variable.
	if n.op == "<" || n.op == "<=" || n.op == ">" || n.op == ">=" {
		l, err := n.left.number(ctx)
		if err != nil {
			return false, err
		}
		r, err := n.right.number(ctx)
		if err != nil {
			return false, err
		}
		switch n.op {
		case "<":
			return l < r, nil
		case "<=":
			return l <= r, nil
		case ">":
			return l > r, nil
		case ">=":
			return l >= r, nil
		}
	}

	// == / != : compare as bool if both look boolean, else numeric if both
	// numeric, else string. This lets `attested == true`, `value == 7` and
	// `answer == 'yes'` all read naturally.
	if lb, lok := n.left.boolean(ctx); lok {
		if rb, rok := n.right.boolean(ctx); rok {
			eq := lb == rb
			return applyEq(n.op, eq), nil
		}
	}
	if ln, lok := n.left.numberOK(ctx); lok {
		if rn, rok := n.right.numberOK(ctx); rok {
			return applyEq(n.op, ln == rn), nil
		}
	}
	ls := n.left.stringVal(ctx)
	rs := n.right.stringVal(ctx)
	return applyEq(n.op, ls == rs), nil
}

func applyEq(op string, eq bool) bool {
	if op == "!=" {
		return !eq
	}
	return eq
}

type setNode struct {
	op   string // in | superset
	left operand
	list []string
}

func (n setNode) eval(ctx *EvalContext) (bool, error) {
	switch n.op {
	case "in":
		v := n.left.stringVal(ctx)
		for _, item := range n.list {
			if item == v {
				return true, nil
			}
		}
		return false, nil
	case "superset":
		// left must be a list variable (selected); it must contain every item.
		if n.left.kind != tIdent || n.left.raw != "selected" {
			return false, fmt.Errorf("fwqa: 'superset' applies to 'selected'")
		}
		have := map[string]bool{}
		for _, s := range ctx.Selected {
			have[s] = true
		}
		for _, item := range n.list {
			if !have[item] {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("fwqa: unknown set operator %q", n.op)
}

// operand resolution ---------------------------------------------------------

func (o operand) stringVal(ctx *EvalContext) string {
	switch o.kind {
	case tString:
		return o.raw
	case tNumber:
		return o.raw
	case tIdent:
		switch o.raw {
		case "answer":
			return ctx.Answer
		case "verdict":
			return ctx.Verdict
		case "true":
			return "true"
		case "false":
			return "false"
		default:
			return "" // numeric/list vars stringify to empty for string compare
		}
	}
	return ""
}

func (o operand) numberOK(ctx *EvalContext) (float64, bool) {
	switch o.kind {
	case tNumber:
		f, err := strconv.ParseFloat(o.raw, 64)
		return f, err == nil
	case tIdent:
		switch o.raw {
		case "score":
			return ctx.Score, true
		case "value":
			return ctx.Value, true
		case "ageDays":
			return ctx.AgeDays, true
		case "evidence.count":
			return float64(ctx.EvidenceCount), true
		case "selected.count":
			return float64(len(ctx.Selected)), true
		}
	}
	return 0, false
}

func (o operand) number(ctx *EvalContext) (float64, error) {
	if f, ok := o.numberOK(ctx); ok {
		return f, nil
	}
	return 0, fmt.Errorf("fwqa: %q is not numeric", o.raw)
}

func (o operand) boolean(ctx *EvalContext) (bool, bool) {
	if o.kind == tIdent {
		switch o.raw {
		case "true":
			return true, true
		case "false":
			return false, true
		case "attested":
			return ctx.Attested, true
		}
	}
	return false, false
}

// KnownVar reports whether an identifier is a recognised variable — used to
// give a precise validation error for a typo'd variable name.
func KnownVar(name string) bool {
	switch name {
	case "answer", "verdict", "score", "value", "ageDays",
		"evidence.count", "selected.count", "selected", "attested", "true", "false":
		return true
	}
	return false
}

// referencedVars returns the identifiers used in an expression, for validation.
func referencedVars(expr string) ([]string, error) {
	toks, err := lex(expr)
	if err != nil {
		return nil, err
	}
	var vars []string
	for _, t := range toks {
		if t.kind == tIdent {
			vars = append(vars, t.text)
		}
	}
	return vars, nil
}

// trimListLiteral is unused externally; kept internal helpers minimal.
var _ = strings.TrimSpace
