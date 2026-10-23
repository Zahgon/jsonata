// JavaScript string semantics for the port: JSONata strings are sequences of UTF-16 code units. They
// are held as WTF-8, so text that is valid UTF-16 is plain UTF-8 and an unpaired surrogate keeps its
// three-byte generalised UTF-8 form.

package jsonata

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// decodeWTF8 decodes the code point at s[i], accepting encoded surrogates.
func decodeWTF8(s string, i int) (rune, int) {
	c := s[i]
	if c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
		return rune(c&0x0F)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), 3
	}
	return utf8.DecodeRuneInString(s[i:])
}

func appendWTF8(b []byte, r rune) []byte {
	if r >= 0xD800 && r <= 0xDFFF {
		return append(b, 0xED, byte(0x80|(r>>6)&0x3F), byte(0x80|r&0x3F))
	}
	return utf8.AppendRune(b, r)
}

func isHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

func isLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// utf16Units is the string's UTF-16 code units.
func utf16Units(s string) []uint16 {
	units := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		r, n := decodeWTF8(s, i)
		i += n
		if r >= 0x10000 {
			r -= 0x10000
			units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			units = append(units, uint16(r))
		}
	}
	return units
}

// fromUnits is the string of the code units; a surrogate pair becomes one code point.
func fromUnits(units []uint16) string {
	b := make([]byte, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := units[i]
		if isHighSurrogate(u) && i+1 < len(units) && isLowSurrogate(units[i+1]) {
			b = utf8.AppendRune(b, 0x10000+(rune(u)-0xD800)<<10+rune(units[i+1]-0xDC00))
			i++
			continue
		}
		b = appendWTF8(b, rune(u))
	}
	return string(b)
}

// utf16Len is the string's length property.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		r, size := decodeWTF8(s, i)
		i += size
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// hasSurrogate reports whether s holds an encoded (unpaired) surrogate.
func hasSurrogate(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == 0xED && s[i+1] >= 0xA0 {
			return true
		}
	}
	return false
}

// normalizeWTF8 joins an encoded high surrogate followed by an encoded low surrogate into one code
// point, as happens when JavaScript concatenates the two halves of a pair.
func normalizeWTF8(s string) string {
	if !hasSurrogate(s) {
		return s
	}
	return fromUnits(utf16Units(s))
}

// concatStrings is JavaScript string concatenation.
func concatStrings(parts ...string) string {
	return normalizeWTF8(strings.Join(parts, ""))
}

// compareUTF16 orders strings as JavaScript's relational operators do: by UTF-16 code units.
func compareUTF16(a, b string) int {
	if !hasSurrogate(a) && !hasSurrogate(b) {
		// Code point order equals code unit order except where a supplementary character meets a
		// character from U+E000 to U+FFFF.
		ia, ib := 0, 0
		for ia < len(a) && ib < len(b) {
			ra, na := utf8.DecodeRuneInString(a[ia:])
			rb, nb := utf8.DecodeRuneInString(b[ib:])
			if ra != rb {
				return compareRuneUTF16(ra, rb)
			}
			ia += na
			ib += nb
		}
		switch {
		case ia < len(a):
			return 1
		case ib < len(b):
			return -1
		}
		return 0
	}
	ua, ub := utf16Units(a), utf16Units(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(ua) < len(ub):
		return -1
	case len(ua) > len(ub):
		return 1
	}
	return 0
}

func compareRuneUTF16(a, b rune) int {
	lead := func(r rune) rune {
		if r >= 0x10000 {
			return 0xD800 + (r-0x10000)>>10
		}
		return r
	}
	la, lb := lead(a), lead(b)
	if la == lb {
		// both supplementary with the same lead surrogate: the trail surrogates order as the code points
		if a < b {
			return -1
		}
		return 1
	}
	if la < lb {
		return -1
	}
	return 1
}

// codePointStrings is utils.stringToArray: one string per code point (an unpaired surrogate is its own item).
func codePointStrings(s string) []string {
	out := make([]string, 0, len(s))
	for i := 0; i < len(s); {
		_, n := decodeWTF8(s, i)
		out = append(out, s[i:i+n])
		i += n
	}
	return out
}

