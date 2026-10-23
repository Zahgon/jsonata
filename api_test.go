/**
 * © Copyright IBM Corp. 2016 All Rights Reserved
 *   Project name: JSONata
 *   This project is licensed under the MIT License, see LICENSE
 */

// Go port of the API-level tests of test/implementation-tests.js and test/parser-recovery.js: the
// tests that exercise the library interface rather than the language.

package jsonata

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testdata1 = `{"foo": {"bar": 42, "blah": [{"baz": {"fud": "hello"}}, {"baz": {"fud": "world"}}, {"bazz": "gotcha"}],
 "blah.baz": "here"}, "bar": 98}`

const testdata2 = `{"Account": {"Account Name": "Firefly", "Order": [
 {"OrderID": "order103", "Product": [{"Product Name": "Bowler Hat", "ProductID": 858383, "Price": 34.45, "Quantity": 2}]},
 {"OrderID": "order104", "Product": [{"Product Name": "Trilby hat", "ProductID": 858236, "Price": 21.67, "Quantity": 1}]}]}}`

func mustParse(t *testing.T, text string) any {
	t.Helper()
	v, err := ParseJSON(text)
	if err != nil {
		t.Fatalf("parsing %s: %v", text, err)
	}
	return v
}

// evalExpr compiles and evaluates an expression and returns the canonical JSON of the result.
func evalExpr(t *testing.T, expr string, input any, setup func(e *Expression)) (string, error) {
	t.Helper()
	compiled, err := New(expr, nil)
	if err != nil {
		return "", err
	}
	if setup != nil {
		setup(compiled)
	}
	result, err := compiled.Evaluate(input, nil)
	if err != nil {
		return "", err
	}
	return canonical(result), nil
}

func expectResult(t *testing.T, expr string, input any, setup func(e *Expression), want string) {
	t.Helper()
	got, err := evalExpr(t, expr, input, setup)
	if err != nil {
		t.Fatalf("%s: unexpected error %v", expr, err)
	}
	if wantCanonical := canonical(mustParse(t, want)); got != wantCanonical {
		t.Fatalf("%s: want %s, got %s", expr, wantCanonical, got)
	}
}

func expectCode(t *testing.T, expr string, input any, setup func(e *Expression), code string) {
	t.Helper()
	_, err := evalExpr(t, expr, input, setup)
	var je *Error
	if !errors.As(err, &je) || je.Code != code {
		t.Fatalf("%s: want error %s, got %v", expr, code, err)
	}
}

func TestObjectTraversalOrder(t *testing.T) {
	expectResult(t, "foo.*[0]", mustParse(t, testdata1), nil, "42")
	expectResult(t, "**[2]", mustParse(t, testdata2), nil, `"Firefly"`)
}

func TestClone(t *testing.T) {
	data := mustParse(t, testdata2)
	got, err := evalExpr(t, "$clone(foo)", data, nil)
	if err != nil || got != "undefined" {
		t.Fatalf("$clone(foo): want undefined, got %s (%v)", got, err)
	}
	expectResult(t, "$clone({})", data, nil, "{}")
	expectResult(t, `$clone({"a": 1})`, data, nil, `{"a": 1}`)
	count := 0
	expectResult(t, `Account ~> |Order|{"Product":"blah"},nomatch|`, data, func(e *Expression) {
		if err := e.RegisterFunction("clone", func(args ...any) (any, error) {
			count++
			s, _, err := Stringify(args[0])
			if err != nil {
				return nil, err
			}
			return ParseJSON(s)
		}, ""); err != nil {
			t.Fatal(err)
		}
	}, `{"Account Name": "Firefly", "Order": [{"OrderID": "order103", "Product": "blah"}, {"OrderID": "order104", "Product": "blah"}]}`)
	if count != 1 {
		t.Fatalf("overridden $clone called %d times, want 1", count)
	}
	expectCode(t, `( $clone := 5; $ ~> |Account.Order.Product|{"blah":"foo"}| )`, data, nil, "T2013")
}

