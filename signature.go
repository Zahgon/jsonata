/**
 * © Copyright IBM Corp. 2016, 2018 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of src/signature.js: function signatures and argument validation.

package jsonata

import (
	"regexp"
	"strings"
	"sync"
)

// arraySignatureMapping names the plural of each array item type for error messages.
var arraySignatureMapping = map[string]string{
	"a": "arrays",
	"b": "booleans",
	"f": "functions",
	"n": "numbers",
	"o": "objects",
	"s": "strings",
}

type sigParam struct {
	regex        string
	hasRegex     bool
	typ          string
	array        bool
	context      bool
	contextRegex *regexp.Regexp
	subtype      string
	hasSubtype   bool
}

// Signature is a parsed function signature such as <s-nn?:s>.
type Signature struct {
	definition string
	params     []*sigParam
	regex      *regexp.Regexp
}

// Definition is the signature text.
func (s *Signature) Definition() string { return s.definition }

var regexCache sync.Map

// compileCached compiles a signature regex once.
func compileCached(expr string) *regexp.Regexp {
	if re, ok := regexCache.Load(expr); ok {
		return re.(*regexp.Regexp)
	}
	re := regexp.MustCompile(expr)
	regexCache.Store(expr, re)
	return re
}

// regexText is the param's regex as JavaScript's string concatenation renders it.
func (p *sigParam) regexText() string {
	if !p.hasRegex {
		return "undefined"
	}
	return p.regex
}

// parseSignature parses the text between the angle brackets (brackets included).
func parseSignature(signature string) (*Signature, error) {
	units := utf16Units(signature)
	charAt := func(i int) uint16 {
		if i < 0 || i >= len(units) {
			return 0
		}
		return units[i]
	}
	var params []*sigParam
	param := &sigParam{}
	prevParam := param
	next := func() {
		params = append(params, param)
		prevParam = param
		param = &sigParam{}
	}
	findClosingBracket := func(start int, open, close uint16) int {
		depth := 1
		position := start
		for position < len(units) {
			position++
			symbol := charAt(position)
			if symbol == close {
				depth--
				if depth == 0 {
					break
				}
			} else if symbol == open {
				depth++
			}
		}
		return position
	}
	for position := 1; position < len(units); position++ {
		symbol := units[position]
		if symbol == ':' {
			// the return type is not checked
			break
		}
		switch symbol {
		case 's', 'n', 'b', 'l', 'o':
			param.regex, param.hasRegex = "["+string(rune(symbol))+"m]", true
			param.typ = string(rune(symbol))
			next()
		case 'a':
			// any value is treated as a singleton array
			param.regex, param.hasRegex = "[asnblfom]", true
			param.typ = "a"
			param.array = true
			next()
		case 'f':
			param.regex, param.hasRegex = "f", true
			param.typ = "f"
			next()
		case 'j':
			param.regex, param.hasRegex = "[asnblom]", true
			param.typ = "j"
			next()
		case 'x':
			param.regex, param.hasRegex = "[asnblfom]", true
			param.typ = "x"
			next()
		case '-':
			prevParam.context = true
			prevParam.contextRegex = compileCached(regexpSource(prevParam.regexText(), prevParam.hasRegex))
			prevParam.regex, prevParam.hasRegex = prevParam.regexText()+"?", true
		case '?', '+':
			prevParam.regex, prevParam.hasRegex = prevParam.regexText()+string(rune(symbol)), true
		case '(':
			endParen := findClosingBracket(position, '(', ')')
			choice := fromUnits(units[position+1 : clampIndex(endParen, len(units))])
			if strings.Contains(choice, "<") {
				return nil, &Error{Code: "S0402", Value: choice, HasValue: true, offset: position, hasOffset: true}
			}
			param.regex, param.hasRegex = "["+choice+"m]", true
			param.typ = "(" + choice + ")"
			position = endParen
			next()
		case '<':
			if prevParam.typ == "a" || prevParam.typ == "f" {
				endPos := findClosingBracket(position, '<', '>')
				prevParam.subtype = fromUnits(units[position+1 : clampIndex(endPos, len(units))])
				prevParam.hasSubtype = true
				position = endPos
			} else {
				var value any
				if prevParam.typ != "" {
					value = prevParam.typ
				}
				return nil, &Error{Code: "S0401", Value: value, HasValue: value != nil, offset: position, hasOffset: true}
			}
		}
	}
	var b strings.Builder
	b.WriteString("^")
	for _, p := range params {
		b.WriteString("(" + p.regexText() + ")")
	}
	b.WriteString("$")
	return &Signature{definition: signature, params: params, regex: compileCached(b.String())}, nil
}

// regexpSource is new RegExp(source).source for a parameter regex; an undefined regex matches anything.
func regexpSource(text string, defined bool) string {
	if !defined {
		return "(?:)"
	}
	return text
}

// signatureSymbol is the type letter of a value: s, n, b, l (null), a, o, f, or m (missing).
func signatureSymbol(v any) byte {
	if isFunction(v) {
		return 'f'
	}
	switch v.(type) {
	case string:
		return 's'
	case float64:
		return 'n'
	case bool:
		return 'b'
	case nullValue:
		return 'l'
	case *Array:
		return 'a'
	case nil:
		return 'm'
	}
	return 'o'
}

// validate checks the arguments against the signature and returns them with the context value
// substituted for a missing argument where the signature allows it, and with single values
// wrapped where an array is expected.
func (s *Signature) validate(args []any, context any) ([]any, error) {
	supplied := make([]byte, len(args))
	for i, a := range args {
		supplied[i] = signatureSymbol(a)
	}
	isValid := s.regex.FindStringSubmatch(string(supplied))
	if isValid == nil {
		return nil, s.validationError(args, string(supplied))
	}
	argAt := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	var validated []any
	argIndex := 0
	for index, param := range s.params {
		match := isValid[index+1]
		if match == "" {
			if param.context && param.contextRegex != nil {
				contextType := string(signatureSymbol(context))
				if param.contextRegex.MatchString(contextType) {
					validated = append(validated, context)
				} else {
					return nil, &Error{Code: "T0411", Value: context, HasValue: context != nil, Index: argIndex + 1, HasIndex: true}
				}
			} else {
				validated = append(validated, argAt(argIndex))
				argIndex++
			}
			continue
		}
		for _, single := range []byte(match) {
			arg := argAt(argIndex)
			if param.typ == "a" {
				if single == 'm' {
					arg = nil
				} else {
					arrayOK := true
					if param.hasSubtype {
						if single != 'a' && match != param.subtype {
							arrayOK = false
						} else if single == 'a' {
							items := arg.(*Array).items
							if len(items) > 0 {
								itemType := signatureSymbol(items[0])
								if param.subtype == "" || itemType != param.subtype[0] {
									arrayOK = false
								} else {
									for _, item := range items {
										if signatureSymbol(item) != itemType {
											arrayOK = false
											break
										}
									}
								}
							}
						}
					}
					if !arrayOK {
						return nil, &Error{Code: "T0412", Value: arg, HasValue: arg != nil, Index: argIndex + 1, HasIndex: true,
							Type: arraySignatureMapping[param.subtype]}
					}
					if single != 'a' {
						arg = newArray(arg)
					}
				}
			}
			validated = append(validated, arg)
			argIndex++
		}
	}
	return validated, nil
}

// validationError finds the first argument that does not match its parameter (T0410).
func (s *Signature) validationError(args []any, supplied string) error {
	argAt := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	partial := "^"
	goodTo := 0
	for _, p := range s.params {
		partial += p.regexText()
		m := compileCached(partial).FindString(supplied)
		if m == "" && !compileCached(partial).MatchString(supplied) {
			v := argAt(goodTo)
			return &Error{Code: "T0410", Value: v, HasValue: v != nil, Index: goodTo + 1, HasIndex: true}
		}
		goodTo = len(m)
	}
	v := argAt(goodTo)
	return &Error{Code: "T0410", Value: v, HasValue: v != nil, Index: goodTo + 1, HasIndex: true}
}
