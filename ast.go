/**
 * © Copyright IBM Corp. 2016, 2018 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// The syntax tree built by the parser (src/parser.js), and its JSON form, which is what the original's
// ast() returns.

package jsonata

// Node is a node of the syntax tree. Which members are set depends on Type; the tree mirrors the
// objects the original parser builds, so it can be written as the same JSON.
type Node struct {
	Type     string
	Value    any
	HasValue bool
	Position int
	HasPos   bool

	LHS, RHS    *Node
	Pairs       [][2]*Node // object constructor and group-by pairs
	Terms       []*SortTerm
	Expression  *Node
	Expressions []*Node
	Steps       []*Node
	Predicate   []*Stage
	Stages      []*Stage
	Group       *Group
	Procedure   *Node
	Arguments   []*Node
	Body        *Node
	Signature   *Signature
	Condition   *Node
	Then, Else  *Node
	Pattern     *Node
	Update      *Node
	Delete      *Node
	Slot        *Slot
	Ancestor    *Slot

	seekingList        *slotList
	HasSeekingParent   bool
	KeepArray          bool
	ConsArray          bool
	KeepSingletonArray bool
	Tuple              bool
	Thunk              bool
	Focus              string
	HasFocus           bool
	Index              string
	HasIndex           bool
	Descending         bool

	// Error members of nodes recorded in recovery mode.
	Err          *Error
	HasErr       bool
	Remaining    []*token
	HasRemaining bool
	errorObject  *Error // a recorded error used as a node

	// native is the Go implementation wrapped by a partially applied built-in.
	native *Function

	sym *symbol // the parser symbol the node was created from
}

// Stage is a predicate filter or index binding applied to a step.
type Stage struct {
	Type     string // "filter" or "index"
	Expr     *Node
	Value    string
	Position int
	HasPos   bool
}

// Group is the group-by clause of a step.
type Group struct {
	Pairs    [][2]*Node
	Position int
	HasPos   bool
}

// SortTerm is one term of an order-by clause.
type SortTerm struct {
	Descending bool
	Expression *Node
}

// Slot is the ancestor slot of a parent (%) reference.
type Slot struct {
	Label string
	Level int
	Index int
}

// id is the parser symbol identifier of the node.
func (n *Node) id() string {
	if n.sym != nil {
		return n.sym.id
	}
	return ""
}

// value is node.value, inherited from the symbol when the node has none of its own.
func (n *Node) value() any {
	if n.HasValue {
		return n.Value
	}
	if n.sym != nil {
		return n.sym.id
	}
	return nil
}

func (n *Node) setValue(v any) {
	n.Value, n.HasValue = v, true
}

func (n *Node) setPos(p int) {
	n.Position, n.HasPos = p, true
}

// stringValue is the node's value as a string (names, variables and operators).
func (n *Node) stringValue() string {
	s, _ := n.value().(string)
	return s
}

// jsonFields lists the node's members as the original's JSON.stringify(ast) sees them.
func (n *Node) jsonFields() []field {
	if n.errorObject != nil {
		return n.errorObject.jsonFields()
	}
	var f []field
	add := func(k string, v any) { f = append(f, field{k, v}) }
	if n.sym != nil && n.sym.isNode == n {
		add("id", n.sym.id)
		add("value", n.sym.id)
		add("lbp", float64(n.sym.lbp))
		if n.HasPos {
			add("position", float64(n.Position))
		}
		return f
	}
	if n.HasValue {
		add("value", jsonLiteral(n.Value))
	}
	if n.Type != "" {
		add("type", n.Type)
	}
	if n.HasPos {
		add("position", float64(n.Position))
	}
	if n.Thunk {
		add("thunk", true)
	}
	if n.HasErr {
		if n.Err != nil {
			add("error", n.Err)
		}
	}
	if n.LHS != nil {
		add("lhs", n.LHS)
	}
	if n.Pairs != nil && n.Type == "unary" {
		add("lhs", pairsJSON(n.Pairs))
	}
	if n.RHS != nil {
		add("rhs", n.RHS)
	}
	if n.Pairs != nil && n.Type != "unary" {
		add("rhs", pairsJSON(n.Pairs))
	}
	if n.Terms != nil {
		terms := newArray()
		for _, t := range n.Terms {
			o := NewObject()
			o.Set("descending", t.Descending)
			o.Set("expression", t.Expression)
			terms.push(o)
		}
		if n.Type == "sort" {
			add("terms", terms)
		} else {
			add("rhs", terms)
		}
	}
	if n.Expression != nil {
		add("expression", n.Expression)
	}
	if n.Expressions != nil {
		add("expressions", nodesJSON(n.Expressions))
	}
	if n.Steps != nil {
		add("steps", nodesJSON(n.Steps))
	}
	if n.Procedure != nil {
		add("procedure", n.Procedure)
	}
	if n.Arguments != nil {
		add("arguments", nodesJSON(n.Arguments))
	}
	if n.Signature != nil {
		// the validator function of the signature prints as an empty string
		sig := NewObject()
		sig.Set("definition", n.Signature.definition)
		sig.Set("validate", "")
		add("signature", sig)
	}
	if n.Body != nil {
		add("body", n.Body)
	}
	if n.Condition != nil {
		add("condition", n.Condition)
	}
	if n.Then != nil {
		add("then", n.Then)
	}
	if n.Else != nil {
		add("else", n.Else)
	}
	if n.Pattern != nil {
		add("pattern", n.Pattern)
	}
	if n.Update != nil {
		add("update", n.Update)
	}
	if n.Delete != nil {
		add("delete", n.Delete)
	}
	if n.Slot != nil {
		add("slot", n.Slot.object())
	}
	if n.Predicate != nil {
		add("predicate", stagesJSON(n.Predicate))
	}
	if n.Stages != nil {
		add("stages", stagesJSON(n.Stages))
	}
	if n.Group != nil {
		g := NewObject()
		g.Set("lhs", pairsJSON(n.Group.Pairs))
		if n.Group.HasPos {
			g.Set("position", float64(n.Group.Position))
		}
		add("group", g)
	}
	if n.HasFocus {
		add("focus", n.Focus)
	}
	if n.HasIndex {
		add("index", n.Index)
	}
	if n.Ancestor != nil {
		add("ancestor", n.Ancestor.object())
	}
	if n.Tuple {
		add("tuple", true)
	}
	if n.KeepArray {
		add("keepArray", true)
	}
	if n.ConsArray {
		add("consarray", true)
	}
	if n.KeepSingletonArray {
		add("keepSingletonArray", true)
	}
	if n.HasSeekingParent {
		slots := newArray()
		for _, s := range n.seekingList.items {
			slots.push(s.object())
		}
		add("seekingParent", slots)
	}
	if n.HasRemaining {
		add("remaining", tokensJSON(n.Remaining))
	}
	return f
}

// jsonLiteral is the JSON view of a literal value: a regular expression serialises as an empty object.
func jsonLiteral(v any) any {
	if _, ok := v.(*regexLiteral); ok {
		return NewObject()
	}
	return v
}

func (s *Slot) object() *Object {
	o := NewObject()
	o.Set("label", s.Label)
	o.Set("level", float64(s.Level))
	o.Set("index", float64(s.Index))
	return o
}

func nodesJSON(nodes []*Node) *Array {
	out := newArray()
	for _, n := range nodes {
		if n == nil {
			out.push(nil)
		} else {
			out.push(n)
		}
	}
	return out
}

func pairsJSON(pairs [][2]*Node) *Array {
	out := newArray()
	for _, p := range pairs {
		out.push(newArray(p[0], p[1]))
	}
	return out
}

func stagesJSON(stages []*Stage) *Array {
	out := newArray()
	for _, s := range stages {
		o := NewObject()
		if s.Type == "filter" {
			o.Set("type", "filter")
			o.Set("expr", s.Expr)
		} else {
			o.Set("type", "index")
			o.Set("value", s.Value)
		}
		if s.HasPos {
			o.Set("position", float64(s.Position))
		}
		out.push(o)
	}
	return out
}

func tokensJSON(tokens []*token) *Array {
	out := newArray()
	for _, t := range tokens {
		o := NewObject()
		o.Set("type", t.typ)
		o.Set("value", jsonLiteral(t.value))
		o.Set("position", float64(t.position))
		out.push(o)
	}
	return out
}

// jsonFields lists the members of an error as JSON.stringify sees them.
func (e *Error) jsonFields() []field {
	var f []field
	add := func(k string, v any) { f = append(f, field{k, v}) }
	add("code", e.Code)
	if e.HasToken {
		add("token", jsonLiteral(e.Token))
	}
	if e.HasPosition {
		add("position", float64(e.Position))
	}
	if e.HasValue {
		add("value", jsonLiteral(e.Value))
	}
	if e.HasRemainingTokens() {
		add("remaining", tokensJSON(e.remaining))
	}
	if e.errType != "" {
		add("type", e.errType)
	}
	return f
}

// HasRemainingTokens reports whether the error was recorded in recovery mode with the tokens that follow it.
func (e *Error) HasRemainingTokens() bool { return e.remaining != nil }

// ASTJSON returns the syntax tree as JSON, in the form the original's ast() serialises to.
func (e *Expression) ASTJSON() string {
	s, _, _ := stringifyJSON(e.ast, functionsAsEmpty, 0)
	return s
}

// ErrorsJSON returns the errors recorded in recovery mode as JSON ("" when there are none).
func (e *Expression) ErrorsJSON() string {
	if len(e.errors) == 0 {
		return ""
	}
	arr := newArray()
	for _, err := range e.errors {
		arr.push(err)
	}
	s, _, _ := stringifyJSON(arr, functionsAsEmpty, 0)
	return s
}

// functionsAsEmpty is the replacer that writes function values as empty strings.
func functionsAsEmpty(v any) (any, error) {
	if isFunction(v) {
		return "", nil
	}
	return v, nil
}