func TestTimestamps(t *testing.T) {
	expectResult(t, "$millis() = $millis()", nil, nil, "true")
	expectResult(t, "$now() = $now()", nil, nil, "true")
	expectResult(t, "$type($now())", nil, nil, `"string"`)
	expectResult(t, "$now('[Y0001]') = $fromMillis($millis(), '[Y0001]')", nil, nil, "true")
	expectResult(t, "$now('[H01]', '+0100') = $fromMillis($millis(), '[H01]', '+0100')", nil, nil, "true")
	compiled, err := New("$millis()", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := compiled.Evaluate(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	second, err := compiled.Evaluate(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.(float64) >= second.(float64) {
		t.Fatalf("$millis() did not advance between evaluations: %v then %v", first, second)
	}
	if now := float64(time.Now().UnixMilli()); now-second.(float64) > 60000 {
		t.Fatalf("$millis() = %v is not the current time %v", second, now)
	}
}

func TestRegisteredFunctions(t *testing.T) {
	squareRoot := func(e *Expression) {
		if err := e.RegisterFunction("squareroot", func(args ...any) (any, error) {
			n := args[0].(float64)
			r := n
			for i := 0; i < 50; i++ {
				r = (r + n/r) / 2
			}
			return r, nil
		}, "<n:n>"); err != nil {
			t.Fatal(err)
		}
	}
	expectResult(t, "$map([1,4,9,16], $squareroot)", nil, squareRoot, "[1,2,3,4]")
	expectCode(t, `$squareroot("a")`, nil, squareRoot, "T0410")
	even := func(e *Expression) {
		if err := e.RegisterFunction("even", func(args ...any) (any, error) {
			return int(args[0].(float64))%2 == 0, nil
		}, ""); err != nil {
			t.Fatal(err)
		}
	}
	expectResult(t, "$filter([1,4,9,16], $even)", nil, even, "[4,16]")
	expectResult(t, `$sift({"a": 1, "b": 2}, function($v){$v > 1})`, nil, nil, `{"b": 2}`)
	expectResult(t, `$each({"a": 1, "b": 2}, function($v, $k){$k & $v})`, nil, nil, `["a1", "b2"]`)
	double := func(e *Expression) {
		if err := e.RegisterFunction("myfunc", func(args ...any) (any, error) {
			v, err := Call(args[1], args[0])
			if err != nil {
				return nil, err
			}
			return 2 * v.(float64), nil
		}, ""); err != nil {
			t.Fatal(err)
		}
		if err := e.RegisterFunction("startsWith", func(args ...any) (any, error) {
			prefix := args[0].(string)
			return NewFunction(1, func(a ...any) (any, error) {
				s, _ := a[0].(string)
				return strings.HasPrefix(s, prefix), nil
			}), nil
		}, ""); err != nil {
			t.Fatal(err)
		}
	}
	expectResult(t, "$myfunc([1,2,3], $sum)", nil, double, "12")
	expectResult(t, "$myfunc([1,2,3], λ($arr) { $arr[1] + $arr[2] })", nil, double, "10")
	expectResult(t, `($s := $startsWith("Hello"); [$s("Hello, Bob"), $s("Goodbye, Bill")])`, nil, double, "[true, false]")
}

// repeatingLetters is a custom matcher of `repeat` contiguous occurrences of `char`.
func repeatingLetters(args ...any) (any, error) {
	chars := strings.Repeat(args[0].(string), int(args[1].(float64)))
	var match func(str string, offset int) any
	match = func(str string, offset int) any {
		pos := strings.Index(str[offset:], chars)
		if pos < 0 {
			return nil
		}
		pos += offset
		result := NewObject()
		result.Set("match", chars)
		result.Set("start", float64(pos))
		result.Set("end", float64(pos+len(chars)))
		result.Set("groups", NewArray())
		result.Set("next", NewFunction(0, func(...any) (any, error) { return match(str, pos+len(chars)), nil }))
		return result
	}
	return NewFunction(2, func(a ...any) (any, error) {
		str, _ := a[0].(string)
		return match(str, 0), nil
	}), nil
}

func TestCustomMatchers(t *testing.T) {
	const town = "LLANFAIRPWLLGWYNGYLLGOGERYCHWYRNDROBWLLLLANTYSILIOGOGOGOCH"
	setup := func(e *Expression) {
		if err := e.RegisterFunction("repeatingLetters", repeatingLetters, ""); err != nil {
			t.Fatal(err)
		}
	}
	expectResult(t, "$match('"+town+"', $repeatingLetters('L', 2))", nil, setup,
		`[{"match": "LL", "index": 0, "groups": []}, {"match": "LL", "index": 10, "groups": []},
		  {"match": "LL", "index": 18, "groups": []}, {"match": "LL", "index": 37, "groups": []},
		  {"match": "LL", "index": 39, "groups": []}]`)
	expectResult(t, "$split('"+town+"', $repeatingLetters('L', 2))", nil, setup,
		`["","ANFAIRPW","GWYNGY","GOGERYCHWYRNDROBW","","ANTYSILIOGOGOGOCH"]`)
	expectResult(t, "$replace('"+town+"', $repeatingLetters('L', 2), 'Ỻ')", nil, setup,
		`"ỺANFAIRPWỺGWYNGYỺGOGERYCHWYRNDROBWỺỺANTYSILIOGOGOGOCH"`)
	expectResult(t, "$contains('"+town+"', $repeatingLetters('L', 4))", nil, setup, "true")
}

func TestRegexFunctions(t *testing.T) {
	expectResult(t, `/ab/ ("ab")`, nil, nil, `{"match": "ab", "start": 0, "end": 2, "groups": [], "next": ""}`)
	got, err := evalExpr(t, `/ab/ ()`, nil, nil)
	if err != nil || got != "undefined" {
		t.Fatalf(`/ab/ (): want undefined, got %s (%v)`, got, err)
	}
	expectResult(t, `/a(b+)/ ("ababbabbcc").next().next()`, nil, nil,
		`{"match": "abb", "start": 5, "end": 8, "groups": ["bb"], "next": ""}`)
	expectResult(t, `/a(b+)/i ("Ababbabbcc")`, nil, nil, `{"match": "Ab", "start": 0, "end": 2, "groups": ["b"], "next": ""}`)
	for _, c := range []struct {
		expr     string
		code     string
		position int
	}{{"//", "S0301", 1}, {"/", "S0302", 1}, {"/\\/", "S0302", 3}, {"/\\\\\\/", "S0302", 5}} {
		_, err := New(c.expr, nil)
		var je *Error
		if !errors.As(err, &je) || je.Code != c.code || je.Position != c.position {
			t.Fatalf("%s: want %s at %d, got %v", c.expr, c.code, c.position, err)
		}
	}
	expectResult(t, `$match("ababbabbcc",/a(b+)/, 1)`, nil, nil, `{"match": "ab", "index": 0, "groups": ["b"]}`)
	expectCode(t, `$match("a, b, c, d", /ab/, -3)`, nil, nil, "D3040")
}

func TestParserRecovery(t *testing.T) {
	cases := []struct{ expr, ast, errors string }{
		{"Account.Order[0]",
			`{"type": "path", "steps": [{"value": "Account", "type": "name", "position": 7}, {"value": "Order", "type": "name",
			 "position": 13, "stages": [{"expr": {"value": 0, "type": "number", "position": 15}, "position": 14, "type": "filter"}]}]}`, ""},
		{"Account.",
			`{"type": "path", "steps": [{"value": "Account", "type": "name", "position": 7},
			 {"type": "error", "error": {"code": "S0207", "position": 8, "token": "(end)"}}]}`,
			`[{"code": "S0207", "position": 8, "token": "(end)"}]`},
		{"Account[",
			`{"type": "path", "steps": [{"value": "Account", "type": "name", "position": 7, "stages": [{"expr": {"type": "error",
			 "error": {"code": "S0207", "position": 8, "token": "(end)"}}, "position": 8, "type": "filter"}]}]}`,
			`[{"code": "S0203", "position": 8, "token": "(end)", "value": "]", "remaining": []},
			  {"code": "S0207", "position": 8, "token": "(end)"}]`},
	}
	for _, c := range cases {
		e, err := New(c.expr, &Options{Recover: true})
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got, want := canonical(mustParse(t, e.ASTJSON())), canonical(mustParse(t, c.ast)); got != want {
			t.Fatalf("%s: ast\nwant %s\ngot  %s", c.expr, want, got)
		}
		gotErrors := "undefined"
		if s := e.ErrorsJSON(); s != "" {
			gotErrors = canonical(mustParse(t, s))
		}
		wantErrors := "undefined"
		if c.errors != "" {
			wantErrors = canonical(mustParse(t, c.errors))
		}
		if gotErrors != wantErrors {
			t.Fatalf("%s: errors\nwant %s\ngot  %s", c.expr, wantErrors, gotErrors)
		}
		if _, err := e.Evaluate(nil, nil); c.errors != "" {
			var je *Error
			if !errors.As(err, &je) || je.Code != "S0500" {
				t.Fatalf("%s: evaluating with syntax errors: want S0500, got %v", c.expr, err)
			}
		}
	}
}
