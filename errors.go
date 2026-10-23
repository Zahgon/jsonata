/**
 * © Copyright IBM Corp. 2016, 2017 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of the error catalogue and populateMessage of src/jsonata.js.

package jsonata

import (
	"errors"
	"regexp"
	"strings"
)

// Error is a JSONata error: a static (S), type (T) or dynamic (D) error with its code, the position
// in the expression (in UTF-16 code units) and the token and values the message refers to.
type Error struct {
	Code string
	// Position is the character position of the error in the expression, when HasPosition is set.
	Position    int
	HasPosition bool
	// Token is the token or function name the error is associated with, when HasToken is set.
	Token    any
	HasToken bool
	// Value and Value2 are the values the error refers to, when HasValue and HasValue2 are set.
	Value     any
	HasValue  bool
	Value2    any
	HasValue2 bool
	// Index is the argument number of signature errors; Type is the expected array item type.
	Index    int
	HasIndex bool
	Type     string
	// Exp is the exponent of a D3061 error and FunctionName the function of a D3140 error.
	Exp          any
	HasExp       bool
	FunctionName string
	// Message is the populated message.
	Message string
	// Cause is the error of the expression passed to $eval (D3120, D3121).
	Cause *Error

	offset    int
	hasOffset bool
	// remaining and errType are set on errors recorded in recovery mode.
	remaining []*token
	errType   string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

func (e *Error) setPosition(p int) {
	e.Position, e.HasPosition = p, true
}

func (e *Error) setToken(t any) {
	e.Token, e.HasToken = t, true
}

// positionTruthy is the JavaScript truth value of err.position.
func (e *Error) positionTruthy() bool {
	return e.HasPosition && e.Position != 0
}

// tokenDefined is typeof err.token !== 'undefined' (and `err.token` is truthy only when it is set and truthy).
func (e *Error) tokenTruthy() bool {
	if !e.HasToken {
		return false
	}
	switch t := e.Token.(type) {
	case nil, nullValue:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case float64:
		return t != 0 && t == t
	}
	return true
}

// errHostRuntime is an evaluation failure that is not a JSONata error: it stands for the
// JavaScript TypeError the original raises when it stringifies a circular value.
var errHostRuntime = errors.New("value cannot be serialised as JSON")

var errorCodes = map[string]string{
	"S0101": "String literal must be terminated by a matching quote",
	"S0102": "Number out of range: {{token}}",
	"S0103": "Unsupported escape sequence: \\{{token}}",
	"S0104": "The escape sequence \\u must be followed by 4 hex digits",
	"S0105": "Quoted property name must be terminated with a backquote (`)",
	"S0106": "Comment has no closing tag",
	"S0201": "Syntax error: {{token}}",
	"S0202": "Expected {{value}}, got {{token}}",
	"S0203": "Expected {{value}} before end of expression",
	"S0204": "Unknown operator: {{token}}",
	"S0205": "Unexpected token: {{token}}",
	"S0206": "Unknown expression type: {{token}}",
	"S0207": "Unexpected end of expression",
	"S0208": "Parameter {{value}} of function definition must be a variable name (start with $)",
	"S0209": "A predicate cannot follow a grouping expression in a step",
	"S0210": "Each step can only have one grouping expression",
	"S0211": "The symbol {{token}} cannot be used as a unary operator",
	"S0212": "The left side of := must be a variable name (start with $)",
	"S0213": "The literal value {{value}} cannot be used as a step within a path expression",
	"S0214": "The right side of {{token}} must be a variable name (start with $)",
	"S0215": "A context variable binding must precede any predicates on a step",
	"S0216": "A context variable binding must precede the 'order-by' clause on a step",
	"S0217": "The object representing the 'parent' cannot be derived from this expression",
	"S0301": "Empty regular expressions are not allowed",
	"S0302": "No terminating / in regular expression",
	"S0402": "Choice groups containing parameterized types are not supported",
	"S0401": "Type parameters can only be applied to functions and arrays",
	"S0500": "Attempted to evaluate an expression containing syntax error(s)",
	"T0410": "Argument {{index}} of function {{token}} does not match function signature",
	"T0411": "Context value is not a compatible type with argument {{index}} of function {{token}}",
	"T0412": "Argument {{index}} of function {{token}} must be an array of {{type}}",
	"D1001": "Number out of range: {{value}}",
	"D1002": "Cannot negate a non-numeric value: {{value}}",
	"T1003": "Key in object structure must evaluate to a string; got: {{value}}",
	"D1004": "Regular expression matches zero length string",
	"T1005": "Attempted to invoke a non-function. Did you mean ${{{token}}}?",
	"T1006": "Attempted to invoke a non-function",
	"T1007": "Attempted to partially apply a non-function. Did you mean ${{{token}}}?",
	"T1008": "Attempted to partially apply a non-function",
	"D1009": "Multiple key definitions evaluate to same key: {{value}}",
	"T1010": "The matcher function argument passed to function {{token}} does not return the correct object structure",
	"D1011": "Stack overflow. Check for non-terminating recursive function.  Consider rewriting as tail-recursive",
	"D1012": "Evaluation timeout after {{value}} milliseconds. Check for infinite loop",
	"D1013": "Object property names starting with _jsonata_ are reserved for internal use: {{value}}",
	"T2001": "The left side of the {{token}} operator must evaluate to a number",
	"T2002": "The right side of the {{token}} operator must evaluate to a number",
	"T2003": "The left side of the range operator (..) must evaluate to an integer",
	"T2004": "The right side of the range operator (..) must evaluate to an integer",
	"D2005": "The left side of := must be a variable name (start with $)",
	"T2006": "The right side of the function application operator ~> must be a function",
	"T2007": "Type mismatch when comparing values {{value}} and {{value2}} in order-by clause",
	"T2008": "The expressions within an order-by clause must evaluate to numeric or string values",
	"T2009": "The values {{value}} and {{value2}} either side of operator {{token}} must be of the same data type",
	"T2010": "The expressions either side of operator {{token}} must evaluate to numeric or string values",
	"T2011": "The insert/update clause of the transform expression must evaluate to an object: {{value}}",
	"T2012": "The delete clause of the transform expression must evaluate to a string or array of strings: {{value}}",
	"T2013": "The transform expression clones the input object using the $clone() function.  This has been overridden in the current scope by a non-function.",
	"D2014": "The size of the sequence allocated by the range operator (..) must not exceed 1e7.  Attempted to allocate {{value}}.",
	"D2015": "The maximum sequence length of {{value}} was exceeded.",
	"D2016": "The size of the string requested by the $pad function must not exceed 1e7.  Attempted to allocate {{value}}.",
	"D3001": "Attempting to invoke string function on Infinity or NaN",
	"D3010": "Second argument of replace function cannot be an empty string",
	"D3011": "Fourth argument of replace function must evaluate to a positive number",
	"D3012": "Attempted to replace a matched string with a non-string value",
	"D3020": "Third argument of split function must evaluate to a positive number",
	"D3030": "Unable to cast value to a number: {{value}}",
	"D3040": "Third argument of match function must evaluate to a positive number",
	"D3050": "The second argument of reduce function must be a function with at least two arguments",
	"D3060": "The sqrt function cannot be applied to a negative number: {{value}}",
	"D3061": "The power function has resulted in a value that cannot be represented as a JSON number: base={{value}}, exponent={{exp}}",
	"D3070": "The single argument form of the sort function can only be applied to an array of strings or an array of numbers.  Use the second argument to specify a comparison function",
	"D3080": "The picture string must only contain a maximum of two sub-pictures",
	"D3081": "The sub-picture must not contain more than one instance of the 'decimal-separator' character",
	"D3082": "The sub-picture must not contain more than one instance of the 'percent' character",
	"D3083": "The sub-picture must not contain more than one instance of the 'per-mille' character",
	"D3084": "The sub-picture must not contain both a 'percent' and a 'per-mille' character",
	"D3085": "The mantissa part of a sub-picture must contain at least one character that is either an 'optional digit character' or a member of the 'decimal digit family'",
	"D3086": "The sub-picture must not contain a passive character that is preceded by an active character and that is followed by another active character",
	"D3087": "The sub-picture must not contain a 'grouping-separator' character that appears adjacent to a 'decimal-separator' character",
	"D3088": "The sub-picture must not contain a 'grouping-separator' at the end of the integer part",
	"D3089": "The sub-picture must not contain two adjacent instances of the 'grouping-separator' character",
	"D3090": "The integer part of the sub-picture must not contain a member of the 'decimal digit family' that is followed by an instance of the 'optional digit character'",
	"D3091": "The fractional part of the sub-picture must not contain an instance of the 'optional digit character' that is followed by a member of the 'decimal digit family'",
	"D3092": "A sub-picture that contains a 'percent' or 'per-mille' character must not contain a character treated as an 'exponent-separator'",
	"D3093": "The exponent part of the sub-picture must comprise only of one or more characters that are members of the 'decimal digit family'",
	"D3100": "The radix of the formatBase function must be between 2 and 36.  It was given {{value}}",
	"D3110": "The argument of the toMillis function must be an ISO 8601 formatted timestamp. Given {{value}}",
	"D3120": "Syntax error in expression passed to function eval: {{value}}",
	"D3121": "Dynamic error evaluating the expression passed to function eval: {{value}}",
	"D3130": "Formatting or parsing an integer as a sequence starting with {{value}} is not supported by this implementation",
	"D3131": "In a decimal digit pattern, all digits must be from the same decimal group",
	"D3132": "Unknown component specifier {{value}} in date/time picture string",
	"D3133": "The 'name' modifier can only be applied to months and days in the date/time picture string, not {{value}}",
	"D3134": "The timezone integer format specifier cannot have more than four digits",
	"D3135": "No matching closing bracket ']' in date/time picture string",
	"D3136": "The date/time picture string is missing specifiers required to parse the timestamp",
	"D3137": "{{{message}}}",
	"D3138": "The $single() function expected exactly 1 matching result.  Instead it matched more.",
	"D3139": "The $single() function expected exactly 1 matching result.  Instead it matched 0.",
	"D3140": "Malformed URL passed to ${{{functionName}}}(): {{value}}",
	"D3141": "{{{message}}}",
}

var (
	tripleBraces = regexp.MustCompile(`\{\{\{([^}]+)}}}`)
	doubleBraces = regexp.MustCompile(`\{\{([^}]+)}}`)
)

// field returns the error member a message template names, and whether it is defined.
func (e *Error) field(name string) (any, bool) {
	switch name {
	case "token":
		return e.Token, e.HasToken
	case "value":
		return e.Value, e.HasValue
	case "value2":
		return e.Value2, e.HasValue2
	case "index":
		return float64(e.Index), e.HasIndex
	case "type":
		return e.Type, e.Type != ""
	case "exp":
		return e.Exp, e.HasExp
	case "message":
		return e.Message, true
	case "functionName":
		return e.FunctionName, e.FunctionName != ""
	case "position":
		return float64(e.Position), e.HasPosition
	case "code":
		return e.Code, true
	}
	return nil, false
}

// populateMessage fills Message from the catalogue: {{{name}}} inserts the member as a string and
// {{name}} inserts it as JSON. A code outside the catalogue keeps its message.
func populateMessage(e *Error) error {
	template, ok := errorCodes[e.Code]
	if !ok {
		return nil
	}
	message := tripleBraces.ReplaceAllStringFunc(template, func(m string) string {
		v, ok := e.field(tripleBraces.FindStringSubmatch(m)[1])
		if !ok {
			return "undefined"
		}
		return toStringJS(v)
	})
	var failure error
	message = doubleBraces.ReplaceAllStringFunc(message, func(m string) string {
		v, ok := e.field(doubleBraces.FindStringSubmatch(m)[1])
		if !ok {
			return "undefined"
		}
		if containsLambda(v) {
			failure = errHostRuntime
			return ""
		}
		s, defined, err := stringifyJSON(v, nil, 0)
		if err != nil {
			failure = err
		}
		if !defined {
			return "undefined"
		}
		return s
	})
	if failure != nil {
		return failure
	}
	e.Message = message
	return nil
}

// containsLambda reports whether JSON.stringify of v would reach a lambda, whose captured environment
// is circular.
func containsLambda(v any) bool {
	switch x := v.(type) {
	case *Lambda:
		return true
	case *Array:
		for _, item := range x.items {
			if containsLambda(item) {
				return true
			}
		}
	case *Object:
		for _, item := range x.vals {
			if containsLambda(item) {
				return true
			}
		}
	}
	return false
}

// toStringJS is String(v) for the values that error members hold.
func toStringJS(v any) string {
	switch x := v.(type) {
	case nil:
		return "undefined"
	case nullValue:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return formatNumberJS(x)
	case string:
		return x
	case *Array:
		parts := make([]string, len(x.items))
		for i, item := range x.items {
			switch item.(type) {
			case nil, nullValue:
			default:
				parts[i] = toStringJS(item)
			}
		}
		return strings.Join(parts, ",")
	case *nativeFunc:
		return "function"
	}
	return "[object Object]"
}