// indexOfUnits is String.prototype.indexOf on code units.
func indexOfUnits(s, sub []uint16, from int) int {
	if from < 0 {
		from = 0
	}
	if from > len(s) {
		from = len(s)
	}
	for i := from; i+len(sub) <= len(s); i++ {
		match := true
		for j := range sub {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// indexOf is String.prototype.indexOf(sub).
func indexOf(s, sub string) int {
	if !hasSurrogate(s) && !hasSurrogate(sub) {
		i := strings.Index(s, sub)
		if i < 0 {
			return -1
		}
		return utf16Len(s[:i])
	}
	return indexOfUnits(utf16Units(s), utf16Units(sub), 0)
}

// substringUnits is String.prototype.substring(start, end) for 0 <= start <= end <= length.
func substringUnits(units []uint16, start, end int) string {
	return fromUnits(units[start:end])
}

// clampIndex clamps a code unit index into [0, n].
func clampIndex(i, n int) int {
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
}

// splitString is String.prototype.split(separator, limit) with a string separator; limit < 0 means none.
func splitString(s, sep string, limit int) []string {
	var out []string
	if limit == 0 {
		return []string{}
	}
	units := utf16Units(s)
	if sep == "" {
		for _, u := range units {
			if limit >= 0 && len(out) == limit {
				break
			}
			out = append(out, fromUnits([]uint16{u}))
		}
		if out == nil {
			out = []string{}
		}
		return out
	}
	su := utf16Units(sep)
	pos := 0
	for {
		i := indexOfUnits(units, su, pos)
		if i < 0 {
			break
		}
		out = append(out, fromUnits(units[pos:i]))
		if limit >= 0 && len(out) == limit {
			return out
		}
		pos = i + len(su)
	}
	out = append(out, fromUnits(units[pos:]))
	return out
}

// toUpperJS is String.prototype.toUpperCase.
func toUpperJS(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := decodeWTF8(s, i)
		i += n
		if r >= 0xD800 && r <= 0xDFFF {
			b.WriteString(string(appendWTF8(nil, r)))
			continue
		}
		if m, ok := upperExceptions[r]; ok {
			b.WriteString(m)
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// toLowerJS is String.prototype.toLowerCase, with the Final_Sigma rule for U+03A3.
func toLowerJS(s string) string {
	var b strings.Builder
	runes := decodeAll(s)
	for i, r := range runes {
		switch {
		case r >= 0xD800 && r <= 0xDFFF:
			b.WriteString(string(appendWTF8(nil, r)))
		case r == 0x03A3 && finalSigma(runes, i):
			b.WriteRune(0x03C2)
		default:
			if m, ok := lowerExceptions[r]; ok {
				b.WriteString(m)
			} else {
				b.WriteRune(unicode.ToLower(r))
			}
		}
	}
	return b.String()
}

func decodeAll(s string) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, n := decodeWTF8(s, i)
		out = append(out, r)
		i += n
	}
	return out
}

// finalSigma is the Final_Sigma casing context: a cased letter before (skipping case-ignorable
// characters) and none after.
func finalSigma(runes []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if inRanges(caseIgnorableRanges, runes[j]) {
			continue
		}
		before = inRanges(casedRanges, runes[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		if inRanges(caseIgnorableRanges, runes[j]) {
			continue
		}
		return !inRanges(casedRanges, runes[j])
	}
	return true
}

func inRanges(ranges [][2]rune, r rune) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i][1] >= r })
	return i < len(ranges) && ranges[i][0] <= r
}

const (
	uriUnreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	uriReserved   = ";/?:@&=+$,"
)

