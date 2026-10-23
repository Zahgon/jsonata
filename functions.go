/**
 * © Copyright IBM Corp. 2016, 2018 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of src/functions.js: the built-in function library.

package jsonata

import (
	"math"
	"math/rand"
	"regexp"
	"strings"
)

// argNumber is args[i] as a number (the signature has checked it is one, or undefined).
func argNumber(args []any, i int) (float64, bool) {
	f, ok := argAt(args, i).(float64)
	return f, ok
}

// sliceIndex is the relative index conversion of Array.prototype.slice for an array of length n.
func sliceIndex(rel float64, n int) int {
	if math.IsNaN(rel) {
		return 0
	}
	rel = math.Trunc(rel)
	if rel < 0 {
		return int(math.Max(float64(n)+rel, 0))
	}
	return int(math.Min(rel, float64(n)))
}

// fnSum is $sum: the total of an array of numbers.
func fnSum(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return nil, nil
	}
	total := 0.0
	for _, num := range arr.items {
		total += num.(float64)
	}
	return total, nil
}

// fnCount is $count: the number of items, 0 for undefined.
func fnCount(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return 0.0, nil
	}
	return float64(len(arr.items)), nil
}

// extreme is Math.max (max) or Math.min (!max) over the items.
func extreme(items []any, max bool) float64 {
	result := math.Inf(-1)
	if !max {
		result = math.Inf(1)
	}
	for _, item := range items {
		x := item.(float64)
		if math.IsNaN(x) {
			return math.NaN()
		}
		if max {
			result = math.Max(result, x)
		} else {
			result = math.Min(result, x)
		}
	}
	return result
}

// fnMax is $max.
func fnMax(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok || len(arr.items) == 0 {
		return nil, nil
	}
	return extreme(arr.items, true), nil
}

// fnMin is $min.
func fnMin(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok || len(arr.items) == 0 {
		return nil, nil
	}
	return extreme(arr.items, false), nil
}

// fnAverage is $average.
func fnAverage(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok || len(arr.items) == 0 {
		return nil, nil
	}
	total := 0.0
	for _, num := range arr.items {
		total += num.(float64)
	}
	return total / float64(len(arr.items)), nil
}

// fnString is $string: the value as a string, JSON for structures (optionally indented).
func fnString(f *focus, args []any) (any, error) {
	prettify, _ := argAt(args, 1).(bool)
	return toJSONataString(argAt(args, 0), prettify)
}

// fnSubstring is $substring: characters (code points) from start, optionally of a length.
func fnSubstring(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	start, ok := argNumber(args, 1)
	if !ok {
		start = math.NaN()
	}
	strArray := codePointStrings(str)
	strLength := float64(len(strArray))
	if strLength+start < 0 {
		start = 0
	}
	if length, ok := argNumber(args, 2); ok {
		if length <= 0 {
			return "", nil
		}
		end := strLength + start + length
		if start >= 0 {
			end = start + length
		}
		return joinSlice(strArray, sliceIndex(start, len(strArray)), sliceIndex(end, len(strArray))), nil
	}
	return joinSlice(strArray, sliceIndex(start, len(strArray)), len(strArray)), nil
}

func joinSlice(parts []string, from, to int) string {
	if from >= to {
		return ""
	}
	return concatStrings(parts[from:to]...)
}

// fnSubstringBefore is $substringBefore.
func fnSubstringBefore(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	units := utf16Units(str)
	pos := indexOfUnits(units, utf16Units(argString(args, 1)), 0)
	if pos > -1 {
		return fromUnits(units[:pos]), nil
	}
	return str, nil
}

// fnSubstringAfter is $substringAfter.
func fnSubstringAfter(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	units := utf16Units(str)
	chars := utf16Units(argString(args, 1))
	pos := indexOfUnits(units, chars, 0)
	if pos > -1 && argAt(args, 1) == nil {
		// undefined has no length
		return nil, errHostRuntime
	}
	if pos > -1 {
		return fromUnits(units[clampIndex(pos+len(chars), len(units)):]), nil
	}
	return str, nil
}

// fnLowercase is $lowercase.
func fnLowercase(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	return toLowerJS(str), nil
}

// fnUppercase is $uppercase.
func fnUppercase(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	return toUpperJS(str), nil
}

// fnLength is $length: the number of characters (code points).
func fnLength(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	return float64(len(codePointStrings(str))), nil
}

var trimWhitespace = regexp.MustCompile(`[ \t\n\r]+`)

// fnTrim is $trim: runs of space, tab, CR and LF become one space, and a leading and trailing space go.
func fnTrim(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	result := trimWhitespace.ReplaceAllString(str, " ")
	result = strings.TrimPrefix(result, " ")
	result = strings.TrimSuffix(result, " ")
	return result, nil
}

// fnPad is $pad: pads to a width (positive pads on the right, negative on the left).
func fnPad(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	char, _ := argAt(args, 2).(string)
	if char == "" {
		char = " "
	}
	width, _ := argNumber(args, 1)
	width = math.Trunc(width)
	padLength := math.Abs(width) - float64(len(codePointStrings(str)))
	if !(padLength > 0) {
		return str, nil
	}
	// the padding is limited to ten million characters, like the range operator
	if padLength > 1e7 {
		return nil, &Error{Code: "D2016", Value: padLength, HasValue: true}
	}
	padding := strings.Repeat(char, int(padLength))
	if utf16Len(char) > 1 {
		padding = joinSlice(codePointStrings(normalizeWTF8(padding)), 0, int(padLength))
	}
	if width > 0 {
		return concatStrings(str, padding), nil
	}
	return concatStrings(padding, str), nil
}

// propOf reads an own property of an object value; other values have none.
func propOf(v any, key string) any {
	switch x := v.(type) {
	case *Object:
		val, _ := x.Get(key)
		return val
	case tuple:
		return x[key]
	}
	return nil
}

// evaluateMatcher applies a matcher function (a regex or a custom matcher) to a string and checks
// the structure of what it returns.
func evaluateMatcher(f *focus, matcher any, str any) (any, error) {
	result, err := f.callMethod(matcher, []any{str})
	if err != nil {
		return nil, err
	}
	if jsTruthy(result) {
		_, startIsNumber := propOf(result, "start").(float64)
		_, groupsIsArray := propOf(result, "groups").(*Array)
		// the original compares result.end with the string 'number', which never holds
		if !(startIsNumber || groupsIsArray || isFunction(propOf(result, "next"))) {
			return nil, &Error{Code: "T1010"}
		}
	}
	return result, nil
}

// lessThanZero is `x < 0` for an optional number argument.
func lessThanZero(v any) bool {
	f, ok := v.(float64)
	return ok && f < 0
}

// withinLimit is `typeof limit === 'undefined' || count < limit`.
func withinLimit(limit any, count int) bool {
	l, ok := limit.(float64)
	return !ok || float64(count) < l
}

// limitAllows is `typeof limit === 'undefined' || limit > 0`.
func limitAllows(limit any) bool {
	l, ok := limit.(float64)
	return !ok || l > 0
}

// fnMatch is $match: the matches of a regex, as objects with match, index and groups.
func fnMatch(f *focus, args []any) (any, error) {
	str := argAt(args, 0)
	if str == nil {
		return nil, nil
	}
	limit := argAt(args, 2)
	if lessThanZero(limit) {
		return nil, &Error{Code: "D3040", Value: limit, HasValue: true, Index: 3, HasIndex: true}
	}
	result := f.createSequence()
	if !limitAllows(limit) {
		return result, nil
	}
	count := 0
	matches, err := evaluateMatcher(f, argAt(args, 1), str)
	if err != nil {
		return nil, err
	}
	for matches != nil && withinLimit(limit, count) {
		item := NewObject()
		item.Set("match", propOf(matches, "match"))
		item.Set("index", propOf(matches, "start"))
		item.Set("groups", propOf(matches, "groups"))
		if err := result.add(item); err != nil {
			return nil, err
		}
		if matches, err = evaluateMatcher(f, propOf(matches, "next"), nil); err != nil {
			return nil, err
		}
		count++
	}
	return result, nil
}

// fnContains is $contains: whether the string contains a substring or matches a regex.
func fnContains(f *focus, args []any) (any, error) {
	str, token := argAt(args, 0), argAt(args, 1)
	if str == nil || token == nil {
		return nil, nil
	}
	if t, ok := token.(string); ok {
		return indexOfUnits(utf16Units(str.(string)), utf16Units(t), 0) != -1, nil
	}
	matches, err := evaluateMatcher(f, token, str)
	if err != nil {
		return nil, err
	}
	return matches != nil, nil
}

// jsSubstring is String.prototype.substring(a, b) on code units.
func jsSubstring(units []uint16, a, b float64) []uint16 {
	clamp := func(x float64) int {
		if math.IsNaN(x) {
			return 0
		}
		return int(math.Max(0, math.Min(math.Trunc(x), float64(len(units)))))
	}
	start, end := clamp(a), clamp(b)
	if start > end {
		start, end = end, start
	}
	return units[start:end]
}

// numberOf is a property read as a number, NaN when it is not one.
func numberOf(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return math.NaN()
}

// substitute expands the $ patterns of a $replace replacement string for one match.
func substitute(replacement []uint16, regexMatch any) []uint16 {
	var out []uint16
	groups, _ := propOf(regexMatch, "groups").(*Array)
	groupCount := 0
	if groups != nil {
		groupCount = len(groups.items)
	}
	position := 0
	index := indexOfUnits(replacement, []uint16{'$'}, position)
	for index != -1 && position < len(replacement) {
		out = append(out, replacement[position:index]...)
		position = index + 1
		var dollarVal uint16
		if position < len(replacement) {
			dollarVal = replacement[position]
		}
		switch {
		case position < len(replacement) && dollarVal == '$':
			out = append(out, '$')
			position++
		case position < len(replacement) && dollarVal == '0':
			out = append(out, utf16Units(toStringJS(propOf(regexMatch, "match")))...)
			position++
		default:
			maxDigits := 1
			if groupCount != 0 {
				maxDigits = int(math.Floor(math.Log(float64(groupCount))*math.Log10E)) + 1
			}
			n := parseIntJS(fromUnits(jsSubstring(replacement, float64(position), float64(position+maxDigits))))
			if maxDigits > 1 && n > float64(groupCount) {
				n = parseIntJS(fromUnits(jsSubstring(replacement, float64(position), float64(position+maxDigits-1))))
			}
			if math.IsNaN(n) {
				// not a capture group: the $ is literal
				out = append(out, '$')
			} else {
				if groupCount > 0 {
					i := n - 1
					if i >= 0 && i < float64(groupCount) && i == math.Trunc(i) {
						if submatch := groups.items[int(i)]; submatch != nil {
							out = append(out, utf16Units(toStringJS(submatch))...)
						}
					}
				}
				position += len(formatNumberJS(n))
			}
		}
		index = indexOfUnits(replacement, []uint16{'$'}, position)
	}
	if position < len(replacement) {
		out = append(out, replacement[position:]...)
	}
	return out
}

// fnReplace is $replace: replaces a substring or the matches of a regex, with a string (with $
// patterns) or with what a function returns for each match.
func fnReplace(f *focus, args []any) (any, error) {
	str, pattern, replacement, limit := argAt(args, 0), argAt(args, 1), argAt(args, 2), argAt(args, 3)
	if str == nil || pattern == nil {
		return nil, nil
	}
	if pattern == "" {
		return nil, &Error{Code: "D3010", Value: pattern, HasValue: true, Index: 2, HasIndex: true}
	}
	if lessThanZero(limit) {
		return nil, &Error{Code: "D3011", Value: limit, HasValue: true, Index: 4, HasIndex: true}
	}
	units := utf16Units(str.(string))
	if !limitAllows(limit) {
		return str, nil
	}
	count := 0
	var result []uint16
	if p, ok := pattern.(string); ok {
		pu := utf16Units(p)
		position := 0
		index := indexOfUnits(units, pu, position)
		if index != -1 && isFunction(replacement) && withinLimit(limit, count) {
			// the original concatenates the function's source text
			return nil, errHostRuntime
		}
		rep := utf16Units(toStringJS(replacement))
		for index != -1 && withinLimit(limit, count) {
			result = append(result, units[position:index]...)
			result = append(result, rep...)
			position = index + len(pu)
			count++
			index = indexOfUnits(units, pu, position)
		}
		result = append(result, units[position:]...)
		return fromUnits(result), nil
	}
	replacer := func(match any) (any, error) {
		if r, ok := replacement.(string); ok {
			return fromUnits(substitute(utf16Units(r), match)), nil
		}
		return f.callMethod(replacement, []any{match})
	}
	matches, err := evaluateMatcher(f, pattern, str)
	if err != nil {
		return nil, err
	}
	if matches == nil {
		return str, nil
	}
	position := 0.0
	for matches != nil && withinLimit(limit, count) {
		start := numberOf(propOf(matches, "start"))
		result = append(result, jsSubstring(units, position, start)...)
		replacedWith, err := replacer(matches)
		if err != nil {
			return nil, err
		}
		s, ok := replacedWith.(string)
		if !ok {
			return nil, &Error{Code: "D3012", Value: replacedWith, HasValue: replacedWith != nil}
		}
		result = append(result, utf16Units(s)...)
		matchLength := math.NaN()
		if m, ok := propOf(matches, "match").(string); ok {
			matchLength = float64(utf16Len(m))
		}
		position = start + matchLength
		count++
		if matches, err = evaluateMatcher(f, propOf(matches, "next"), nil); err != nil {
			return nil, err
		}
	}
	result = append(result, jsSubstring(units, position, float64(len(units)))...)
	return fromUnits(result), nil
}

// fnBase64encode is $base64encode (of the string's code units as bytes).
func fnBase64encode(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	return base64EncodeBinary(str), nil
}

// fnBase64decode is $base64decode (to a string of one code unit per byte).
func fnBase64decode(f *focus, args []any) (any, error) {
	str, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	return base64DecodeBinary(str), nil
}

func urlFunction(name string, convert func(string) (string, bool)) func(f *focus, args []any) (any, error) {
	return func(f *focus, args []any) (any, error) {
		str, ok := argAt(args, 0).(string)
		if !ok {
			return nil, nil
		}
		result, ok := convert(str)
		if !ok {
			// a malformed URI sequence
			return nil, &Error{Code: "D3140", Value: str, HasValue: true, FunctionName: name}
		}
		return result, nil
	}
}

var (
	fnEncodeUrlComponent = urlFunction("encodeUrlComponent", func(s string) (string, bool) { return encodeURI(s, uriUnreserved) })
	fnEncodeUrl          = urlFunction("encodeUrl", func(s string) (string, bool) { return encodeURI(s, uriUnreserved+uriReserved+"#") })
	fnDecodeUrlComponent = urlFunction("decodeUrlComponent", func(s string) (string, bool) { return decodeURI(s, "") })
	fnDecodeUrl          = urlFunction("decodeUrl", func(s string) (string, bool) { return decodeURI(s, uriReserved+"#") })
)

// fnSplit is $split: splits on a substring or on the matches of a regex.
func fnSplit(f *focus, args []any) (any, error) {
	str, separator, limit := argAt(args, 0), argAt(args, 1), argAt(args, 2)
	if str == nil || separator == nil {
		return nil, nil
	}
	if lessThanZero(limit) {
		return nil, &Error{Code: "D3020", Value: limit, HasValue: true, Index: 3, HasIndex: true}
	}
	result := newArray()
	if !limitAllows(limit) {
		return result, nil
	}
	if sep, ok := separator.(string); ok {
		lim := -1
		if l, ok := limit.(float64); ok {
			lim = int(toUint32(l))
		}
		for _, part := range splitString(str.(string), sep, lim) {
			result.push(part)
		}
		return result, nil
	}
	units := utf16Units(str.(string))
	count := 0
	matches, err := evaluateMatcher(f, separator, str)
	if err != nil {
		return nil, err
	}
	if matches == nil {
		result.push(str)
		return result, nil
	}
	start := 0.0
	for matches != nil && withinLimit(limit, count) {
		result.push(fromUnits(jsSubstring(units, start, numberOf(propOf(matches, "start")))))
		start = numberOf(propOf(matches, "end"))
		if matches, err = evaluateMatcher(f, propOf(matches, "next"), nil); err != nil {
			return nil, err
		}
		count++
	}
	if withinLimit(limit, count) {
		result.push(fromUnits(jsSubstring(units, start, float64(len(units)))))
	}
	return result, nil
}

// fnJoin is $join: the strings joined with a separator (default empty).
func fnJoin(f *focus, args []any) (any, error) {
	strs, ok := argAt(args, 0).(*Array)
	if !ok {
		return nil, nil
	}
	separator := ""
	if s, ok := argAt(args, 1).(string); ok {
		separator = s
	}
	parts := make([]string, len(strs.items))
	for i, item := range strs.items {
		switch item.(type) {
		case nil, nullValue:
		default:
			parts[i] = toStringJS(item)
		}
	}
	return normalizeWTF8(strings.Join(parts, separator)), nil
}

// fnFormatNumber is $formatNumber: XPath 3.1 fn:format-number with a picture string and optional
// decimal format properties.
func fnFormatNumber(f *focus, args []any) (any, error) {
	value, ok := argNumber(args, 0)
	if !ok {
		return nil, nil
	}
	picture, _ := argAt(args, 1).(string)
	properties := map[string]string{
		"decimal-separator":  ".",
		"grouping-separator": ",",
		"exponent-separator": "e",
		"infinity":           "Infinity",
		"minus-sign":         "-",
		"NaN":                "NaN",
		"percent":            "%",
		"per-mille":          "‰",
		"zero-digit":         "0",
		"digit":              "#",
		"pattern-separator":  ";",
	}
	if options, ok := argAt(args, 2).(*Object); ok {
		for _, key := range options.Keys() {
			v, _ := options.Get(key)
			s, ok := v.(string)
			if !ok {
				if _, used := properties[key]; used {
					return nil, errHostRuntime
				}
				continue
			}
			properties[key] = s
		}
	}
	return formatNumberPicture(value, picture, properties)
}

// ustr is a JavaScript string as code units, for the string operations of fn:format-number.
type ustr []uint16

func u(s string) ustr { return ustr(utf16Units(s)) }

func (s ustr) String() string { return fromUnits(s) }

func (s ustr) eq(t ustr) bool {
	if len(s) != len(t) {
		return false
	}
	for i := range s {
		if s[i] != t[i] {
			return false
		}
	}
	return true
}

// charAt is s.charAt(i): the code unit at i, or the empty string.
func (s ustr) charAt(i int) ustr {
	if i < 0 || i >= len(s) {
		return ustr{}
	}
	return s[i : i+1]
}

// indexOf is s.indexOf(t, from).
func (s ustr) indexOf(t ustr, from int) int {
	from = clampIndex(from, len(s))
	if len(t) == 0 {
		return from
	}
	return indexOfUnits(s, t, from)
}

// lastIndexOf is s.lastIndexOf(t).
func (s ustr) lastIndexOf(t ustr) int {
	for i := len(s) - len(t); i >= 0; i-- {
		if s[i : i+len(t)].eq(t) {
			return i
		}
	}
	return -1
}

// substring is s.substring(a, b).
func (s ustr) substring(a, b int) ustr {
	return ustr(jsSubstring(s, float64(a), float64(b)))
}

// slice is s.slice(a, b), where negative indexes count from the end.
func (s ustr) slice(a, b int) ustr {
	start, end := sliceIndex(float64(a), len(s)), sliceIndex(float64(b), len(s))
	if start >= end {
		return ustr{}
	}
	return s[start:end]
}

func (s ustr) sliceFrom(a int) ustr { return s.slice(a, len(s)) }

// chars is s.split(”): one string per code unit.
func (s ustr) chars() []ustr {
	out := make([]ustr, len(s))
	for i := range s {
		out[i] = s[i : i+1]
	}
	return out
}

// split is s.split(sep) without a limit.
func (s ustr) split(sep ustr) []ustr {
	if len(sep) == 0 {
		return s.chars()
	}
	var out []ustr
	pos := 0
	for {
		i := indexOfUnits(s, sep, pos)
		if i < 0 {
			break
		}
		out = append(out, s[pos:i])
		pos = i + len(sep)
	}
	return append(out, s[pos:])
}

func concatU(parts ...ustr) ustr {
	var out ustr
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func repeatU(s ustr, n int) ustr {
	var out ustr
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return out
}

func containsU(list []ustr, s ustr) bool {
	for _, item := range list {
		if item.eq(s) {
			return true
		}
	}
	return false
}

// countIn counts the characters of s that are in the list (or equal to extra, when given).
func countIn(s ustr, list []ustr, extra ustr) int {
	n := 0
	for _, c := range s.chars() {
		if containsU(list, c) || (extra != nil && c.eq(extra)) {
			n++
		}
	}
	return n
}

// pictureParts is the split of a sub-picture (F&O 4.7.4).
type pictureParts struct {
	prefix, suffix, activePart, mantissaPart ustr
	exponentPart                             ustr
	hasExponent                              bool
	integerPart, fractionalPart, subpicture  ustr
}

// pictureVariables is the analysis of a sub-picture (F&O 4.7.4).
type pictureVariables struct {
	integerPartGroupingPositions    []int
	regularGrouping                 int
	minimumIntegerPartSize          int
	scalingFactor                   int
	prefix                          ustr
	fractionalPartGroupingPositions []int
	minimumFactionalPartSize        int
	maximumFactionalPartSize        int
	minimumExponentSize             int
	suffix                          ustr
	picture                         ustr
}

func formatNumberPicture(value float64, pictureText string, props map[string]string) (any, error) {
	prop := func(k string) ustr { return u(props[k]) }
	decimalSep, groupingSep, exponentSep := prop("decimal-separator"), prop("grouping-separator"), prop("exponent-separator")
	digit, patternSep, percent, perMille := prop("digit"), prop("pattern-separator"), prop("percent"), prop("per-mille")
	minusSign, zeroDigit := prop("minus-sign"), prop("zero-digit")

	var decimalDigitFamily []ustr
	if len(zeroDigit) > 0 {
		for ii := int(zeroDigit[0]); ii < int(zeroDigit[0])+10; ii++ {
			decimalDigitFamily = append(decimalDigitFamily, ustr{uint16(ii)})
		}
	}
	activeChars := append(append([]ustr(nil), decimalDigitFamily...), decimalSep, exponentSep, groupingSep, digit, patternSep)
	isActive := func(ch ustr) bool { return containsU(activeChars, ch) && !ch.eq(exponentSep) }

	picture := u(pictureText)
	subPictures := picture.split(patternSep)
	if len(subPictures) > 2 {
		return nil, &Error{Code: "D3080"}
	}

	splitParts := func(subpicture ustr) *pictureParts {
		prefix, suffix := ustr{}, ustr{}
		for ii := 0; ii < len(subpicture); ii++ {
			if isActive(subpicture.charAt(ii)) {
				prefix = subpicture.substring(0, ii)
				break
			}
		}
		for ii := len(subpicture) - 1; ii >= 0; ii-- {
			if isActive(subpicture.charAt(ii)) {
				suffix = subpicture.substring(ii+1, len(subpicture))
				break
			}
		}
		p := &pictureParts{prefix: prefix, suffix: suffix, subpicture: subpicture}
		p.activePart = subpicture.substring(len(prefix), len(subpicture)-len(suffix))
		exponentPosition := subpicture.indexOf(exponentSep, len(prefix))
		if exponentPosition == -1 || exponentPosition > len(subpicture)-len(suffix) {
			p.mantissaPart = p.activePart
		} else {
			p.mantissaPart = p.activePart.substring(0, exponentPosition)
			p.exponentPart = p.activePart.substring(exponentPosition+1, len(p.activePart))
			p.hasExponent = true
		}
		decimalPosition := p.mantissaPart.indexOf(decimalSep, 0)
		if decimalPosition == -1 {
			p.integerPart = p.mantissaPart
			p.fractionalPart = suffix
		} else {
			p.integerPart = p.mantissaPart.substring(0, decimalPosition)
			p.fractionalPart = p.mantissaPart.substring(decimalPosition+1, len(p.mantissaPart))
		}
		return p
	}

	// validate the picture string, F&O 4.7.3: the last failing rule names the error
	validate := func(parts *pictureParts) error {
		code := ""
		subpicture := parts.subpicture
		decimalPos := subpicture.indexOf(decimalSep, 0)
		if decimalPos != subpicture.lastIndexOf(decimalSep) {
			code = "D3081"
		}
		if subpicture.indexOf(percent, 0) != subpicture.lastIndexOf(percent) {
			code = "D3082"
		}
		if subpicture.indexOf(perMille, 0) != subpicture.lastIndexOf(perMille) {
			code = "D3083"
		}
		if subpicture.indexOf(percent, 0) != -1 && subpicture.indexOf(perMille, 0) != -1 {
			code = "D3084"
		}
		valid := false
		for _, ch := range parts.mantissaPart.chars() {
			if containsU(decimalDigitFamily, ch) || ch.eq(digit) {
				valid = true
				break
			}
		}
		if !valid {
			code = "D3085"
		}
		for _, ch := range parts.activePart.chars() {
			if !containsU(activeChars, ch) {
				code = "D3086"
				break
			}
		}
		if decimalPos != -1 {
			if subpicture.charAt(decimalPos-1).eq(groupingSep) || subpicture.charAt(decimalPos+1).eq(groupingSep) {
				code = "D3087"
			}
		} else if parts.integerPart.charAt(len(parts.integerPart) - 1).eq(groupingSep) {
			code = "D3088"
		}
		if subpicture.indexOf(concatU(groupingSep, groupingSep), 0) != -1 {
			code = "D3089"
		}
		if pos := parts.integerPart.indexOf(digit, 0); pos != -1 && countIn(parts.integerPart.substring(0, pos), decimalDigitFamily, nil) > 0 {
			code = "D3090"
		}
		if pos := parts.fractionalPart.lastIndexOf(digit); pos != -1 && countIn(parts.fractionalPart.substring(pos, len(parts.fractionalPart)), decimalDigitFamily, nil) > 0 {
			code = "D3091"
		}
		if parts.hasExponent && len(parts.exponentPart) > 0 && (subpicture.indexOf(percent, 0) != -1 || subpicture.indexOf(perMille, 0) != -1) {
			code = "D3092"
		}
		if parts.hasExponent && (len(parts.exponentPart) == 0 || countIn(parts.exponentPart, decimalDigitFamily, nil) != len(parts.exponentPart)) {
			code = "D3093"
		}
		if code != "" {
			return &Error{Code: code}
		}
		return nil
	}

	// analyse the picture string, F&O 4.7.4
	analyse := func(parts *pictureParts) (*pictureVariables, error) {
		getGroupingPositions := func(part ustr, toLeft bool) ([]int, error) {
			var positions []int
			groupingPosition := part.indexOf(groupingSep, 0)
			for steps := 0; groupingPosition != -1; steps++ {
				if steps > len(part)+len(parts.integerPart)+1 {
					// an empty grouping separator never stops matching; the original loops forever
					return nil, errHostRuntime
				}
				var side ustr
				if toLeft {
					side = part.substring(0, groupingPosition)
				} else {
					side = part.substring(groupingPosition, len(part))
				}
				positions = append(positions, countIn(side, decimalDigitFamily, digit))
				// the original searches the integer part for the next separator
				groupingPosition = parts.integerPart.indexOf(groupingSep, groupingPosition+1)
			}
			return positions, nil
		}
		v := &pictureVariables{prefix: parts.prefix, suffix: parts.suffix, picture: parts.subpicture}
		var err error
		if v.integerPartGroupingPositions, err = getGroupingPositions(parts.integerPart, false); err != nil {
			return nil, err
		}
		v.regularGrouping = regularPositions(v.integerPartGroupingPositions)
		if v.fractionalPartGroupingPositions, err = getGroupingPositions(parts.fractionalPart, true); err != nil {
			return nil, err
		}
		v.minimumIntegerPartSize = countIn(parts.integerPart, decimalDigitFamily, nil)
		v.scalingFactor = v.minimumIntegerPartSize
		v.minimumFactionalPartSize = countIn(parts.fractionalPart, decimalDigitFamily, nil)
		v.maximumFactionalPartSize = countIn(parts.fractionalPart, decimalDigitFamily, digit)
		if v.minimumIntegerPartSize == 0 && v.maximumFactionalPartSize == 0 {
			if parts.hasExponent {
				v.minimumFactionalPartSize = 1
				v.maximumFactionalPartSize = 1
			} else {
				v.minimumIntegerPartSize = 1
			}
		}
		if parts.hasExponent && v.minimumIntegerPartSize == 0 && parts.integerPart.indexOf(digit, 0) != -1 {
			v.minimumIntegerPartSize = 1
		}
		if v.minimumIntegerPartSize == 0 && v.minimumFactionalPartSize == 0 {
			v.minimumFactionalPartSize = 1
		}
		if parts.hasExponent {
			v.minimumExponentSize = countIn(parts.exponentPart, decimalDigitFamily, nil)
		}
		return v, nil
	}

	parts := make([]*pictureParts, len(subPictures))
	for i, sp := range subPictures {
		parts[i] = splitParts(sp)
	}
	for _, p := range parts {
		if err := validate(p); err != nil {
			return nil, err
		}
	}
	variables := make([]*pictureVariables, len(parts))
	for i, p := range parts {
		v, err := analyse(p)
		if err != nil {
			return nil, err
		}
		variables[i] = v
	}
	if len(variables) == 1 {
		neg := *variables[0]
		neg.integerPartGroupingPositions = append([]int(nil), neg.integerPartGroupingPositions...)
		neg.fractionalPartGroupingPositions = append([]int(nil), neg.fractionalPartGroupingPositions...)
		neg.prefix = concatU(minusSign, neg.prefix)
		variables = append(variables, &neg)
	}
	pic := variables[1]
	if value >= 0 {
		pic = variables[0]
	}
	adjustedNumber := value
	if pic.picture.indexOf(percent, 0) != -1 {
		adjustedNumber = value * 100
	} else if pic.picture.indexOf(perMille, 0) != -1 {
		adjustedNumber = value * 1000
	}
	mantissa := adjustedNumber
	exponent := 0.0
	hasExponent := pic.minimumExponentSize != 0
	if hasExponent {
		// mantissa * 10^exponent = adjustedNumber
		maxMantissa := mathPow(10, float64(pic.scalingFactor))
		minMantissa := mathPow(10, float64(pic.scalingFactor-1))
		if math.IsInf(mantissa, 0) {
			// the original never stops scaling an infinite mantissa
			return nil, errHostRuntime
		}
		if mantissa != 0 {
			for math.Abs(mantissa) < minMantissa {
				mantissa *= 10
				exponent--
			}
			for math.Abs(mantissa) > maxMantissa {
				mantissa /= 10
				exponent++
			}
		}
	}
	roundedNumber := roundNumber(mantissa, float64(pic.maximumFactionalPartSize))
	makeString := func(v float64, dp int) ustr {
		str := u(toFixed(math.Abs(v), dp))
		if !zeroDigit.eq(ustr{'0'}) {
			mapped := ustr{}
			for _, c := range str {
				switch {
				case c < '0' || c > '9':
					mapped = append(mapped, c)
				case int(c-'0') < len(decimalDigitFamily):
					mapped = append(mapped, decimalDigitFamily[c-'0'][0])
				}
			}
			str = mapped
		}
		return str
	}
	stringValue := makeString(roundedNumber, pic.maximumFactionalPartSize)
	if dp := stringValue.indexOf(ustr{'.'}, 0); dp == -1 {
		stringValue = concatU(stringValue, decimalSep)
	} else {
		stringValue = concatU(stringValue[:dp], getSubstitution(decimalSep, stringValue, dp, dp+1), stringValue[dp+1:])
	}
	if len(stringValue) == 0 && len(zeroDigit) == 0 {
		// the original strips the empty zero digit from the empty string forever
		return nil, errHostRuntime
	}
	for len(stringValue) > 0 && stringValue.charAt(0).eq(zeroDigit) {
		stringValue = stringValue.substring(1, len(stringValue))
	}
	for len(stringValue) > 0 && stringValue.charAt(len(stringValue)-1).eq(zeroDigit) {
		stringValue = stringValue.substring(0, len(stringValue)-1)
	}
	decimalPos := stringValue.indexOf(decimalSep, 0)
	padLeft := pic.minimumIntegerPartSize - decimalPos
	padRight := pic.minimumFactionalPartSize - (len(stringValue) - decimalPos - 1)
	if padLeft > 0 {
		stringValue = concatU(repeatU(zeroDigit, padLeft), stringValue)
	}
	if padRight > 0 {
		stringValue = concatU(stringValue, repeatU(zeroDigit, padRight))
	}
	decimalPos = stringValue.indexOf(decimalSep, 0)
	if pic.regularGrouping > 0 {
		groupCount := int(math.Floor(float64(decimalPos-1) / float64(pic.regularGrouping)))
		for group := 1; group <= groupCount; group++ {
			at := decimalPos - group*pic.regularGrouping
			stringValue = concatU(stringValue.slice(0, at), groupingSep, stringValue.sliceFrom(at))
		}
	} else {
		for _, pos := range pic.integerPartGroupingPositions {
			at := decimalPos - pos
			stringValue = concatU(stringValue.slice(0, at), groupingSep, stringValue.sliceFrom(at))
			decimalPos++
		}
	}
	decimalPos = stringValue.indexOf(decimalSep, 0)
	for _, pos := range pic.fractionalPartGroupingPositions {
		at := pos + decimalPos + 1
		stringValue = concatU(stringValue.slice(0, at), groupingSep, stringValue.sliceFrom(at))
	}
	decimalPos = stringValue.indexOf(decimalSep, 0)
	if pic.picture.indexOf(decimalSep, 0) == -1 || decimalPos == len(stringValue)-1 {
		stringValue = stringValue.substring(0, len(stringValue)-1)
	}
	if hasExponent {
		stringExponent := makeString(exponent, 0)
		if pl := pic.minimumExponentSize - len(stringExponent); pl > 0 {
			stringExponent = concatU(repeatU(zeroDigit, pl), stringExponent)
		}
		sign := ustr{}
		if exponent < 0 {
			sign = minusSign
		}
		stringValue = concatU(stringValue, exponentSep, sign, stringExponent)
	}
	return concatU(pic.prefix, stringValue, pic.suffix).String(), nil
}

// getSubstitution expands a replacement string of String.prototype.replace for a match at [start, end).
func getSubstitution(replacement, str ustr, start, end int) ustr {
	var out ustr
	for i := 0; i < len(replacement); i++ {
		c := replacement[i]
		if c != '$' || i+1 >= len(replacement) {
			out = append(out, c)
			continue
		}
		switch replacement[i+1] {
		case '$':
			out = append(out, '$')
		case '&':
			out = append(out, str[start:end]...)
		case '`':
			out = append(out, str[:start]...)
		case '\'':
			out = append(out, str[end:]...)
		default:
			out = append(out, c)
			continue
		}
		i++
	}
	return out
}

// regularPositions is the interval of regularly spaced grouping positions, 0 when they are not regular.
func regularPositions(indexes []int) int {
	if len(indexes) == 0 {
		return 0
	}
	gcd := func(a, b int) int {
		for b != 0 {
			a, b = b, a%b
		}
		return a
	}
	factor := indexes[0]
	for _, x := range indexes[1:] {
		factor = gcd(factor, x)
	}
	for index := 1; index <= len(indexes); index++ {
		found := false
		for _, x := range indexes {
			if x == index*factor {
				found = true
				break
			}
		}
		if !found {
			return 0
		}
	}
	return factor
}

// fnFormatBase is $formatBase: the rounded number in a radix from 2 to 36.
func fnFormatBase(f *focus, args []any) (any, error) {
	value, ok := argNumber(args, 0)
	if !ok {
		return nil, nil
	}
	value = roundNumber(value, 0)
	radix := 10.0
	if r, ok := argNumber(args, 1); ok {
		radix = roundNumber(r, 0)
	}
	if radix < 2 || radix > 36 {
		return nil, &Error{Code: "D3100", Value: radix, HasValue: true}
	}
	if math.IsNaN(radix) {
		// Number.prototype.toString rejects the radix
		return nil, errHostRuntime
	}
	return toStringRadix(value, int(radix)), nil
}

var (
	decimalNumber = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([Ee][-+]?[0-9]+)?$`)
	// the alternation is not grouped, so only the first branch is anchored at the start and only
	// the last at the end
	prefixedNumber = regexp.MustCompile(`^(0[xX][0-9A-Fa-f]+)|(0[oO][0-7]+)|(0[bB][0-1]+)$`)
)

// fnNumber is $number: a number from a numeric string or a boolean.
func fnNumber(f *focus, args []any) (any, error) {
	switch arg := argAt(args, 0).(type) {
	case nil:
		return nil, nil
	case float64:
		return arg, nil
	case string:
		if decimalNumber.MatchString(arg) {
			if n := stringToNumber(arg); !math.IsInf(n, 0) && !math.IsNaN(n) {
				return parseFloatPrefix(arg), nil
			}
		}
		if prefixedNumber.MatchString(arg) {
			return stringToNumber(arg), nil
		}
	case bool:
		if arg {
			return 1.0, nil
		}
		return 0.0, nil
	}
	v := argAt(args, 0)
	return nil, &Error{Code: "D3030", Value: v, HasValue: true, Index: 1, HasIndex: true}
}

func mathFunction(op func(float64) float64) func(f *focus, args []any) (any, error) {
	return func(f *focus, args []any) (any, error) {
		x, ok := argNumber(args, 0)
		if !ok {
			return nil, nil
		}
		return op(x), nil
	}
}

var (
	fnAbs   = mathFunction(math.Abs)
	fnFloor = mathFunction(math.Floor)
	fnCeil  = mathFunction(math.Ceil)
)

// roundNumber is fn.round: round half to even, at a number of decimal places (shifted through the
// number's string form to avoid binary scaling errors).
func roundNumber(arg float64, precision float64) float64 {
	shift := precision != 0 && !math.IsNaN(precision)
	if shift {
		mant, exp, hasExp := strings.Cut(formatNumberJS(arg), "e")
		e := precision
		if hasExp {
			e = stringToNumber(exp) + precision
		}
		arg = stringToNumber(mant + "e" + formatNumberJS(e))
	}
	result := mathRound(arg)
	diff := result - arg
	if math.Abs(diff) == 0.5 && math.Abs(math.Mod(result, 2)) == 1 {
		// rounded the wrong way: adjust to the nearest even number
		result--
	}
	if shift {
		mant, exp, hasExp := strings.Cut(formatNumberJS(result), "e")
		e := -precision
		if hasExp {
			e = stringToNumber(exp) - precision
		}
		result = stringToNumber(mant + "e" + formatNumberJS(e))
	}
	if result == 0 {
		// JSON has no -0
		result = 0
	}
	return result
}

// fnRound is $round.
func fnRound(f *focus, args []any) (any, error) {
	x, ok := argNumber(args, 0)
	if !ok {
		return nil, nil
	}
	precision, _ := argNumber(args, 1)
	return roundNumber(x, precision), nil
}

// fnSqrt is $sqrt.
func fnSqrt(f *focus, args []any) (any, error) {
	x, ok := argNumber(args, 0)
	if !ok {
		return nil, nil
	}
	if x < 0 {
		return nil, &Error{Code: "D3060", Index: 1, HasIndex: true, Value: x, HasValue: true}
	}
	return math.Sqrt(x), nil
}

// fnPower is $power.
func fnPower(f *focus, args []any) (any, error) {
	x, ok := argNumber(args, 0)
	if !ok {
		return nil, nil
	}
	exp, hasExp := argNumber(args, 1)
	if !hasExp {
		exp = math.NaN()
	}
	result := mathPow(x, exp)
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil, &Error{Code: "D3061", Index: 1, HasIndex: true, Value: x, HasValue: true, Exp: exp, HasExp: hasExp}
	}
	return result, nil
}

// fnRandom is $random: a number in [0, 1).
func fnRandom(f *focus, args []any) (any, error) {
	return rand.Float64(), nil
}

// fnBoolean is $boolean: the effective boolean value.
func fnBoolean(f *focus, args []any) (any, error) {
	return toBoolean(argAt(args, 0))
}

// fnNot is $not.
func fnNot(f *focus, args []any) (any, error) {
	arg := argAt(args, 0)
	if arg == nil {
		return nil, nil
	}
	b, err := toBoolean(arg)
	if err != nil {
		return nil, err
	}
	return b != true, nil
}

// hofFuncArgs builds the arguments for the function of a higher-order function: the value, then the
// position and the whole structure when the function takes them.
func hofFuncArgs(fn any, arg1, arg2, arg3 any) []any {
	args := []any{arg1}
	length := getFunctionArity(fn)
	if length >= 2 {
		args = append(args, arg2)
	}
	if length >= 3 {
		args = append(args, arg3)
	}
	return args
}

// fnMap is $map.
func fnMap(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return nil, nil
	}
	fn := argAt(args, 1)
	result := f.createSequence()
	for i := 0; i < len(arr.items); i++ {
		res, err := f.callMethod(fn, hofFuncArgs(fn, arr.items[i], float64(i), arr))
		if err != nil {
			return nil, err
		}
		if res != nil {
			if err := result.add(res); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// fnFilter is $filter.
func fnFilter(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return nil, nil
	}
	fn := argAt(args, 1)
	result := f.createSequence()
	for i := 0; i < len(arr.items); i++ {
		entry := arr.items[i]
		res, err := f.callMethod(fn, hofFuncArgs(fn, entry, float64(i), arr))
		if err != nil {
			return nil, err
		}
		b, err := toBoolean(res)
		if err != nil {
			return nil, err
		}
		if b == true {
			if err := result.add(entry); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// fnSingle is $single: the one item that matches, an error for none or several.
func fnSingle(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return nil, nil
	}
	fn := argAt(args, 1)
	hasFoundMatch := false
	var result any
	for i := 0; i < len(arr.items); i++ {
		entry := arr.items[i]
		positive := true
		if fn != nil {
			res, err := f.callMethod(fn, hofFuncArgs(fn, entry, float64(i), arr))
			if err != nil {
				return nil, err
			}
			b, err := toBoolean(res)
			if err != nil {
				return nil, err
			}
			positive = b == true
		}
		if positive {
			if hasFoundMatch {
				return nil, &Error{Code: "D3138", Index: i, HasIndex: true}
			}
			result = entry
			hasFoundMatch = true
		}
	}
	if !hasFoundMatch {
		return nil, &Error{Code: "D3139"}
	}
	return result, nil
}

// fnZip is $zip: tuples of the items at each position, as long as the shortest array.
func fnZip(f *focus, args []any) (any, error) {
	result := newArray()
	length := math.Inf(1)
	for _, arg := range args {
		n := 0.0
		if arr, ok := arg.(*Array); ok {
			n = float64(len(arr.items))
		}
		length = math.Min(length, n)
	}
	if math.IsInf(length, 1) {
		return nil, errHostRuntime
	}
	for i := 0; i < int(length); i++ {
		t := newArray()
		for _, arg := range args {
			t.push(memberOf(arg, itoa(i)))
		}
		result.push(t)
	}
	return result, nil
}

func itoa(i int) string { return formatNumberJS(float64(i)) }

// fnFoldLeft is $reduce.
func fnFoldLeft(f *focus, args []any) (any, error) {
	sequence, ok := argAt(args, 0).(*Array)
	if !ok {
		return nil, nil
	}
	fn, init := argAt(args, 1), argAt(args, 2)
	arity := getFunctionArity(fn)
	if arity < 2 {
		return nil, &Error{Code: "D3050", Index: 1, HasIndex: true}
	}
	var result any
	index := 0
	if init == nil && len(sequence.items) > 0 {
		result = sequence.items[0]
		index = 1
	} else {
		result = init
	}
	for ; index < len(sequence.items); index++ {
		callArgs := []any{result, sequence.items[index]}
		if arity >= 3 {
			callArgs = append(callArgs, float64(index))
		}
		if arity >= 4 {
			callArgs = append(callArgs, sequence)
		}
		var err error
		if result, err = f.callMethod(fn, callArgs); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// keysOfValue is fn.keys: the keys of an object, or the union of the keys of the items of an array.
func keysOfValue(f *focus, arg any) (*Array, error) {
	result := f.createSequence()
	if arr, ok := arg.(*Array); ok {
		merge := NewObject()
		for _, item := range arr.items {
			allkeys, err := keysOfValue(f, item)
			if err != nil {
				return nil, err
			}
			for _, key := range allkeys.items {
				merge.Set(key.(string), true)
			}
		}
		return keysOfValue(f, merge)
	}
	if isObjectLike(arg) && !isFunction(arg) {
		for _, key := range keysOf(arg) {
			if err := result.add(key); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// fnKeys is $keys.
func fnKeys(f *focus, args []any) (any, error) {
	return keysOfValue(f, argAt(args, 0))
}

// fnLookup is $lookup.
func fnLookup(f *focus, args []any) (any, error) {
	key, _ := argAt(args, 1).(string)
	return lookupValue(argAt(args, 0), key, f.env.base), nil
}

// fnAppend is $append.
func fnAppend(f *focus, args []any) (any, error) {
	return appendValues(f.env.base, argAt(args, 0), argAt(args, 1))
}

// fnExists is $exists.
func fnExists(f *focus, args []any) (any, error) {
	return argAt(args, 0) != nil, nil
}

// spreadValue is fn.spread: an object as an array of one-property objects.
func spreadValue(f *focus, arg any) (any, error) {
	switch x := arg.(type) {
	case *Array:
		var result any = f.createSequence()
		for _, item := range x.items {
			spread, err := spreadValue(f, item)
			if err != nil {
				return nil, err
			}
			if result, err = appendValues(f.env.base, result, spread); err != nil {
				return nil, err
			}
		}
		return result, nil
	case *Object:
		result := f.createSequence()
		for _, key := range x.Keys() {
			obj := NewObject()
			obj.Set(key, x.vals[key])
			if err := result.add(obj); err != nil {
				return nil, err
			}
		}
		return result, nil
	case *Function:
		// the original spreads the members of the function definition object
		return nil, errHostRuntime
	}
	return arg, nil
}

// fnSpread is $spread.
func fnSpread(f *focus, args []any) (any, error) {
	return spreadValue(f, argAt(args, 0))
}

// fnMerge is $merge: the objects merged, later properties winning.
func fnMerge(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return nil, nil
	}
	result := NewObject()
	for _, obj := range arr.items {
		for _, prop := range keysOf(obj) {
			result.Set(prop, memberOf(obj, prop))
		}
	}
	return result, nil
}

// fnReverse is $reverse.
func fnReverse(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return argAt(args, 0), nil
	}
	if len(arr.items) <= 1 {
		return arr, nil
	}
	result := &Array{items: make([]any, len(arr.items))}
	for i, item := range arr.items {
		result.items[len(arr.items)-i-1] = item
	}
	return result, nil
}

// fnEach is $each: the results of a function of each value and key of an object.
func fnEach(f *focus, args []any) (any, error) {
	obj, fn := argAt(args, 0), argAt(args, 1)
	result := f.createSequence()
	for _, key := range keysOf(obj) {
		val, err := f.callMethod(fn, hofFuncArgs(fn, memberOf(obj, key), key, obj))
		if err != nil {
			return nil, err
		}
		if val != nil {
			if err := result.add(val); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// fnError is $error: raises an error with the message.
func fnError(f *focus, args []any) (any, error) {
	message, _ := argAt(args, 0).(string)
	if message == "" {
		message = "$error() function evaluated"
	}
	return nil, &Error{Code: "D3137", Message: message}
}

// fnAssert is $assert: raises an error with the message when the condition is false.
func fnAssert(f *focus, args []any) (any, error) {
	if !jsTruthy(argAt(args, 0)) {
		message, _ := argAt(args, 1).(string)
		if message == "" {
			message = "$assert() statement failed"
		}
		return nil, &Error{Code: "D3141", Message: message}
	}
	return nil, nil
}

// fnType is $type.
func fnType(f *focus, args []any) (any, error) {
	value := argAt(args, 0)
	switch value.(type) {
	case nil:
		return nil, nil
	case nullValue:
		return "null", nil
	}
	num, err := isNumeric(value)
	if err != nil {
		return nil, err
	}
	switch {
	case num:
		return "number", nil
	case isFunction(value):
		return "function", nil
	}
	switch value.(type) {
	case string:
		return "string", nil
	case bool:
		return "boolean", nil
	case *Array:
		return "array", nil
	}
	return "object", nil
}

// fnSort is $sort: a stable merge sort, by the natural order of numbers or strings or by a function.
func fnSort(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return argAt(args, 0), nil
	}
	if len(arr.items) <= 1 {
		return arr, nil
	}
	comparator := argAt(args, 1)
	var swap func(a, b any) (bool, error)
	if comparator == nil {
		numbers, err := isArrayOfNumbers(arr)
		if err != nil {
			return nil, err
		}
		if !numbers && !isArrayOfStrings(arr) {
			return nil, &Error{Code: "D3070", Index: 1, HasIndex: true}
		}
		swap = func(a, b any) (bool, error) { return lessThan(b, a), nil }
	} else {
		swap = func(a, b any) (bool, error) {
			res, err := callDirect(comparator, []any{a, b})
			if err != nil {
				return false, err
			}
			return jsTruthy(res), nil
		}
	}
	return mergeSort(arr, swap)
}

// fnShuffle is $shuffle: a random permutation (the inside-out Fisher-Yates algorithm).
func fnShuffle(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok {
		return argAt(args, 0), nil
	}
	if len(arr.items) <= 1 {
		return arr, nil
	}
	result := &Array{items: make([]any, len(arr.items))}
	for i, item := range arr.items {
		j := rand.Intn(i + 1)
		if i != j {
			result.items[i] = result.items[j]
		}
		result.items[j] = item
	}
	return result, nil
}

// fnDistinct is $distinct: the values without duplicates (by deep equality).
func fnDistinct(f *focus, args []any) (any, error) {
	arr, ok := argAt(args, 0).(*Array)
	if !ok || len(arr.items) <= 1 {
		return argAt(args, 0), nil
	}
	results := newArray()
	if arr.sequence {
		results = f.createSequence()
	}
	for _, value := range arr.items {
		includes := false
		for _, r := range results.items {
			if isDeepEqual(value, r) {
				includes = true
				break
			}
		}
		if !includes {
			if err := results.add(value); err != nil {
				return nil, err
			}
		}
	}
	return results, nil
}

// fnSift is $sift: the properties of an object for which a function is true.
func fnSift(f *focus, args []any) (any, error) {
	arg, fn := argAt(args, 0), argAt(args, 1)
	result := NewObject()
	for _, item := range keysOf(arg) {
		entry := memberOf(arg, item)
		res, err := f.callMethod(fn, hofFuncArgs(fn, entry, item, arg))
		if err != nil {
			return nil, err
		}
		b, err := toBoolean(res)
		if err != nil {
			return nil, err
		}
		if b == true {
			result.Set(item, entry)
		}
	}
	// an empty object is undefined
	if result.Len() == 0 {
		return nil, nil
	}
	return result, nil
}
