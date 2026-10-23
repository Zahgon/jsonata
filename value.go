/**
 * © Copyright IBM Corp. 2016, 2018 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of the value helpers of src/utils.js, with the JavaScript value model they rely on.

package jsonata

import (
	"math"
	"sort"
	"strconv"
)

// The value model follows JavaScript, because JSONata's semantics are defined by it:
//
//	nil        undefined (no value)
//	Null       the JSON null
//	bool       a boolean
//	float64    a number
//	string     a string: UTF-16 text held as WTF-8 (an unpaired surrogate keeps its three-byte form)
//	*Array     an array; sequences are arrays with flags (see Array)
//	*Object    an object with JavaScript property order
//	functions  *Lambda, *Function and *nativeFunc values
//
// Inputs and results exchanged through the public API use the same model, except that arrays are []any.

// nullValue is the type of Null.
type nullValue struct{}

// Null is the JSON null value.
var Null = nullValue{}

// MarshalJSON writes null.
func (nullValue) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// Array is a JavaScript array with the flags JSONata attaches to it: a sequence is a result
// sequence that collapses to its only item or to undefined, keepSingleton stops that collapse,
// cons marks an array built by an array constructor, outerWrapper marks the singleton sequence
// that wraps an array input, and tupleStream marks a stream of tuple bindings.
type Array struct {
	items         []any
	sequence      bool
	keepSingleton bool
	cons          bool
	outerWrapper  bool
	tupleStream   bool
	limit         int // the maximum length of a sequence (the Sequence option), 0 for none
}

func newArray(items ...any) *Array { return &Array{items: items} }

func newSequence(items ...any) *Array { return &Array{items: items, sequence: true} }

func (a *Array) push(v ...any) { a.items = append(a.items, v...) }

func (a *Array) length() int { return len(a.items) }

// at is a[i], undefined out of range.
func (a *Array) at(i int) any {
	if i < 0 || i >= len(a.items) {
		return nil
	}
	return a.items[i]
}

// concat is Array.prototype.concat with one argument: a new plain array.
func (a *Array) concat(b *Array) *Array {
	items := make([]any, 0, len(a.items)+len(b.items))
	items = append(items, a.items...)
	items = append(items, b.items...)
	return &Array{items: items}
}

// slice is Array.prototype.slice(start, end) for in-range non-negative bounds: a new plain array.
func (a *Array) slice(start, end int) *Array {
	return &Array{items: append([]any(nil), a.items[start:end]...)}
}

// Object is a JavaScript object. Keys enumerate in JavaScript property order: array-index keys
// ascending, then the other keys in insertion order.
type Object struct {
	order []string
	vals  map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object { return &Object{vals: map[string]any{}} }

// Set assigns a property: a new key is added last, an existing key keeps its place.
func (o *Object) Set(key string, value any) {
	if _, ok := o.vals[key]; !ok {
		o.order = append(o.order, key)
	}
	o.vals[key] = value
}

// Get returns the value of an own property.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[key]
	return v, ok
}

// Delete removes an own property.
func (o *Object) Delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.order {
		if k == key {
			o.order = append(o.order[:i:i], o.order[i+1:]...)
			break
		}
	}
}

// Len is the number of own properties.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.order)
}

// Keys lists the own properties in JavaScript enumeration order.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	var ints, rest []string
	for _, k := range o.order {
		if _, ok := arrayIndex(k); ok {
			ints = append(ints, k)
		} else {
			rest = append(rest, k)
		}
	}
	if len(ints) == 0 {
		return rest
	}
	sort.Slice(ints, func(i, j int) bool {
		a, _ := arrayIndex(ints[i])
		b, _ := arrayIndex(ints[j])
		return a < b
	})
	return append(ints, rest...)
}

// arrayIndex reports whether key is a canonical array index (0 to 2^32-2), which JavaScript
// enumerates before every other key.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n >= math.MaxUint32 {
		return 0, false
	}
	return n, true
}

// isNumeric is utils.isNumeric: a number that is not NaN. An infinite number is out of range (D1001).
func isNumeric(v any) (bool, error) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) {
		return false, nil
	}
	if math.IsInf(f, 0) {
		return false, &Error{Code: "D1001", Value: f, HasValue: true}
	}
	return true, nil
}

// isArrayOfStrings is utils.isArrayOfStrings.
func isArrayOfStrings(v any) bool {
	a, ok := v.(*Array)
	if !ok {
		return false
	}
	for _, item := range a.items {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

// isArrayOfNumbers is utils.isArrayOfNumbers.
func isArrayOfNumbers(v any) (bool, error) {
	a, ok := v.(*Array)
	if !ok {
		return false, nil
	}
	all := true
	for _, item := range a.items {
		num, err := isNumeric(item)
		if err != nil {
			return false, err
		}
		if !num {
			all = false
		}
	}
	return all, nil
}

func isSequence(v any) bool {
	a, ok := v.(*Array)
	return ok && a.sequence
}

func isArray(v any) bool {
	_, ok := v.(*Array)
	return ok
}

// isFunction is utils.isFunction: a lambda, a built-in or registered function, or a native closure.
func isFunction(v any) bool {
	switch v.(type) {
	case *Lambda, *Function, *nativeFunc:
		return true
	}
	return false
}

func isLambda(v any) bool {
	_, ok := v.(*Lambda)
	return ok
}

// isObjectLike is JavaScript's `v !== null && typeof v === 'object'`: arrays, objects and the lambda
// and function definition objects (closures are typeof 'function').
func isObjectLike(v any) bool {
	switch v.(type) {
	case *Array, *Object, *Lambda, *Function, tuple:
		return true
	}
	return false
}

// typeOf is JavaScript's typeof.
func typeOf(v any) string {
	switch v.(type) {
	case nil:
		return "undefined"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case *nativeFunc:
		return "function"
	}
	return "object"
}

// keysOf is utils.keys: the own enumerable keys of an object-like value.
func keysOf(v any) []string {
	switch x := v.(type) {
	case *Object:
		return x.Keys()
	case *Array:
		keys := make([]string, 0, len(x.items)+1)
		for i := range x.items {
			keys = append(keys, strconv.Itoa(i))
		}
		if x.sequence {
			keys = append(keys, "sequence")
		}
		return keys
	case tuple:
		return x.keys()
	}
	return nil
}

// strictEquals is JavaScript's ===.
func strictEquals(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case nullValue:
		_, ok := b.(nullValue)
		return ok
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case *Array:
		y, ok := b.(*Array)
		return ok && x == y
	case *Object:
		y, ok := b.(*Object)
		return ok && x == y
	case *Lambda:
		y, ok := b.(*Lambda)
		return ok && x == y
	case *Function:
		y, ok := b.(*Function)
		return ok && x == y
	case *nativeFunc:
		y, ok := b.(*nativeFunc)
		return ok && x == y
	}
	return false
}

// isDeepEqual is utils.isDeepEqual.
func isDeepEqual(lhs, rhs any) bool {
	if strictEquals(lhs, rhs) {
		return true
	}
	la, lok := lhs.(*Array)
	ra, rok := rhs.(*Array)
	if lok && rok {
		if len(la.items) != len(ra.items) {
			return false
		}
		for i := range la.items {
			if !isDeepEqual(la.items[i], ra.items[i]) {
				return false
			}
		}
		return true
	}
	lo, lok := lhs.(*Object)
	ro, rok := rhs.(*Object)
	if !lok || !rok {
		return false
	}
	if lo.Len() != ro.Len() {
		return false
	}
	lkeys := append([]string(nil), lo.order...)
	rkeys := append([]string(nil), ro.order...)
	sortUTF16(lkeys)
	sortUTF16(rkeys)
	for i := range lkeys {
		if lkeys[i] != rkeys[i] {
			return false
		}
	}
	for _, k := range lkeys {
		if !isDeepEqual(lo.vals[k], ro.vals[k]) {
			return false
		}
	}
	return true
}

// sortUTF16 is Array.prototype.sort on strings: UTF-16 code unit order.
func sortUTF16(s []string) {
	sort.SliceStable(s, func(i, j int) bool { return compareUTF16(s[i], s[j]) < 0 })
}

// tuple is a binding tuple of a tuple stream (an object created with Object.create(null)): '@' holds
// the context value, and the other keys hold focus, index and ancestor bindings.
type tuple map[string]any

func (t tuple) keys() []string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (t tuple) clone() tuple {
	c := make(tuple, len(t))
	for k, v := range t {
		c[k] = v
	}
	return c
}
