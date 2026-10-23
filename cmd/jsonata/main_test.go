package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(`{"b": [1, 2], "10": "x", "2": "y"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args           []string
		stdin          string
		status         int
		stdout, stderr string
	}{
		{args: []string{"1 + 1"}, status: 0, stdout: "2\n"},
		{args: []string{"-i", input, "$sum(b)"}, status: 0, stdout: "3\n"},
		{args: []string{"-i", "-", "$keys($)"}, stdin: `{"b": 1, "10": 2, "2": 3}`, status: 0, stdout: `["2","10","b"]` + "\n"},
		{args: []string{"-b", `{"x": 0.1}`, "$x + 0.2"}, status: 0, stdout: "0.30000000000000004\n"},
		{args: []string{"nothing"}, status: 0, stdout: ""},
		{args: []string{"[$sum, 1e21, -0]"}, status: 0, stdout: `["",1e+21,0]` + "\n"},
		{args: []string{"1 +"}, status: 1, stderr: `{"code":"S0207","position":3,"token":"(end)","message":"Unexpected end of expression"}` + "\n"},
		{args: []string{"1 + 'a'"}, status: 1,
			stderr: `{"code":"T2002","position":3,"token":"+","value":"a","message":"The right side of the \"+\" operator must evaluate to a number"}` + "\n"},
		{args: []string{"--ast", "a"}, status: 0, stdout: `{"type":"path","steps":[{"value":"a","type":"name","position":1}]}` + "\n"},
		{args: []string{"-i", "-", "$"}, stdin: "{", status: 2},
		{args: []string{"-b", "[1]", "1"}, status: 2},
		{args: []string{}, status: 2},
		{args: []string{"a", "b"}, status: 2},
	}
	for _, c := range cases {
		var stdout, stderr bytes.Buffer
		status := run(append([]string(nil), c.args...), strings.NewReader(c.stdin), &stdout, &stderr)
		if status != c.status {
			t.Errorf("jsonata %q: status %d, want %d (stderr %q)", c.args, status, c.status, stderr.String())
			continue
		}
		if c.status != 2 && stdout.String() != c.stdout {
			t.Errorf("jsonata %q: stdout %q, want %q", c.args, stdout.String(), c.stdout)
		}
		if c.status == 1 && stderr.String() != c.stderr {
			t.Errorf("jsonata %q: stderr %q, want %q", c.args, stderr.String(), c.stderr)
		}
	}
}
