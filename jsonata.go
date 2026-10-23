/**
 * © Copyright IBM Corp. 2016, 2017 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Package jsonata is the JSONata query and transformation language (https://jsonata.org): a Go port of
// the JavaScript reference implementation.
//
// Values follow the JavaScript data model that JSONata is defined over: nil is undefined (no value),
// Null is the JSON null, numbers are float64, strings are UTF-16 text held as WTF-8, arrays are *Array
// and objects are *Object, whose keys enumerate in JavaScript property order. Inputs and bindings may
// also be given as []any, map[string]any and Go integer types; ParseJSON reads JSON text into values.
package jsonata

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"
)

// Options configure an expression: recovery mode for the parser, and the guardrails that limit an
// evaluation's time, evaluation depth and sequence length.
type Options struct {
	// Recover records syntax errors (see Errors) instead of failing to compile.
	Recover bool
	// Timeout ends an evaluation that runs longer (D1012); zero means no limit.
	Timeout time.Duration
	// Stack ends an evaluation whose depth exceeds it (D1011); zero means no limit.
	Stack int
	// Sequence ends an evaluation that builds a longer sequence (D2015); zero means no limit.
	Sequence int
}

// Expression is a compiled JSONata expression. An Expression keeps state between evaluations, as the
// original does, and must not be evaluated concurrently.
type Expression struct {
	ast         *Node
	errors      []*Error
	options     Options
	environment *frame
	ev          *evaluator
	timestamp   time.Time
}

// New compiles an expression. opts may be nil.
func New(expr string, opts *Options) (*Expression, error) {
	var options Options
	if opts != nil {
		options = *opts
	}
	ast, errs, err := parse(expr, options.Recover)
	if err != nil {
		return nil, finishError(err)
	}
	chain, _, err := parse(chainSource, false)
	if err != nil {
		return nil, err
	}
	e := &Expression{ast: ast, errors: errs, options: options, ev: &evaluator{chainAST: chain}}
	static := newFrame(nil)
	for _, fn := range builtins(e.ev) {
		static.bind(fn.name, fn)
	}
	e.environment = newFrame(static)
	e.environment.bind("now", defineFunction("now", "picture, timezone", 2, func(f *focus, args []any) (any, error) {
		return fnFromMillis(f, []any{float64(e.timestamp.UnixMilli()), argAt(args, 0), argAt(args, 1)})
	}, "<s?s?:s>"))
	e.environment.bind("millis", defineFunction("millis", "", 0, func(*focus, []any) (any, error) {
		return float64(e.timestamp.UnixMilli()), nil
	}, "<:n>"))
	return e, nil
}

// finishError populates the message of a JSONata error.
func finishError(err error) error {
	if je, ok := err.(*Error); ok {
		if perr := populateMessage(je); perr != nil {
			return perr
		}
	}
	return err
}

// Evaluate evaluates the expression against an input document (nil for none), with variable bindings.
func (e *Expression) Evaluate(input any, bindings map[string]any) (any, error) {
	if len(e.errors) > 0 {
		// the expression was compiled with syntax errors
		return nil, finishError(&Error{Code: "S0500", Position: 0, HasPosition: true})
	}
	exec := newFrame(e.environment)
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		exec.bind(name, ToValue(bindings[name]))
	}
	input = ToValue(input)
	exec.bind("$", input)
	e.timestamp = time.Now()
	base := &evaluation{options: e.options, start: e.timestamp, timestamp: e.timestamp, frame: exec}
	exec.base = base
	// an array input is wrapped in a singleton sequence, so that it is treated as one input
	if arr, ok := input.(*Array); ok && !arr.sequence {
		wrapper := base.createSequence(arr)
		wrapper.outerWrapper = true
		input = wrapper
	}
	result, err := e.ev.evaluate(e.ast, input, exec)
	if err != nil {
		return nil, finishError(err)
	}
	return result, nil
}

// Assign binds a value to a variable name for every evaluation of the expression.
func (e *Expression) Assign(name string, value any) {
	e.environment.bind(name, ToValue(value))
}

// RegisterFunction binds a Go function as a JSONata function. The signature (for example "<s-n?:s>")
// may be empty; arguments that match it are validated, and the context value is substituted where it
// allows. impl receives the arguments as values of this package's value model.
func (e *Expression) RegisterFunction(name string, impl func(args ...any) (any, error), signature string) error {
	fn := &Function{name: name, params: "args", arity: 0, impl: func(_ *focus, args []any) (any, error) {
		result, err := impl(args...)
		if err != nil {
			return nil, err
		}
		return ToValue(result), nil
	}}
	if signature != "" {
		sig, err := parseSignature(signature)
		if err != nil {
			return finishError(err)
		}
		fn.signature = sig
	}
	e.environment.bind(name, fn)
	return nil
}

// AST returns the syntax tree.
func (e *Expression) AST() *Node { return e.ast }

// Errors returns the syntax errors recorded in recovery mode.
func (e *Expression) Errors() []*Error {
	for _, err := range e.errors {
		if err.Message == "" {
			_ = populateMessage(err)
		}
	}
	return e.errors
}

// defineFunction creates a built-in function: params and arity describe the original implementation's
// parameter list, which partial application of the built-in relies on. The signature must be valid.
func defineFunction(name, params string, arity int, impl func(f *focus, args []any) (any, error), signature string) *Function {
	fn := &Function{name: name, params: params, arity: arity, impl: impl}
	if signature != "" {
		sig, err := parseSignature(signature)
		if err != nil {
			panic(fmt.Sprintf("jsonata: invalid built-in signature %s: %v", signature, err))
		}
		fn.signature = sig
	}
	return fn
}

// builtins is the function library, in the order the original binds it.
func builtins(e *evaluator) []*Function {
	d := defineFunction
	return []*Function{
		d("sum", "args", 1, fnSum, "<a<n>:n>"),
		d("count", "args", 1, fnCount, "<a:n>"),
		d("max", "args", 1, fnMax, "<a<n>:n>"),
		d("min", "args", 1, fnMin, "<a<n>:n>"),
		d("average", "args", 1, fnAverage, "<a<n>:n>"),
		d("string", "arg, prettify = false", 1, fnString, "<x-b?:s>"),
		d("substring", "str, start, length", 3, fnSubstring, "<s-nn?:s>"),
		d("substringBefore", "str, chars", 2, fnSubstringBefore, "<s-s:s>"),
		d("substringAfter", "str, chars", 2, fnSubstringAfter, "<s-s:s>"),
		d("lowercase", "str", 1, fnLowercase, "<s-:s>"),
		d("uppercase", "str", 1, fnUppercase, "<s-:s>"),
		d("length", "str", 1, fnLength, "<s-:n>"),
		d("trim", "str", 1, fnTrim, "<s-:s>"),
		d("pad", "str, width, char", 3, fnPad, "<s-ns?:s>"),
		d("match", "str, regex, limit", 3, fnMatch, "<s-f<s:o>n?:a<o>>"),
		d("contains", "str, token", 2, fnContains, "<s-(sf):b>"),
		d("replace", "str, pattern, replacement, limit", 4, fnReplace, "<s-(sf)(sf)n?:s>"),
		d("split", "str, separator, limit", 3, fnSplit, "<s-(sf)n?:a<s>>"),
		d("join", "strs, separator", 2, fnJoin, "<a<s>s?:s>"),
		d("formatNumber", "value, picture, options", 3, fnFormatNumber, "<n-so?:s>"),
		d("formatBase", "value, radix", 2, fnFormatBase, "<n-n?:s>"),
		d("formatInteger", "value, picture", 2, fnFormatInteger, "<n-s:s>"),
		d("parseInteger", "value, picture", 2, fnParseInteger, "<s-s:n>"),
		d("number", "arg", 1, fnNumber, "<(nsb)-:n>"),
		d("floor", "arg", 1, fnFloor, "<n-:n>"),
		d("ceil", "arg", 1, fnCeil, "<n-:n>"),
		d("round", "arg, precision", 2, fnRound, "<n-n?:n>"),
		d("abs", "arg", 1, fnAbs, "<n-:n>"),
		d("sqrt", "arg", 1, fnSqrt, "<n-:n>"),
		d("power", "arg, exp", 2, fnPower, "<n-n:n>"),
		d("random", "", 0, fnRandom, "<:n>"),
		d("boolean", "arg", 1, fnBoolean, "<x-:b>"),
		d("not", "arg", 1, fnNot, "<x-:b>"),
		d("map", "arr, func", 2, fnMap, "<af>"),
		d("zip", "", 0, fnZip, "<a+>"),
		d("filter", "arr, func", 2, fnFilter, "<af>"),
		d("single", "arr, func", 2, fnSingle, "<af?>"),
		d("reduce", "sequence, func, init", 3, fnFoldLeft, "<afj?:j>"),
		d("sift", "arg, func", 2, fnSift, "<o-f?:o>"),
		d("keys", "arg", 1, fnKeys, "<x-:a<s>>"),
		d("lookup", "input, key", 2, fnLookup, "<x-s:x>"),
		d("append", "arg1, arg2", 2, fnAppend, "<xx:a>"),
		d("exists", "arg", 1, fnExists, "<x:b>"),
		d("spread", "arg", 1, fnSpread, "<x-:a<o>>"),
		d("merge", "arg", 1, fnMerge, "<a<o>:o>"),
		d("reverse", "arr", 1, fnReverse, "<a:a>"),
		d("each", "obj, func", 2, fnEach, "<o-f:a>"),
		d("error", "message", 1, fnError, "<s?:x>"),
		d("assert", "condition, message", 2, fnAssert, "<bs?:x>"),
		d("type", "value", 1, fnType, "<x:s>"),
		d("sort", "arr, comparator", 2, fnSort, "<af?:a>"),
		d("shuffle", "arr", 1, fnShuffle, "<a:a>"),
		d("distinct", "arr", 1, fnDistinct, "<x:x>"),
		d("base64encode", "str", 1, fnBase64encode, "<s-:s>"),
		d("base64decode", "str", 1, fnBase64decode, "<s-:s>"),
		d("encodeUrlComponent", "str", 1, fnEncodeUrlComponent, "<s-:s>"),
		d("encodeUrl", "str", 1, fnEncodeUrl, "<s-:s>"),
		d("decodeUrlComponent", "str", 1, fnDecodeUrlComponent, "<s-:s>"),
		d("decodeUrl", "str", 1, fnDecodeUrl, "<s-:s>"),
		d("eval", "expr, focus", 2, e.functionEval, "<sx?:x>"),
		d("toMillis", "timestamp, picture", 2, fnToMillis, "<s-s?:n>"),
		d("fromMillis", "millis, picture, timezone", 3, fnFromMillis, "<n-s?s?:s>"),
		d("clone", "arg", 1, functionClone, "<(oa)-:o>"),
	}
}

// functionEval is $eval: it parses and evaluates an expression against the focus (or the context).
func (e *evaluator) functionEval(f *focus, args []any) (any, error) {
	expr := argAt(args, 0)
	if expr == nil {
		return nil, nil
	}
	input := f.input
	if len(args) > 1 && args[1] != nil {
		input = args[1]
		// an array is wrapped in a singleton sequence, so that it is treated as one input
		if arr, ok := input.(*Array); ok && !arr.sequence {
			wrapper := f.createSequence(arr)
			wrapper.outerWrapper = true
			input = wrapper
		}
	}
	ast, _, err := parse(expr.(string), false)
	if err != nil {
		return nil, wrapEvalError("D3120", err)
	}
	result, err := e.evaluate(ast, input, f.env)
	if err != nil {
		return nil, wrapEvalError("D3121", err)
	}
	return result, nil
}

// wrapEvalError reports an error of the expression passed to $eval under the given code.
func wrapEvalError(code string, err error) error {
	je, ok := err.(*Error)
	if !ok {
		return err
	}
	if perr := populateMessage(je); perr != nil {
		return perr
	}
	return &Error{Code: code, Value: je.Message, HasValue: true, Cause: je}
}

// functionClone is $clone: a deep copy made through JSON.
func functionClone(_ *focus, args []any) (any, error) {
	arg := argAt(args, 0)
	if arg == nil {
		return nil, nil
	}
	s, err := toJSONataString(arg, false)
	if err != nil {
		return nil, err
	}
	str, _ := s.(string)
	v, err := parseJSON(str)
	if err != nil {
		// JSON.parse throws on text that is not JSON
		return nil, errHostRuntime
	}
	return v, nil
}

// NewFunction wraps a Go function as a JSONata function value. arity is the number of arguments it
// declares, which higher-order functions such as $map use to decide how many arguments to pass.
func NewFunction(arity int, fn func(args ...any) (any, error)) any {
	return &nativeFunc{arity: arity, params: "...args", call: func(args []any) (any, error) {
		result, err := fn(args...)
		if err != nil {
			return nil, err
		}
		return ToValue(result), nil
	}}
}

// Call invokes a function value that a registered function received as an argument.
func Call(fn any, args ...any) (any, error) {
	return callDirect(fn, args)
}

// ParseJSON reads JSON text into values, as JSON.parse does.
func ParseJSON(text string) (any, error) {
	return parseJSON(text)
}

// Stringify writes a value as JSON, as JSON.stringify does, with function values written as empty
// strings. The second result is false for undefined.
func Stringify(v any) (string, bool, error) {
	return stringifyJSON(ToValue(v), functionsAsEmpty, 0)
}

// IsFunction reports whether a value is a JSONata function.
func IsFunction(v any) bool { return isFunction(v) }

// JSON returns the error as one JSON object with its code, position, token, value and message
// (members that are undefined are left out), with function values written as empty strings.
func (e *Error) JSON() (string, error) {
	if e.Message == "" {
		if err := populateMessage(e); err != nil {
			return "", err
		}
	}
	o := NewObject()
	o.Set("code", e.Code)
	if e.HasPosition {
		o.Set("position", float64(e.Position))
	}
	if e.HasToken && e.Token != nil {
		o.Set("token", jsonLiteral(e.Token))
	}
	if e.HasValue && e.Value != nil {
		o.Set("value", jsonLiteral(e.Value))
	}
	o.Set("message", e.Message)
	s, _, err := stringifyJSON(o, functionsAsEmpty, 0)
	return s, err
}

// Items returns the items of an array.
func (a *Array) Items() []any { return a.items }

// NewArray returns an array of the items.
func NewArray(items ...any) *Array { return newArray(items...) }

// ToValue converts a Go value to the value model: []any to *Array, map[string]any to *Object (keys in
// sorted order), Go integer and float types to float64. Values of the model pass through.
func ToValue(v any) any {
	switch x := v.(type) {
	case nil, nullValue, bool, float64, string, *Array, *Object, *Lambda, *Function, *nativeFunc:
		return v
	case []any:
		arr := newArray()
		for _, item := range x {
			arr.push(ToValue(item))
		}
		return arr
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		obj := NewObject()
		for _, k := range keys {
			obj.Set(k, ToValue(x[k]))
		}
		return obj
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case uint:
		return float64(x)
	case uint64:
		return float64(x)
	case uint32:
		return float64(x)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		arr := newArray()
		for i := 0; i < rv.Len(); i++ {
			arr.push(ToValue(rv.Index(i).Interface()))
		}
		return arr
	case reflect.Int8, reflect.Int16, reflect.Uint8, reflect.Uint16:
		return float64(rv.Convert(reflect.TypeOf(int64(0))).Int())
	}
	return math.NaN()
}