// encodeURI is the ECMAScript Encode operation: it fails (URIError) on an unpaired surrogate.
func encodeURI(s, unescaped string) (string, bool) {
	var b strings.Builder
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(s); {
		r, n := decodeWTF8(s, i)
		i += n
		if r < 0x80 && strings.ContainsRune(unescaped, r) {
			b.WriteRune(r)
			continue
		}
		if r >= 0xD800 && r <= 0xDFFF {
			return "", false
		}
		for _, c := range []byte(string(r)) {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String(), true
}

// decodeURI is the ECMAScript Decode operation: escapes of characters in reserved stay as they are.
func decodeURI(s, reserved string) (string, bool) {
	units := utf16Units(s)
	var out []uint16
	// hexVal reads the escape %XX at units[i]; the caller has checked that i+2 is in range.
	hexVal := func(i int) (byte, bool) {
		h, ok1 := hexDigit(units[i+1])
		l, ok2 := hexDigit(units[i+2])
		return h<<4 | l, ok1 && ok2
	}
	for k := 0; k < len(units); k++ {
		c := units[k]
		if c != '%' {
			out = append(out, c)
			continue
		}
		start := k
		if k+2 >= len(units) {
			return "", false
		}
		b, ok := hexVal(k)
		if !ok {
			return "", false
		}
		k += 2
		if b < 0x80 {
			if strings.IndexByte(reserved, b) >= 0 {
				out = append(out, units[start:k+1]...)
			} else {
				out = append(out, uint16(b))
			}
			continue
		}
		n := 0
		switch {
		case b&0xE0 == 0xC0:
			n = 2
		case b&0xF0 == 0xE0:
			n = 3
		case b&0xF8 == 0xF0:
			n = 4
		default:
			return "", false
		}
		octets := []byte{b}
		if k+3*(n-1) >= len(units) {
			return "", false
		}
		for j := 1; j < n; j++ {
			k++
			if units[k] != '%' {
				return "", false
			}
			v, ok := hexVal(k)
			if !ok || v&0xC0 != 0x80 {
				return "", false
			}
			k += 2
			octets = append(octets, v)
		}
		r, size := utf8.DecodeRune(octets)
		if r == utf8.RuneError || size != n || r < 0x80 {
			return "", false
		}
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return fromUnits(out), true
}

func hexDigit(u uint16) (byte, bool) {
	switch {
	case u >= '0' && u <= '9':
		return byte(u - '0'), true
	case u >= 'a' && u <= 'f':
		return byte(u-'a') + 10, true
	case u >= 'A' && u <= 'F':
		return byte(u-'A') + 10, true
	}
	return 0, false
}

const base64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// base64EncodeBinary is Buffer.from(str, 'binary').toString('base64'): each code unit contributes its low byte.
func base64EncodeBinary(s string) string {
	units := utf16Units(s)
	data := make([]byte, len(units))
	for i, u := range units {
		data[i] = byte(u)
	}
	var b strings.Builder
	for i := 0; i < len(data); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], data[i:])
		v := uint(chunk[0])<<16 | uint(chunk[1])<<8 | uint(chunk[2])
		b.WriteByte(base64Alphabet[v>>18&63])
		b.WriteByte(base64Alphabet[v>>12&63])
		if n > 1 {
			b.WriteByte(base64Alphabet[v>>6&63])
		} else {
			b.WriteByte('=')
		}
		if n > 2 {
			b.WriteByte(base64Alphabet[v&63])
		} else {
			b.WriteByte('=')
		}
	}
	return b.String()
}

// base64DecodeBinary is Buffer.from(str, 'base64').toString('binary'): Node's lenient decoder, which
// accepts the URL-safe alphabet too, skips characters outside the alphabet and stops at the first '='.
func base64DecodeBinary(s string) string {
	var acc uint
	bits := 0
	var out []uint16
	for _, u := range utf16Units(s) {
		var v int
		switch {
		case u >= 'A' && u <= 'Z':
			v = int(u - 'A')
		case u >= 'a' && u <= 'z':
			v = int(u-'a') + 26
		case u >= '0' && u <= '9':
			v = int(u-'0') + 52
		case u == '+' || u == '-':
			v = 62
		case u == '/' || u == '_':
			v = 63
		case u == '=':
			return fromUnits(out)
		default:
			continue
		}
		acc = acc<<6 | uint(v)
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, uint16(byte(acc>>bits)))
		}
	}
	return fromUnits(out)
}
