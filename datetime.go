/**
 * © Copyright IBM Corp. 2018 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of src/datetime.js: the XPath fn:format-integer, fn:format-dateTime and their parsing
// counterparts, behind $formatInteger, $parseInteger, $fromMillis and $toMillis.

package jsonata

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jsonata-js/jsonata/internal/jsre"
)

var (
	fewWords     = []string{"Zero", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten", "Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen", "Seventeen", "Eighteen", "Nineteen"}
	ordinalWords = []string{"Zeroth", "First", "Second", "Third", "Fourth", "Fifth", "Sixth", "Seventh", "Eighth", "Ninth", "Tenth", "Eleventh", "Twelfth", "Thirteenth", "Fourteenth", "Fifteenth", "Sixteenth", "Seventeenth", "Eighteenth", "Nineteenth"}
	decadeWords  = []string{"Twenty", "Thirty", "Forty", "Fifty", "Sixty", "Seventy", "Eighty", "Ninety", "Hundred"}
	magnitudes   = []string{"Thousand", "Million", "Billion", "Trillion"}
)

// floorLog10 is Math.floor(Math.log10(num)) for a positive integral num.
func floorLog10(num float64) float64 {
	if num < 1 || math.IsInf(num, 0) {
		return math.Floor(math.Log10(num))
	}
	digits := len(strconv.FormatFloat(num, 'f', 0, 64))
	// Math.log10 rounds 999999999999999 up to 15
	if num == 999999999999999 {
		return 15
	}
	return float64(digits - 1)
}

// numberToWords converts a number into English words, cardinal or ordinal.
func numberToWords(value float64, ordinal bool) (string, error) {
	var lookup func(num float64, prev, ord bool, depth int) (string, error)
	lookup = func(num float64, prev, ord bool, depth int) (string, error) {
		if math.IsNaN(num) || math.IsInf(num, 0) || depth > 1000 {
			// the original recurses without end and overflows the stack
			return "", errHostRuntime
		}
		var words string
		switch {
		case num <= 19:
			if prev {
				words = " and "
			}
			if ord {
				words += ordinalWords[int(num)]
			} else {
				words += fewWords[int(num)]
			}
		case num < 100:
			tens := math.Floor(num / 10)
			remainder := math.Mod(num, 10)
			if prev {
				words = " and "
			}
			words += decadeWords[int(tens)-2]
			if remainder > 0 {
				rest, err := lookup(remainder, false, ord, depth+1)
				if err != nil {
					return "", err
				}
				words += "-" + rest
			} else if ord {
				words = words[:len(words)-1] + "ieth"
			}
		case num < 1000:
			hundreds := math.Floor(num / 100)
			remainder := math.Mod(num, 100)
			if prev {
				words = ", "
			}
			words += fewWords[int(hundreds)] + " Hundred"
			if remainder > 0 {
				rest, err := lookup(remainder, true, ord, depth+1)
				if err != nil {
					return "", err
				}
				words += rest
			} else if ord {
				words += "th"
			}
		default:
			mag := math.Floor(floorLog10(num) / 3)
			if mag > float64(len(magnitudes)) {
				mag = float64(len(magnitudes)) // the largest word
			}
			factor := powJS(10, mag*3)
			mant := math.Floor(num / factor)
			remainder := num - float64(mant*factor)
			if prev {
				words = ", "
			}
			head, err := lookup(mant, false, false, depth+1)
			if err != nil {
				return "", err
			}
			words += head + " " + magnitudes[int(mag)-1]
			if remainder > 0 {
				rest, err := lookup(remainder, true, ord, depth+1)
				if err != nil {
					return "", err
				}
				words += rest
			} else if ord {
				words += "th"
			}
		}
		return words, nil
	}
	return lookup(value, false, ordinal, 0)
}

// wordValues maps each number word (lower case) to its value, in the original's insertion order,
// which is the order of the alternatives of the parsing regex.
var wordKeys, wordValues = func() ([]string, map[string]float64) {
	var keys []string
	values := map[string]float64{}
	set := func(k string, v float64) {
		if _, ok := values[k]; !ok {
			keys = append(keys, k)
		}
		values[k] = v
	}
	for i, w := range fewWords {
		set(strings.ToLower(w), float64(i))
	}
	for i, w := range ordinalWords {
		set(strings.ToLower(w), float64(i))
	}
	for i, w := range decadeWords {
		lword := strings.ToLower(w)
		set(lword, float64((i+2)*10))
		set(lword[:len(w)-1]+"ieth", float64((i+2)*10))
	}
	set("hundredth", 100)
	for i, w := range magnitudes {
		lword := strings.ToLower(w)
		v := powJS(10, float64((i+1)*3))
		set(lword, v)
		set(lword+"th", v)
	}
	return keys, values
}()

var wordSeparators = jsreMust(`,\s|\sand\s|[\s\\-]`, "g")

func jsreMust(pattern, flags string) *jsre.Regexp {
	re, err := jsre.Compile(pattern, flags)
	if err != nil {
		panic(err)
	}
	return re
}

// splitRegex is String.prototype.split with a regex that never matches the empty string.
func splitRegex(re *jsre.Regexp, s string) []string {
	units := utf16Units(s)
	var out []string
	p := 0
	for {
		caps, next := re.Exec(units, p)
		if caps == nil {
			break
		}
		out = append(out, fromUnits(units[p:caps[0]]))
		p = caps[1]
		if next <= caps[0] {
			break
		}
	}
	return append(out, fromUnits(units[p:]))
}

// wordsToNumber converts a number in English words to its value.
func wordsToNumber(text string) float64 {
	parts := splitRegex(wordSeparators, text)
	segs := []float64{0}
	for _, part := range parts {
		value, ok := wordValues[part]
		if ok && value < 100 {
			top := segs[len(segs)-1]
			segs = segs[:len(segs)-1]
			if top >= 1000 {
				segs = append(segs, top)
				top = 0
			}
			segs = append(segs, top+value)
		} else {
			if !ok {
				value = math.NaN()
			}
			segs[len(segs)-1] *= value
		}
	}
	result := 0.0
	for _, s := range segs {
		result += s
	}
	return result
}

var romanNumerals = []struct {
	value   float64
	numeral string
}{
	{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"}, {100, "c"}, {90, "xc"}, {50, "l"},
	{40, "xl"}, {10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"},
}

var romanValues = map[uint16]float64{'M': 1000, 'D': 500, 'C': 100, 'L': 50, 'X': 10, 'V': 5, 'I': 1}

// romanLimit is about where the original's recursive conversion overflows the stack.
const romanLimit = 6880000

func decimalToRoman(value float64) (string, error) {
	if value >= romanLimit {
		return "", errHostRuntime
	}
	var b strings.Builder
	for {
		found := false
		for _, n := range romanNumerals {
			if value >= n.value {
				b.WriteString(n.numeral)
				value -= n.value
				found = true
				break
			}
		}
		if !found {
			return b.String(), nil
		}
	}
}

func romanToDecimal(roman string) float64 {
	decimal := 0.0
	max := 1.0
	units := utf16Units(roman)
	for i := len(units) - 1; i >= 0; i-- {
		value, ok := romanValues[units[i]]
		if !ok {
			return math.NaN()
		}
		if value < max {
			decimal -= value
		} else {
			max = value
			decimal += value
		}
	}
	return decimal
}

// toUint16 is the ToUint16 conversion of String.fromCharCode.
func toUint16(f float64) uint16 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	m := math.Mod(math.Trunc(f), 65536)
	if m < 0 {
		m += 65536
	}
	return uint16(m)
}

func decimalToLetters(value float64, aChar uint16) (string, error) {
	if math.IsInf(value, 1) {
		// the original never terminates
		return "", errHostRuntime
	}
	var letters []uint16
	for value > 0 {
		letters = append([]uint16{toUint16(math.Mod(value-1, 26) + float64(aChar))}, letters...)
		value = math.Floor((value - 1) / 26)
	}
	return fromUnits(letters), nil
}

func lettersToDecimal(letters string, aChar uint16) float64 {
	units := utf16Units(letters)
	decimal := 0.0
	for i := range units {
		// float64() keeps the compiler from fusing the multiply and add, which would round differently
		decimal += float64((float64(units[len(units)-i-1]) - float64(aChar) + 1) * powJS(26, float64(i)))
	}
	return decimal
}

const (
	formatDecimal  = "decimal"
	formatLetters  = "letters"
	formatRoman    = "roman"
	formatWords    = "words"
	formatSequence = "sequence"

	caseUpper = "upper"
	caseLower = "lower"
	caseTitle = "title"
)

// groupingSeparator is a grouping-separator-sign of a decimal digit pattern and its position.
type groupingSeparator struct {
	position  float64
	character string
}

// integerFormat is the analysis of an integer picture string.
type integerFormat struct {
	primary  string
	caseMode string
	ordinal  bool
	token    string

	zeroCode        rune
	mandatoryDigits float64
	hasMandatory    bool
	optionalDigits  float64
	hasOptional     bool
	regular         bool
	regularSep      groupingSeparator
	separators      []groupingSeparator
	hasSeparators   bool
	parseWidth      float64
	hasParseWidth   bool
}

// decimalGroups are the zero digits of the Unicode decimal digit groups.
var decimalGroups = []rune{0x30, 0x0660, 0x06F0, 0x07C0, 0x0966, 0x09E6, 0x0A66, 0x0AE6, 0x0B66, 0x0BE6, 0x0C66, 0x0CE6,
	0x0D66, 0x0DE6, 0x0E50, 0x0ED0, 0x0F20, 0x1040, 0x1090, 0x17E0, 0x1810, 0x1946, 0x19D0, 0x1A80, 0x1A90, 0x1B50, 0x1BB0,
	0x1C40, 0x1C50, 0xA620, 0xA8D0, 0xA900, 0xA9D0, 0xA9F0, 0xAA50, 0xABF0, 0xFF10}

// lastIndexOfUnit is String.prototype.lastIndexOf for a single code unit.
func lastIndexOfUnit(units []uint16, u uint16) int {
	for i := len(units) - 1; i >= 0; i-- {
		if units[i] == u {
			return i
		}
	}
	return -1
}

func gcdJS(a, b float64) float64 {
	for b != 0 {
		a, b = b, math.Mod(a, b)
	}
	return a
}

// analyseIntegerPicture preprocesses an integer picture string.
func analyseIntegerPicture(picture string) (*integerFormat, error) {
	format := &integerFormat{primary: formatDecimal, caseMode: caseLower}
	units := utf16Units(picture)
	primaryFormat := picture
	if semicolon := lastIndexOfUnit(units, ';'); semicolon != -1 {
		primaryFormat = fromUnits(units[:semicolon])
		if modifier := units[semicolon+1:]; len(modifier) > 0 && modifier[0] == 'o' {
			format.ordinal = true
		}
	}
	switch primaryFormat {
	case "A":
		format.caseMode = caseUpper
		format.primary = formatLetters
	case "a":
		format.primary = formatLetters
	case "I":
		format.caseMode = caseUpper
		format.primary = formatRoman
	case "i":
		format.primary = formatRoman
	case "W":
		format.caseMode = caseUpper
		format.primary = formatWords
	case "Ww":
		format.caseMode = caseTitle
		format.primary = formatWords
	case "w":
		format.primary = formatWords
	default:
		// a decimal digit pattern contains a digit of a Unicode decimal digit group
		zeroCode := rune(-1)
		mandatory, optional, separatorPosition := 0.0, 0.0, 0.0
		var separators []groupingSeparator
		points := codePointStrings(primaryFormat)
		for i := len(points) - 1; i >= 0; i-- {
			codePoint, _ := decodeWTF8(points[i], 0)
			digit := false
			for _, group := range decimalGroups {
				if codePoint >= group && codePoint <= group+9 {
					digit = true
					mandatory++
					separatorPosition++
					if zeroCode == -1 {
						zeroCode = group
					} else if group != zeroCode {
						// digits from different decimal groups
						return nil, &Error{Code: "D3131"}
					}
					break
				}
			}
			if !digit {
				if codePoint == '#' {
					separatorPosition++
					optional++
				} else {
					separators = append(separators, groupingSeparator{position: separatorPosition, character: points[i]})
				}
			}
		}
		if mandatory > 0 {
			format.primary = formatDecimal
			format.zeroCode = zeroCode
			format.mandatoryDigits, format.hasMandatory = mandatory, true
			format.optionalDigits, format.hasOptional = optional, true
			if regular := regularRepeat(separators); regular > 0 {
				format.regular = true
				format.regularSep = groupingSeparator{position: regular, character: separators[0].character}
			} else {
				format.separators = separators
				format.hasSeparators = true
			}
		} else {
			// a numbering sequence, which this implementation does not support
			format.primary = formatSequence
			format.token = primaryFormat
		}
	}
	return format, nil
}

// regularRepeat returns the interval of equally spaced, identical grouping separators, or 0.
func regularRepeat(separators []groupingSeparator) float64 {
	if len(separators) == 0 {
		return 0
	}
	for _, s := range separators[1:] {
		if s.character != separators[0].character {
			return 0
		}
	}
	factor := separators[0].position
	for _, s := range separators[1:] {
		factor = gcdJS(factor, s.position)
	}
	for index := 1; index <= len(separators); index++ {
		found := false
		for _, s := range separators {
			if s.position == float64(index)*factor {
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

// substrUnits is String.prototype.substr(start, length) on code units; length < 0 means to the end.
func substrUnits(units []uint16, start float64, length float64, hasLength bool) []uint16 {
	n := float64(len(units))
	if start < 0 {
		start = math.Max(n+start, 0)
	}
	start = math.Min(start, n)
	end := n
	if hasLength {
		end = math.Min(math.Max(length, 0)+start, n)
	}
	if end <= start {
		return nil
	}
	return units[int(start):int(end)]
}

// maxStringLength is V8's maximum string length.
const maxStringLength = 1<<29 - 24

func repeatZeros(n float64, zero string) (string, error) {
	if n > maxStringLength {
		return "", errHostRuntime
	}
	return strings.Repeat(zero, int(n)), nil
}

// formatIntegerValue is _formatInteger: formats a number with an analysed picture.
func formatIntegerValue(value float64, format *integerFormat) (string, error) {
	var formatted string
	negative := value < 0
	value = math.Abs(value)
	switch format.primary {
	case formatLetters:
		aChar := uint16('a')
		if format.caseMode == caseUpper {
			aChar = 'A'
		}
		s, err := decimalToLetters(value, aChar)
		if err != nil {
			return "", err
		}
		formatted = s
	case formatRoman:
		s, err := decimalToRoman(value)
		if err != nil {
			return "", err
		}
		formatted = s
		if format.caseMode == caseUpper {
			formatted = toUpperJS(formatted)
		}
	case formatWords:
		s, err := numberToWords(value, format.ordinal)
		if err != nil {
			return "", err
		}
		formatted = s
		if format.caseMode == caseUpper {
			formatted = toUpperJS(formatted)
		} else if format.caseMode == caseLower {
			formatted = toLowerJS(formatted)
		}
	case formatDecimal:
		formatted = formatNumberJS(value)
		if format.hasMandatory {
			if padLength := format.mandatoryDigits - float64(utf16Len(formatted)); padLength > 0 {
				padding, err := repeatZeros(padLength, "0")
				if err != nil {
					return "", err
				}
				formatted = padding + formatted
			}
		}
		if format.zeroCode != 0x30 {
			var b []byte
			for _, c := range codePointStrings(formatted) {
				r, _ := decodeWTF8(c, 0)
				cp := r + format.zeroCode - 0x30
				if cp < 0 || cp > 0x10FFFF {
					// String.fromCodePoint rejects it
					return "", errHostRuntime
				}
				b = appendWTF8(b, cp)
			}
			formatted = normalizeWTF8(string(b))
		}
		// insert the grouping separators
		if format.regular {
			units := utf16Units(formatted)
			n := math.Floor((float64(len(units)) - 1) / format.regularSep.position)
			for ii := n; ii > 0; ii-- {
				units = utf16Units(formatted)
				pos := float64(len(units)) - ii*format.regularSep.position
				formatted = concatStrings(fromUnits(substrUnits(units, 0, pos, true)), format.regularSep.character,
					fromUnits(substrUnits(units, pos, 0, false)))
			}
		} else if format.hasSeparators {
			// the original reverses the separators in place
			for i, j := 0, len(format.separators)-1; i < j; i, j = i+1, j-1 {
				format.separators[i], format.separators[j] = format.separators[j], format.separators[i]
			}
			for _, separator := range format.separators {
				units := utf16Units(formatted)
				pos := float64(len(units)) - separator.position
				formatted = concatStrings(fromUnits(substrUnits(units, 0, pos, true)), separator.character,
					fromUnits(substrUnits(units, pos, 0, false)))
			}
		}
		if format.ordinal {
			units := utf16Units(formatted)
			suffix := ""
			if len(units) > 0 {
				switch units[len(units)-1] {
				case '1':
					suffix = "st"
				case '2':
					suffix = "nd"
				case '3':
					suffix = "rd"
				}
			}
			if suffix == "" || (len(units) > 1 && units[len(units)-2] == '1') {
				suffix = "th"
			}
			formatted += suffix
		}
	case formatSequence:
		return "", &Error{Code: "D3130", Value: format.token, HasValue: true}
	}
	if negative {
		formatted = "-" + formatted
	}
	return formatted, nil
}

// fnFormatInteger is $formatInteger(value, picture).
func fnFormatInteger(f *focus, args []any) (any, error) {
	value, ok := argAt(args, 0).(float64)
	if !ok {
		return nil, nil
	}
	picture, _ := argAt(args, 1).(string)
	return formatInteger(math.Floor(value), picture)
}

func formatInteger(value float64, picture string) (any, error) {
	format, err := analyseIntegerPicture(picture)
	if err != nil {
		return nil, err
	}
	return formatIntegerValue(value, format)
}

// widthModifier is the width modifier of a date/time variable marker.
type widthModifier struct {
	min, max       float64
	hasMin, hasMax bool
}

// pictureMarker is a literal or a variable marker of a date/time picture string.
type pictureMarker struct {
	literal       bool
	value         string
	component     string
	width         *widthModifier
	presentation1 string
	presentation2 string
	hasP2         bool
	ordinal       bool
	names         string
	integerFormat *integerFormat
	n             float64
}

var defaultPresentationModifiers = map[string]string{
	"Y": "1", "M": "1", "D": "1", "d": "1", "F": "n", "W": "1", "w": "1", "X": "1", "x": "1", "H": "1", "h": "1",
	"P": "n", "m": "01", "s": "01", "f": "1", "Z": "01:01", "z": "01:01", "C": "n", "E": "n",
}

// parseIntAuto is parseInt(string) without a radix: hexadecimal with a 0x prefix, decimal otherwise.
func parseIntAuto(s string) float64 {
	t := strings.TrimLeftFunc(s, isJSWhitespace)
	negative := false
	if t != "" && (t[0] == '+' || t[0] == '-') {
		negative = t[0] == '-'
		t = t[1:]
	}
	if len(t) >= 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
		end := 2
		for end < len(t) && isHexByte(t[end]) {
			end++
		}
		if end == 2 {
			return math.NaN()
		}
		n, _ := new(big.Int).SetString(t[2:end], 16)
		f, _ := new(big.Float).SetInt(n).Float64()
		if negative {
			f = -f
		}
		return f
	}
	if negative {
		return parseIntJS("-" + t)
	}
	return parseIntJS(t)
}

func isHexByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func indexOfUnit(units []uint16, u uint16, from int) int {
	for i := from; i < len(units); i++ {
		if i >= 0 && units[i] == u {
			return i
		}
	}
	return -1
}

// removeWhitespace is marker.split(/\s+/).join(”).
func removeWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if isJSWhitespace(r) {
			return -1
		}
		return r
	}, s)
}

// analyseDateTimePicture splits a date/time picture string into literals and variable markers.
func analyseDateTimePicture(picture string) ([]*pictureMarker, error) {
	var spec []*pictureMarker
	units := utf16Units(picture)
	addLiteral := func(start, end int) {
		if end > start {
			literal := fromUnits(units[start:end])
			// a doubled ]] is a literal ]
			literal = strings.ReplaceAll(literal, "]]", "]")
			spec = append(spec, &pictureMarker{literal: true, value: literal})
		}
	}
	charAt := func(i int) uint16 {
		if i >= 0 && i < len(units) {
			return units[i]
		}
		return 0
	}
	start, pos := 0, 0
	for pos < len(units) {
		if charAt(pos) == '[' {
			if charAt(pos+1) == '[' {
				// a literal [
				addLiteral(start, pos)
				spec = append(spec, &pictureMarker{literal: true, value: "["})
				pos += 2
				start = pos
				continue
			}
			addLiteral(start, pos)
			start = pos
			pos = indexOfUnit(units, ']', start)
			if pos == -1 {
				return nil, &Error{Code: "D3135"}
			}
			marker := utf16Units(removeWhitespace(fromUnits(units[start+1 : pos])))
			def := &pictureMarker{n: math.NaN()}
			if len(marker) > 0 {
				def.component = fromUnits(marker[:1])
			}
			var presMod []uint16
			if comma := lastIndexOfUnit(marker, ','); comma != -1 {
				widthMod := marker[comma+1:]
				parseWidth := func(wm []uint16, defined bool) (float64, bool) {
					if !defined || fromUnits(wm) == "*" {
						return 0, false
					}
					return parseIntAuto(fromUnits(wm)), true
				}
				w := &widthModifier{}
				if dash := indexOfUnit(widthMod, '-', 0); dash == -1 {
					w.min, w.hasMin = parseWidth(widthMod, true)
				} else {
					w.min, w.hasMin = parseWidth(widthMod[:dash], true)
					w.max, w.hasMax = parseWidth(widthMod[dash+1:], true)
				}
				def.width = w
				// substring(1, comma) swaps its bounds when comma is 0
				if comma >= 1 {
					presMod = marker[1:comma]
				} else {
					presMod = marker[:1]
				}
			} else if len(marker) > 1 {
				presMod = marker[1:]
			}
			hasP1 := true
			switch {
			case len(presMod) == 1:
				def.presentation1 = fromUnits(presMod)
			case len(presMod) > 1:
				lastChar := presMod[len(presMod)-1]
				if lastChar == 'a' || lastChar == 't' || lastChar == 'c' || lastChar == 'o' {
					def.presentation2, def.hasP2 = string(rune(lastChar)), true
					if lastChar == 'o' {
						def.ordinal = true
					}
					def.presentation1 = fromUnits(presMod[:len(presMod)-1])
				} else {
					def.presentation1 = fromUnits(presMod)
				}
			default:
				def.presentation1, hasP1 = defaultPresentationModifiers[def.component]
			}
			if !hasP1 {
				return nil, &Error{Code: "D3132", Value: def.component, HasValue: true}
			}
			p1 := utf16Units(def.presentation1)
			switch {
			case len(p1) > 0 && p1[0] == 'n':
				def.names = caseLower
			case len(p1) > 0 && p1[0] == 'N':
				if len(p1) > 1 && p1[1] == 'n' {
					def.names = caseTitle
				} else {
					def.names = caseUpper
				}
			case def.component != "" && strings.Contains("YMDdFWwXxHhmsf", def.component):
				integerPattern := def.presentation1
				if def.hasP2 {
					integerPattern += ";" + def.presentation2
				}
				format, err := analyseIntegerPicture(integerPattern)
				if err != nil {
					return nil, err
				}
				def.integerFormat = format
				if def.width != nil && def.width.hasMin && format.hasMandatory && format.mandatoryDigits < def.width.min {
					format.mandatoryDigits = def.width.min
				}
				if def.component == "Y" {
					def.n = -1
					if def.width != nil && def.width.hasMax {
						def.n = def.width.max
						format.mandatoryDigits, format.hasMandatory = def.n, true
					} else if format.hasMandatory && format.hasOptional {
						if w := format.mandatoryDigits + format.optionalDigits; w >= 2 {
							def.n = w
						}
					}
				}
				// an integer directly after another one needs a precise width for parsing
				if len(spec) > 0 {
					if prev := spec[len(spec)-1]; prev.integerFormat != nil {
						prev.integerFormat.parseWidth = prev.integerFormat.mandatoryDigits
						prev.integerFormat.hasParseWidth = prev.integerFormat.hasMandatory
					}
				}
			}
			if def.component == "Z" || def.component == "z" {
				format, err := analyseIntegerPicture(def.presentation1)
				if err != nil {
					return nil, err
				}
				def.integerFormat = format
			}
			spec = append(spec, def)
			start = pos + 1
		}
		pos++
	}
	addLiteral(start, pos)
	return spec, nil
}

var (
	dayNames   = []string{"", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	monthNames = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
)

const millisInADay = 1000 * 60 * 60 * 24

// JavaScript Date arithmetic (ECMAScript 21.4.1) on time values in milliseconds; NaN is an invalid date.

func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func civilFromDays(z int64) (y, m, d int64) {
	z += 719468
	era := z / 146097
	if z < 0 && z%146097 != 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// makeDay is MakeDay(year, month, date).
func makeDay(year, month, date float64) float64 {
	if !isFinite(year) || !isFinite(month) || !isFinite(date) {
		return math.NaN()
	}
	y, m, dt := math.Trunc(year), math.Trunc(month), math.Trunc(date)
	ym := y + math.Floor(m/12)
	if math.Abs(ym) > 1e8 {
		return math.NaN()
	}
	mn := math.Mod(m, 12)
	if mn < 0 {
		mn += 12
	}
	return float64(daysFromCivil(int64(ym), int64(mn)+1, 1)) + dt - 1
}

// makeTime is MakeTime(hour, min, sec, ms).
func makeTime(hour, min, sec, ms float64) float64 {
	if !isFinite(hour) || !isFinite(min) || !isFinite(sec) || !isFinite(ms) {
		return math.NaN()
	}
	return float64(math.Trunc(hour)*3600000) + float64(math.Trunc(min)*60000) + float64(math.Trunc(sec)*1000) + math.Trunc(ms)
}

// timeClip is TimeClip: an invalid date beyond ±8.64e15 ms.
func timeClip(t float64) float64 {
	if !isFinite(t) || math.Abs(t) > 8.64e15 {
		return math.NaN()
	}
	return math.Trunc(t) + 0
}

// dateUTC is Date.UTC(year, month, day, hours, minutes, seconds, ms): years 0 to 99 mean 1900 to 1999.
func dateUTC(year, month float64, rest ...float64) float64 {
	fields := []float64{1, 0, 0, 0, 0}
	copy(fields, rest)
	if !math.IsNaN(year) {
		if yi := math.Trunc(year); yi >= 0 && yi <= 99 {
			year = 1900 + yi
		}
	}
	return timeClip(float64(makeDay(year, month, fields[0])*millisInADay) + makeTime(fields[1], fields[2], fields[3], fields[4]))
}

// jsDate is a Date object's time value.
type jsDate float64

func newDate(ms float64) jsDate { return jsDate(timeClip(ms)) }

func (d jsDate) civil() (y, m, day float64) {
	t := float64(d)
	if math.IsNaN(t) {
		return math.NaN(), math.NaN(), math.NaN()
	}
	yy, mm, dd := civilFromDays(int64(math.Floor(t / millisInADay)))
	return float64(yy), float64(mm - 1), float64(dd)
}

func (d jsDate) fullYear() float64 { y, _, _ := d.civil(); return y }

func (d jsDate) month() float64 { _, m, _ := d.civil(); return m }

func (d jsDate) date() float64 { _, _, dd := d.civil(); return dd }

func posMod(a, b float64) float64 {
	m := math.Mod(a, b)
	if m < 0 {
		m += b
	}
	return m
}

func (d jsDate) day() float64 {
	t := float64(d)
	if math.IsNaN(t) {
		return t
	}
	return posMod(math.Floor(t/millisInADay)+4, 7)
}

func (d jsDate) timeOfDay() float64 { return posMod(float64(d), millisInADay) }

func (d jsDate) hours() float64 { return math.Floor(d.timeOfDay() / 3600000) }

func (d jsDate) minutes() float64 { return posMod(math.Floor(float64(d)/60000), 60) }

func (d jsDate) seconds() float64 { return posMod(math.Floor(float64(d)/1000), 60) }

func (d jsDate) milliseconds() float64 { return posMod(float64(d), 1000) }

// yearMonth is a year and a zero-based month.
type yearMonth struct{ year, month float64 }

func (ym yearMonth) nextMonth() yearMonth {
	if ym.month == 11 {
		return yearMonth{ym.year + 1, 0}
	}
	return yearMonth{ym.year, ym.month + 1}
}

func (ym yearMonth) previousMonth() yearMonth {
	if ym.month == 0 {
		return yearMonth{ym.year - 1, 11}
	}
	return yearMonth{ym.year, ym.month - 1}
}

func (ym yearMonth) nextYear() yearMonth     { return yearMonth{ym.year + 1, ym.month} }
func (ym yearMonth) previousYear() yearMonth { return yearMonth{ym.year - 1, ym.month} }

// startOfFirstWeek is the start of week 1 of the year (or month): the week with its first Thursday.
func startOfFirstWeek(ym yearMonth) float64 {
	jan1 := dateUTC(ym.year, ym.month)
	dayOfJan1 := newDate(jan1).day()
	if dayOfJan1 == 0 {
		dayOfJan1 = 7
	}
	if dayOfJan1 > 4 {
		return jan1 + (8-dayOfJan1)*millisInADay
	}
	return jan1 - (dayOfJan1-1)*millisInADay
}

func deltaWeeks(start, end float64) float64 {
	return (end-start)/(millisInADay*7) + 1
}

// getDateTimeFragment is the value of a component of a date: a number, a string or undefined.
func getDateTimeFragment(date jsDate, component string) any {
	switch component {
	case "Y":
		return date.fullYear()
	case "M":
		return date.month() + 1
	case "D":
		return date.date()
	case "d":
		today := dateUTC(date.fullYear(), date.month(), date.date())
		firstJan := dateUTC(date.fullYear(), 0)
		return (today-firstJan)/millisInADay + 1
	case "F":
		v := date.day()
		if v == 0 {
			v = 7
		}
		return v
	case "W":
		thisYear := yearMonth{date.fullYear(), 0}
		startOfWeek1 := startOfFirstWeek(thisYear)
		today := dateUTC(thisYear.year, date.month(), date.date())
		week := deltaWeeks(startOfWeek1, today)
		if week > 52 {
			if today >= startOfFirstWeek(thisYear.nextYear()) {
				week = 1
			}
		} else if week < 1 {
			week = deltaWeeks(startOfFirstWeek(thisYear.previousYear()), today)
		}
		return math.Floor(week)
	case "w":
		thisMonth := yearMonth{date.fullYear(), date.month()}
		startOfWeek1 := startOfFirstWeek(thisMonth)
		today := dateUTC(thisMonth.year, thisMonth.month, date.date())
		week := deltaWeeks(startOfWeek1, today)
		if week > 4 {
			if today >= startOfFirstWeek(thisMonth.nextMonth()) {
				week = 1
			}
		} else if week < 1 {
			week = deltaWeeks(startOfFirstWeek(thisMonth.previousMonth()), today)
		}
		return math.Floor(week)
	case "X":
		thisYear := yearMonth{date.fullYear(), 0}
		startOfISOYear := startOfFirstWeek(thisYear)
		endOfISOYear := startOfFirstWeek(thisYear.nextYear())
		now := float64(date)
		switch {
		case now < startOfISOYear:
			return thisYear.year - 1
		case now >= endOfISOYear:
			return thisYear.year + 1
		}
		return thisYear.year
	case "x":
		thisMonth := yearMonth{date.fullYear(), date.month()}
		startOfISOMonth := startOfFirstWeek(thisMonth)
		nextMonth := thisMonth.nextMonth()
		endOfISOMonth := startOfFirstWeek(nextMonth)
		now := float64(date)
		switch {
		case now < startOfISOMonth:
			return thisMonth.previousMonth().month + 1
		case now >= endOfISOMonth:
			return nextMonth.month + 1
		}
		return thisMonth.month + 1
	case "H":
		return date.hours()
	case "h":
		v := math.Mod(date.hours(), 12)
		if v == 0 {
			v = 12
		}
		return v
	case "P":
		if date.hours() >= 12 {
			return "pm"
		}
		return "am"
	case "m":
		return date.minutes()
	case "s":
		return date.seconds()
	case "f":
		return date.milliseconds()
	case "C", "E":
		return "ISO"
	}
	// Z and z: a date built from epoch millis is always UTC
	return nil
}

// nameAt is array[index] for a name table: undefined unless index is a valid position.
func nameAt(names []string, index float64) (string, bool) {
	if index != math.Trunc(index) || index < 0 || index >= float64(len(names)) {
		return "", false
	}
	return names[int(index)], true
}

// formatDateTime formats epoch millis with a picture (nil for ISO 8601) and a timezone (+hhmm or nil).
func formatDateTime(millis float64, picture, timezone any) (string, error) {
	offsetHours, offsetMinutes := 0.0, 0.0
	if tz, ok := timezone.(string); ok {
		offset := parseIntAuto(tz)
		offsetHours = math.Floor(offset / 100)
		offsetMinutes = math.Mod(offset, 100)
	}
	pictureText, ok := picture.(string)
	if !ok {
		pictureText = "[Y0001]-[M01]-[D01]T[H01]:[m01]:[s01].[f001][Z01:01t]"
	}
	spec, err := analyseDateTimePicture(pictureText)
	if err != nil {
		return "", err
	}
	offsetMillis := (60*offsetHours + offsetMinutes) * 60 * 1000
	date := newDate(millis + offsetMillis)
	var b strings.Builder
	for _, part := range spec {
		if part.literal {
			b.WriteString(part.value)
			continue
		}
		s, err := formatComponent(date, part, offsetHours, offsetMinutes)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return normalizeWTF8(b.String()), nil
}

func formatComponent(date jsDate, spec *pictureMarker, offsetHours, offsetMinutes float64) (string, error) {
	componentValue := getDateTimeFragment(date, spec.component)
	switch {
	case strings.Contains("YMDdFWwXxHhms", spec.component):
		num, _ := componentValue.(float64)
		if spec.component == "Y" && spec.n != -1 {
			num = math.Mod(num, powJS(10, spec.n))
		}
		if spec.names != "" {
			var name string
			var found bool
			switch spec.component {
			case "M", "x":
				name, found = nameAt(monthNames, num-1)
			case "F":
				name, found = nameAt(dayNames, num)
			default:
				return "", &Error{Code: "D3133", Value: spec.component, HasValue: true}
			}
			if !found {
				// the original calls a string method on undefined, or concatenates it
				if spec.names != caseTitle || spec.width != nil {
					return "", errHostRuntime
				}
				return "undefined", nil
			}
			if spec.names == caseUpper {
				name = toUpperJS(name)
			} else if spec.names == caseLower {
				name = toLowerJS(name)
			}
			if spec.width != nil && spec.width.hasMax && float64(utf16Len(name)) > spec.width.max {
				name = fromUnits(utf16Units(name)[:int(spec.width.max)])
			}
			return name, nil
		}
		return formatIntegerValue(num, spec.integerFormat)
	case spec.component == "f":
		if spec.integerFormat == nil {
			return "", errHostRuntime
		}
		num, _ := componentValue.(float64)
		return formatIntegerValue(num, spec.integerFormat)
	case spec.component == "Z" || spec.component == "z":
		offset := offsetHours*100 + offsetMinutes
		var value string
		format := spec.integerFormat
		if format.regular {
			s, err := formatIntegerValue(offset, format)
			if err != nil {
				return "", err
			}
			value = s
		} else {
			numDigits := math.NaN()
			if format.hasMandatory {
				numDigits = format.mandatoryDigits
			}
			switch numDigits {
			case 1, 2:
				s, err := formatIntegerValue(offsetHours, format)
				if err != nil {
					return "", err
				}
				value = s
				if offsetMinutes != 0 {
					m, err := formatInteger(math.Floor(offsetMinutes), "00")
					if err != nil {
						return "", err
					}
					value += ":" + m.(string)
				}
			case 3, 4:
				s, err := formatIntegerValue(offset, format)
				if err != nil {
					return "", err
				}
				value = s
			default:
				var v any
				if format.hasMandatory {
					v = numDigits
				}
				return "", &Error{Code: "D3134", Value: v, HasValue: v != nil}
			}
		}
		if offset >= 0 {
			value = "+" + value
		}
		if spec.component == "z" {
			value = "GMT" + value
		}
		if offset == 0 && spec.presentation2 == "t" {
			value = "Z"
		}
		return value, nil
	case spec.component == "P":
		s, _ := componentValue.(string)
		if spec.names == caseUpper {
			s = toUpperJS(s)
		}
		return s, nil
	}
	return toStringJS(componentValue), nil
}

// jsObjectMember stands for a property inherited from Object.prototype (a function): truthy, and
// not a number.
type jsObjectMember struct{}

var objectPrototypeMembers = map[string]bool{
	"constructor": true, "hasOwnProperty": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toLocaleString": true, "toString": true, "valueOf": true,
}

// matchPart is a part of a generated parsing regex, with the parser of what it matched.
type matchPart struct {
	regex     string
	parse     func(string) (any, error)
	component string
}

var regexMeta = regexp.MustCompile(`[.*+?^${}()|[\]\\]`)

// integerMatcher is generateRegex for an integer picture.
func integerMatcher(format *integerFormat) (*matchPart, error) {
	m := &matchPart{}
	isUpper := format.caseMode == caseUpper
	switch format.primary {
	case formatLetters:
		aChar := uint16('a')
		m.regex = "[a-z]+"
		if isUpper {
			aChar = 'A'
			m.regex = "[A-Z]+"
		}
		m.parse = func(value string) (any, error) { return lettersToDecimal(value, aChar), nil }
	case formatRoman:
		m.regex = "[mdclxvi]+"
		if isUpper {
			m.regex = "[MDCLXVI]+"
		}
		m.parse = func(value string) (any, error) {
			if !isUpper {
				value = toUpperJS(value)
			}
			return romanToDecimal(value), nil
		}
	case formatWords:
		m.regex = "(?:" + strings.Join(append(append([]string(nil), wordKeys...), "and", `[\-, ]`), "|") + ")+"
		m.parse = func(value string) (any, error) { return wordsToNumber(toLowerJS(value)), nil }
	case formatDecimal:
		m.regex = "[0-9]"
		if format.hasParseWidth && jsTruthy(format.parseWidth) {
			m.regex += "{" + formatNumberJS(format.parseWidth) + "}"
		} else {
			m.regex += "+"
		}
		if format.ordinal {
			m.regex += "(?:th|st|nd|rd)"
		}
		m.parse = func(value string) (any, error) {
			digits := utf16Units(value)
			if format.ordinal {
				// strip the suffix
				digits = digits[:clampIndex(len(digits)-2, len(digits))]
			}
			text := fromUnits(digits)
			if format.regular {
				text = strings.ReplaceAll(text, ",", "")
			} else {
				for _, sep := range format.separators {
					text = strings.Join(splitString(text, sep.character, -1), "")
				}
			}
			if format.zeroCode != 0x30 {
				var b []byte
				for _, u := range utf16Units(text) {
					cp := rune(u) - format.zeroCode + 0x30
					if cp < 0 || cp > 0x10FFFF {
						return nil, errHostRuntime
					}
					b = appendWTF8(b, cp)
				}
				text = normalizeWTF8(string(b))
			}
			return parseIntAuto(text), nil
		}
	case formatSequence:
		return nil, &Error{Code: "D3130", Value: format.token, HasValue: true}
	}
	return m, nil
}

// dateTimeMatcher is generateRegex for a date/time picture.
func dateTimeMatcher(spec []*pictureMarker) ([]*matchPart, error) {
	parts := make([]*matchPart, 0, len(spec))
	for _, part := range spec {
		res := &matchPart{}
		switch {
		case part.literal:
			res.regex = regexMeta.ReplaceAllString(part.value, `\$0`)
		case part.component == "Z" || part.component == "z":
			var separator *groupingSeparator
			if part.integerFormat.regular {
				separator = &part.integerFormat.regularSep
			}
			if part.component == "z" {
				res.regex = "GMT"
			}
			res.regex += "[-+][0-9]+"
			if separator != nil {
				res.regex += separator.character + "[0-9]+"
			}
			isGMT := part.component == "z"
			res.parse = func(value string) (any, error) {
				units := utf16Units(value)
				if isGMT {
					units = substrUnits(units, 3, 0, false)
				}
				offsetHours, offsetMinutes := 0.0, 0.0
				if separator != nil {
					sep := utf16Units(separator.character)
					idx := indexOfUnits(units, sep, 0)
					if idx < 0 {
						offsetHours = math.NaN()
						offsetMinutes = parseIntAuto(fromUnits(units))
					} else {
						offsetHours = parseIntAuto(fromUnits(units[:idx]))
						offsetMinutes = parseIntAuto(fromUnits(units[idx+1:]))
					}
				} else if len(units)-1 <= 2 {
					offsetHours = parseIntAuto(fromUnits(units))
				} else {
					offsetHours = parseIntAuto(fromUnits(units[:3]))
					offsetMinutes = parseIntAuto(fromUnits(units[3:]))
				}
				return offsetHours*60 + offsetMinutes, nil
			}
		case part.component == "f":
			res.regex = "[0-9]+"
			res.parse = func(value string) (any, error) {
				units := utf16Units(value)
				return parseFloatPrefix("0."+fromUnits(units[:clampIndex(3, len(units))])) * 1000, nil
			}
		case part.integerFormat != nil:
			m, err := integerMatcher(part.integerFormat)
			if err != nil {
				return nil, err
			}
			res = m
		default:
			// a month or day name
			res.regex = "[a-zA-Z]+"
			lookup := map[string]float64{}
			isP := false
			truncate := func(name string) string {
				if part.width != nil && part.width.hasMax && jsTruthy(part.width.max) {
					u := utf16Units(name)
					return fromUnits(u[:clampIndex(int(part.width.max), len(u))])
				}
				return name
			}
			switch part.component {
			case "M", "x":
				for i, name := range monthNames {
					lookup[truncate(name)] = float64(i + 1)
				}
			case "F":
				for i, name := range dayNames[1:] {
					lookup[truncate(name)] = float64(i + 1)
				}
			case "P":
				isP = true
				lookup = map[string]float64{"am": 0, "AM": 0, "pm": 1, "PM": 1}
			default:
				return nil, &Error{Code: "D3133", Value: part.component, HasValue: true}
			}
			res.parse = func(value string) (any, error) {
				if v, ok := lookup[value]; ok {
					return v, nil
				}
				if isP && objectPrototypeMembers[value] {
					return jsObjectMember{}, nil
				}
				return nil, nil
			}
		}
		res.component = part.component
		parts = append(parts, res)
	}
	return parts, nil
}

// fnParseInteger is $parseInteger(value, picture): the picture's parser applied to the string as it is.
func fnParseInteger(f *focus, args []any) (any, error) {
	value, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	picture, _ := argAt(args, 1).(string)
	format, err := analyseIntegerPicture(picture)
	if err != nil {
		return nil, err
	}
	m, err := integerMatcher(format)
	if err != nil {
		return nil, err
	}
	return m.parse(value)
}

// toNumberJS is ToNumber for the values the date parser handles.
func toNumberJS(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		return stringToNumber(x)
	case nullValue:
		return 0
	}
	return math.NaN()
}

// timestampOf is the evaluation's timestamp ($now) as a time value.
func timestampOf(f *focus) float64 {
	if f != nil && f.env != nil && f.env.base != nil && !f.env.base.timestamp.IsZero() {
		return float64(f.env.base.timestamp.UnixMilli())
	}
	return float64(time.Now().UnixMilli())
}

// parseDateTime parses a timestamp with a date/time picture string into epoch millis.
func parseDateTime(f *focus, timestamp, picture string) (any, error) {
	spec, err := analyseDateTimePicture(picture)
	if err != nil {
		return nil, err
	}
	parts, err := dateTimeMatcher(spec)
	if err != nil {
		return nil, err
	}
	var full strings.Builder
	full.WriteString("^")
	for _, p := range parts {
		full.WriteString("(" + p.regex + ")")
	}
	full.WriteString("$")
	matcher, err := jsre.Compile(full.String(), "i")
	if err != nil {
		return nil, errHostRuntime
	}
	units := utf16Units(timestamp)
	info, _ := matcher.Exec(units, 0)
	if info == nil {
		return nil, nil
	}
	components := map[string]any{}
	for i := 1; i < len(info)/2; i++ {
		if i-1 >= len(parts) {
			// a capturing group inside a separator: the original reads a part that does not exist
			return nil, errHostRuntime
		}
		mpart := parts[i-1]
		if mpart.parse == nil {
			continue
		}
		matched := "undefined"
		if info[2*i] >= 0 {
			matched = fromUnits(units[info[2*i]:info[2*i+1]])
		}
		v, err := mpart.parse(matched)
		if err != nil {
			return nil, err
		}
		components[mpart.component] = v
	}
	if len(components) == 0 {
		// nothing specified
		return nil, nil
	}
	// which date and time component combinations are present (bit masks, most significant first)
	maskOf := func(order string) int {
		mask := 0
		for _, c := range order {
			mask <<= 1
			if jsTruthy(components[string(c)]) {
				mask++
			}
		}
		return mask
	}
	isType := func(typ, mask int) bool { return ^typ&mask == 0 && typ&mask != 0 }
	const (
		dmA, dmB, dmC, dmD = 161, 130, 84, 72
		tmA, tmB           = 23, 47
	)
	mask := maskOf("YXMxWwdD")
	dateA := isType(dmA, mask)
	dateB := !dateA && isType(dmB, mask)
	dateC := isType(dmC, mask)
	dateD := !dateC && isType(dmD, mask)
	mask = maskOf("PHhmsf")
	timeA := isType(tmA, mask)
	timeB := !timeA && isType(tmB, mask)

	dateComps := "YMD"
	switch {
	case dateB:
		dateComps = "YD"
	case dateC:
		dateComps = "XxwF"
	case dateD:
		dateComps = "XWF"
	}
	timeComps := "Hmsf"
	if timeB {
		timeComps = "Phmsf"
	}
	now := newDate(timestampOf(f))
	startSpecified, endSpecified := false, false
	for _, c := range dateComps + timeComps {
		part := string(c)
		if v, ok := components[part]; !ok || v == nil {
			if startSpecified {
				// after the specified components: default to zero (one for months and days)
				if strings.Contains("MDd", part) {
					components[part] = 1.0
				} else {
					components[part] = 0.0
				}
				endSpecified = true
			} else {
				// before them: default to the current timestamp's component
				components[part] = getDateTimeFragment(now, part)
			}
		} else {
			startSpecified = true
			if endSpecified {
				return nil, &Error{Code: "D3136"}
			}
		}
	}
	if toNumberJS(components["M"]) > 0 {
		components["M"] = toNumberJS(components["M"]) - 1
	} else {
		components["M"] = 0.0
	}
	if dateB {
		firstJan := dateUTC(toNumberJS(components["Y"]), 0)
		offsetMillis := (toNumberJS(components["d"]) - 1) * 1000 * 60 * 60 * 24
		derived := newDate(firstJan + offsetMillis)
		components["M"] = derived.month()
		components["D"] = derived.date()
	}
	if dateC || dateD {
		// week-based dates are not supported for parsing
		return nil, &Error{Code: "D3136"}
	}
	if timeB {
		if h, ok := components["h"].(float64); ok && h == 12 {
			components["H"] = 0.0
		} else {
			components["H"] = components["h"]
		}
		if p, ok := components["P"].(float64); ok && p == 1 {
			components["H"] = toNumberJS(components["H"]) + 12
		}
	}
	millis := dateUTC(toNumberJS(components["Y"]), toNumberJS(components["M"]), toNumberJS(components["D"]),
		toNumberJS(components["H"]), toNumberJS(components["m"]), toNumberJS(components["s"]), toNumberJS(components["f"]))
	tz := components["Z"]
	if !jsTruthy(tz) {
		tz = components["z"]
	}
	if jsTruthy(tz) {
		millis -= float64(toNumberJS(tz) * 60 * 1000)
	}
	return millis, nil
}

// iso8601regex matches an ISO 8601 timestamp.
var iso8601regex = regexp.MustCompile(`^\d{4}(-[01]\d)?(-[0-3]\d)?(T[0-2]\d:[0-5]\d:[0-5]\d)?(\.\d+)?([+-][0-2]\d:?[0-5]\d|Z)?$`)

// fnToMillis is $toMillis(timestamp, picture).
func fnToMillis(f *focus, args []any) (any, error) {
	timestamp, ok := argAt(args, 0).(string)
	if !ok {
		return nil, nil
	}
	picture, ok := argAt(args, 1).(string)
	if !ok {
		if !iso8601regex.MatchString(timestamp) {
			return nil, &Error{Code: "D3110", Value: timestamp, HasValue: true}
		}
		return dateParse(timestamp), nil
	}
	return parseDateTime(f, timestamp, picture)
}

// fnFromMillis is $fromMillis(millis, picture, timezone).
func fnFromMillis(f *focus, args []any) (any, error) {
	millis, ok := argAt(args, 0).(float64)
	if !ok {
		return nil, nil
	}
	return formatDateTime(millis, argAt(args, 1), argAt(args, 2))
}

// Date.parse as V8 implements it, for the strings iso8601regex admits: the ES5 date-time string format
// first, then V8's legacy parser for whatever the ES5 parser leaves.

const dpNone = math.MinInt32

type dpTokenKind int

const (
	dpEnd dpTokenKind = iota
	dpNumber
	dpSymbol
	dpKeyword
	dpWhiteSpace
	dpUnknown
	dpInvalid
)

type dpToken struct {
	kind   dpTokenKind
	number int
	length int
	symbol uint16
	word   string
}

func (t dpToken) isNumber() bool                 { return t.kind == dpNumber }
func (t dpToken) isSymbol(c uint16) bool         { return t.kind == dpSymbol && t.symbol == c }
func (t dpToken) isAsciiSign() bool              { return t.isSymbol('+') || t.isSymbol('-') }
func (t dpToken) isFixedLengthNumber(n int) bool { return t.isNumber() && t.length == n }
func (t dpToken) isKeywordZ() bool               { return t.kind == dpKeyword && t.word == "z" }
func (t dpToken) isTimeSeparator() bool          { return t.kind == dpKeyword && t.word == "t" }

type dpScanner struct {
	in   []uint16
	pos  int
	next dpToken
}

func newDPScanner(s string) *dpScanner {
	sc := &dpScanner{in: utf16Units(s)}
	sc.next = sc.scan()
	return sc
}

func (sc *dpScanner) peek() dpToken { return sc.next }

func (sc *dpScanner) nextToken() dpToken {
	t := sc.next
	sc.next = sc.scan()
	return t
}

func (sc *dpScanner) skipSymbol(c uint16) bool {
	if sc.next.isSymbol(c) {
		sc.nextToken()
		return true
	}
	return false
}

func (sc *dpScanner) scan() dpToken {
	if sc.pos >= len(sc.in) {
		return dpToken{kind: dpEnd}
	}
	c := sc.in[sc.pos]
	switch {
	case c >= '0' && c <= '9':
		start := sc.pos
		n := 0
		for sc.pos < len(sc.in) && sc.in[sc.pos] >= '0' && sc.in[sc.pos] <= '9' {
			if n < math.MaxInt32/10-10 {
				n = n*10 + int(sc.in[sc.pos]-'0')
			}
			sc.pos++
		}
		return dpToken{kind: dpNumber, number: n, length: sc.pos - start}
	case c == ':' || c == '-' || c == '+' || c == '.' || c == ')':
		sc.pos++
		return dpToken{kind: dpSymbol, symbol: c}
	case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		start := sc.pos
		for sc.pos < len(sc.in) && (sc.in[sc.pos] >= 'a' && sc.in[sc.pos] <= 'z' || sc.in[sc.pos] >= 'A' && sc.in[sc.pos] <= 'Z') {
			sc.pos++
		}
		return dpToken{kind: dpKeyword, word: strings.ToLower(fromUnits(sc.in[start:sc.pos]))}
	case isJSWhitespace(rune(c)):
		for sc.pos < len(sc.in) && isJSWhitespace(rune(sc.in[sc.pos])) {
			sc.pos++
		}
		return dpToken{kind: dpWhiteSpace}
	}
	sc.pos++
	return dpToken{kind: dpUnknown}
}

func between(x, lo, hi int) bool { return x >= lo && x <= hi }

type dpTime struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (t *dpTime) add(n int) bool {
	if t.index < 4 {
		t.comp[t.index] = n
		t.index++
		return true
	}
	return false
}

func (t *dpTime) addFinal(n int) bool {
	if !t.add(n) {
		return false
	}
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}

func (t *dpTime) isExpecting(n int) bool {
	return (t.index == 1 && between(n, 0, 59)) || (t.index == 2 && between(n, 0, 59)) || (t.index == 3 && between(n, 0, 999))
}

func (t *dpTime) write() ([4]int, bool) {
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	h, m, s, ms := t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != dpNone {
		if !between(h, 0, 12) {
			return t.comp, false
		}
		h = h%12 + t.hourOffset
	}
	if !between(h, 0, 23) || !between(m, 0, 59) || !between(s, 0, 59) || !between(ms, 0, 999) {
		// 24:00:00.000 is allowed
		if h != 24 || m != 0 || s != 0 || ms != 0 {
			return t.comp, false
		}
	}
	return [4]int{h, m, s, ms}, true
}

type dpZone struct{ sign, hour, minute int }

func (z *dpZone) set(offsetHours int) {
	z.sign = 1
	if offsetHours < 0 {
		z.sign = -1
	}
	z.hour = absInt(offsetHours)
	z.minute = 0
}

func (z *dpZone) isExpecting(n int) bool {
	return z.hour != dpNone && z.minute == dpNone && between(n, 0, 59)
}

func (z *dpZone) isUTC() bool { return z.hour == 0 && z.minute == 0 }

func (z *dpZone) write() (float64, bool) {
	if z.sign == dpNone {
		return math.NaN(), true
	}
	if z.hour == dpNone {
		z.hour = 0
	}
	if z.minute == dpNone {
		z.minute = 0
	}
	total := float64(z.hour)*3600 + float64(z.minute)*60
	if total > 1<<30-1 {
		return 0, false
	}
	if z.sign < 0 {
		total = -total
	}
	return total, true
}

type dpDay struct {
	comp  [3]int
	index int
	iso   bool
}

func (d *dpDay) add(n int) bool {
	if d.index < 3 {
		d.comp[d.index] = n
		d.index++
		return true
	}
	return false
}

func (d *dpDay) write() (year, month, day int, ok bool) {
	if d.index < 1 {
		return 0, 0, 0, false
	}
	for d.index < 3 {
		d.comp[d.index] = 1
		d.index++
	}
	if d.iso || !between(d.comp[0], 1, 31) {
		year, month, day = d.comp[0], d.comp[1], d.comp[2]
	} else {
		month, day, year = d.comp[0], d.comp[1], d.comp[2]
	}
	if !d.iso {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	return year, month, day, between(month, 1, 12) && between(day, 1, 31)
}

// readMilliseconds keeps the three most significant digits of a fraction.
func readMilliseconds(t dpToken) int {
	number, length := t.number, t.length
	switch {
	case length == 1:
		number *= 100
	case length == 2:
		number *= 10
	case length > 3:
		if length > 9 {
			length = 9
		}
		factor := 1
		for length > 3 {
			factor *= 10
			length--
		}
		number /= factor
	}
	return number
}

// parseES5 parses the ES5 date-time string format; the token it returns is the first one it did
// not handle (end of input on success, invalid on failure).
func parseES5(sc *dpScanner, day *dpDay, tm *dpTime, tz *dpZone) dpToken {
	invalid := dpToken{kind: dpInvalid}
	switch {
	case sc.peek().isAsciiSign():
		sign := sc.nextToken()
		if !sc.peek().isFixedLengthNumber(6) {
			return sign
		}
		year := sc.nextToken().number
		if sign.symbol == '-' {
			if year == 0 {
				return sign
			}
			year = -year
		}
		day.add(year)
	case sc.peek().isFixedLengthNumber(4):
		day.add(sc.nextToken().number)
	default:
		return sc.nextToken()
	}
	if sc.skipSymbol('-') {
		if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().number, 1, 12) {
			return sc.nextToken()
		}
		day.add(sc.nextToken().number)
		if sc.skipSymbol('-') {
			if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().number, 1, 31) {
				return sc.nextToken()
			}
			day.add(sc.nextToken().number)
		}
	}
	if !sc.peek().isTimeSeparator() {
		if sc.peek().kind != dpEnd {
			return sc.nextToken()
		}
	} else {
		sc.nextToken()
		if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().number, 0, 24) {
			return invalid
		}
		hourIs24 := sc.peek().number == 24
		tm.add(sc.nextToken().number)
		if !sc.skipSymbol(':') {
			return invalid
		}
		if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().number, 0, 59) || (hourIs24 && sc.peek().number > 0) {
			return invalid
		}
		tm.add(sc.nextToken().number)
		if sc.skipSymbol(':') {
			if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().number, 0, 59) || (hourIs24 && sc.peek().number > 0) {
				return invalid
			}
			tm.add(sc.nextToken().number)
			if sc.skipSymbol('.') {
				if !sc.peek().isNumber() || (hourIs24 && sc.peek().number > 0) {
					return invalid
				}
				tm.add(readMilliseconds(sc.nextToken()))
			}
		}
		if sc.peek().isKeywordZ() {
			sc.nextToken()
			tz.set(0)
		} else if sc.peek().isAsciiSign() {
			tz.sign = 1
			if sc.nextToken().symbol == '-' {
				tz.sign = -1
			}
			if sc.peek().isFixedLengthNumber(4) {
				hourmin := sc.nextToken().number
				hour, min := hourmin/100, hourmin%100
				if !between(hour, 0, 23) || !between(min, 0, 59) {
					return invalid
				}
				tz.hour, tz.minute = hour, min
			} else {
				if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().number, 0, 23) {
					return invalid
				}
				tz.hour = sc.nextToken().number
				if !sc.skipSymbol(':') {
					return invalid
				}
				if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().number, 0, 59) {
					return invalid
				}
				tz.minute = sc.nextToken().number
			}
		}
		if sc.peek().kind != dpEnd {
			return invalid
		}
	}
	// date-only forms are UTC, date-time forms local time
	if tz.hour == dpNone && tm.index == 0 {
		tz.set(0)
	}
	day.iso = true
	return dpToken{kind: dpEnd}
}

// dateParse is Date.parse(s).
func dateParse(s string) float64 {
	sc := newDPScanner(s)
	day := &dpDay{}
	tm := &dpTime{hourOffset: dpNone}
	tz := &dpZone{sign: dpNone, hour: dpNone, minute: dpNone}
	token := parseES5(sc, day, tm, tz)
	if token.kind == dpInvalid {
		return math.NaN()
	}
	hasReadNumber := day.index > 0
	for ; token.kind != dpEnd; token = sc.nextToken() {
		switch {
		case token.isNumber():
			hasReadNumber = true
			n := token.number
			if sc.skipSymbol(':') {
				if sc.skipSymbol(':') {
					if tm.index != 0 {
						return math.NaN()
					}
					tm.add(n)
					tm.add(0)
				} else {
					if !tm.add(n) {
						return math.NaN()
					}
					if sc.peek().isSymbol('.') {
						sc.nextToken()
					}
				}
			} else if sc.skipSymbol('.') && tm.isExpecting(n) {
				tm.add(n)
				if !sc.peek().isNumber() {
					return math.NaN()
				}
				value := readMilliseconds(sc.nextToken())
				if value < 0 {
					return math.NaN()
				}
				tm.addFinal(value)
			} else if tz.isExpecting(n) {
				tz.minute = n
			} else if tm.isExpecting(n) {
				tm.addFinal(n)
				// the time must be followed by the end, white space, Z or a sign
				p := sc.peek()
				if p.kind != dpEnd && p.kind != dpWhiteSpace && !p.isKeywordZ() && !p.isAsciiSign() {
					return math.NaN()
				}
			} else {
				if !day.add(n) {
					return math.NaN()
				}
				sc.skipSymbol('-')
			}
		case token.kind == dpKeyword:
			switch {
			case (token.word == "am" || token.word == "pm") && tm.index != 0:
				tm.hourOffset = 0
				if token.word == "pm" {
					tm.hourOffset = 12
				}
			case (token.word == "z" || token.word == "ut" || token.word == "utc" || token.word == "gmt") && hasReadNumber:
				tz.set(0)
			default:
				if hasReadNumber {
					return math.NaN()
				}
				if sc.peek().isNumber() {
					return math.NaN()
				}
			}
		case token.isAsciiSign() && (tz.isUTC() || tm.index != 0):
			tz.sign = 1
			if token.symbol == '-' {
				tz.sign = -1
			}
			n, length := 0, 0
			if sc.peek().isNumber() {
				t := sc.nextToken()
				n, length = t.number, t.length
			}
			hasReadNumber = true
			switch {
			case sc.peek().isSymbol(':'):
				tz.hour, tz.minute = n, dpNone
			case length == 1 || length == 2:
				tz.hour, tz.minute = n, 0
			case length == 3 || length == 4:
				tz.hour, tz.minute = n/100, n%100
			default:
				return math.NaN()
			}
		case (token.isAsciiSign() || token.isSymbol(')')) && hasReadNumber:
			return math.NaN()
		}
	}
	year, month, dd, ok := day.write()
	if !ok {
		return math.NaN()
	}
	fields, ok := tm.write()
	if !ok {
		return math.NaN()
	}
	offset, ok := tz.write()
	if !ok {
		return math.NaN()
	}
	date := float64(makeDay(float64(year), float64(month-1), float64(dd))*millisInADay) +
		makeTime(float64(fields[0]), float64(fields[1]), float64(fields[2]), float64(fields[3]))
	if math.IsNaN(offset) {
		if math.Abs(date) > 8.64e15+864e5*10 {
			return math.NaN()
		}
		date = localToUTC(date)
	} else {
		date -= offset * 1000
		if math.Abs(date) > 8.64e15 {
			return math.NaN()
		}
	}
	return timeClip(date)
}

// localToUTC converts a local time value to UTC with the process time zone.
func localToUTC(local float64) float64 {
	d := jsDate(local)
	y, m, day := d.civil()
	t := time.Date(int(y), time.Month(int(m)+1), int(day), int(d.hours()), int(d.minutes()), int(d.seconds()),
		int(d.milliseconds())*int(time.Millisecond), time.Local)
	return float64(t.UnixMilli())
}

// powJS is Math.pow as V8 computes it: fdlibm's __ieee754_pow, which is not always correctly
// rounded (Math.pow(26, 15) is one unit in the last place below the exact value). The float64()
// conversions stop the compiler from fusing multiplies and adds, which would round differently.
func powJS(x, y float64) float64 {
	const (
		two53   = 9007199254740992.0
		huge    = 1.0e300
		tiny    = 1.0e-300
		l1      = 5.99999999999994648725e-01
		l2      = 4.28571428578550184252e-01
		l3      = 3.33333329818377432918e-01
		l4      = 2.72728123808534006489e-01
		l5      = 2.30660745775561754067e-01
		l6      = 2.06975017800338417784e-01
		p1      = 1.66666666666666019037e-01
		p2      = -2.77777777770155933842e-03
		p3      = 6.61375632143793436117e-05
		p4      = -1.65339022054652515390e-06
		p5      = 4.13813679705723846039e-08
		lg2     = 6.93147180559945286227e-01
		lg2H    = 6.93147182464599609375e-01
		lg2L    = -1.90465429995776804525e-09
		ovt     = 8.0085662595372944372e-17
		cp      = 9.61796693925975554329e-01
		cpH     = 9.61796700954437255859e-01
		cpL     = -7.02846165095275826516e-09
		ivln2   = 1.44269504088896338700e+00
		ivln2H  = 1.44269502162933349609e+00
		ivln2L  = 1.92596299112661746887e-08
		thrd    = 3.3333333333333331483e-01
		bpOne   = 1.0
		bpOneHf = 1.5
	)
	// ECMAScript: a NaN exponent gives NaN, and so does ±1 to an infinite power
	if math.IsNaN(y) || (math.Abs(x) == 1 && math.IsInf(y, 0)) {
		return math.NaN()
	}
	bp := [2]float64{bpOne, bpOneHf}
	dpH := [2]float64{0.0, 5.84962487220764160156e-01}
	dpL := [2]float64{0.0, 1.35003920212974897128e-08}
	words := func(d float64) (int32, uint32) {
		b := math.Float64bits(d)
		return int32(b >> 32), uint32(b)
	}
	setLow := func(d float64, lo uint32) float64 {
		return math.Float64frombits(math.Float64bits(d)&0xFFFFFFFF00000000 | uint64(lo))
	}
	setHigh := func(d float64, hi int32) float64 {
		return math.Float64frombits(uint64(uint32(hi))<<32 | math.Float64bits(d)&0xFFFFFFFF)
	}
	hx, lx := words(x)
	hy, ly := words(y)
	ix := hx & 0x7fffffff
	iy := hy & 0x7fffffff
	if iy == 0 && ly == 0 {
		return 1
	}
	if hx == 0x3ff00000 && lx == 0 {
		return 1
	}
	if ix > 0x7ff00000 || (ix == 0x7ff00000 && lx != 0) || iy > 0x7ff00000 || (iy == 0x7ff00000 && ly != 0) {
		return math.NaN()
	}
	yisint := int32(0)
	if hx < 0 {
		if iy >= 0x43400000 {
			yisint = 2
		} else if iy >= 0x3ff00000 {
			k := (iy >> 20) - 0x3ff
			if k > 20 {
				j := ly >> uint(52-k)
				if j<<uint(52-k) == ly {
					yisint = 2 - int32(j&1)
				}
			} else if ly == 0 {
				j := iy >> uint(20-k)
				if j<<uint(20-k) == iy {
					yisint = 2 - (j & 1)
				}
			}
		}
	}
	if ly == 0 {
		if iy == 0x7ff00000 {
			switch {
			case (ix-0x3ff00000)|int32(lx) == 0:
				return math.NaN() // (-1)**±Infinity is NaN in JavaScript
			case ix >= 0x3ff00000:
				if hy >= 0 {
					return y
				}
				return 0
			default:
				if hy < 0 {
					return -y
				}
				return 0
			}
		}
		if iy == 0x3ff00000 {
			if hy < 0 {
				return 1 / x
			}
			return x
		}
		if hy == 0x40000000 {
			return x * x
		}
		if hy == 0x3fe00000 && hx >= 0 {
			return math.Sqrt(x)
		}
	}
	ax := math.Abs(x)
	if lx == 0 && (ix == 0x7ff00000 || ix == 0 || ix == 0x3ff00000) {
		z := ax
		if hy < 0 {
			z = 1 / z
		}
		if hx < 0 {
			if (ix-0x3ff00000)|yisint == 0 {
				z = math.NaN()
			} else if yisint == 1 {
				z = -z
			}
		}
		return z
	}
	n := int32(uint32(hx)>>31) - 1
	if n|yisint == 0 {
		return math.NaN()
	}
	s := 1.0
	if n|(yisint-1) == 0 {
		s = -1
	}
	var t1, t2 float64
	if iy > 0x41e00000 {
		if iy > 0x43f00000 {
			if ix <= 0x3fefffff {
				if hy < 0 {
					return math.Inf(1)
				}
				return 0
			}
			if ix >= 0x3ff00000 {
				if hy > 0 {
					return math.Inf(1)
				}
				return 0
			}
		}
		if ix < 0x3fefffff {
			if hy < 0 {
				return s * math.Inf(1)
			}
			return s * 0
		}
		if ix > 0x3ff00000 {
			if hy > 0 {
				return s * math.Inf(1)
			}
			return s * 0
		}
		t := ax - 1
		w := float64(t*t) * (0.5 - float64(t*(thrd-float64(t*0.25))))
		u := float64(ivln2H * t)
		v := float64(t*ivln2L) - float64(w*ivln2)
		t1 = setLow(u+v, 0)
		t2 = v - (t1 - u)
	} else {
		n = 0
		if ix < 0x00100000 {
			ax *= two53
			n -= 53
			ix, _ = words(ax)
		}
		n += (ix >> 20) - 0x3ff
		j := ix & 0x000fffff
		ix = j | 0x3ff00000
		k := 0
		switch {
		case j <= 0x3988E:
			k = 0
		case j < 0xBB67A:
			k = 1
		default:
			k = 0
			n++
			ix -= 0x00100000
		}
		ax = setHigh(ax, ix)
		u := ax - bp[k]
		v := 1 / (ax + bp[k])
		ss := float64(u * v)
		sH := setLow(ss, 0)
		tH := setHigh(0, ((ix>>1)|0x20000000)+0x00080000+int32(k<<18))
		tL := ax - (tH - bp[k])
		sL := float64(v * (float64(u-float64(sH*tH)) - float64(sH*tL)))
		s2 := float64(ss * ss)
		r := float64(float64(s2*s2) * (l1 + float64(s2*(l2+float64(s2*(l3+float64(s2*(l4+float64(s2*(l5+float64(s2*l6)))))))))))
		r += float64(sL * (sH + ss))
		s2 = float64(sH * sH)
		tH = setLow(3.0+s2+r, 0)
		tL = r - ((tH - 3.0) - s2)
		u = float64(sH * tH)
		v = float64(sL*tH) + float64(tL*ss)
		pH := setLow(u+v, 0)
		pL := v - (pH - u)
		zH := float64(cpH * pH)
		zL := float64(cpL*pH) + float64(pL*cp) + dpL[k]
		t := float64(n)
		t1 = setLow(((zH+zL)+dpH[k])+t, 0)
		t2 = zL - (((t1 - t) - dpH[k]) - zH)
	}
	y1 := setLow(y, 0)
	pL := float64((y-y1)*t1) + float64(y*t2)
	pH := float64(y1 * t1)
	z := pL + pH
	j, i := words(z)
	if j >= 0x40900000 {
		if (j-0x40900000)|int32(i) != 0 {
			return s * math.Inf(1)
		}
		if pL+ovt > z-pH {
			return s * math.Inf(1)
		}
	} else if j&0x7fffffff >= 0x4090cc00 {
		if (j-int32(-0x3f6f3400))|int32(i) != 0 {
			return s * 0
		}
		if pL <= z-pH {
			return s * 0
		}
	}
	i2 := j & 0x7fffffff
	k := (i2 >> 20) - 0x3ff
	n = 0
	if i2 > 0x3fe00000 {
		n = j + (0x00100000 >> uint(k+1))
		k = ((n & 0x7fffffff) >> 20) - 0x3ff
		t := setHigh(0, n&^(0x000fffff>>uint(k)))
		n = ((n & 0x000fffff) | 0x00100000) >> uint(20-k)
		if j < 0 {
			n = -n
		}
		pH -= t
	}
	t := setLow(pL+pH, 0)
	u := float64(t * lg2H)
	v := float64((pL-(t-pH))*lg2) + float64(t*lg2L)
	z = u + v
	w := v - (z - u)
	t = float64(z * z)
	t1 = z - float64(t*(p1+float64(t*(p2+float64(t*(p3+float64(t*(p4+float64(t*p5)))))))))
	r := float64(z*t1)/(t1-2) - (w + float64(z*w))
	z = 1 - (r - z)
	j, _ = words(z)
	j += n << 20
	if j>>20 <= 0 {
		z = math.Ldexp(z, int(n))
	} else {
		z = setHigh(z, j)
	}
	return s * z
}
