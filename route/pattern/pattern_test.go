// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package pattern_test

import (
	"fmt"
	"testing"

	"github.com/fiberfx/mx-echo/v7/route/pattern"
)

func static(value string) pattern.Part {
	return pattern.Part{Kind: pattern.Static, Value: value}
}

func param(value string) pattern.Part {
	return pattern.Part{Kind: pattern.Param, Value: value}
}

func anyPart(value string) pattern.Part {
	return pattern.Part{Kind: pattern.Any, Value: value}
}

// grammar is the route pattern grammar as the root's scanner read it before the move. The rows marked "root" are the
// patterns the root's route_syntax_test.go registers or reverses.
var grammar = []struct {
	name string
	path string
	want []pattern.Part
}{
	{"the empty path", "", nil},
	{"static", "/users/profile", []pattern.Part{static("/users/profile")}},
	{"static without a leading slash (root)", "x", []pattern.Part{static("x")}},
	{"one param (root)", "/u/:id", []pattern.Part{static("/u/"), param("id")}},
	{"two params", "/users/:id/books/:bid", []pattern.Part{static("/users/"), param("id"), static("/books/"), param("bid")}},
	{"a param then static text (root)", "/r/:id/info", []pattern.Part{static("/r/"), param("id"), static("/info")}},
	{"a param after static text in its segment (root)", "/a:id", []pattern.Part{static("/a"), param("id")}},
	{"an empty param name", "/:/x", []pattern.Part{static("/"), param(""), static("/x")}},
	{"the wildcard (root)", "/*", []pattern.Part{static("/"), anyPart("*")}},
	{"the wildcard after static text (root)", "/files/*", []pattern.Part{static("/files/"), anyPart("*")}},
	{"the wildcard alone", "*", []pattern.Part{anyPart("*")}},
	{"the wildcard keeps the rest of its segment", "/*rest/x", []pattern.Part{static("/"), anyPart("*rest"), static("/x")}},
	{"a param then the wildcard (root)", "/r/:n/*", []pattern.Part{static("/r/"), param("n"), static("/"), anyPart("*")}},
	{"an escaped colon is static (root)", `/a\:b`, []pattern.Part{static("/a"), static(":"), static("b")}},
	{"an escaped colon first", `\:a`, []pattern.Part{static(":"), static("a")}},
	{"an escaped colon before a param", `/a\::id`, []pattern.Part{static("/a"), static(":"), param("id")}},
	{"a backslash before another byte is literal", `/a\b`, []pattern.Part{static(`/a\b`)}},
	{"a trailing backslash is literal", `/a\`, []pattern.Part{static(`/a\`)}},
	{"an inline verb", `/:name\:cancel`, []pattern.Part{static("/"), param("name"), static(":"), static("cancel")}},
	{"an inline verb (root)", `/r/:name\:cancel`, []pattern.Part{static("/r/"), param("name"), static(":"), static("cancel")}},
	{"an empty inline verb", `/:name\:`, []pattern.Part{static("/"), param("name"), static(":")}},
	{"an inline verb of two words (root)", `/r/:name\:foo\:bar`, []pattern.Part{static("/r/"), param("name"), static(":"), static("foo"), static(":"), static("bar")}},
	{"an inline verb then a param (root)", `/r/:name\:cancel/:action`, []pattern.Part{static("/r/"), param("name"), static(":"), static("cancel/"), param("action")}},
	{"an inline verb then the wildcard (root)", `/r/:name\:cancel/*`, []pattern.Part{static("/r/"), param("name"), static(":"), static("cancel/"), anyPart("*")}},
	{"two inline verbs (root)", `/r/:name\:x/:a\:y/z`, []pattern.Part{static("/r/"), param("name"), static(":"), static("x/"), param("a"), static(":"), static("y/z")}},
	{"not an inline verb: the wildcard follows", `/:a\:b*`, []pattern.Part{static("/"), param(`a\:b*`)}},
	{"not an inline verb: the wildcard follows (root)", `/s/:name\:x*`, []pattern.Part{static("/s/"), param(`name\:x*`)}},
	{"not an inline verb: a param follows (root)", `/r/:name\:x:id`, []pattern.Part{static("/r/"), param(`name\:x:id`)}},
	{"not an inline verb: the first escaped colon decides (root)", `/t/:a\:x:y\:z`, []pattern.Part{static("/t/"), param(`a\:x:y\:z`)}},
	{"not an inline verb: the name holds a colon (root)", `/s/:a:b\:v`, []pattern.Part{static("/s/"), param(`a:b\:v`)}},
	{"not an inline verb: the name holds the wildcard", `/:a*b\:v`, []pattern.Part{static("/"), param(`a*b\:v`)}},
	{"a colon inside a param name", "/:a:b", []pattern.Part{static("/"), param("a:b")}},
}

func show(parts []pattern.Part) string {
	out := "["
	for i, part := range parts {
		if i > 0 {
			out += " "
		}
		switch part.Kind {
		case pattern.Static:
			out += "Static"
		case pattern.Param:
			out += "Param"
		case pattern.Any:
			out += "Any"
		default:
			out += fmt.Sprintf("Kind(%d)", part.Kind)
		}
		out += fmt.Sprintf(" %q", part.Value)
	}
	return out + "]"
}

func checkParts(call string, got, want []pattern.Part) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s is %s, want %s", call, show(got), show(want))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("%s is %s, want %s", call, show(got), show(want))
		}
	}
	return nil
}

func checkWalk(path string, want []pattern.Part) error {
	var got []pattern.Part
	pattern.Walk(path, func(part pattern.Part) { got = append(got, part) })
	return checkParts(fmt.Sprintf("Walk(%q)", path), got, want)
}

func checkParse(path string, want []pattern.Part) error {
	return checkParts(fmt.Sprintf("Parse(%q)", path), pattern.Parse(path), want)
}

func checkNoAllocs(call string, f func()) error {
	if allocs := testing.AllocsPerRun(100, f); allocs != 0 {
		return fmt.Errorf("%s allocates %v times a run, want 0", call, allocs)
	}
	return nil
}

func TestWalk(t *testing.T) {
	for _, row := range grammar {
		t.Run(row.name, func(t *testing.T) {
			if err := checkWalk(row.path, row.want); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestWalkViolation(t *testing.T) {
	violations := []struct {
		name string
		path string
		want []pattern.Part
	}{
		{"an escaped colon read as a param", `/a\:b`, []pattern.Part{static("/a"), param("b")}},
		{"an inline verb kept in the param name", `/:name\:cancel`, []pattern.Part{static("/"), param(`name\:cancel`)}},
		{"a part too few", "/users/:id", []pattern.Part{static("/users/")}},
		{"a part too many", "", []pattern.Part{static("")}},
	}
	for _, row := range violations {
		if checkWalk(row.path, row.want) == nil {
			t.Errorf("%s: Walk(%q) passed the check as %s", row.name, row.path, show(row.want))
		}
	}
}

func TestParse(t *testing.T) {
	for _, row := range grammar {
		t.Run(row.name, func(t *testing.T) {
			if err := checkParse(row.path, row.want); err != nil {
				t.Error(err)
			}
		})
	}
	if parts := pattern.Parse(""); parts != nil {
		t.Errorf(`Parse("") is %s, want nil`, show(parts))
	}
}

func TestParseViolation(t *testing.T) {
	if checkParse(`/a\:b`, []pattern.Part{static("/a"), param("b")}) == nil {
		t.Fatal(`Parse("/a\:b") passed the check as a param`)
	}
}

func TestWalkAllocs(t *testing.T) {
	for _, row := range grammar {
		t.Run(row.name, func(t *testing.T) {
			count := 0
			err := checkNoAllocs(fmt.Sprintf("Walk(%q)", row.path), func() {
				pattern.Walk(row.path, func(pattern.Part) { count++ })
			})
			if err != nil {
				t.Error(err)
			}
			if count == 0 && len(row.want) > 0 {
				t.Errorf("Walk(%q) emitted nothing", row.path)
			}
		})
	}
}

var sink []pattern.Part

func TestWalkAllocsViolation(t *testing.T) {
	if checkNoAllocs("Parse", func() { sink = pattern.Parse("/users/:id") }) == nil {
		t.Fatal("Parse building a slice passed the check for no allocation")
	}
}
