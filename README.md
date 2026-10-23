# JSONata

JSON query and transformation language

Reference implementation of the [JSONata query and transformation language](http://jsonata.org/).

* [JSONata in 5 minutes](https://www.youtube.com/embed/ZBaK40rtIBM)
* [JSONata language documentation](http://docs.jsonata.org/)
* [Try it out!](http://try.jsonata.org/)

## Installation

```
go get github.com/jsonata-js/jsonata
```

The `jsonata` command line tool:

```
go install github.com/jsonata-js/jsonata/cmd/jsonata@latest
```

## Quick start

As a Go library:

```go
package main

import (
	"fmt"

	"github.com/jsonata-js/jsonata"
)

func main() {
	data, _ := jsonata.ParseJSON(`{"example": [{"value": 4}, {"value": 7}, {"value": 13}]}`)
	expression, err := jsonata.New("$sum(example.value)", nil)
	if err != nil {
		panic(err)
	}
	result, err := expression.Evaluate(data, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(result) // 24
}
```

Values follow the JSON data model as JavaScript represents it: `nil` is undefined, `jsonata.Null` is
`null`, numbers are `float64`, arrays are `*jsonata.Array` and objects are `*jsonata.Object`, whose keys
keep JavaScript property order. `jsonata.ParseJSON` and `jsonata.Stringify` convert from and to JSON text.

From the command line:

```
$ echo '{"name": "Wilbur"}' | jsonata -i - '"Hello, " & name'
"Hello, Wilbur"
```

`jsonata [-i FILE] [-b JSON] [--ast] [--recover] EXPRESSION` reads the input document from `FILE` (`-`
for standard input), binds the members of the `-b` object as variables, and prints the result as JSON.
A JSONata error is printed to standard error as JSON with its code, position, token, value and message,
with exit status 1; `--ast` prints the parsed expression and `--recover` parses in recovery mode.

## More information
- JSONata [documentation](http://docs.jsonata.org/)
- [Go API reference](https://pkg.go.dev/github.com/jsonata-js/jsonata)
- [Intro talk](https://www.youtube.com/watch?v=TDWf6R8aqDo) at London Node User Group

## Contributing

See the [CONTRIBUTING.md](CONTRIBUTING.md) for details of how to contribute to this repo.
