// JSON.parse and JSON.stringify as JavaScript defines them, over the port's value model.

package jsonata

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// replacerFunc is the replacer argument of JSON.stringify: it maps each value (the root included)
// before it is written. It returns undefined (nil) to leave a member out.
type replacerFunc func(v any) (any, error)

type jsonWriter struct {
	b        strings.Builder
	indent   string
	replacer replacerFunc
	stack    map[any]bool // the containers being written, to detect a circular structure
}

// errCircular is the TypeError JSON.stringify raises for a circular structure.
var errCircular = errors.New("converting circular structure to JSON")

// stringifyJSON is JSON.stringify(value, replacer, indent). The second result is false when the
// result is undefined.
func stringifyJSON(v any, replacer replacerFunc, indent int) (string, bool, error) {
	w := &jsonWriter{indent: strings.Repeat(" ", indent), replacer: replacer, stack: map[any]bool{}}
	ok, err := w.value(v, 0)
	if err != nil || !ok {
		return "", false, err
	}
	return w.b.String(), true, nil
}

// serializable maps a value through the replacer and reports whether it is written at all
// (undefined and functions are not).
func (w *jsonWriter) serializable(v any) (any, bool, error) {
	if w.replacer != nil {
		var err error
		if v, err = w.replacer(v); err != nil {
			return nil, false, err
		}
	}
	switch v.(type) {
	case nil, *nativeFunc:
		return nil, false, nil
	}
	return v, true, nil
}

func (w *jsonWriter) newline(depth int) {
	if w.indent != "" {
		w.b.WriteByte('\n')
		for i := 0; i < depth; i++ {
			w.b.WriteString(w.indent)
		}
	}
}

func (w *jsonWriter) value(v any, depth int) (bool, error) {
	v, ok, err := w.serializable(v)
	if err != nil || !ok {
		return false, err
	}
	switch v.(type) {
	case *Array, *Object, *Node, *Error, *Function:
		if w.stack[v] {
			return false, errCircular
		}
		w.stack[v] = true
		defer delete(w.stack, v)
	}
	switch x := v.(type) {
	case nullValue:
		w.b.WriteString("null")
	case bool:
		w.b.WriteString(strconv.FormatBool(x))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			w.b.WriteString("null")
		} else {
			w.b.WriteString(formatNumberJS(x))
		}
	case string:
		writeJSONString(&w.b, x)
	case *Array:
		if len(x.items) == 0 {
			w.b.WriteString("[]")
			return true, nil
		}
		w.b.WriteByte('[')
		for i, item := range x.items {
			if i > 0 {
				w.b.WriteByte(',')
			}
			w.newline(depth + 1)
			written, err := w.value(item, depth+1)
			if err != nil {
				return false, err
			}
			if !written {
				w.b.WriteString("null")
			}
		}
		w.newline(depth)
		w.b.WriteByte(']')
	default:
		return true, w.object(objectView(v), depth)
	}
	return true, nil
}

// field is one own enumerable property of an object being written.
type field struct {
	key   string
	value any
}

// objectView lists the own enumerable properties JSON.stringify sees on an object-like value.
func objectView(v any) []field {
	switch x := v.(type) {
	case *Object:
		keys := x.Keys()
		out := make([]field, len(keys))
		for i, k := range keys {
			out[i] = field{k, x.vals[k]}
		}
		return out
	case tuple:
		var out []field
		for _, k := range x.keys() {
			out = append(out, field{k, x[k]})
		}
		return out
	case *Function:
		return x.jsonFields()
	case *Lambda:
		return x.jsonFields()
	case *Node:
		return x.jsonFields()
	case *Error:
		return x.jsonFields()
	}
	return nil
}

func (w *jsonWriter) object(fields []field, depth int) error {
	start := w.b.Len()
	w.b.WriteByte('{')
	wrote := false
	for _, f := range fields {
		mark := w.b.Len()
		if wrote {
			w.b.WriteByte(',')
		}
		w.newline(depth + 1)
		writeJSONString(&w.b, f.key)
		w.b.WriteByte(':')
		if w.indent != "" {
			w.b.WriteByte(' ')
		}
		written, err := w.value(f.value, depth+1)
		if err != nil {
			return err
		}
		if !written {
			s := w.b.String()[:mark]
			w.b.Reset()
			w.b.WriteString(s)
			continue
		}
		wrote = true
	}
	if !wrote {
		s := w.b.String()[:start]
		w.b.Reset()
		w.b.WriteString(s)
		w.b.WriteString("{}")
		return nil
	}
	w.newline(depth)
	w.b.WriteByte('}')
	return nil
}

// writeJSONString writes the JSON form of a string as JSON.stringify does: control characters and
// unpaired surrogates as \u escapes, everything else unescaped.
func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, n := decodeWTF8(s, i)
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || (r >= 0xD800 && r <= 0xDFFF):
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	b.WriteByte('"')
}

// jsonString is JSON.stringify(s) for a string.
func jsonString(s string) string {
	var b strings.Builder
	writeJSONString(&b, s)
	return b.String()
}

