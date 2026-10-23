/**
 * © Copyright IBM Corp. 2016, 2018 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of src/parser.js: the tokenizer, the top-down operator precedence parser (Vaughan R Pratt,
// http://dl.acm.org/citation.cfm?id=512931, after Douglas Crockford's
// http://javascript.crockford.com/tdop/tdop.html and 'Beautiful Code', edited by Andy Oram and Greg
// Wilson, O'Reilly Media, 2007, ISBN 978-0-596-51004-6) and the post-parse stage that
// turns the parse tree into the syntax tree the evaluator walks.

package jsonata

import (
	"math"
	"strconv"
)

// operators maps each operator to its binding power.
var operators = map[string]int{
	".": 75, "[": 80, "]": 0, "{": 70, "}": 0, "(": 80, ")": 0, ",": 0, "@": 80, "#": 80, ";": 80, ":": 80,
	"?": 20, "+": 50, "-": 50, "*": 60, "/": 60, "%": 60, "|": 20, "=": 40, "<": 40, ">": 40, "^": 40,
	"**": 60, "..": 20, ":=": 10, "!=": 40, "<=": 40, ">=": 40, "~>": 40, "?:": 40, "??": 40,
	"and": 30, "or": 25, "in": 40, "&": 50,
	"!": 0, // not an operator, but a stop character for name tokens
	"~": 0, // likewise
}

// escapes are the JSON string escape sequences.
var escapes = map[uint16]uint16{'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}

// token is a lexical token: its type (name, variable, operator, string, number, value or regex),
// its value and its position (the UTF-16 index just after it).
type token struct {
	typ      string
	value    any
	position int
}

// regexLiteral is the value of a regex token: the pattern and flags of the literal.
type regexLiteral struct {
	pattern string
	flags   string
}

// tokenizer returns the expression's tokens one at a time.
type tokenizer struct {
	path     []uint16
	position int
}

func (t *tokenizer) charAt(i int) uint16 {
	if i < 0 || i >= len(t.path) {
		return 0
	}
	return t.path[i]
}

func isOperatorChar(c uint16) bool {
	_, ok := operators[string(rune(c))]
	return c < 0x80 && ok
}

func isSpaceChar(c uint16) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v'
}

func (t *tokenizer) create(typ string, value any) *token {
	return &token{typ: typ, value: value, position: t.position}
}

func (t *tokenizer) scanRegex() (*regexLiteral, error) {
	// the prefix '/' has been scanned; find the closing '/', ignoring any that are escaped or within brackets
	start := t.position
	depth := 0
	isClosingSlash := func(position int) bool {
		if t.charAt(position) == '/' && depth == 0 {
			backslashCount := 0
			for position-(backslashCount+1) >= 0 && t.charAt(position-(backslashCount+1)) == '\\' {
				backslashCount++
			}
			return backslashCount%2 == 0
		}
		return false
	}
	length := len(t.path)
	for t.position < length {
		currentChar := t.charAt(t.position)
		if isClosingSlash(t.position) {
			pattern := fromUnits(t.path[start:t.position])
			if pattern == "" {
				return nil, &Error{Code: "S0301", Position: t.position, HasPosition: true}
			}
			t.position++
			start = t.position
			for t.charAt(t.position) == 'i' || t.charAt(t.position) == 'm' {
				t.position++
			}
			flags := fromUnits(t.path[start:t.position]) + "g"
			return &regexLiteral{pattern: pattern, flags: flags}, nil
		}
		prev := t.charAt(t.position - 1)
		if (currentChar == '(' || currentChar == '[' || currentChar == '{') && prev != '\\' {
			depth++
		}
		if (currentChar == ')' || currentChar == ']' || currentChar == '}') && prev != '\\' {
			depth--
		}
		t.position++
	}
	return nil, &Error{Code: "S0302", Position: t.position, HasPosition: true}
}

// next returns the next token, or nil at the end. prefix is set when the token follows an operand,
// where '/' is division rather than the start of a regex.
func (t *tokenizer) next(prefix bool) (*token, error) {
	length := len(t.path)
	if t.position >= length {
		return nil, nil
	}
	currentChar := t.charAt(t.position)
	for t.position < length && isSpaceChar(currentChar) {
		t.position++
		currentChar = t.charAt(t.position)
	}
	// comments
	if currentChar == '/' && t.charAt(t.position+1) == '*' {
		commentStart := t.position
		t.position += 2
		currentChar = t.charAt(t.position)
		for !(currentChar == '*' && t.charAt(t.position+1) == '/') {
			t.position++
			currentChar = t.charAt(t.position)
			if t.position >= length {
				return nil, &Error{Code: "S0106", Position: commentStart, HasPosition: true}
			}
		}
		t.position += 2
		return t.next(prefix)
	}
	if !prefix && currentChar == '/' && t.position < length {
		t.position++
		re, err := t.scanRegex()
		if err != nil {
			return nil, err
		}
		return t.create("regex", re), nil
	}
	if t.position < length {
		two := string([]rune{rune(currentChar), rune(t.charAt(t.position + 1))})
		switch two {
		case "..", ":=", "!=", ">=", "<=", "**", "~>", "?:", "??":
			t.position += 2
			return t.create("operator", two), nil
		}
		if isOperatorChar(currentChar) {
			t.position++
			return t.create("operator", string(rune(currentChar))), nil
		}
	}
	// string literals
	if currentChar == '"' || currentChar == '\'' {
		quoteType := currentChar
		t.position++
		var qstr []uint16
		for t.position < length {
			currentChar = t.charAt(t.position)
			if currentChar == '\\' {
				t.position++
				currentChar = t.charAt(t.position)
				if e, ok := escapes[currentChar]; ok && t.position < length {
					qstr = append(qstr, e)
				} else if currentChar == 'u' && t.position < length {
					end := t.position + 5
					if end > length {
						end = length
					}
					octets := t.path[t.position+1 : end]
					if !allHex(octets) {
						return nil, &Error{Code: "S0104", Position: t.position, HasPosition: true}
					}
					v, _ := strconv.ParseUint(string(utf16ToASCII(octets)), 16, 32)
					qstr = append(qstr, uint16(v))
					t.position += 4
				} else {
					var tok any = ""
					if t.position < length {
						tok = fromUnits([]uint16{currentChar})
					}
					return nil, &Error{Code: "S0103", Position: t.position, HasPosition: true, Token: tok, HasToken: true}
				}
			} else if currentChar == quoteType {
				t.position++
				return t.create("string", fromUnits(qstr)), nil
			} else {
				qstr = append(qstr, currentChar)
			}
			t.position++
		}
		return nil, &Error{Code: "S0101", Position: t.position, HasPosition: true}
	}
	// numbers
	if n := numberPrefix(t.path[t.position:]); n > 0 {
		text := string(utf16ToASCII(t.path[t.position : t.position+n]))
		num, err := strconv.ParseFloat(text, 64)
		if err == nil && !math.IsInf(num, 0) {
			t.position += n
			return t.create("number", num), nil
		}
		return nil, &Error{Code: "S0102", Position: t.position, HasPosition: true, Token: text, HasToken: true}
	}
	// quoted names (backticks)
	if currentChar == '`' {
		t.position++
		end := indexOfUnits(t.path, []uint16{'`'}, t.position)
		if end != -1 {
			name := fromUnits(t.path[t.position:end])
			t.position = end + 1
			return t.create("name", name), nil
		}
		t.position = length
		return nil, &Error{Code: "S0105", Position: t.position, HasPosition: true}
	}
	// names and variables
	i := t.position
	for {
		ch := t.charAt(i)
		if i >= length || isSpaceChar(ch) || isOperatorChar(ch) {
			if t.charAt(t.position) == '$' && t.position < length {
				name := fromUnits(t.path[t.position+1 : i])
				t.position = i
				return t.create("variable", name), nil
			}
			name := fromUnits(t.path[t.position:i])
			t.position = i
			switch name {
			case "or", "in", "and":
				return t.create("operator", name), nil
			case "true":
				return t.create("value", true), nil
			case "false":
				return t.create("value", false), nil
			case "null":
				return t.create("value", Null), nil
			}
			if t.position == length && name == "" {
				// white space at the end of the input
				return nil, nil
			}
			return t.create("name", name), nil
		}
		i++
	}
}

func allHex(units []uint16) bool {
	if len(units) == 0 {
		return false
	}
	for _, u := range units {
		if _, ok := hexDigit(u); !ok {
			return false
		}
	}
	return true
}

// utf16ToASCII narrows code units known to be ASCII.
func utf16ToASCII(units []uint16) []byte {
	b := make([]byte, len(units))
	for i, u := range units {
		b[i] = byte(u)
	}
	return b
}

// numberPrefix is the length of the match of /^-?(0|([1-9][0-9]*))(\.[0-9]+)?([Ee][-+]?[0-9]+)?/.
func numberPrefix(s []uint16) int {
	digit := func(i int) bool { return i < len(s) && s[i] >= '0' && s[i] <= '9' }
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		for digit(i) {
			i++
		}
	default:
		return 0
	}
	if i < len(s) && s[i] == '.' && digit(i+1) {
		i++
		for digit(i) {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if digit(j) {
			for digit(j) {
				j++
			}
			i = j
		}
	}
	return i
}

// symbol is an entry of the parser's symbol table.
type symbol struct {
	id     string
	lbp    int
	nud    func(p *parser, this *Node) (*Node, error)
	led    func(p *parser, this *Node, left *Node) (*Node, error)
	isNode *Node // the symbol itself used as a node, for the (end) terminal
}

type parser struct {
	source   []uint16
	lexer    *tokenizer
	node     *Node
	symbols  map[string]*symbol
	errors   []*Error
	recover  bool
	ancestry []*Node

	ancestorLabel int
	ancestorIndex int
	depth         int
}

// maxParseDepth bounds the parser's recursion, where the original exhausts the host's call stack.
const maxParseDepth = 10000

// baseNud is the nud of a symbol used as a prefix operator when it is not one (S0211).
func baseNud(p *parser, this *Node) (*Node, error) {
	err := &Error{Code: "S0211", Token: this.value(), HasToken: true, Position: this.Position, HasPosition: this.HasPos}
	if p.recover {
		remaining, lexErr := p.remainingTokens()
		if lexErr != nil {
			return nil, lexErr
		}
		err.remaining = remaining
		err.errType = "error"
		p.errors = append(p.errors, err)
		return &Node{Type: "error", errorObject: err}, nil
	}
	return nil, err
}

func (p *parser) symbol(id string, bp int) *symbol {
	if s, ok := p.symbols[id]; ok {
		if bp >= s.lbp {
			s.lbp = bp
		}
		return s
	}
	s := &symbol{id: id, lbp: bp, nud: baseNud}
	p.symbols[id] = s
	return s
}

func (p *parser) terminal(id string) {
	s := p.symbol(id, 0)
	s.nud = func(p *parser, this *Node) (*Node, error) { return this, nil }
}

// infix declares a left-associative binary operator, with the default led unless one is given.
func (p *parser) infix(id string, bp int, led func(p *parser, this, left *Node) (*Node, error)) {
	bindingPower := bp
	if bindingPower == 0 {
		bindingPower = operators[id]
	}
	s := p.symbol(id, bindingPower)
	if led == nil {
		led = func(p *parser, this, left *Node) (*Node, error) {
			this.LHS = left
			rhs, err := p.expression(bindingPower)
			if err != nil {
				return nil, err
			}
			this.RHS = rhs
			this.Type = "binary"
			return this, nil
		}
	}
	s.led = led
}

func (p *parser) infixr(id string, bp int, led func(p *parser, this, left *Node) (*Node, error)) {
	s := p.symbol(id, bp)
	s.led = led
}

// prefix declares a prefix operator, with the default nud (unary) unless one is given.
func (p *parser) prefix(id string, nud func(p *parser, this *Node) (*Node, error)) {
	s := p.symbol(id, 0)
	if nud == nil {
		nud = func(p *parser, this *Node) (*Node, error) {
			expr, err := p.expression(70)
			if err != nil {
				return nil, err
			}
			this.Expression = expr
			this.Type = "unary"
			return this, nil
		}
	}
	s.nud = nud
}

func (p *parser) remainingTokens() ([]*token, error) {
	if p.node == nil {
		// an error at the first token: the original reads a member of its undefined current node
		return nil, errHostRuntime
	}
	remaining := []*token{}
	if p.node.id() != "(end)" {
		remaining = append(remaining, &token{typ: p.node.Type, value: p.node.value(), position: p.node.Position})
	}
	for {
		nxt, err := p.lexer.next(false)
		if err != nil {
			return nil, err
		}
		if nxt == nil {
			return remaining, nil
		}
		remaining = append(remaining, nxt)
	}
}

// handleError records the error and continues in recovery mode, and fails otherwise.
func (p *parser) handleError(err *Error) (*Node, error) {
	if !p.recover {
		return nil, err
	}
	remaining, lexErr := p.remainingTokens()
	if lexErr != nil {
		return nil, lexErr
	}
	err.remaining = remaining
	p.errors = append(p.errors, err)
	p.node = &Node{sym: p.symbols["(error)"], Err: err, HasErr: true, Type: "(error)"}
	return p.node, nil
}

// advance checks that the current token is id (when id is given) and moves to the next token.
func (p *parser) advance(id string, infix bool) (*Node, error) {
	if id != "" && p.node.id() != id {
		code := "S0202"
		if p.node.id() == "(end)" {
			code = "S0203"
		}
		return p.handleError(&Error{Code: code, Position: p.node.Position, HasPosition: p.node.HasPos,
			Token: p.node.value(), HasToken: true, Value: id, HasValue: true})
	}
	next, err := p.lexer.next(infix)
	if err != nil {
		return nil, err
	}
	if next == nil {
		end := p.symbols["(end)"]
		p.node = end.isNode
		p.node.setPos(len(p.source))
		return p.node, nil
	}
	var sym *symbol
	switch next.typ {
	case "name", "variable":
		sym = p.symbols["(name)"]
	case "operator":
		s, ok := p.symbols[next.value.(string)]
		if !ok {
			return p.handleError(&Error{Code: "S0204", Position: next.position, HasPosition: true, Token: next.value, HasToken: true})
		}
		sym = s
	case "string", "number", "value":
		sym = p.symbols["(literal)"]
	case "regex":
		sym = p.symbols["(regex)"]
	}
	p.node = &Node{sym: sym, Value: next.value, HasValue: true, Type: next.typ, Position: next.position, HasPos: true}
	return p.node, nil
}

// expression is Pratt's algorithm.
func (p *parser) expression(rbp int) (*Node, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		return nil, errHostRuntime
	}
	t := p.node
	if _, err := p.advance("", true); err != nil {
		return nil, err
	}
	left, err := t.sym.nud(p, t)
	if err != nil {
		return nil, err
	}
	for rbp < p.node.sym.lbp {
		t = p.node
		if _, err := p.advance("", false); err != nil {
			return nil, err
		}
		if t.sym.led == nil {
			return nil, &Error{Code: "S0201", Position: t.Position, HasPosition: t.HasPos, Token: t.value(), HasToken: true}
		}
		if left, err = t.sym.led(p, t, left); err != nil {
			return nil, err
		}
	}
	return left, nil
}

// newParser builds the symbol table.
func newParser(source string, recover bool) *parser {
	p := &parser{source: utf16Units(source), symbols: map[string]*symbol{}, recover: recover}
	p.terminal("(end)")
	end := p.symbols["(end)"]
	end.isNode = &Node{sym: end}
	p.terminal("(name)")
	p.terminal("(literal)")
	p.terminal("(regex)")
	for _, id := range []string{":", ";", ",", ")", "]", "}", ".."} {
		p.symbol(id, 0)
	}
	for _, id := range []string{".", "+", "-", "*", "/", "%", "=", "<", ">", "!=", "<=", ">=", "&", "and", "or", "in"} {
		p.infix(id, 0, nil)
	}
	p.terminal("and") // the keywords can also be field names
	p.terminal("or")
	p.terminal("in")
	p.prefix("-", nil)
	p.infix("~>", 0, nil)
	p.infix("??", operators["??"], ledCoalesce)
	p.infixr("(error)", 10, ledError)
	p.prefix("*", func(p *parser, this *Node) (*Node, error) {
		this.Type = "wildcard"
		return this, nil
	})
	p.prefix("**", func(p *parser, this *Node) (*Node, error) {
		this.Type = "descendant"
		return this, nil
	})
	p.prefix("%", func(p *parser, this *Node) (*Node, error) {
		this.Type = "parent"
		return this, nil
	})
	p.infix("(", operators["("], ledInvoke)
	p.prefix("(", nudBlock)
	p.prefix("[", nudArray)
	p.infix("[", operators["["], ledFilter)
	p.infix("^", operators["^"], ledOrderBy)
	p.prefix("{", func(p *parser, this *Node) (*Node, error) { return p.objectParser(this, nil) })
	p.infix("{", operators["{"], func(p *parser, this, left *Node) (*Node, error) { return p.objectParser(this, left) })
	p.infixr(":=", operators[":="], ledBind)
	p.infix("@", operators["@"], func(p *parser, this, left *Node) (*Node, error) { return p.ledBindVariable(this, left, "@") })
	p.infix("#", operators["#"], func(p *parser, this, left *Node) (*Node, error) { return p.ledBindVariable(this, left, "#") })
	p.infix("?", operators["?"], ledCondition)
	p.infix("?:", operators["?:"], ledElvis)
	p.prefix("|", nudTransform)
	return p
}

// ledCoalesce is the ?? operator: lhs if it exists, else rhs.
func ledCoalesce(p *parser, this, left *Node) (*Node, error) {
	this.Type = "condition"
	this.Condition = &Node{Type: "function", Value: "(", HasValue: true,
		Procedure: &Node{Type: "variable", Value: "exists", HasValue: true},
		Arguments: []*Node{cloneNode(left)}}
	this.Then = left
	elseExpr, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	this.Else = elseExpr
	return this, nil
}

func ledError(p *parser, this, left *Node) (*Node, error) {
	this.LHS = left
	this.Err, this.HasErr = p.node.Err, true
	remaining, err := p.remainingTokens()
	if err != nil {
		return nil, err
	}
	this.Remaining, this.HasRemaining = remaining, true
	this.Type = "error"
	return this, nil
}

// ledInvoke is function invocation, partial application and the function definition (lambda).
func ledInvoke(p *parser, this, left *Node) (*Node, error) {
	this.Procedure = left
	this.Type = "function"
	this.Arguments = []*Node{}
	if p.node.id() != ")" {
		for {
			if p.node.Type == "operator" && p.node.id() == "?" {
				// partial function application
				this.Type = "partial"
				this.Arguments = append(this.Arguments, p.node)
				if _, err := p.advance("?", false); err != nil {
					return nil, err
				}
			} else {
				arg, err := p.expression(0)
				if err != nil {
					return nil, err
				}
				this.Arguments = append(this.Arguments, arg)
			}
			if p.node.id() != "," {
				break
			}
			if _, err := p.advance(",", false); err != nil {
				return nil, err
			}
		}
	}
	if _, err := p.advance(")", true); err != nil {
		return nil, err
	}
	// a function named 'function' or λ is a function definition
	if left.Type == "name" && (left.value() == "function" || left.value() == "λ") {
		for index, arg := range this.Arguments {
			if arg.Type != "variable" {
				if _, err := p.handleError(&Error{Code: "S0208", Position: arg.Position, HasPosition: arg.HasPos,
					Token: arg.value(), HasToken: true, Value: float64(index + 1), HasValue: true}); err != nil {
					return nil, err
				}
			}
		}
		this.Type = "lambda"
		if p.node.id() == "<" {
			sigPos := p.node.Position
			depth := 1
			sig := "<"
			for depth > 0 && p.node.id() != "{" && p.node.id() != "(end)" {
				tok, err := p.advance("", false)
				if err != nil {
					return nil, err
				}
				if tok.id() == ">" {
					depth--
				} else if tok.id() == "<" {
					depth++
				}
				sig += toStringJS(tok.value())
			}
			if _, err := p.advance(">", false); err != nil {
				return nil, err
			}
			signature, err := parseSignature(sig)
			if err != nil {
				e := err.(*Error)
				e.setPosition(sigPos + e.offset)
				return p.handleError(e)
			}
			this.Signature = signature
		}
		if _, err := p.advance("{", false); err != nil {
			return nil, err
		}
		body, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		this.Body = body
		if _, err := p.advance("}", false); err != nil {
			return nil, err
		}
	}
	return this, nil
}

func nudBlock(p *parser, this *Node) (*Node, error) {
	expressions := []*Node{}
	for p.node.id() != ")" {
		expr, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		expressions = append(expressions, expr)
		if p.node.id() != ";" {
			break
		}
		if _, err := p.advance(";", false); err != nil {
			return nil, err
		}
	}
	if _, err := p.advance(")", true); err != nil {
		return nil, err
	}
	this.Type = "block"
	this.Expressions = expressions
	return this, nil
}

func nudArray(p *parser, this *Node) (*Node, error) {
	a := []*Node{}
	if p.node.id() != "]" {
		for {
			item, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			if p.node.id() == ".." {
				// range operator
				rng := &Node{Type: "binary", Value: "..", HasValue: true, Position: p.node.Position, HasPos: p.node.HasPos, LHS: item}
				if _, err := p.advance("..", false); err != nil {
					return nil, err
				}
				if rng.RHS, err = p.expression(0); err != nil {
					return nil, err
				}
				item = rng
			}
			a = append(a, item)
			if p.node.id() != "," {
				break
			}
			if _, err := p.advance(",", false); err != nil {
				return nil, err
			}
		}
	}
	if _, err := p.advance("]", true); err != nil {
		return nil, err
	}
	this.Expressions = a
	this.Type = "unary"
	return this, nil
}

// ledFilter is a predicate or array index; an empty predicate keeps singleton arrays.
func ledFilter(p *parser, this, left *Node) (*Node, error) {
	if p.node.id() == "]" {
		step := left
		for step != nil && step.Type == "binary" && step.value() == "[" {
			step = step.LHS
		}
		step.KeepArray = true
		if _, err := p.advance("]", false); err != nil {
			return nil, err
		}
		return left, nil
	}
	this.LHS = left
	rhs, err := p.expression(operators["]"])
	if err != nil {
		return nil, err
	}
	this.RHS = rhs
	this.Type = "binary"
	if _, err := p.advance("]", true); err != nil {
		return nil, err
	}
	return this, nil
}

func ledOrderBy(p *parser, this, left *Node) (*Node, error) {
	if _, err := p.advance("(", false); err != nil {
		return nil, err
	}
	terms := []*SortTerm{}
	for {
		term := &SortTerm{}
		if p.node.id() == "<" {
			if _, err := p.advance("<", false); err != nil {
				return nil, err
			}
		} else if p.node.id() == ">" {
			term.Descending = true
			if _, err := p.advance(">", false); err != nil {
				return nil, err
			}
		}
		expr, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		term.Expression = expr
		terms = append(terms, term)
		if p.node.id() != "," {
			break
		}
		if _, err := p.advance(",", false); err != nil {
			return nil, err
		}
	}
	if _, err := p.advance(")", false); err != nil {
		return nil, err
	}
	this.LHS = left
	this.Terms = terms
	this.Type = "binary"
	return this, nil
}

// objectParser is the object constructor (prefix) and the group-by clause (infix).
func (p *parser) objectParser(this, left *Node) (*Node, error) {
	a := [][2]*Node{}
	if p.node.id() != "}" {
		for {
			n, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			if _, err := p.advance(":", false); err != nil {
				return nil, err
			}
			v, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			a = append(a, [2]*Node{n, v})
			if p.node.id() != "," {
				break
			}
			if _, err := p.advance(",", false); err != nil {
				return nil, err
			}
		}
	}
	if _, err := p.advance("}", true); err != nil {
		return nil, err
	}
	this.Pairs = a
	if left == nil {
		this.Type = "unary"
	} else {
		this.LHS = left
		this.Type = "binary"
	}
	return this, nil
}

func ledBind(p *parser, this, left *Node) (*Node, error) {
	if left.Type != "variable" {
		return p.handleError(&Error{Code: "S0212", Position: left.Position, HasPosition: left.HasPos, Token: left.value(), HasToken: true})
	}
	this.LHS = left
	rhs, err := p.expression(operators[":="] - 1) // right associative
	if err != nil {
		return nil, err
	}
	this.RHS = rhs
	this.Type = "binary"
	return this, nil
}

// ledBindVariable is the focus (@) and index (#) variable binding.
func (p *parser) ledBindVariable(this, left *Node, op string) (*Node, error) {
	this.LHS = left
	rhs, err := p.expression(operators[op])
	if err != nil {
		return nil, err
	}
	this.RHS = rhs
	if rhs.Type != "variable" {
		return p.handleError(&Error{Code: "S0214", Position: rhs.Position, HasPosition: rhs.HasPos, Token: op, HasToken: true})
	}
	this.Type = "binary"
	return this, nil
}

func ledCondition(p *parser, this, left *Node) (*Node, error) {
	this.Type = "condition"
	this.Condition = left
	then, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	this.Then = then
	if p.node.id() == ":" {
		if _, err := p.advance(":", false); err != nil {
			return nil, err
		}
		if this.Else, err = p.expression(0); err != nil {
			return nil, err
		}
	}
	return this, nil
}

// ledElvis is the ?: default operator; the condition is an independent copy of the left side.
func ledElvis(p *parser, this, left *Node) (*Node, error) {
	this.Type = "condition"
	this.Condition = cloneNode(left)
	this.Then = left
	elseExpr, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	this.Else = elseExpr
	return this, nil
}

func nudTransform(p *parser, this *Node) (*Node, error) {
	this.Type = "transform"
	pattern, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	this.Pattern = pattern
	if _, err := p.advance("|", false); err != nil {
		return nil, err
	}
	if this.Update, err = p.expression(0); err != nil {
		return nil, err
	}
	if p.node.id() == "," {
		if _, err := p.advance(",", false); err != nil {
			return nil, err
		}
		if this.Delete, err = p.expression(0); err != nil {
			return nil, err
		}
	}
	if _, err := p.advance("|", false); err != nil {
		return nil, err
	}
	return this, nil
}

// cloneNode is JSON.parse(JSON.stringify(node)): a deep copy of the node's own members, without
// its parser symbol.
func cloneNode(n *Node) *Node {
	if n == nil {
		return nil
	}
	if n.sym != nil && n.sym.isNode == n {
		c := &Node{Type: "", Position: n.Position, HasPos: n.HasPos, Value: n.sym.id, HasValue: true}
		c.sym = &symbol{id: n.sym.id}
		return c
	}
	c := *n
	c.sym = nil
	c.LHS, c.RHS = cloneNode(n.LHS), cloneNode(n.RHS)
	c.Expression = cloneNode(n.Expression)
	c.Expressions = cloneNodes(n.Expressions)
	c.Arguments = cloneNodes(n.Arguments)
	c.Procedure, c.Body = cloneNode(n.Procedure), cloneNode(n.Body)
	c.Condition, c.Then, c.Else = cloneNode(n.Condition), cloneNode(n.Then), cloneNode(n.Else)
	c.Pattern, c.Update, c.Delete = cloneNode(n.Pattern), cloneNode(n.Update), cloneNode(n.Delete)
	if n.Pairs != nil {
		c.Pairs = make([][2]*Node, len(n.Pairs))
		for i, pair := range n.Pairs {
			c.Pairs[i] = [2]*Node{cloneNode(pair[0]), cloneNode(pair[1])}
		}
	}
	if n.Terms != nil {
		c.Terms = make([]*SortTerm, len(n.Terms))
		for i, t := range n.Terms {
			c.Terms[i] = &SortTerm{Descending: t.Descending, Expression: cloneNode(t.Expression)}
		}
	}
	if n.Signature != nil {
		// a signature loses its validator in a JSON round trip
		c.Signature = &Signature{definition: n.Signature.definition, regex: compileCached(".*")}
	}
	return &c
}

func cloneNodes(nodes []*Node) []*Node {
	if nodes == nil {
		return nil
	}
	out := make([]*Node, len(nodes))
	for i, n := range nodes {
		out[i] = cloneNode(n)
	}
	return out
}

// tailCallOptimize wraps a call in tail position in a thunk, which the trampoline in apply evaluates.
func tailCallOptimize(expr *Node) *Node {
	switch {
	case expr.Type == "function" && expr.Predicate == nil:
		return &Node{Type: "lambda", Thunk: true, Arguments: []*Node{}, Position: expr.Position, HasPos: expr.HasPos, Body: expr}
	case expr.Type == "condition":
		expr.Then = tailCallOptimize(expr.Then)
		if expr.Else != nil {
			expr.Else = tailCallOptimize(expr.Else)
		}
	case expr.Type == "block":
		if n := len(expr.Expressions); n > 0 {
			expr.Expressions[n-1] = tailCallOptimize(expr.Expressions[n-1])
		}
	}
	return expr
}

// slotList is an array of ancestor slots shared by reference, as the original shares its arrays.
type slotList struct {
	items []*Slot
}

func (p *parser) seekParent(node *Node, slot *Slot) (*Slot, error) {
	switch node.Type {
	case "name", "wildcard":
		slot.Level--
		if slot.Level == 0 {
			if node.Ancestor == nil {
				node.Ancestor = slot
			} else {
				// reuse the existing label
				p.ancestry[slot.Index].Slot.Label = node.Ancestor.Label
				node.Ancestor = slot
			}
			node.Tuple = true
		}
	case "parent":
		slot.Level++
	case "block":
		if n := len(node.Expressions); n > 0 {
			node.Tuple = true
			return p.seekParent(node.Expressions[n-1], slot)
		}
	case "path":
		node.Tuple = true
		index := len(node.Steps) - 1
		var err error
		if slot, err = p.seekParent(node.Steps[index], slot); err != nil {
			return nil, err
		}
		index--
		for slot.Level > 0 && index >= 0 {
			if slot, err = p.seekParent(node.Steps[index], slot); err != nil {
				return nil, err
			}
			index--
		}
	default:
		// the ancestor cannot be derived
		return nil, &Error{Code: "S0217", Token: node.Type, HasToken: true, Position: node.Position, HasPosition: node.HasPos}
	}
	return slot, nil
}

func (p *parser) pushAncestry(result, value *Node) {
	if !value.HasSeekingParent && value.Type != "parent" {
		return
	}
	slots := &slotList{}
	if value.HasSeekingParent {
		slots = value.seekingList
	}
	if value.Type == "parent" {
		slots.items = append(slots.items, value.Slot)
	}
	if !result.HasSeekingParent {
		result.setSeeking(slots)
	} else {
		result.seekingList.items = append(result.seekingList.items, slots.items...)
	}
}

func (n *Node) setSeeking(list *slotList) {
	n.seekingList = list
	n.HasSeekingParent = true
}

func (p *parser) resolveAncestry(path *Node) error {
	index := len(path.Steps) - 1
	laststep := path.Steps[index]
	slots := &slotList{}
	if laststep.HasSeekingParent {
		slots = laststep.seekingList
	}
	if laststep.Type == "parent" {
		slots.items = append(slots.items, laststep.Slot)
	}
	for is := 0; is < len(slots.items); is++ {
		slot := slots.items[is]
		index = len(path.Steps) - 2
		for slot.Level > 0 {
			if index < 0 {
				if !path.HasSeekingParent {
					path.setSeeking(&slotList{items: []*Slot{slot}})
				} else {
					path.seekingList.items = append(path.seekingList.items, slot)
				}
				break
			}
			// try the previous step; contiguous steps that bind the focus are skipped
			step := path.Steps[index]
			index--
			for index >= 0 && step.Focus != "" && path.Steps[index].Focus != "" {
				step = path.Steps[index]
				index--
			}
			var err error
			if slot, err = p.seekParent(step, slot); err != nil {
				return err
			}
		}
	}
	return nil
}

// processAST is the post-parse stage: it flattens location paths into steps with their predicates,
// and resolves parent references and tail calls.
func (p *parser) processAST(expr *Node) (*Node, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		return nil, errHostRuntime
	}
	var result *Node
	var err error
	switch expr.Type {
	case "binary":
		switch expr.stringValue() {
		case ".":
			lstep, err := p.processAST(expr.LHS)
			if err != nil {
				return nil, err
			}
			if lstep.Type == "path" {
				result = lstep
			} else {
				result = &Node{Type: "path", Steps: []*Node{lstep}}
			}
			if lstep.Type == "parent" {
				result.setSeeking(&slotList{items: []*Slot{lstep.Slot}})
			}
			rest, err := p.processAST(expr.RHS)
			if err != nil {
				return nil, err
			}
			if rest.Type == "path" {
				result.Steps = append(result.Steps, rest.Steps...)
			} else {
				if rest.Predicate != nil {
					rest.Stages = rest.Predicate
					rest.Predicate = nil
				}
				result.Steps = append(result.Steps, rest)
			}
			// literal steps: numbers and values are errors, strings are names
			for _, step := range result.Steps {
				if step.Type == "number" || step.Type == "value" {
					return nil, &Error{Code: "S0213", Position: step.Position, HasPosition: step.HasPos, Value: step.value(), HasValue: true}
				}
			}
			for _, step := range result.Steps {
				if step.Type == "string" {
					step.Type = "name"
				}
			}
			for _, step := range result.Steps {
				if step.KeepArray {
					result.KeepSingletonArray = true
					break
				}
			}
			if first := result.Steps[0]; first.Type == "unary" && first.stringValue() == "[" {
				first.ConsArray = true
			}
			if last := result.Steps[len(result.Steps)-1]; last.Type == "unary" && last.stringValue() == "[" {
				last.ConsArray = true
			}
			if err := p.resolveAncestry(result); err != nil {
				return nil, err
			}
		case "[":
			// predicated step: the lhs is a step or a predicated step, the rhs the predicate
			if result, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
			step := result
			stages := false
			if result.Type == "path" {
				step = result.Steps[len(result.Steps)-1]
				stages = true
			}
			if step.Group != nil {
				return nil, &Error{Code: "S0209", Position: expr.Position, HasPosition: expr.HasPos}
			}
			predicate, err := p.processAST(expr.RHS)
			if err != nil {
				return nil, err
			}
			if predicate.HasSeekingParent {
				for _, slot := range predicate.seekingList.items {
					if slot.Level == 1 {
						if _, err := p.seekParent(step, slot); err != nil {
							return nil, err
						}
					} else {
						slot.Level--
					}
				}
				p.pushAncestry(step, predicate)
			}
			stage := &Stage{Type: "filter", Expr: predicate, Position: expr.Position, HasPos: expr.HasPos}
			if stages {
				step.Stages = append(nonNilStages(step.Stages), stage)
			} else {
				step.Predicate = append(nonNilStages(step.Predicate), stage)
			}
		case "{":
			// group-by: the lhs is a step or a predicated step, the rhs the object constructor
			if result, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
			if result.Group != nil {
				return nil, &Error{Code: "S0210", Position: expr.Position, HasPosition: expr.HasPos}
			}
			pairs := make([][2]*Node, len(expr.Pairs))
			for i, pair := range expr.Pairs {
				k, err := p.processAST(pair[0])
				if err != nil {
					return nil, err
				}
				v, err := p.processAST(pair[1])
				if err != nil {
					return nil, err
				}
				pairs[i] = [2]*Node{k, v}
			}
			result.Group = &Group{Pairs: pairs, Position: expr.Position, HasPos: expr.HasPos}
		case "^":
			// order-by: the lhs is the array to order, the rhs the terms
			if result, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
			if result.Type != "path" {
				result = &Node{Type: "path", Steps: []*Node{result}}
			}
			sortStep := &Node{Type: "sort", Position: expr.Position, HasPos: expr.HasPos}
			sortStep.Terms = make([]*SortTerm, len(expr.Terms))
			for i, term := range expr.Terms {
				e, err := p.processAST(term.Expression)
				if err != nil {
					return nil, err
				}
				p.pushAncestry(sortStep, e)
				sortStep.Terms[i] = &SortTerm{Descending: term.Descending, Expression: e}
			}
			result.Steps = append(result.Steps, sortStep)
			if err := p.resolveAncestry(result); err != nil {
				return nil, err
			}
		case ":=":
			result = &Node{Type: "bind", Value: expr.value(), HasValue: true, Position: expr.Position, HasPos: expr.HasPos}
			if result.LHS, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
			if result.RHS, err = p.processAST(expr.RHS); err != nil {
				return nil, err
			}
			p.pushAncestry(result, result.RHS)
		case "@":
			if result, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
			step := result
			if result.Type == "path" {
				step = result.Steps[len(result.Steps)-1]
			}
			// a focus binding cannot follow predicates or an order-by clause
			if step.Stages != nil || step.Predicate != nil {
				return nil, &Error{Code: "S0215", Position: expr.Position, HasPosition: expr.HasPos}
			}
			if step.Type == "sort" {
				return nil, &Error{Code: "S0216", Position: expr.Position, HasPosition: expr.HasPos}
			}
			if expr.KeepArray {
				step.KeepArray = true
			}
			step.Focus, step.HasFocus = expr.RHS.stringValue(), true
			step.Tuple = true
		case "#":
			if result, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
			step := result
			if result.Type == "path" {
				step = result.Steps[len(result.Steps)-1]
			} else {
				result = &Node{Type: "path", Steps: []*Node{result}}
				if step.Predicate != nil {
					step.Stages = step.Predicate
					step.Predicate = nil
				}
			}
			if step.Stages == nil {
				step.Index, step.HasIndex = expr.RHS.stringValue(), true
			} else {
				step.Stages = append(step.Stages, &Stage{Type: "index", Value: expr.RHS.stringValue(), Position: expr.Position, HasPos: expr.HasPos})
			}
			step.Tuple = true
		case "~>":
			result = &Node{Type: "apply", Value: expr.value(), HasValue: true, Position: expr.Position, HasPos: expr.HasPos}
			if result.LHS, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
			if result.RHS, err = p.processAST(expr.RHS); err != nil {
				return nil, err
			}
			result.KeepArray = result.LHS.KeepArray || result.RHS.KeepArray
		default:
			result = &Node{Type: expr.Type, Value: expr.value(), HasValue: true, Position: expr.Position, HasPos: expr.HasPos}
			if result.LHS, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
			if result.RHS, err = p.processAST(expr.RHS); err != nil {
				return nil, err
			}
			p.pushAncestry(result, result.LHS)
			p.pushAncestry(result, result.RHS)
		}
	case "unary":
		result = &Node{Type: expr.Type, Value: expr.value(), HasValue: true, Position: expr.Position, HasPos: expr.HasPos}
		switch expr.stringValue() {
		case "[":
			// array constructor
			result.Expressions = make([]*Node, len(expr.Expressions))
			for i, item := range expr.Expressions {
				v, err := p.processAST(item)
				if err != nil {
					return nil, err
				}
				p.pushAncestry(result, v)
				result.Expressions[i] = v
			}
		case "{":
			// object constructor
			result.Pairs = make([][2]*Node, len(expr.Pairs))
			for i, pair := range expr.Pairs {
				k, err := p.processAST(pair[0])
				if err != nil {
					return nil, err
				}
				p.pushAncestry(result, k)
				v, err := p.processAST(pair[1])
				if err != nil {
					return nil, err
				}
				p.pushAncestry(result, v)
				result.Pairs[i] = [2]*Node{k, v}
			}
		default:
			if result.Expression, err = p.processAST(expr.Expression); err != nil {
				return nil, err
			}
			// unary minus on a number is folded into the number
			if expr.stringValue() == "-" && result.Expression.Type == "number" {
				result = result.Expression
				result.setValue(-result.Value.(float64))
			} else {
				p.pushAncestry(result, result.Expression)
			}
		}
	case "function", "partial":
		result = &Node{Type: expr.Type, Value: expr.value(), HasValue: expr.HasValue || expr.sym != nil, Position: expr.Position, HasPos: expr.HasPos}
		result.Arguments = make([]*Node, len(expr.Arguments))
		for i, arg := range expr.Arguments {
			a, err := p.processAST(arg)
			if err != nil {
				return nil, err
			}
			p.pushAncestry(result, a)
			result.Arguments[i] = a
		}
		if result.Procedure, err = p.processAST(expr.Procedure); err != nil {
			return nil, err
		}
	case "lambda":
		result = &Node{Type: expr.Type, Arguments: expr.Arguments, Signature: expr.Signature, Position: expr.Position, HasPos: expr.HasPos}
		body, err := p.processAST(expr.Body)
		if err != nil {
			return nil, err
		}
		result.Body = tailCallOptimize(body)
	case "condition":
		result = &Node{Type: expr.Type, Position: expr.Position, HasPos: expr.HasPos}
		if result.Condition, err = p.processAST(expr.Condition); err != nil {
			return nil, err
		}
		p.pushAncestry(result, result.Condition)
		if result.Then, err = p.processAST(expr.Then); err != nil {
			return nil, err
		}
		p.pushAncestry(result, result.Then)
		if expr.Else != nil {
			if result.Else, err = p.processAST(expr.Else); err != nil {
				return nil, err
			}
			p.pushAncestry(result, result.Else)
		}
	case "transform":
		result = &Node{Type: expr.Type, Position: expr.Position, HasPos: expr.HasPos}
		if result.Pattern, err = p.processAST(expr.Pattern); err != nil {
			return nil, err
		}
		if result.Update, err = p.processAST(expr.Update); err != nil {
			return nil, err
		}
		if expr.Delete != nil {
			if result.Delete, err = p.processAST(expr.Delete); err != nil {
				return nil, err
			}
		}
	case "block":
		result = &Node{Type: expr.Type, Position: expr.Position, HasPos: expr.HasPos}
		result.Expressions = make([]*Node, len(expr.Expressions))
		for i, item := range expr.Expressions {
			part, err := p.processAST(item)
			if err != nil {
				return nil, err
			}
			p.pushAncestry(result, part)
			if part.ConsArray || (part.Type == "path" && part.Steps[0].ConsArray) {
				result.ConsArray = true
			}
			result.Expressions[i] = part
		}
	case "name":
		result = &Node{Type: "path", Steps: []*Node{expr}}
		if expr.KeepArray {
			result.KeepSingletonArray = true
		}
	case "parent":
		result = &Node{Type: "parent", Slot: &Slot{Label: "!" + strconv.Itoa(p.ancestorLabel), Level: 1, Index: p.ancestorIndex}}
		p.ancestorLabel++
		p.ancestorIndex++
		p.ancestry = append(p.ancestry, result)
	case "string", "number", "value", "wildcard", "descendant", "variable", "regex":
		result = expr
	case "operator":
		// the keywords 'and', 'or' and 'in' can be field names
		switch expr.stringValue() {
		case "and", "or", "in":
			expr.Type = "name"
			if result, err = p.processAST(expr); err != nil {
				return nil, err
			}
		case "?":
			// partial application placeholder
			result = expr
		default:
			return nil, &Error{Code: "S0201", Position: expr.Position, HasPosition: expr.HasPos, Token: expr.value(), HasToken: true}
		}
	case "error":
		result = expr
		if expr.LHS != nil {
			if result, err = p.processAST(expr.LHS); err != nil {
				return nil, err
			}
		}
	default:
		code := "S0206"
		if expr.id() == "(end)" {
			code = "S0207"
		}
		e := &Error{Code: code, Position: expr.Position, HasPosition: expr.HasPos, Token: expr.value(), HasToken: true}
		if p.recover {
			p.errors = append(p.errors, e)
			return &Node{Type: "error", Err: e, HasErr: true}, nil
		}
		return nil, e
	}
	if expr.KeepArray {
		result.KeepArray = true
	}
	return result, nil
}

func nonNilStages(s []*Stage) []*Stage {
	if s == nil {
		return []*Stage{}
	}
	return s
}

// parse parses an expression; in recovery mode syntax errors are recorded instead of returned.
func parse(source string, recover bool) (*Node, []*Error, error) {
	p := newParser(source, recover)
	p.lexer = &tokenizer{path: p.source}
	if _, err := p.advance("", false); err != nil {
		return nil, nil, err
	}
	expr, err := p.expression(0)
	if err != nil {
		return nil, nil, err
	}
	if p.node.id() != "(end)" {
		if _, err := p.handleError(&Error{Code: "S0201", Position: p.node.Position, HasPosition: p.node.HasPos,
			Token: p.node.value(), HasToken: true}); err != nil {
			return nil, nil, err
		}
	}
	if expr, err = p.processAST(expr); err != nil {
		return nil, nil, err
	}
	if expr.Type == "parent" || expr.HasSeekingParent {
		// the ancestor cannot be derived at the top level
		return nil, nil, &Error{Code: "S0217", Token: expr.Type, HasToken: expr.Type != "", Position: expr.Position, HasPosition: expr.HasPos}
	}
	return expr, p.errors, nil
}
