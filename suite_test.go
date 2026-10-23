/**
 * © Copyright IBM Corp. 2016 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of test/run-test-suite.js: runs the language-neutral test suite in test/test-suite.

package jsonata

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const suiteDir = "test/test-suite"

func readJSONFile(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	v, err := ParseJSON(string(data))
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return v
}

// canonical writes a value as JSON with object keys sorted, so that results compare as deep equality.
func canonical(v any) string {
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case *Array:
			b.WriteByte('[')
			for i, item := range x.items {
				if i > 0 {
					b.WriteByte(',')
				}
				walk(item)
			}
			b.WriteByte(']')
		case *Object:
			keys := x.Keys()
			sort.Strings(keys)
			b.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					b.WriteByte(',')
				}
				writeJSONString(&b, k)
				b.WriteByte(':')
				walk(x.vals[k])
			}
			b.WriteByte('}')
		default:
			s, ok, _ := stringifyJSON(v, functionsAsEmpty, 0)
			if !ok {
				s = "undefined"
			}
			b.WriteString(s)
		}
	}
	walk(v)
	return b.String()
}

func member(o *Object, key string) (any, bool) {
	return o.Get(key)
}

func TestSuite(t *testing.T) {
	datasets := map[string]any{}
	entries, err := os.ReadDir(filepath.Join(suiteDir, "datasets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		datasets[strings.TrimSuffix(e.Name(), ".json")] = readJSONFile(t, filepath.Join(suiteDir, "datasets", e.Name()))
	}
	groups, err := os.ReadDir(filepath.Join(suiteDir, "groups"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if !g.IsDir() {
			continue
		}
		dir := filepath.Join(suiteDir, "groups", g.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			spec := readJSONFile(t, filepath.Join(dir, f.Name()))
			cases := []any{spec}
			if arr, ok := spec.(*Array); ok {
				cases = arr.items
			}
			for i, c := range cases {
				tc := c.(*Object)
				name := g.Name() + "/" + strings.TrimSuffix(f.Name(), ".json")
				if len(cases) > 1 {
					name += "/" + string(rune('a'+i%26))
				}
				t.Run(name, func(t *testing.T) { runSuiteCase(t, dir, tc, datasets) })
			}
		}
	}
}

func runSuiteCase(t *testing.T, dir string, tc *Object, datasets map[string]any) {
	exprValue, _ := member(tc, "expr")
	expr, _ := exprValue.(string)
	if file, ok := member(tc, "expr-file"); ok {
		data, err := os.ReadFile(filepath.Join(dir, file.(string)))
		if err != nil {
			t.Fatal(err)
		}
		expr = string(data)
	}
	code, hasCode := member(tc, "code")
	opts := &Options{}
	if limit, ok := member(tc, "timelimit"); ok {
		opts.Timeout = time.Duration(limit.(float64)) * time.Millisecond
	}
	if depth, ok := member(tc, "depth"); ok {
		opts.Stack = int(depth.(float64))
	}
	compiled, err := New(expr, opts)
	if err != nil {
		if !hasCode {
			t.Fatalf("compiling %q: unexpected error %v", expr, err)
		}
		checkErrorCode(t, expr, err, code.(string), tc)
		return
	}
	var input any
	if data, ok := member(tc, "data"); ok {
		input = data
	} else if ds, ok := member(tc, "dataset"); ok && ds != Null {
		input, ok = datasets[ds.(string)]
		if !ok {
			t.Fatalf("unknown dataset %v", ds)
		}
	}
	bindings := map[string]any{}
	if b, ok := member(tc, "bindings"); ok {
		if obj, ok := b.(*Object); ok {
			for _, k := range obj.Keys() {
				bindings[k] = obj.vals[k]
			}
		}
	}
	result, err := compiled.Evaluate(input, bindings)
	if _, ok := member(tc, "undefinedResult"); ok {
		if err != nil || result != nil {
			t.Fatalf("%q: want undefined, got %s (error %v)", expr, canonical(result), err)
		}
		return
	}
	if want, ok := member(tc, "result"); ok {
		if err != nil {
			t.Fatalf("%q: want %s, got error %v", expr, canonical(want), err)
		}
		if got := canonical(result); got != canonical(want) {
			t.Fatalf("%q: want %s, got %s", expr, canonical(want), got)
		}
		return
	}
	if want, ok := member(tc, "error"); ok {
		if err == nil {
			t.Fatalf("%q: want error %s, got %s", expr, canonical(want), canonical(result))
		}
		je, isJSONata := err.(*Error)
		if !isJSONata {
			t.Fatalf("%q: want error %s, got %v", expr, canonical(want), err)
		}
		fields := NewObject()
		for _, f := range je.jsonFields() {
			fields.Set(f.key, f.value)
		}
		fields.Set("message", je.Message)
		if je.FunctionName != "" {
			fields.Set("functionName", je.FunctionName)
		}
		for _, k := range want.(*Object).Keys() {
			got, _ := fields.Get(k)
			if canonical(got) != canonical(want.(*Object).vals[k]) {
				t.Fatalf("%q: error member %s: want %s, got %s", expr, k, canonical(want.(*Object).vals[k]), canonical(got))
			}
		}
		return
	}
	if !hasCode {
		t.Fatalf("%q: nothing to test in this case", expr)
	}
	if err == nil {
		t.Fatalf("%q: want error %v, got %s", expr, code, canonical(result))
	}
	checkErrorCode(t, expr, err, code.(string), tc)
}

// checkErrorCode checks the code (and token, when given) of an error. U1001 is the code the original
// runner gives its own stack and time limits, which the Stack and Timeout options report as D1011 and D1012.
func checkErrorCode(t *testing.T, expr string, err error, code string, tc *Object) {
	t.Helper()
	je, ok := err.(*Error)
	if !ok {
		t.Fatalf("%q: want error %s, got %v", expr, code, err)
	}
	if code == "U1001" && (je.Code == "D1011" || je.Code == "D1012") {
		return
	}
	if je.Code != code {
		t.Fatalf("%q: want error %s, got %s (%s)", expr, code, je.Code, je.Message)
	}
	if token, ok := member(tc, "token"); ok && canonical(je.Token) != canonical(token) {
		t.Fatalf("%q: want token %s, got %s", expr, canonical(token), canonical(je.Token))
	}
}
