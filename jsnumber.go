// JavaScript number semantics for the port: the conversions between numbers and strings that the
// original gets from Number.prototype methods, parseFloat, parseInt and Math.

package jsonata

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// formatNumberJS is Number.prototype.toString(): the shortest round-trip digits, in exponential form
// below 1e-6 and from 1e21.
func formatNumberJS(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	a := math.Abs(f)
	if a >= 1e-6 && a < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(s, "e")
	digits := strings.TrimLeft(exp[1:], "0")
	if digits == "" {
		digits = "0"
	}
	return mant + "e" + exp[:1] + digits
}

// pow10Rat is 10^n as a rational number (n may be negative).
func pow10Rat(n int) *big.Rat {
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(n))), nil)
	if n >= 0 {
		return new(big.Rat).SetInt(p)
	}
	return new(big.Rat).SetFrac(big.NewInt(1), p)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// roundHalfUp is floor(r + 1/2) for a non-negative rational.
func roundHalfUp(r *big.Rat) *big.Int {
	half := new(big.Rat).Add(r, big.NewRat(1, 2))
	return new(big.Int).Quo(half.Num(), half.Denom())
}

// toFixed is Number.prototype.toFixed(digits).
func toFixed(x float64, digits int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	if math.Abs(x) >= 1e21 || math.IsInf(x, 0) {
		return formatNumberJS(x)
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	r := new(big.Rat).SetFloat64(x)
	n := roundHalfUp(r.Mul(r, pow10Rat(digits)))
	m := n.String()
	if digits != 0 {
		k := len(m)
		if k <= digits {
			m = strings.Repeat("0", digits+1-k) + m
			k = digits + 1
		}
		m = m[:k-digits] + "." + m[k-digits:]
	}
	return sign + m
}

// toPrecision is Number.prototype.toPrecision(precision) for 1 <= precision <= 100.
func toPrecision(x float64, precision int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	if math.IsInf(x, 0) {
		return sign + "Infinity"
	}
	var m string
	e := 0
	if x == 0 {
		m = strings.Repeat("0", precision)
	} else {
		r := new(big.Rat).SetFloat64(x)
		e = int(math.Floor(math.Log10(x)))
		lower := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision-1)), nil)
		upper := new(big.Int).Mul(lower, big.NewInt(10))
		var n *big.Int
		for {
			scaled := new(big.Rat).Mul(r, pow10Rat(precision-1-e))
			n = roundHalfUp(scaled)
			if n.Cmp(upper) >= 0 {
				// either e is too small, or rounding carried into a new digit
				if new(big.Rat).SetInt(upper).Cmp(scaled) <= 0 {
					e++
					continue
				}
				n = lower
				e++
				break
			}
			if n.Cmp(lower) < 0 {
				e--
				continue
			}
			break
		}
		m = n.String()
	}
	if e < -6 || e >= precision {
		a, b := m[:1], m[1:]
		if precision != 1 {
			a += "." + b
		}
		c := "+"
		if e < 0 {
			c = "-"
		}
		return sign + a + "e" + c + strconv.Itoa(absInt(e))
	}
	if e == precision-1 {
		return sign + m
	}
	if e >= 0 {
		return sign + m[:e+1] + "." + m[e+1:]
	}
	return sign + "0." + strings.Repeat("0", -(e+1)) + m
}

// toPrecision15 is Number(x.toPrecision(15)).
func toPrecision15(x float64) float64 {
	f, _ := strconv.ParseFloat(toPrecision(x, 15), 64)
	return f
}

