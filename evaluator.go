/**
 * © Copyright IBM Corp. 2016, 2017 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of the evaluator of src/jsonata.js.

package jsonata

import (
	"math"
	"strings"
	"time"

	"github.com/jsonata-js/jsonata/internal/jsre"
)

// frame is a variable scope. Frames chain to their enclosing frame; base is the per-evaluation state
// shared by every frame of one evaluation.
type frame struct {
	bindings map[string]any
	parent   *frame
	base     *evaluation
}

func newFrame(parent *frame) *frame {
	f := &frame{bindings: map[string]any{}, parent: parent}
	if parent != nil {
		f.base = parent.base
	}
	return f
}

// bind sets a variable in this frame; binding undefined still hides an outer variable of the same name.
func (f *frame) bind(name string, value any) { f.bindings[name] = value }

func (f *frame) lookup(name string) any {
	for fr := f; fr != nil; fr = fr.parent {
		if v, ok := fr.bindings[name]; ok {
			return v
		}
	}
	return nil
}

// evaluation is the state of one call to Evaluate.
type evaluation struct {
	options   Options
	depth     int
	start     time.Time
	timestamp time.Time
	frame     *frame
}

// createSequence is environment.base.createSequence: a new result sequence, holding the item when one is given.
func (ev *evaluation) createSequence(items ...any) *Array {
	a := &Array{sequence: true}
	if ev != nil && ev.options.Sequence > 0 {
		a.limit = ev.options.Sequence
	}
	a.items = append(a.items, items...)
	return a
}

// guardrails enforces the stack depth and time limits.
func (ev *evaluation) guardrails() error {
	if ev.options.Stack > 0 && ev.depth > ev.options.Stack {
		return &Error{Code: "D1011", Value: float64(ev.options.Stack), HasValue: true}
	}
	if ev.options.Timeout > 0 && time.Since(ev.start) > ev.options.Timeout {
		return &Error{Code: "D1012", Value: float64(ev.options.Timeout / time.Millisecond), HasValue: true}
	}
	return nil
}

// add appends to a sequence, enforcing the maximum sequence length of the Sequence option.
func (a *Array) add(items ...any) error {
	if a.limit > 0 && len(a.items)+len(items) > a.limit {
		return &Error{Code: "D2015", Value: float64(a.limit), HasValue: true}
	}
	a.items = append(a.items, items...)
	return nil
}

// Lambda is a function defined in an expression, or a partial application.
type Lambda struct {
	input       any
	environment *frame
	arguments   []*Node
	signature   *Signature
	body        *Node
	native      *Function // a partially applied built-in, which the body stands for
	nativeFn    *nativeFunc
	thunk       bool
	invocation
}

// invocation holds the token and position the evaluator writes onto a function object when it
// invokes it; later failures of the function report them.
type invocation struct {
	token       any
	hasToken    bool
	position    any
	hasPosition bool
}

func (in *invocation) setInvocation(token any, hasToken bool, position int, hasPosition bool) {
	in.token, in.hasToken = token, hasToken
	if hasPosition {
		in.position, in.hasPosition = float64(position), true
	} else {
		in.position, in.hasPosition = nil, false
	}
}

func (l *Lambda) jsonFields() []field { return nil }

// Function is a built-in or registered function.
type Function struct {
	name      string
	params    string // the parameter list of the original implementation, used by partial application
	arity     int    // the number of declared parameters before any default
	impl      func(f *focus, args []any) (any, error)
	signature *Signature
	invocation
}

func (fn *Function) jsonFields() []field {
	out := []field{{"_jsonata_function", true}}
	if fn.signature != nil {
		sig := NewObject()
		sig.Set("definition", fn.signature.definition)
		out = append(out, field{"signature", sig})
	}
	if fn.hasToken {
		out = append(out, field{"token", fn.token})
	}
	if fn.hasPosition {
		out = append(out, field{"position", fn.position})
	}
	return out
}

// nativeFunc is a plain host function value: a regex matcher, the next() of a match, or the closure
// that wraps a function argument.
type nativeFunc struct {
	arity  int
	params string
	call   func(args []any) (any, error)
	invocation
}

// focus is the `this` a built-in function runs with: the environment and the context value of the
// invocation, and the evaluator for calling function arguments.
type focus struct {
	env   *frame
	input any
	ev    *evaluator
}

// callMethod is func.apply(this, args) on a function value a built-in received: a host function or a
// lambda. A built-in's definition object has no apply method, which fails as in the original.
func (f *focus) callMethod(fn any, args []any) (any, error) {
	switch x := fn.(type) {
	case *nativeFunc:
		return x.call(args)
	case *Lambda:
		return f.ev.apply(x, args, x.input, f.env)
	}
	return nil, errHostRuntime
}

// callDirect is fn(args...), which only host functions support.
func callDirect(fn any, args []any) (any, error) {
	if x, ok := fn.(*nativeFunc); ok {
		return x.call(args)
	}
	return nil, errHostRuntime
}

func (f *focus) createSequence(items ...any) *Array { return f.env.base.createSequence(items...) }

func (f *focus) options() Options { return f.env.base.options }

// getFunctionArity is utils.getFunctionArity.
func getFunctionArity(fn any) int {
	switch x := fn.(type) {
	case *nativeFunc:
		return x.arity
	case *Function:
		return x.arity
	case *Lambda:
		return len(x.arguments)
	}
	return 0
}

// maxEvalDepth bounds the evaluator's recursion.
const maxEvalDepth = 100000

// evaluator evaluates the syntax tree.
type evaluator struct {
	chainAST *Node
}

// evaluate evaluates an expression against the input in the environment.
func (e *evaluator) evaluate(expr *Node, input any, env *frame) (any, error) {
	base := env.base
	base.depth++
	if base.depth > maxEvalDepth {
		// the original exhausts the host's call stack (a RangeError, not a JSONata error)
		return nil, errHostRuntime
	}
	if err := base.guardrails(); err != nil {
		return nil, err
	}
	var result any
	var err error
	switch expr.Type {
	case "path":
		result, err = e.evaluatePath(expr, input, env)
	case "binary":
		result, err = e.evaluateBinary(expr, input, env)
	case "unary":
		result, err = e.evaluateUnary(expr, input, env)
	case "name":
		result = lookupValue(input, expr.stringValue(), base)
	case "string", "number", "value":
		result = expr.Value
	case "wildcard":
		result, err = e.evaluateWildcard(input, env)
	case "descendant":
		result = evaluateDescendants(input, env)
	case "parent":
		result = env.lookup(expr.Slot.Label)
	case "condition":
		result, err = e.evaluateCondition(expr, input, env)
	case "block":
		result, err = e.evaluateBlock(expr, input, env)
	case "bind":
		result, err = e.evaluateBindExpression(expr, input, env)
	case "regex":
		result, err = e.evaluateRegex(expr)
	case "function":
		result, err = e.evaluateFunction(expr, input, env, nil, false)
	case "variable":
		result = evaluateVariable(expr, input, env)
	case "lambda":
		result = evaluateLambda(expr, input, env)
	case "partial":
		result, err = e.evaluatePartialApplication(expr, input, env)
	case "apply":
		result, err = e.evaluateApplyExpression(expr, input, env)
	case "transform":
		result = e.evaluateTransformExpression(expr, env)
	}
	if err != nil {
		return nil, err
	}
	for _, stage := range expr.Predicate {
		if result, err = e.evaluateFilter(stage.Expr, result, env); err != nil {
			return nil, err
		}
	}
	if expr.Type != "path" && expr.Group != nil {
		if result, err = e.evaluateGroupExpression(expr.Group.Pairs, expr.Group.Position, expr.Group.HasPos, result, env); err != nil {
			return nil, err
		}
	}
	if seq, ok := result.(*Array); ok && seq.sequence && !seq.tupleStream {
		if expr.KeepArray {
			seq.keepSingleton = true
		}
		switch {
		case len(seq.items) == 0:
			result = nil
		case len(seq.items) == 1 && !seq.keepSingleton:
			result = seq.items[0]
		}
	}
	base.depth--
	return result, nil
}

// sortInput is the array an order-by step sorts; a value that is not an array sorts as no items.
func sortInput(v any) *Array {
	if a, ok := v.(*Array); ok {
		return a
	}
	return &Array{}
}

// stepItems are the items a path step iterates over: the items of an array, or the code units of a
// string (input[i] for i < input.length).
func stepItems(v any) []any {
	switch x := v.(type) {
	case *Array:
		return x.items
	case string:
		units := utf16Units(x)
		out := make([]any, len(units))
		for i, u := range units {
			out[i] = fromUnits([]uint16{u})
		}
		return out
	}
	return nil
}

// items are the items of an array value; other values have none.
func items(v any) []any {
	if a, ok := v.(*Array); ok {
		return a.items
	}
	return nil
}

func (e *evaluator) evaluatePath(expr *Node, input any, env *frame) (any, error) {
	base := env.base
	var inputSequence any
	// a path that starts with a variable reference is absolute, not relative to the context
	if arr, ok := input.(*Array); ok && expr.Steps[0].Type != "variable" {
		inputSequence = arr
	} else {
		inputSequence = base.createSequence(input)
	}
	var resultSequence any
	isTupleStream := false
	var tupleBindings *Array
	var err error
	for ii, step := range expr.Steps {
		if step.Tuple {
			isTupleStream = true
		}
		switch {
		case ii == 0 && step.ConsArray:
			// an explicit array constructor as first step is evaluated, not iterated over
			resultSequence, err = e.evaluate(step, inputSequence, env)
		case isTupleStream:
			tupleBindings, err = e.evaluateTupleStep(step, inputSequence, tupleBindings, env)
		default:
			var rs *Array
			rs, err = e.evaluateStep(step, inputSequence, env, ii == len(expr.Steps)-1)
			resultSequence = rs
		}
		if err != nil {
			return nil, err
		}
		if !isTupleStream && (resultSequence == nil || jsLength(resultSequence) == 0) {
			break
		}
		if !step.HasFocus {
			inputSequence = resultSequence
		}
	}
	if isTupleStream {
		if expr.Tuple {
			// the tuple stream carries ancestry information
			resultSequence = tupleBindings
		} else {
			rs := base.createSequence()
			if tupleBindings != nil {
				for _, t := range tupleBindings.items {
					if err := rs.add(tupleValue(t, "@")); err != nil {
						return nil, err
					}
				}
			}
			resultSequence = rs
		}
	}
	if expr.KeepSingletonArray {
		arr, ok := resultSequence.(*Array)
		if ok && arr.cons && !arr.sequence {
			arr = base.createSequence(arr)
			resultSequence = arr
		}
		switch rs := resultSequence.(type) {
		case *Array:
			rs.keepSingleton = true
		case *Object, *Lambda, *Function, *nativeFunc:
			// a property on a non-array value has no effect here
		default:
			if !ok {
				return nil, errHostRuntime
			}
		}
	}
	if expr.Group != nil {
		var groupInput any = resultSequence
		if isTupleStream {
			groupInput = tupleBindings
		}
		return e.evaluateGroupExpression(expr.Group.Pairs, expr.Group.Position, expr.Group.HasPos, groupInput, env)
	}
	return resultSequence, nil
}

// jsLength is value.length for the values a path step can produce.
func jsLength(v any) int {
	switch x := v.(type) {
	case *Array:
		return len(x.items)
	case string:
		return utf16Len(x)
	}
	return -1
}

// tupleValue reads a member of a tuple.
func tupleValue(t any, key string) any {
	switch x := t.(type) {
	case tuple:
		return x[key]
	case *Object:
		v, _ := x.Get(key)
		return v
	}
	return nil
}

func createFrameFromTuple(env *frame, t any) *frame {
	f := newFrame(env)
	switch x := t.(type) {
	case tuple:
		for k, v := range x {
			f.bind(k, v)
		}
	case *Object:
		for _, k := range x.Keys() {
			f.bind(k, x.vals[k])
		}
	}
	return f
}

func (e *evaluator) evaluateStep(expr *Node, inputValue any, env *frame, lastStep bool) (*Array, error) {
	base := env.base
	if expr.Type == "sort" {
		result, err := e.evaluateSortExpression(expr, sortInput(inputValue), env)
		if err != nil {
			return nil, err
		}
		if expr.Stages != nil {
			stagesResult, err := e.evaluateStages(expr.Stages, result, env)
			if err != nil {
				return nil, err
			}
			return asArray(stagesResult), nil
		}
		return asArray(result), nil
	}
	result := base.createSequence()
	for _, item := range stepItems(inputValue) {
		res, err := e.evaluate(expr, item, env)
		if err != nil {
			return nil, err
		}
		for _, stage := range expr.Stages {
			if res, err = e.evaluateFilter(stage.Expr, res, env); err != nil {
				return nil, err
			}
		}
		if res != nil {
			if err := result.add(res); err != nil {
				return nil, err
			}
		}
	}
	if lastStep && len(result.items) == 1 {
		if arr, ok := result.items[0].(*Array); ok && !arr.sequence {
			return arr, nil
		}
	}
	resultSequence := base.createSequence()
	for _, res := range result.items {
		arr, ok := res.(*Array)
		if !ok || arr.cons {
			if err := resultSequence.add(res); err != nil {
				return nil, err
			}
			continue
		}
		for _, val := range arr.items {
			if err := resultSequence.add(val); err != nil {
				return nil, err
			}
		}
	}
	return resultSequence, nil
}

// asArray views a value produced by a sort as an array (a sort of one item or less returns its input).
func asArray(v any) *Array {
	if a, ok := v.(*Array); ok {
		return a
	}
	return nil
}

func (e *evaluator) evaluateStages(stages []*Stage, input any, env *frame) (any, error) {
	result := input
	var err error
	for _, stage := range stages {
		switch stage.Type {
		case "filter":
			if result, err = e.evaluateFilter(stage.Expr, result, env); err != nil {
				return nil, err
			}
		case "index":
			for ee, t := range items(result) {
				setTupleMember(t, stage.Value, float64(ee))
			}
		}
	}
	return result, nil
}

func setTupleMember(t any, key string, v any) {
	switch x := t.(type) {
	case tuple:
		x[key] = v
	case *Object:
		x.Set(key, v)
	}
}

// indexKey is the name a tuple binds an index under: an unset index binds "undefined", as the
// original's tuple[expr.index] does.
func indexKey(n *Node) string {
	if n.HasIndex {
		return n.Index
	}
	return "undefined"
}

func (e *evaluator) evaluateTupleStep(expr *Node, inputValue any, tupleBindings *Array, env *frame) (*Array, error) {
	base := env.base
	input := items(inputValue)
	if expr.Type == "sort" {
		var result any
		if tupleBindings != nil {
			sorted, err := e.evaluateSortExpression(expr, tupleBindings, env)
			if err != nil {
				return nil, err
			}
			result = sorted
		} else {
			sorted, err := e.evaluateSortExpression(expr, sortInput(inputValue), env)
			if err != nil {
				return nil, err
			}
			rs := base.createSequence()
			rs.tupleStream = true
			for ss, item := range items(sorted) {
				t := tuple{"@": item}
				t[indexKey(expr)] = float64(ss)
				if err := rs.add(t); err != nil {
					return nil, err
				}
			}
			result = rs
		}
		if expr.Stages != nil {
			var err error
			if result, err = e.evaluateStages(expr.Stages, result, env); err != nil {
				return nil, err
			}
		}
		return asArray(result), nil
	}
	result := base.createSequence()
	result.tupleStream = true
	var bindings []any
	if tupleBindings == nil {
		for _, item := range input {
			bindings = append(bindings, tuple{"@": item})
		}
	} else {
		bindings = tupleBindings.items
	}
	for _, binding := range bindings {
		stepEnv := createFrameFromTuple(env, binding)
		res, err := e.evaluate(expr, tupleValue(binding, "@"), stepEnv)
		if err != nil {
			return nil, err
		}
		if res == nil {
			continue
		}
		resArr, isArr := res.(*Array)
		if !isArr {
			resArr = newArray(res)
		}
		for bb, r := range resArr.items {
			t := tuple{}
			copyTuple(t, binding)
			if resArr.tupleStream {
				copyTuple(t, r)
			} else {
				if expr.Focus != "" {
					t[expr.Focus] = r
					t["@"] = tupleValue(binding, "@")
				} else {
					t["@"] = r
				}
				if expr.Index != "" {
					t[expr.Index] = float64(bb)
				}
				if expr.Ancestor != nil {
					t[expr.Ancestor.Label] = tupleValue(binding, "@")
				}
			}
			if err := result.add(t); err != nil {
				return nil, err
			}
		}
	}
	if expr.Stages != nil {
		staged, err := e.evaluateStages(expr.Stages, result, env)
		if err != nil {
			return nil, err
		}
		return asArray(staged), nil
	}
	return result, nil
}

// copyTuple is Object.assign(dst, src) for tuples.
func copyTuple(dst tuple, src any) {
	switch x := src.(type) {
	case tuple:
		for k, v := range x {
			dst[k] = v
		}
	case *Object:
		for _, k := range x.Keys() {
			dst[k] = x.vals[k]
		}
	}
}

// evaluateFilter applies a predicate (or an array index) to the input.
func (e *evaluator) evaluateFilter(predicate *Node, input any, env *frame) (any, error) {
	base := env.base
	results := base.createSequence()
	if arr, ok := input.(*Array); ok && arr.tupleStream {
		results.tupleStream = true
	}
	arr, ok := input.(*Array)
	if !ok {
		arr = base.createSequence(input)
	}
	if predicate.Type == "number" {
		index := int(math.Floor(predicate.Value.(float64)))
		if index < 0 {
			// count in from the end of the array
			index = len(arr.items) + index
		}
		item := arr.at(index)
		if item != nil {
			if a, ok := item.(*Array); ok {
				return a, nil
			}
			if err := results.add(item); err != nil {
				return nil, err
			}
		}
		return results, nil
	}
	for index, item := range arr.items {
		context := item
		ctxEnv := env
		if arr.tupleStream {
			context = tupleValue(item, "@")
			ctxEnv = createFrameFromTuple(env, item)
		}
		res, err := e.evaluate(predicate, context, ctxEnv)
		if err != nil {
			return nil, err
		}
		num, err := isNumeric(res)
		if err != nil {
			return nil, err
		}
		if num {
			res = newArray(res)
		}
		allNumbers, err := isArrayOfNumbers(res)
		if err != nil {
			return nil, err
		}
		if allNumbers {
			for _, ires := range res.(*Array).items {
				ii := int(math.Floor(ires.(float64)))
				if ii < 0 {
					ii = len(arr.items) + ii
				}
				if ii == index {
					if err := results.add(item); err != nil {
						return nil, err
					}
				}
			}
		} else if truthy, err := toBoolean(res); err != nil {
			return nil, err
		} else if truthy == true {
			if err := results.add(item); err != nil {
				return nil, err
			}
		}
	}
	return results, nil
}

// annotate writes the operator's position and token onto an error raised by an operator.
func annotate(err error, expr *Node, token string) error {
	if je, ok := err.(*Error); ok {
		je.Position, je.HasPosition = expr.Position, expr.HasPos
		je.setToken(token)
	}
	return err
}

func (e *evaluator) evaluateBinary(expr *Node, input any, env *frame) (any, error) {
	lhs, err := e.evaluate(expr.LHS, input, env)
	if err != nil {
		return nil, err
	}
	op := expr.stringValue()
	if op == "and" || op == "or" {
		// the rhs is evaluated only when needed
		lBool, err := boolize(lhs)
		if err != nil {
			return nil, annotate(err, expr, op)
		}
		if (op == "and" && !lBool) || (op == "or" && lBool) {
			return lBool, nil
		}
		rhs, err := e.evaluate(expr.RHS, input, env)
		if err != nil {
			return nil, annotate(err, expr, op)
		}
		rBool, err := boolize(rhs)
		if err != nil {
			return nil, annotate(err, expr, op)
		}
		return rBool, nil
	}
	rhs, err := e.evaluate(expr.RHS, input, env)
	if err != nil {
		return nil, err
	}
	var result any
	switch op {
	case "+", "-", "*", "/", "%":
		result, err = evaluateNumericExpression(lhs, rhs, op)
	case "=", "!=":
		result = evaluateEqualityExpression(lhs, rhs, op)
	case "<", "<=", ">", ">=":
		result, err = evaluateComparisonExpression(lhs, rhs, op)
	case "&":
		result, err = evaluateStringConcat(lhs, rhs)
	case "..":
		result, err = evaluateRangeExpression(lhs, rhs, env.base)
	case "in":
		result = evaluateIncludesExpression(lhs, rhs)
	}
	if err != nil {
		return nil, annotate(err, expr, op)
	}
	return result, nil
}

func (e *evaluator) evaluateUnary(expr *Node, input any, env *frame) (any, error) {
	switch expr.stringValue() {
	case "-":
		result, err := e.evaluate(expr.Expression, input, env)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, nil
		}
		num, err := isNumeric(result)
		if err != nil {
			return nil, err
		}
		if num {
			return -result.(float64), nil
		}
		return nil, &Error{Code: "D1002", Position: expr.Position, HasPosition: expr.HasPos, Token: expr.value(), HasToken: true,
			Value: result, HasValue: true}
	case "[":
		// array constructor: evaluate each item
		result := newArray()
		for _, item := range expr.Expressions {
			value, err := e.evaluate(item, input, env)
			if err != nil {
				return nil, err
			}
			if value == nil {
				continue
			}
			if item.value() == "[" {
				result.push(value)
			} else {
				appended, err := appendValues(env.base, result, value)
				if err != nil {
					return nil, err
				}
				result = appended.(*Array)
			}
		}
		if expr.ConsArray {
			result.cons = true
		}
		return result, nil
	case "{":
		// object constructor
		return e.evaluateGroupExpression(expr.Pairs, expr.Position, expr.HasPos, input, env)
	}
	return nil, nil
}

// lookupValue is fn.lookup: the value of a key in an object, or the sequence of the values of the
// key in the items of an array.
func lookupValue(input any, key string, base *evaluation) any {
	switch x := input.(type) {
	case *Array:
		result := base.createSequence()
		for _, item := range x.items {
			res := lookupValue(item, key, base)
			if res == nil {
				continue
			}
			if arr, ok := res.(*Array); ok {
				result.items = append(result.items, arr.items...)
			} else {
				result.items = append(result.items, res)
			}
		}
		return result
	case *Object:
		v, _ := x.Get(key)
		return v
	case tuple:
		return x[key]
	}
	return nil
}

func (e *evaluator) evaluateWildcard(input any, env *frame) (any, error) {
	base := env.base
	var results any = base.createSequence()
	if arr, ok := input.(*Array); ok && arr.outerWrapper && len(arr.items) > 0 {
		input = arr.items[0]
	}
	if !isObjectLike(input) || isFunction(input) {
		return results, nil
	}
	for _, key := range keysOf(input) {
		value := memberOf(input, key)
		if arr, ok := value.(*Array); ok {
			flat := flatten(arr, newArray())
			var err error
			if results, err = appendValues(base, results, flat); err != nil {
				return nil, err
			}
		} else {
			results.(*Array).push(value)
		}
	}
	return results, nil
}

// memberOf is input[key] for an object-like value.
func memberOf(input any, key string) any {
	switch x := input.(type) {
	case *Object:
		v, _ := x.Get(key)
		return v
	case *Array:
		if key == "sequence" {
			return x.sequence
		}
		if i, ok := arrayIndex(key); ok && int(i) < len(x.items) {
			return x.items[i]
		}
	case tuple:
		return x[key]
	}
	return nil
}

// flatten flattens nested arrays into one plain array.
func flatten(arg any, flattened *Array) *Array {
	if arr, ok := arg.(*Array); ok {
		for _, item := range arr.items {
			flatten(item, flattened)
		}
	} else {
		flattened.push(arg)
	}
	return flattened
}

func evaluateDescendants(input any, env *frame) any {
	if input == nil {
		return nil
	}
	resultSequence := env.base.createSequence()
	recurseDescendants(input, resultSequence)
	if len(resultSequence.items) == 1 {
		return resultSequence.items[0]
	}
	return resultSequence
}

func recurseDescendants(input any, results *Array) {
	// the equivalent of //* in XPath
	arr, isArr := input.(*Array)
	if !isArr {
		results.push(input)
	}
	if isArr {
		for _, member := range arr.items {
			recurseDescendants(member, results)
		}
	} else if isObjectLike(input) && !isFunction(input) {
		for _, key := range keysOf(input) {
			recurseDescendants(memberOf(input, key), results)
		}
	}
}

func evaluateNumericExpression(lhs, rhs any, op string) (any, error) {
	if lhs != nil {
		num, err := isNumeric(lhs)
		if err != nil {
			return nil, err
		}
		if !num {
			return nil, &Error{Code: "T2001", Value: lhs, HasValue: true}
		}
	}
	if rhs != nil {
		num, err := isNumeric(rhs)
		if err != nil {
			return nil, err
		}
		if !num {
			return nil, &Error{Code: "T2002", Value: rhs, HasValue: true}
		}
	}
	if lhs == nil || rhs == nil {
		return nil, nil
	}
	l, r := lhs.(float64), rhs.(float64)
	switch op {
	case "+":
		return l + r, nil
	case "-":
		return l - r, nil
	case "*":
		return l * r, nil
	case "/":
		return l / r, nil
	}
	return math.Mod(l, r), nil
}

func evaluateEqualityExpression(lhs, rhs any, op string) bool {
	if lhs == nil || rhs == nil {
		return false
	}
	if op == "=" {
		return isDeepEqual(lhs, rhs)
	}
	return !isDeepEqual(lhs, rhs)
}

func evaluateComparisonExpression(lhs, rhs any, op string) (any, error) {
	ltype, rtype := typeOf(lhs), typeOf(rhs)
	comparable := func(t string) bool { return t == "undefined" || t == "string" || t == "number" }
	if !comparable(ltype) || !comparable(rtype) {
		value := rhs
		if !(ltype == "string" || ltype == "number") {
			value = lhs
		}
		return nil, &Error{Code: "T2010", Value: value, HasValue: value != nil}
	}
	if lhs == nil || rhs == nil {
		return nil, nil
	}
	if ltype != rtype {
		return nil, &Error{Code: "T2009", Value: lhs, HasValue: true, Value2: rhs, HasValue2: true}
	}
	var c int
	if ltype == "number" {
		l, r := lhs.(float64), rhs.(float64)
		if math.IsNaN(l) || math.IsNaN(r) {
			return false, nil
		}
		switch {
		case l < r:
			c = -1
		case l > r:
			c = 1
		}
	} else {
		c = compareUTF16(lhs.(string), rhs.(string))
	}
	switch op {
	case "<":
		return c < 0, nil
	case "<=":
		return c <= 0, nil
	case ">":
		return c > 0, nil
	}
	return c >= 0, nil
}

func evaluateIncludesExpression(lhs, rhs any) bool {
	if lhs == nil || rhs == nil {
		return false
	}
	arr, ok := rhs.(*Array)
	if !ok {
		arr = newArray(rhs)
	}
	for _, item := range arr.items {
		if strictEquals(item, lhs) {
			return true
		}
	}
	return false
}

// boolize is the truth value of an operand of and/or: undefined is false.
func boolize(value any) (bool, error) {
	b, err := toBoolean(value)
	if err != nil {
		return false, err
	}
	return b == true, nil
}

func evaluateStringConcat(lhs, rhs any) (any, error) {
	lstr, rstr := "", ""
	if lhs != nil {
		s, err := toJSONataString(lhs, false)
		if err != nil {
			return nil, err
		}
		lstr = s.(string)
	}
	if rhs != nil {
		s, err := toJSONataString(rhs, false)
		if err != nil {
			return nil, err
		}
		rstr = s.(string)
	}
	return concatStrings(lstr, rstr), nil
}

// groupEntry collects the items that evaluate to one key, with the pair that produced it.
type groupEntry struct {
	data      any
	exprIndex int
}

// evaluateGroupExpression is the object constructor and the group-by clause.
func (e *evaluator) evaluateGroupExpression(pairs [][2]*Node, position int, hasPos bool, input any, env *frame) (any, error) {
	base := env.base
	result := NewObject()
	groups := NewObject()
	inArr, isArr := input.(*Array)
	reduce := isArr && inArr.tupleStream
	if !isArr {
		inArr = base.createSequence(input)
	}
	// an empty input still produces the object of a literal constructor
	if len(inArr.items) == 0 && !reduce {
		inArr.items = append(inArr.items, nil)
	}
	for _, item := range inArr.items {
		itemEnv := env
		context := item
		if reduce {
			itemEnv = createFrameFromTuple(env, item)
			context = tupleValue(item, "@")
		}
		for pairIndex, pair := range pairs {
			key, err := e.evaluate(pair[0], context, itemEnv)
			if err != nil {
				return nil, err
			}
			k, isString := key.(string)
			if !isString && key != nil {
				return nil, &Error{Code: "T1003", Position: position, HasPosition: hasPos, Value: key, HasValue: true}
			}
			if key == nil {
				continue
			}
			// the internal JSONata flags cannot be set
			if k == "_jsonata_lambda" || k == "_jsonata_function" {
				return nil, &Error{Code: "D1013", Position: position, HasPosition: hasPos, Value: k, HasValue: true}
			}
			if existing, ok := groups.Get(k); ok {
				entry := existing.(*groupEntry)
				if entry.exprIndex != pairIndex {
					// the key is also produced by another pair of this constructor
					return nil, &Error{Code: "D1009", Position: position, HasPosition: hasPos, Value: k, HasValue: true}
				}
				if entry.data, err = appendValues(base, entry.data, item); err != nil {
					return nil, err
				}
			} else {
				groups.Set(k, &groupEntry{data: item, exprIndex: pairIndex})
			}
		}
	}
	for _, key := range groups.Keys() {
		v, _ := groups.Get(key)
		entry := v.(*groupEntry)
		context := entry.data
		valueEnv := env
		if reduce {
			t := reduceTupleStream(entry.data, base)
			context = tupleValue(t, "@")
			deleteTupleMember(t, "@")
			valueEnv = createFrameFromTuple(env, t)
		}
		value, err := e.evaluate(pairs[entry.exprIndex][1], context, valueEnv)
		if err != nil {
			return nil, err
		}
		if value != nil {
			result.Set(key, value)
		}
	}
	return result, nil
}

func deleteTupleMember(t any, key string) {
	switch x := t.(type) {
	case tuple:
		delete(x, key)
	case *Object:
		x.Delete(key)
	}
}

// reduceTupleStream merges a group's tuples into one, appending the values of each member.
func reduceTupleStream(tupleStream any, base *evaluation) any {
	arr, ok := tupleStream.(*Array)
	if !ok {
		return tupleStream
	}
	result := tuple{}
	if len(arr.items) > 0 {
		copyTuple(result, arr.items[0])
	}
	for _, t := range arr.items[1:] {
		for _, prop := range keysOf(t) {
			merged, _ := appendValues(base, result[prop], tupleValue(t, prop))
			result[prop] = merged
		}
	}
	return result
}

func evaluateRangeExpression(lhs, rhs any, base *evaluation) (any, error) {
	if lhs != nil && !isInteger(lhs) {
		return nil, &Error{Code: "T2003", Value: lhs, HasValue: true}
	}
	if rhs != nil && !isInteger(rhs) {
		return nil, &Error{Code: "T2004", Value: rhs, HasValue: true}
	}
	if lhs == nil || rhs == nil {
		return nil, nil
	}
	l, r := lhs.(float64), rhs.(float64)
	if l > r {
		return nil, nil
	}
	// the size is limited to ten million entries
	size := r - l + 1
	if size > 1e7 {
		return nil, &Error{Code: "D2014", Value: size, HasValue: true}
	}
	if base.options.Sequence > 0 && size > float64(base.options.Sequence) {
		return nil, &Error{Code: "D2015", Value: size, HasValue: true}
	}
	result := &Array{items: make([]any, 0, int(size)), sequence: true}
	for item := l; item <= r; item++ {
		result.items = append(result.items, item)
	}
	return result, nil
}

func (e *evaluator) evaluateBindExpression(expr *Node, input any, env *frame) (any, error) {
	value, err := e.evaluate(expr.RHS, input, env)
	if err != nil {
		return nil, err
	}
	env.bind(expr.LHS.stringValue(), value)
	return value, nil
}

func (e *evaluator) evaluateCondition(expr *Node, input any, env *frame) (any, error) {
	condition, err := e.evaluate(expr.Condition, input, env)
	if err != nil {
		return nil, err
	}
	truthy, err := toBoolean(condition)
	if err != nil {
		return nil, err
	}
	if truthy == true {
		return e.evaluate(expr.Then, input, env)
	}
	if expr.Else != nil {
		return e.evaluate(expr.Else, input, env)
	}
	return nil, nil
}

func (e *evaluator) evaluateBlock(expr *Node, input any, env *frame) (any, error) {
	// a block has its own scope for variable assignments
	blockFrame := newFrame(env)
	var result any
	for _, item := range expr.Expressions {
		var err error
		if result, err = e.evaluate(item, input, blockFrame); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// evaluateRegex prepares a regex literal: a matcher function that returns the first match at or after
// a position, with a next() for the following match.
func (e *evaluator) evaluateRegex(expr *Node) (any, error) {
	lit := expr.Value.(*regexLiteral)
	re, err := jsre.Compile(lit.pattern, lit.flags)
	if err != nil {
		return nil, errHostRuntime
	}
	lastIndex := 0
	var closure *nativeFunc
	closure = &nativeFunc{arity: 2, params: "str, fromIndex"}
	closure.call = func(args []any) (any, error) {
		str := argString(args, 0)
		lastIndex = 0
		if len(args) > 1 {
			if f, ok := args[1].(float64); ok && f != 0 && !math.IsNaN(f) {
				lastIndex = int(f)
			}
		}
		units := utf16Units(str)
		match, next := re.Exec(units, lastIndex)
		if match == nil {
			lastIndex = 0
			return nil, nil
		}
		lastIndex = next
		start, end := match[0], match[1]
		result := NewObject()
		result.Set("match", fromUnits(units[start:end]))
		result.Set("start", float64(start))
		result.Set("end", float64(end))
		groups := newArray()
		for i := 1; i < len(match)/2; i++ {
			if match[2*i] < 0 {
				groups.push(nil)
			} else {
				groups.push(fromUnits(units[match[2*i]:match[2*i+1]]))
			}
		}
		result.Set("groups", groups)
		nextFn := &nativeFunc{arity: 0}
		nextFn.call = func([]any) (any, error) {
			if lastIndex >= len(units) {
				return nil, nil
			}
			n, err := closure.call([]any{str, float64(lastIndex)})
			if err != nil {
				return nil, err
			}
			if obj, ok := n.(*Object); ok {
				if m, _ := obj.Get("match"); m == "" {
					// a zero-length match would never progress
					return nil, &Error{Code: "D1004", Position: expr.Position, HasPosition: expr.HasPos, Value: lit.pattern, HasValue: true}
				}
			}
			return n, nil
		}
		result.Set("next", nextFn)
		return result, nil
	}
	return closure, nil
}

// argString is the string a host function receives for args[i] (String(value)).
func argString(args []any, i int) string {
	if i >= len(args) {
		return "undefined"
	}
	if s, ok := args[i].(string); ok {
		return s
	}
	return toStringJS(args[i])
}

func evaluateVariable(expr *Node, input any, env *frame) any {
	name := expr.stringValue()
	if name == "" {
		// the context value
		if arr, ok := input.(*Array); ok && arr.outerWrapper {
			return arr.at(0)
		}
		return input
	}
	return env.lookup(name)
}

// evaluateSortExpression is the order-by clause.
func (e *evaluator) evaluateSortExpression(expr *Node, arr *Array, env *frame) (any, error) {
	isTupleSort := arr.tupleStream
	comparator := func(a, b any) (bool, error) {
		comp := 0
		for index := 0; comp == 0 && index < len(expr.Terms); index++ {
			term := expr.Terms[index]
			context, termEnv := a, env
			if isTupleSort {
				context = tupleValue(a, "@")
				termEnv = createFrameFromTuple(env, a)
			}
			aa, err := e.evaluate(term.Expression, context, termEnv)
			if err != nil {
				return false, err
			}
			context, termEnv = b, env
			if isTupleSort {
				context = tupleValue(b, "@")
				termEnv = createFrameFromTuple(env, b)
			}
			bb, err := e.evaluate(term.Expression, context, termEnv)
			if err != nil {
				return false, err
			}
			atype, btype := typeOf(aa), typeOf(bb)
			// undefined sorts last
			if atype == "undefined" {
				if btype != "undefined" {
					comp = 1
				}
				continue
			}
			if btype == "undefined" {
				comp = -1
				continue
			}
			if !(atype == "string" || atype == "number") || !(btype == "string" || btype == "number") {
				value := bb
				if !(atype == "string" || atype == "number") {
					value = aa
				}
				return false, &Error{Code: "T2008", Position: expr.Position, HasPosition: expr.HasPos, Value: value, HasValue: true}
			}
			if atype != btype {
				return false, &Error{Code: "T2007", Position: expr.Position, HasPosition: expr.HasPos, Value: aa, HasValue: true, Value2: bb, HasValue2: true}
			}
			if strictEquals(aa, bb) {
				continue
			}
			if lessThan(aa, bb) {
				comp = -1
			} else {
				comp = 1
			}
			if term.Descending {
				comp = -comp
			}
		}
		return comp == 1, nil
	}
	return mergeSort(arr, comparator)
}

// lessThan is JavaScript's < on two numbers or two strings.
func lessThan(a, b any) bool {
	switch x := a.(type) {
	case float64:
		return x < b.(float64)
	case string:
		return compareUTF16(x, b.(string)) < 0
	}
	return false
}

// mergeSort is the stable merge sort of fn.sort; swap(a, b) reports whether b must come before a.
// An array of one item or less is returned as it is.
func mergeSort(arr *Array, swap func(a, b any) (bool, error)) (any, error) {
	if len(arr.items) <= 1 {
		return arr, nil
	}
	var msort func(items []any) ([]any, error)
	msort = func(items []any) ([]any, error) {
		if len(items) <= 1 {
			return items, nil
		}
		middle := len(items) / 2
		left, err := msort(append([]any(nil), items[:middle]...))
		if err != nil {
			return nil, err
		}
		right, err := msort(append([]any(nil), items[middle:]...))
		if err != nil {
			return nil, err
		}
		merged := make([]any, 0, len(left)+len(right))
		for len(left) > 0 && len(right) > 0 {
			sw, err := swap(left[0], right[0])
			if err != nil {
				return nil, err
			}
			if sw {
				merged = append(merged, right[0])
				right = right[1:]
			} else {
				merged = append(merged, left[0])
				left = left[1:]
			}
		}
		merged = append(merged, left...)
		return append(merged, right...), nil
	}
	sorted, err := msort(arr.items)
	if err != nil {
		return nil, err
	}
	return &Array{items: sorted}, nil
}

// evaluateTransformExpression creates the function of a transform expression |pattern|update[,delete]|.
func (e *evaluator) evaluateTransformExpression(expr *Node, env *frame) any {
	transformer := func(f *focus, args []any) (any, error) {
		obj := argAt(args, 0)
		if obj == nil {
			return nil, nil
		}
		// the copy is made by the $clone() function in scope
		cloneFunction := env.lookup("clone")
		if !isFunction(cloneFunction) {
			return nil, &Error{Code: "T2013", Position: expr.Position, HasPosition: expr.HasPos}
		}
		result, err := e.apply(cloneFunction, []any{obj}, Null, env)
		if err != nil {
			return nil, err
		}
		matches, err := e.evaluate(expr.Pattern, result, env)
		if err != nil {
			return nil, err
		}
		if matches == nil {
			return result, nil
		}
		matchList, ok := matches.(*Array)
		if !ok {
			matchList = newArray(matches)
		}
		for _, match := range matchList.items {
			update, err := e.evaluate(expr.Update, match, env)
			if err != nil {
				return nil, err
			}
			if update != nil {
				if !isObjectLike(update) || isArray(update) {
					return nil, &Error{Code: "T2011", Position: expr.Update.Position, HasPosition: expr.Update.HasPos, Value: update, HasValue: true}
				}
				for _, prop := range keysOf(update) {
					if err := setMember(match, prop, memberOf(update, prop)); err != nil {
						return nil, err
					}
				}
			}
			if expr.Delete == nil {
				continue
			}
			deletions, err := e.evaluate(expr.Delete, match, env)
			if err != nil {
				return nil, err
			}
			if deletions == nil {
				continue
			}
			val := deletions
			delList, ok := deletions.(*Array)
			if !ok {
				delList = newArray(deletions)
			}
			if !isArrayOfStrings(delList) {
				return nil, &Error{Code: "T2012", Position: expr.Delete.Position, HasPosition: expr.Delete.HasPos, Value: val, HasValue: true}
			}
			for _, d := range delList.items {
				if obj, ok := match.(*Object); ok {
					obj.Delete(d.(string))
				}
			}
		}
		return result, nil
	}
	return defineFunction("transform", "obj", 1, transformer, "<(oa):o>")
}

// setMember is match[prop] = value; a primitive cannot take a property in strict mode.
func setMember(target any, prop string, value any) error {
	switch x := target.(type) {
	case *Object:
		x.Set(prop, value)
		return nil
	case *Array:
		if i, ok := arrayIndex(prop); ok && int(i) < len(x.items) {
			x.items[i] = value
		}
		return nil
	case tuple:
		x[prop] = value
		return nil
	}
	return errHostRuntime
}

func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// chainSource composes two functions for `f ~> g`.
const chainSource = "function($f, $g) { function($x){ $g($f($x)) } }"

func (e *evaluator) evaluateApplyExpression(expr *Node, input any, env *frame) (any, error) {
	lhs, err := e.evaluate(expr.LHS, input, env)
	if err != nil {
		return nil, err
	}
	if expr.RHS.Type == "function" {
		// a function invocation, with the lhs as its first argument
		return e.evaluateFunction(expr.RHS, input, env, lhs, true)
	}
	fn, err := e.evaluate(expr.RHS, input, env)
	if err != nil {
		return nil, err
	}
	if !isFunction(fn) {
		return nil, &Error{Code: "T2006", Position: expr.Position, HasPosition: expr.HasPos, Value: fn, HasValue: fn != nil}
	}
	if isFunction(lhs) {
		// function composition
		chain, err := e.evaluate(e.chainAST, Null, env)
		if err != nil {
			return nil, err
		}
		return e.apply(chain, []any{lhs, fn}, Null, env)
	}
	return e.apply(fn, []any{lhs}, Null, env)
}

// procedureName is the name a function invocation reports in errors.
func procedureName(expr *Node) (any, bool) {
	if expr.Procedure.Type == "path" {
		v := expr.Procedure.Steps[0].value()
		return v, v != nil || expr.Procedure.Steps[0].HasValue
	}
	v := expr.Procedure.value()
	return v, v != nil || expr.Procedure.HasValue
}

// mark records an invocation on a function object (proc.token = name; proc.position = position).
// Setting a property on null fails, as it does in the original.
func mark(proc any, token any, hasToken bool, expr *Node) error {
	switch x := proc.(type) {
	case *Lambda:
		x.setInvocation(token, hasToken, expr.Position, expr.HasPos)
	case *Function:
		x.setInvocation(token, hasToken, expr.Position, expr.HasPos)
	case *Object:
		if hasToken {
			x.Set("token", token)
		}
		if expr.HasPos {
			x.Set("position", float64(expr.Position))
		}
	case nullValue:
		return errHostRuntime
	}
	return nil
}

func (e *evaluator) evaluateFunction(expr *Node, input any, env *frame, context any, applyTo bool) (any, error) {
	proc, err := e.evaluate(expr.Procedure, input, env)
	if err != nil {
		return nil, err
	}
	if proc == nil && expr.Procedure.Type == "path" {
		if name, ok := expr.Procedure.Steps[0].value().(string); ok && jsTruthy(env.lookup(name)) {
			// the user probably forgot the leading $
			return nil, &Error{Code: "T1005", Position: expr.Position, HasPosition: expr.HasPos, Token: name, HasToken: true}
		}
	}
	var evaluatedArgs []any
	if applyTo {
		evaluatedArgs = append(evaluatedArgs, context)
	}
	for _, argExpr := range expr.Arguments {
		arg, err := e.evaluate(argExpr, input, env)
		if err != nil {
			return nil, err
		}
		if isFunction(arg) {
			evaluatedArgs = append(evaluatedArgs, e.wrapFunction(arg, env))
		} else {
			evaluatedArgs = append(evaluatedArgs, arg)
		}
	}
	procName, hasName := procedureName(expr)
	result, err := e.invoke(proc, procName, hasName, expr, evaluatedArgs, input, env)
	if err != nil {
		if je, ok := err.(*Error); ok {
			if !je.positionTruthy() {
				je.Position, je.HasPosition = expr.Position, expr.HasPos
			}
			if !je.tokenTruthy() {
				je.Token, je.HasToken = procName, hasName
			}
		}
		return nil, err
	}
	return result, nil
}

func (e *evaluator) invoke(proc any, procName any, hasName bool, expr *Node, args []any, input any, env *frame) (any, error) {
	if err := mark(proc, procName, hasName, expr); err != nil {
		return nil, err
	}
	return e.apply(proc, args, input, env)
}

// wrapFunction is the closure a function argument is passed as: it applies the function in the
// caller's environment.
func (e *evaluator) wrapFunction(fn any, env *frame) *nativeFunc {
	return &nativeFunc{arity: getFunctionArity(fn), params: "...params", call: func(params []any) (any, error) {
		return e.apply(fn, params, nil, env)
	}}
}

// apply applies a function, running the trampoline for tail calls.
func (e *evaluator) apply(proc any, args []any, input any, env *frame) (any, error) {
	result, err := e.applyInner(proc, args, input, env)
	if err != nil {
		return nil, err
	}
	for {
		thunk, ok := result.(*Lambda)
		if !ok || !thunk.thunk {
			return result, nil
		}
		// a tail call: evaluate the procedure and its arguments, then apply it
		call := thunk.body
		next, err := e.evaluate(call.Procedure, thunk.input, thunk.environment)
		if err != nil {
			return nil, err
		}
		var token any
		hasToken := false
		if call.Procedure.Type == "variable" {
			token, hasToken = call.Procedure.value(), true
		}
		if err := markThunkTarget(next, token, hasToken, call.Procedure); err != nil {
			return nil, err
		}
		var evaluatedArgs []any
		for _, argExpr := range call.Arguments {
			arg, err := e.evaluate(argExpr, thunk.input, thunk.environment)
			if err != nil {
				return nil, err
			}
			evaluatedArgs = append(evaluatedArgs, arg)
		}
		if result, err = e.applyInner(next, evaluatedArgs, input, env); err != nil {
			return nil, err
		}
	}
}

// markThunkTarget writes the token (for a variable) and position of a tail call onto its target;
// primitives, null and undefined cannot take them.
func markThunkTarget(next any, token any, hasToken bool, procedure *Node) error {
	var in *invocation
	switch x := next.(type) {
	case *Lambda:
		in = &x.invocation
	case *Function:
		in = &x.invocation
	case *nativeFunc:
		in = &x.invocation
	case *Object:
		if hasToken {
			x.Set("token", token)
		}
		if procedure.HasPos {
			x.Set("position", float64(procedure.Position))
		} else {
			x.Delete("position")
		}
		return nil
	case *Array, tuple:
		return nil
	default:
		return errHostRuntime
	}
	if hasToken {
		in.token, in.hasToken = token, true
	}
	if procedure.HasPos {
		in.position, in.hasPosition = float64(procedure.Position), true
	} else {
		in.position, in.hasPosition = nil, false
	}
	return nil
}

// invocationOf returns the token and position last written onto a function value.
func invocationOf(proc any) (token any, hasToken bool, position any, hasPosition bool) {
	switch x := proc.(type) {
	case *Lambda:
		return x.token, x.hasToken, x.position, x.hasPosition
	case *Function:
		return x.token, x.hasToken, x.position, x.hasPosition
	case *nativeFunc:
		return x.token, x.hasToken, x.position, x.hasPosition
	case *Object:
		t, ht := x.Get("token")
		p, hp := x.Get("position")
		return t, ht, p, hp
	}
	return nil, false, nil, false
}

func (e *evaluator) applyInner(proc any, args []any, input any, env *frame) (any, error) {
	result, err := e.applyProc(proc, args, input, env)
	if err != nil && jsTruthy(proc) {
		if je, ok := err.(*Error); ok {
			token, hasToken, position, _ := invocationOf(proc)
			if !je.HasToken && hasToken {
				je.Token, je.HasToken = token, true
			}
			if p, ok := position.(float64); ok && p != 0 {
				je.Position, je.HasPosition = int(p), true
			}
		}
	}
	return result, err
}

func (e *evaluator) applyProc(proc any, args []any, input any, env *frame) (any, error) {
	validated := args
	if sig := signatureOf(proc); sig != nil {
		var err error
		if validated, err = sig.validate(args, input); err != nil {
			return nil, err
		}
	}
	switch fn := proc.(type) {
	case *Lambda:
		return e.applyProcedure(fn, validated)
	case *Function:
		return fn.impl(&focus{env: env, input: input, ev: e}, validated)
	case *nativeFunc:
		return fn.call(validated)
	}
	return nil, &Error{Code: "T1006"}
}

func signatureOf(proc any) *Signature {
	switch fn := proc.(type) {
	case *Lambda:
		return fn.signature
	case *Function:
		return fn.signature
	}
	return nil
}

// jsTruthy is JavaScript's ToBoolean.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil, nullValue:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

func evaluateLambda(expr *Node, input any, env *frame) *Lambda {
	return &Lambda{input: input, environment: env, arguments: expr.Arguments, signature: expr.Signature, body: expr.Body, thunk: expr.Thunk}
}

// isPlaceholder reports whether an argument of a partial application is the ? placeholder.
func isPlaceholder(arg any) bool {
	switch x := arg.(type) {
	case *Node:
		return x.Type == "operator" && x.value() == "?"
	case *Object:
		t, _ := x.Get("type")
		v, _ := x.Get("value")
		return t == "operator" && v == "?"
	}
	return false
}

func (e *evaluator) evaluatePartialApplication(expr *Node, input any, env *frame) (any, error) {
	var evaluatedArgs []any
	for _, arg := range expr.Arguments {
		if arg.Type == "operator" && arg.value() == "?" {
			evaluatedArgs = append(evaluatedArgs, arg)
			continue
		}
		v, err := e.evaluate(arg, input, env)
		if err != nil {
			return nil, err
		}
		evaluatedArgs = append(evaluatedArgs, v)
	}
	proc, err := e.evaluate(expr.Procedure, input, env)
	if err != nil {
		return nil, err
	}
	if proc == nil && expr.Procedure.Type == "path" {
		if name, ok := expr.Procedure.Steps[0].value().(string); ok && jsTruthy(env.lookup(name)) {
			return nil, &Error{Code: "T1007", Position: expr.Position, HasPosition: expr.HasPos, Token: name, HasToken: true}
		}
	}
	switch fn := proc.(type) {
	case *Lambda:
		return partialApplyProcedure(fn, evaluatedArgs, env), nil
	case *Function:
		return e.partialApplyNativeFunction(fn.params, fn, nil, evaluatedArgs, env)
	case *nativeFunc:
		return e.partialApplyNativeFunction(fn.params, nil, fn, evaluatedArgs, env)
	}
	token, hasToken := procedureName(expr)
	return nil, &Error{Code: "T1008", Position: expr.Position, HasPosition: expr.HasPos, Token: token, HasToken: hasToken}
}

func (e *evaluator) applyProcedure(proc *Lambda, args []any) (any, error) {
	env := newFrame(proc.environment)
	for index, param := range proc.arguments {
		env.bind(param.stringValue(), argAt(args, index))
	}
	if proc.native != nil || proc.nativeFn != nil {
		// a lambda that wraps a partially applied built-in
		return e.applyNativeFunction(proc, env)
	}
	return e.evaluate(proc.body, proc.input, env)
}

// partialApplyProcedure binds the supplied arguments and returns a function of the placeholders.
func partialApplyProcedure(proc *Lambda, args []any, env *frame) *Lambda {
	enclosing := proc.environment
	if enclosing == nil {
		enclosing = env
	}
	frame := newFrame(enclosing)
	var unbound []*Node
	for index, param := range proc.arguments {
		arg := argAt(args, index)
		if isPlaceholder(arg) {
			unbound = append(unbound, param)
		} else {
			frame.bind(param.stringValue(), arg)
		}
	}
	return &Lambda{input: proc.input, environment: frame, arguments: unbound, body: proc.body, native: proc.native, nativeFn: proc.nativeFn}
}

// nativeParams splits the implementation's parameter list as the original does.
func nativeParams(params string) []string {
	parts := strings.Split(params, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// partialApplyNativeFunction wraps a built-in in a lambda whose parameters are the implementation's
// parameter names, then partially applies that lambda.
func (e *evaluator) partialApplyNativeFunction(params string, fn *Function, native *nativeFunc, args []any, env *frame) (any, error) {
	names := nativeParams(params)
	sigArgs := make([]string, len(names))
	for i, n := range names {
		sigArgs[i] = "$" + n
	}
	body := "function(" + strings.Join(sigArgs, ", ") + "){ _ }"
	ast, _, err := parse(body, false)
	if err != nil {
		return nil, err
	}
	lambda := &Lambda{arguments: ast.Arguments, native: fn, nativeFn: native}
	return partialApplyProcedure(lambda, args, env), nil
}

// applyNativeFunction calls a partially applied built-in with the arguments looked up by name.
func (e *evaluator) applyNativeFunction(proc *Lambda, env *frame) (any, error) {
	var params string
	if proc.native != nil {
		params = proc.native.params
	} else {
		params = proc.nativeFn.params
	}
	names := nativeParams(params)
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = env.lookup(n)
	}
	if proc.nativeFn != nil {
		return proc.nativeFn.call(args)
	}
	// the arguments of a partially applied built-in are not validated against its signature
	return callUnvalidated(proc.native, &focus{env: env, ev: e}, args)
}

// appendValues is fn.append: the concatenation of two values as arrays; an undefined argument is ignored.
func appendValues(base *evaluation, arg1, arg2 any) (any, error) {
	if arg1 == nil {
		return arg2, nil
	}
	if arg2 == nil {
		return arg1, nil
	}
	a1, ok := arg1.(*Array)
	if !ok {
		a1 = base.createSequence(arg1)
	}
	a2, ok := arg2.(*Array)
	if !ok {
		a2 = newArray(arg2)
	}
	size := len(a1.items) + len(a2.items)
	if base != nil && base.options.Sequence > 0 && size > base.options.Sequence {
		return nil, &Error{Code: "D2015", Value: float64(size), HasValue: true}
	}
	return a1.concat(a2), nil
}

// callUnvalidated calls a partially applied built-in. The original does not validate these arguments;
// the port still checks them against the signature, and an argument the implementation cannot take
// fails as a host error rather than with a JSONata error.
func callUnvalidated(fn *Function, f *focus, args []any) (any, error) {
	if fn.signature != nil {
		validated, err := fn.signature.validate(args, nil)
		if err != nil {
			return nil, errHostRuntime
		}
		args = validated
	}
	return fn.impl(f, args)
}

// toBoolean is fn.boolean, the effective boolean value: undefined (nil) for undefined, else a bool.
func toBoolean(arg any) (any, error) {
	switch x := arg.(type) {
	case nil:
		return nil, nil
	case *Array:
		switch len(x.items) {
		case 0:
			return false, nil
		case 1:
			return toBoolean(x.items[0])
		}
		for _, item := range x.items {
			b, err := toBoolean(item)
			if err != nil {
				return nil, err
			}
			if b == true {
				return true, nil
			}
		}
		return false, nil
	case string:
		return x != "", nil
	case bool:
		return x, nil
	case float64:
		num, err := isNumeric(x)
		if err != nil {
			return nil, err
		}
		return num && x != 0, nil
	case *Object:
		return x.Len() > 0, nil
	}
	return false, nil
}

// toJSONataString is fn.string: strings unchanged, functions as "", everything else as JSON with
// non-integral numbers rounded to 15 significant digits.
func toJSONataString(arg any, prettify bool) (any, error) {
	switch x := arg.(type) {
	case nil:
		return nil, nil
	case string:
		return x, nil
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return nil, &Error{Code: "D3001", Value: x, HasValue: true}
		}
	}
	if isFunction(arg) {
		return "", nil
	}
	if arr, ok := arg.(*Array); ok && arr.outerWrapper {
		arg = arr.at(0)
	}
	indent := 0
	if prettify {
		indent = 2
	}
	s, defined, err := stringifyJSON(arg, stringReplacer, indent)
	if err != nil {
		return nil, err
	}
	if !defined {
		return nil, nil
	}
	return s, nil
}

// stringReplacer is the JSON.stringify replacer of fn.string.
func stringReplacer(v any) (any, error) {
	if f, ok := v.(float64); ok {
		num, err := isNumeric(f)
		if err != nil {
			return nil, err
		}
		if num && f != math.Trunc(f) {
			return toPrecision15(f), nil
		}
		return v, nil
	}
	if isFunction(v) {
		return "", nil
	}
	return v, nil
}