// errJSONSyntax reports input that JSON.parse rejects.
var errJSONSyntax = errors.New("invalid JSON")

type jsonParser struct {
	s   string
	pos int
}

// parseJSON is JSON.parse(text) into the value model.
func parseJSON(text string) (any, error) {
	p := &jsonParser{s: text}
	p.space()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.s) {
		return nil, errJSONSyntax
	}
	return v, nil
}

func (p *jsonParser) space() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) literal(word string, v any) (any, error) {
	if !strings.HasPrefix(p.s[p.pos:], word) {
		return nil, errJSONSyntax
	}
	p.pos += len(word)
	return v, nil
}

func (p *jsonParser) value() (any, error) {
	if p.pos >= len(p.s) {
		return nil, errJSONSyntax
	}
	switch c := p.s[p.pos]; {
	case c == '{':
		p.pos++
		obj := NewObject()
		p.space()
		if p.pos < len(p.s) && p.s[p.pos] == '}' {
			p.pos++
			return obj, nil
		}
		for {
			p.space()
			if p.pos >= len(p.s) || p.s[p.pos] != '"' {
				return nil, errJSONSyntax
			}
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			p.space()
			if p.pos >= len(p.s) || p.s[p.pos] != ':' {
				return nil, errJSONSyntax
			}
			p.pos++
			p.space()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			obj.Set(key, v)
			p.space()
			if p.pos >= len(p.s) {
				return nil, errJSONSyntax
			}
			if p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.s[p.pos] == '}' {
				p.pos++
				return obj, nil
			}
			return nil, errJSONSyntax
		}
	case c == '[':
		p.pos++
		arr := newArray()
		p.space()
		if p.pos < len(p.s) && p.s[p.pos] == ']' {
			p.pos++
			return arr, nil
		}
		for {
			p.space()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			arr.push(v)
			p.space()
			if p.pos >= len(p.s) {
				return nil, errJSONSyntax
			}
			if p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.s[p.pos] == ']' {
				p.pos++
				return arr, nil
			}
			return nil, errJSONSyntax
		}
	case c == '"':
		return p.str()
	case c == 't':
		return p.literal("true", true)
	case c == 'f':
		return p.literal("false", false)
	case c == 'n':
		return p.literal("null", Null)
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, errJSONSyntax
}

func (p *jsonParser) number() (any, error) {
	start := p.pos
	if p.s[p.pos] == '-' {
		p.pos++
	}
	digits := func() int {
		n := 0
		for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			p.pos++
			n++
		}
		return n
	}
	if p.pos < len(p.s) && p.s[p.pos] == '0' {
		p.pos++
	} else if digits() == 0 {
		return nil, errJSONSyntax
	}
	if p.pos < len(p.s) && p.s[p.pos] == '.' {
		p.pos++
		if digits() == 0 {
			return nil, errJSONSyntax
		}
	}
	if p.pos < len(p.s) && (p.s[p.pos] == 'e' || p.s[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.s) && (p.s[p.pos] == '+' || p.s[p.pos] == '-') {
			p.pos++
		}
		if digits() == 0 {
			return nil, errJSONSyntax
		}
	}
	f, err := strconv.ParseFloat(p.s[start:p.pos], 64)
	if err != nil && !isRangeError(err) {
		return nil, errJSONSyntax
	}
	return f, nil
}

func (p *jsonParser) str() (string, error) {
	p.pos++ // opening quote
	var units []uint16
	plain := true
	start := p.pos
	for {
		if p.pos >= len(p.s) {
			return "", errJSONSyntax
		}
		c := p.s[p.pos]
		switch {
		case c == '"':
			p.pos++
			if plain {
				return p.s[start : p.pos-1], nil
			}
			return fromUnits(units), nil
		case c < 0x20:
			return "", errJSONSyntax
		case c == '\\':
			if plain {
				units = utf16Units(p.s[start:p.pos])
				plain = false
			}
			p.pos++
			if p.pos >= len(p.s) {
				return "", errJSONSyntax
			}
			e := p.s[p.pos]
			p.pos++
			switch e {
			case '"', '\\', '/':
				units = append(units, uint16(e))
			case 'b':
				units = append(units, '\b')
			case 'f':
				units = append(units, '\f')
			case 'n':
				units = append(units, '\n')
			case 'r':
				units = append(units, '\r')
			case 't':
				units = append(units, '\t')
			case 'u':
				if p.pos+4 > len(p.s) {
					return "", errJSONSyntax
				}
				v, err := strconv.ParseUint(p.s[p.pos:p.pos+4], 16, 16)
				if err != nil || strings.ContainsAny(p.s[p.pos:p.pos+4], "+-") {
					return "", errJSONSyntax
				}
				units = append(units, uint16(v))
				p.pos += 4
			default:
				return "", errJSONSyntax
			}
		default:
			r, n := decodeWTF8(p.s, p.pos)
			if !plain {
				if r >= 0x10000 {
					r -= 0x10000
					units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
				} else {
					units = append(units, uint16(r))
				}
			}
			p.pos += n
		}
	}
}