// toStringRadix is Number.prototype.toString(radix) for an integral value and a radix from 2 to 36,
// following V8's digit generation (DoubleToRadixCString), which fills the digits beyond double
// precision with zeros.
func toStringRadix(value float64, radix int) string {
	if radix == 10 {
		return formatNumberJS(value)
	}
	if math.IsNaN(value) {
		return "NaN"
	}
	if math.IsInf(value, 0) {
		if value < 0 {
			return "-Infinity"
		}
		return "Infinity"
	}
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	negative := value < 0
	if negative {
		value = -value
	}
	integer := math.Floor(value)
	var digits []byte
	r := float64(radix)
	for exponentOf(integer/r) > 0 {
		integer /= r
		digits = append(digits, '0')
	}
	for {
		remainder := math.Mod(integer, r)
		digits = append(digits, chars[int(remainder)])
		integer = (integer - remainder) / r
		if integer <= 0 {
			break
		}
	}
	if negative {
		digits = append(digits, '-')
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// exponentOf is V8's Double::Exponent(): the binary exponent of the value with its significand as a
// 53-bit integer.
func exponentOf(d float64) int {
	bits := math.Float64bits(d)
	biased := int(bits >> 52 & 0x7FF)
	if biased == 0 {
		return -1074
	}
	return biased - 0x3FF - 52
}

// isJSWhitespace is the WhiteSpace and LineTerminator set of ECMAScript.
func isJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// stringToNumber is Number(string): decimal literals (with Infinity), and 0x, 0o and 0b integers.
func stringToNumber(s string) float64 {
	s = strings.TrimFunc(s, isJSWhitespace)
	if s == "" {
		return 0
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			n, ok := new(big.Int).SetString(s[2:], base)
			if !ok || strings.ContainsAny(s[2:], "_+-") {
				return math.NaN()
			}
			f, _ := new(big.Float).SetInt(n).Float64()
			return f
		}
	}
	body := s
	if body[0] == '+' || body[0] == '-' {
		body = body[1:]
	}
	if body == "Infinity" {
		if s[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	if !isDecimalLiteral(body) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !isRangeError(err) {
		return math.NaN()
	}
	return f
}

func isRangeError(err error) bool {
	ne, ok := err.(*strconv.NumError)
	return ok && ne.Err == strconv.ErrRange
}

// isDecimalLiteral matches StrUnsignedDecimalLiteral without Infinity: digits with an optional
// fraction (either side of the point may be empty, not both) and an optional exponent.
func isDecimalLiteral(s string) bool {
	i := 0
	intDigits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		intDigits++
	}
	fracDigits := 0
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			fracDigits++
		}
	}
	if intDigits+fracDigits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		expDigits := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			expDigits++
		}
		if expDigits == 0 {
			return false
		}
	}
	return i == len(s)
}

// parseFloatPrefix is parseFloat(string): the longest decimal-literal prefix after leading white space.
func parseFloatPrefix(s string) float64 {
	s = strings.TrimLeftFunc(s, isJSWhitespace)
	sign := ""
	body := s
	if body != "" && (body[0] == '+' || body[0] == '-') {
		sign, body = body[:1], body[1:]
	}
	if strings.HasPrefix(body, "Infinity") {
		if sign == "-" {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	end := 0
	for j := len(body); j > 0; j-- {
		if isDecimalLiteral(body[:j]) {
			end = j
			break
		}
	}
	if end == 0 {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(sign+body[:end], 64)
	if err != nil && !isRangeError(err) {
		return math.NaN()
	}
	return f
}

// parseIntJS is parseInt(string) with radix 10.
func parseIntJS(s string) float64 {
	s = strings.TrimLeftFunc(s, isJSWhitespace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	f, _ := strconv.ParseFloat(s[:end], 64)
	if negative {
		f = -f
	}
	return f
}

// mathRound is Math.round: halves round up, towards +Infinity.
func mathRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	if x < 0 && x >= -0.5 {
		return math.Copysign(0, -1)
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		f++
	}
	return f
}

// mathPow is Math.pow (the ** operator).
func mathPow(x, y float64) float64 {
	if math.IsNaN(y) {
		return math.NaN()
	}
	if y == 0 {
		return 1
	}
	if (x == 1 || x == -1) && math.IsInf(y, 0) {
		return math.NaN()
	}
	return powJS(x, y)
}

// toUint32 is the ToUint32 conversion.
func toUint32(f float64) uint32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	t := math.Trunc(f)
	m := math.Mod(t, 4294967296)
	if m < 0 {
		m += 4294967296
	}
	return uint32(m)
}

// isInteger is Number.isInteger.
func isInteger(v any) bool {
	f, ok := v.(float64)
	return ok && !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f)
}
