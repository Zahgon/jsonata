// Command jsonata evaluates a JSONata expression.
//
//	jsonata [-i FILE] [-b JSON] [--ast] [--recover] EXPRESSION
//
//	-i, --input FILE      read the input document (JSON) from FILE; "-" reads standard input.
//	                      Without -i the expression is evaluated with no input document.
//	-b, --bindings JSON   a JSON object; each member is bound as a variable ($name).
//	--ast                 print the expression's syntax tree instead of evaluating it.
//	--recover             compile in recovery mode; with --ast, print {"ast": ..., "errors": ...}.
//
// On success the result is printed as one line of JSON (nothing when the result is undefined) and the
// exit status is 0. A JSONata error is printed to standard error as one line of JSON with its code,
// position, token, value and message, and the exit status is 1. Usage errors and unreadable or
// malformed input or bindings exit with status 2. Function values print as empty strings.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/jsonata-js/jsonata"
)

type options struct {
	input, bindings string
	hasInput        bool
	hasBindings     bool
	ast, recover    bool
	expression      string
	hasExpression   bool
}

// usageError is a command line or input problem (exit status 2).
type usageError string

func (e usageError) Error() string { return string(e) }

func parseArgs(args []string) (*options, error) {
	opts := &options{}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "-i", "--input", "-b", "--bindings":
			if i+1 >= len(args) {
				return nil, usageError("option " + arg + " needs a value")
			}
			i++
			if arg == "-i" || arg == "--input" {
				opts.input, opts.hasInput = args[i], true
			} else {
				opts.bindings, opts.hasBindings = args[i], true
			}
		case "--ast":
			opts.ast = true
		case "--recover":
			opts.recover = true
		case "--":
			if opts.hasExpression || i+2 != len(args) {
				return nil, usageError("exactly one expression is required")
			}
			i++
			opts.expression, opts.hasExpression = args[i], true
		default:
			if opts.hasExpression {
				return nil, usageError("exactly one expression is required")
			}
			opts.expression, opts.hasExpression = arg, true
		}
	}
	if !opts.hasExpression {
		return nil, usageError("exactly one expression is required")
	}
	return opts, nil
}

// validText decodes command line text as Node does: invalid UTF-8 becomes U+FFFD.
func validText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += n
	}
	return b.String()
}

// run executes one command line and returns the exit status.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	for i := range args {
		args[i] = validText(args[i])
	}
	opts, err := parseArgs(args)
	if err != nil {
		return usage(stderr, err)
	}
	var input any
	if opts.hasInput {
		var data []byte
		if opts.input == "-" {
			data, err = io.ReadAll(stdin)
		} else {
			data, err = os.ReadFile(opts.input)
		}
		if err != nil {
			return usage(stderr, usageError("cannot read input "+opts.input))
		}
		if input, err = jsonata.ParseJSON(validText(string(data))); err != nil {
			return usage(stderr, usageError("input is not valid JSON"))
		}
	}
	var bindings map[string]any
	if opts.hasBindings {
		value, err := jsonata.ParseJSON(opts.bindings)
		if err != nil {
			return usage(stderr, usageError("bindings are not valid JSON"))
		}
		obj, ok := value.(*jsonata.Object)
		if !ok {
			return usage(stderr, usageError("bindings must be a JSON object"))
		}
		bindings = map[string]any{}
		for _, k := range obj.Keys() {
			bindings[k], _ = obj.Get(k)
		}
	}
	expr, err := jsonata.New(opts.expression, &jsonata.Options{Recover: opts.recover})
	if err != nil {
		return failure(stderr, err)
	}
	if opts.ast {
		out := expr.ASTJSON()
		if opts.recover {
			out = `{"ast":` + out
			if errs := expr.ErrorsJSON(); errs != "" {
				out += `,"errors":` + errs
			}
			out += "}"
		}
		fmt.Fprintln(stdout, out)
		return 0
	}
	result, err := expr.Evaluate(input, bindings)
	if err != nil {
		return failure(stderr, err)
	}
	if result == nil {
		return 0
	}
	text, ok, err := jsonata.Stringify(result)
	if err != nil {
		return internal(stderr)
	}
	if ok {
		fmt.Fprintln(stdout, text)
	}
	return 0
}

func usage(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "jsonata: %v\nusage: jsonata [-i FILE] [-b JSON] [--ast] [--recover] EXPRESSION\n", err)
	return 2
}

func internal(stderr io.Writer) int {
	fmt.Fprintln(stderr, "jsonata: internal error")
	return 3
}

// failure prints a JSONata error as JSON (status 1); any other failure is internal (status 3).
func failure(stderr io.Writer, err error) int {
	var je *jsonata.Error
	if !errors.As(err, &je) {
		return internal(stderr)
	}
	line, jerr := je.JSON()
	if jerr != nil {
		return internal(stderr)
	}
	fmt.Fprintln(stderr, line)
	return 1
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
