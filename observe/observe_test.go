// SPDX-License-Identifier: MIT

package observe_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fiberfx/mx-echo/v7/observe"
	"github.com/fiberfx/mx-echo/v7/outcome"
	"github.com/fiberfx/mx-echo/v7/reqid"
)

// keys is the order in which AppendJSON writes an observation's fields.
var keys = []string{
	"id", "link", "attempt", "build", "method", "admission",
	"pattern", "handler", "kind", "status", "committed", "panicked",
}

// replacement is U+FFFD as UTF-8, which stands for each byte that was not valid UTF-8.
const replacement = "\xef\xbf\xbd"

// traceparent is the example of the W3C Trace Context recommendation.
const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// hostileMethod holds a quote, a backslash, a newline, a byte that is not UTF-8 and a closing quote.
const hostileMethod = "G\"E\\T\n\xff\""

// checkLine reports why line is not one observation as AppendJSON must write it: valid UTF-8, one JSON object
// with nothing between its tokens and nothing after it, the twelve keys in order, and no empty string. When
// want is not nil it holds the value of each key, in the order of keys.
func checkLine(line []byte, want []any) error {
	if !utf8.Valid(line) {
		return errors.New("the line is not valid UTF-8")
	}
	if !json.Valid(line) {
		return errors.New("the line is not valid JSON")
	}
	inString := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inString && c == '\\':
			i++
		case c == '"':
			inString = !inString
		case !inString && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			return fmt.Errorf("byte %d is white space between two tokens", i)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return fmt.Errorf("the line does not open an object: %v, %v", tok, err)
	}
	for i, key := range keys {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if tok != key {
			return fmt.Errorf("key %d is %v, want %q", i, tok, key)
		}
		tok, err = dec.Token()
		if err != nil {
			return err
		}
		var got any
		switch v := tok.(type) {
		case string:
			if key == "status" || key == "committed" || key == "panicked" {
				return fmt.Errorf("%s is the string %q", key, v)
			}
			if v == "" {
				return fmt.Errorf("%s is an empty string", key)
			}
			got = v
		case json.Number:
			n, err := strconv.Atoi(v.String())
			if err != nil || key != "status" {
				return fmt.Errorf("%s is the number %v", key, v)
			}
			got = n
		case bool:
			if key != "committed" && key != "panicked" {
				return fmt.Errorf("%s is the flag %v", key, v)
			}
			got = v
		default:
			return fmt.Errorf("%s holds %v, which is no string, number or flag", key, tok)
		}
		if want != nil && got != want[i] {
			return fmt.Errorf("%s is %#v, want %#v", key, got, want[i])
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return fmt.Errorf("the object does not end after %d keys: %v, %v", len(keys), tok, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("the line holds more than one value")
	}
	return nil
}

// zeroWant is what the zero Observation must write: none for seven fields, and no empty string.
func zeroWant() []any {
	return []any{"none", "none", "none", "none", "none", "unknown", "none", "none", "unknown", 0, false, false}
}

// routed is a request that matched a route and was answered, with every fact present.
func routed(t *testing.T) observe.Observation {
	t.Helper()
	link, ok := reqid.ParseTraceparent(traceparent)
	if !ok {
		t.Fatalf("the traceparent %q did not parse", traceparent)
	}
	var o observe.Observation
	o.ID = reqid.New(0x0123456789ab, 0xdeadbeef)
	o.Link = link
	o.Attempt = "q0/run/a1"
	o.Build = "v6.3.0"
	o.Method = "PUT"
	o.Admission = observe.Routed
	o.Pattern = "/api/responses/:key"
	o.Handler = "PUT:/api/responses/:key"
	o.Kind = outcome.OK
	o.Status = 200
	o.Committed = true
	return o
}

type testCase struct {
	name string
	give observe.Observation
	want []any
	line string
}

func cases(t *testing.T) []testCase {
	t.Helper()

	var notFound observe.Observation
	notFound.ID = reqid.New(1, 2)
	notFound.Method = hostileMethod
	notFound.Admission = observe.NotFound
	notFound.Kind = outcome.AdmissionRefusal
	notFound.Status = 404
	notFound.Committed = true

	var panicked observe.Observation
	panicked.ID = reqid.New(2, 3)
	panicked.Method = "GET"
	panicked.Admission = observe.Routed
	panicked.Pattern = "/users/:id"
	panicked.Handler = "GET:/users/:id"
	panicked.Kind = outcome.WorkerFailure
	panicked.Status = 500
	panicked.Panicked = true

	return []testCase{
		{
			name: "the zero observation",
			give: observe.Observation{},
			want: zeroWant(),
			line: `{"id":"none","link":"none","attempt":"none","build":"none","method":"none",` +
				`"admission":"unknown","pattern":"none","handler":"none","kind":"unknown",` +
				`"status":0,"committed":false,"panicked":false}`,
		},
		{
			name: "a routed request with every fact",
			give: routed(t),
			want: []any{
				"0123456789abdeadbeef", traceparent, "q0/run/a1", "v6.3.0", "PUT", "routed",
				"/api/responses/:key", "PUT:/api/responses/:key", "ok", 200, true, false,
			},
			line: `{"id":"0123456789abdeadbeef","link":"` + traceparent + `","attempt":"q0/run/a1",` +
				`"build":"v6.3.0","method":"PUT","admission":"routed","pattern":"/api/responses/:key",` +
				`"handler":"PUT:/api/responses/:key","kind":"ok","status":200,"committed":true,"panicked":false}`,
		},
		{
			name: "an unknown path with a hostile method",
			give: notFound,
			want: []any{
				"00000000000100000002", "none", "none", "none", "G\"E\\T\n" + replacement + "\"", "not-found",
				"none", "none", "admission-refusal", 404, true, false,
			},
			line: `{"id":"00000000000100000002","link":"none","attempt":"none","build":"none",` +
				`"method":"G\"E\\T\` + `u000a` + replacement + `\"","admission":"not-found","pattern":"none",` +
				`"handler":"none","kind":"admission-refusal","status":404,"committed":true,"panicked":false}`,
		},
		{
			name: "a panic before the status line",
			give: panicked,
			want: []any{
				"00000000000200000003", "none", "none", "none", "GET", "routed",
				"/users/:id", "GET:/users/:id", "worker-failure", 500, false, true,
			},
			line: `{"id":"00000000000200000003","link":"none","attempt":"none","build":"none","method":"GET",` +
				`"admission":"routed","pattern":"/users/:id","handler":"GET:/users/:id","kind":"worker-failure",` +
				`"status":500,"committed":false,"panicked":true}`,
		},
	}
}

// TestAppendJSON is row V1: each line decodes with encoding/json to the same fields, and is the exact text.
func TestAppendJSON(t *testing.T) {
	for _, tc := range cases(t) {
		t.Run(tc.name, func(t *testing.T) {
			line := tc.give.AppendJSON(nil)
			if err := checkLine(line, tc.want); err != nil {
				t.Errorf("%v\n%s", err, line)
			}
			if string(line) != tc.line {
				t.Errorf("the line is\n%s\nwant\n%s", line, tc.line)
			}
		})
	}
}

// TestAppendJSONViolation feeds the checker lines AppendJSON must never write; each must be refused.
func TestAppendJSONViolation(t *testing.T) {
	good := string(observe.Observation{}.AppendJSON(nil))
	if err := checkLine([]byte(good), zeroWant()); err != nil {
		t.Fatalf("the line the violations are planted in is refused: %v", err)
	}
	swapped := strings.Replace(good, `"id":"none","link":"none"`, `"link":"none","id":"none"`, 1)
	violations := []struct {
		name string
		line string
	}{
		{"an empty handler", strings.Replace(good, `"handler":"none"`, `"handler":""`, 1)},
		{"an unclosed object", strings.TrimSuffix(good, "}")},
		{"an unescaped quote", strings.Replace(good, `"method":"none"`, `"method":"G"T"`, 1)},
		{"a raw newline in a string", strings.Replace(good, `"method":"none"`, "\"method\":\"G\nT\"", 1)},
		{"a raw byte that is not UTF-8", strings.Replace(good, `"method":"none"`, "\"method\":\"\xff\"", 1)},
		{"a newline after the object", good + "\n"},
		{"a space after a colon", strings.Replace(good, `"id":`, `"id": `, 1)},
		{"two keys in the wrong order", swapped},
		{"a missing key", strings.Replace(good, `,"panicked":false`, "", 1)},
		{"a status that is a string", strings.Replace(good, `"status":0`, `"status":"0"`, 1)},
		{"a second object", good + good},
	}
	for _, v := range violations {
		if v.line == good {
			t.Errorf("%s: the violation was not planted", v.name)
			continue
		}
		if err := checkLine([]byte(v.line), nil); err == nil {
			t.Errorf("%s passed the check:\n%s", v.name, v.line)
		}
	}

	want := zeroWant()
	want[7] = "GET:/users/:id"
	if err := checkLine([]byte(good), want); err == nil {
		t.Error("a handler of none passed for a named handler")
	}
	want = zeroWant()
	want[9] = "0"
	if err := checkLine([]byte(good), want); err == nil {
		t.Error("a status of 0 passed for the string \"0\"")
	}
}

// TestZeroObservation holds the rule that an absent fact is explicit: seven fields say none, and none is empty.
func TestZeroObservation(t *testing.T) {
	line := observe.Observation{}.AppendJSON(nil)
	if err := checkLine(line, zeroWant()); err != nil {
		t.Fatalf("%v\n%s", err, line)
	}
	if n := bytes.Count(line, []byte(`"none"`)); n != 7 {
		t.Errorf("the zero observation writes none %d times, want 7:\n%s", n, line)
	}
	if bytes.Contains(line, []byte(`""`)) {
		t.Errorf("the zero observation writes an empty string:\n%s", line)
	}
}

// TestAppendJSONWords writes every admission and every kind, and a value outside each set.
func TestAppendJSONWords(t *testing.T) {
	admissions := []struct {
		give observe.Admission
		want string
	}{
		{observe.AdmissionUnknown, "unknown"},
		{observe.BeforeRouting, "before-routing"},
		{observe.NotFound, "not-found"},
		{observe.MethodNotAllowed, "method-not-allowed"},
		{observe.Routed, "routed"},
		{observe.Admission(200), "unknown"},
	}
	for i, a := range admissions {
		if i < 5 && int(a.give) != i {
			t.Errorf("the admission %q has the value %d, want %d", a.want, a.give, i)
		}
		if got := a.give.String(); got != a.want {
			t.Errorf("Admission(%d).String() is %q, want %q", a.give, got, a.want)
		}
		want := zeroWant()
		want[5] = a.want
		line := observe.Observation{Admission: a.give}.AppendJSON(nil)
		if err := checkLine(line, want); err != nil {
			t.Errorf("%v\n%s", err, line)
		}
	}

	kinds := []struct {
		give outcome.Kind
		want string
	}{
		{outcome.Unknown, "unknown"},
		{outcome.OK, "ok"},
		{outcome.AdmissionRefusal, "admission-refusal"},
		{outcome.BusinessRefusal, "business-refusal"},
		{outcome.WorkerFailure, "worker-failure"},
		{outcome.LostContact, "lost-contact"},
	}
	for _, k := range kinds {
		want := zeroWant()
		want[8] = k.want
		line := observe.Observation{Kind: k.give}.AppendJSON(nil)
		if err := checkLine(line, want); err != nil {
			t.Errorf("%v\n%s", err, line)
		}
	}
	line := observe.Observation{Kind: outcome.Kind(200)}.AppendJSON(nil)
	if err := checkLine(line, nil); err != nil {
		t.Errorf("a kind outside the set: %v\n%s", err, line)
	}
}

// TestAppendJSONBytes puts each of the 256 bytes into the method, alone between two letters: the line stays
// one valid object, a byte below 0x80 comes back as itself, and any other byte comes back as U+FFFD.
func TestAppendJSONBytes(t *testing.T) {
	for b := 0; b < 256; b++ {
		method := "a" + string([]byte{byte(b)}) + "z"
		want := zeroWant()
		want[4] = method
		if b >= utf8.RuneSelf {
			want[4] = "a" + replacement + "z"
		}
		line := observe.Observation{Method: method, Pattern: method, Attempt: method}.AppendJSON(nil)
		want[2], want[6] = want[4], want[4]
		if err := checkLine(line, want); err != nil {
			t.Errorf("byte 0x%02x: %v\n%q", b, err, line)
		}
	}
}

// TestAppendJSONText keeps valid UTF-8 as it came, and replaces each byte of a sequence that is not valid.
func TestAppendJSONText(t *testing.T) {
	texts := []struct {
		name string
		give string
		want string
	}{
		{"two bytes", "h\xc3\xa9llo", "h\xc3\xa9llo"},
		{"three bytes", "\xe6\x97\xa5\xe6\x9c\xac", "\xe6\x97\xa5\xe6\x9c\xac"},
		{"four bytes", "\xf0\x9f\x99\x82", "\xf0\x9f\x99\x82"},
		{"U+FFFD itself", replacement, replacement},
		{"U+2028", "a\xe2\x80\xa8b", "a\xe2\x80\xa8b"},
		{"DEL", "a\x7fb", "a\x7fb"},
		{"a sequence cut short", "\xe6\x97", replacement + replacement},
		{"a sequence cut short at the end", "ok\xf0\x9f\x99", "ok" + replacement + replacement + replacement},
		{"an overlong slash", "\xc0\xaf", replacement + replacement},
		{"a surrogate half", "\xed\xa0\x80", replacement + replacement + replacement},
		{"only a quote", "\"", "\""},
		{"only a backslash", "\\", "\\"},
		{"a backslash then a quote", "\\\"", "\\\""},
		{"every escape at once", "\x00\x1f\"\\\xff", "\x00\x1f\"\\" + replacement},
	}
	for _, tt := range texts {
		want := zeroWant()
		want[4], want[7] = tt.want, tt.want
		line := observe.Observation{Method: tt.give, Handler: tt.give}.AppendJSON(nil)
		if err := checkLine(line, want); err != nil {
			t.Errorf("%s: %v\n%q", tt.name, err, line)
		}
	}
}

// TestAppendJSONAppends keeps what dst already holds and writes the same line after it.
func TestAppendJSONAppends(t *testing.T) {
	o := routed(t)
	line := o.AppendJSON(nil)
	prefix := "kept\n"
	got := o.AppendJSON([]byte(prefix))
	if string(got) != prefix+string(line) {
		t.Errorf("appended to %q:\n%q\nwant the prefix and\n%q", prefix, got, line)
	}
}

var appended []byte

// appendAllocs counts the allocations of one AppendJSON into a slice of the given capacity.
func appendAllocs(o observe.Observation, capacity int) float64 {
	buf := make([]byte, 0, capacity)
	return testing.AllocsPerRun(100, func() {
		appended = o.AppendJSON(buf)
	})
}

// TestAppendJSONAllocs holds that a line appended into a slice with room allocates nothing.
func TestAppendJSONAllocs(t *testing.T) {
	o := routed(t)
	if n := len(o.AppendJSON(nil)); n > 512 {
		t.Fatalf("the line is %d bytes, more than the 512 the slice has", n)
	}
	if allocs := appendAllocs(o, 512); allocs != 0 {
		t.Errorf("AppendJSON into a slice of capacity 512 allocates %v times, want 0", allocs)
	}
}

// TestAppendJSONAllocsViolation shows the meter counts: a slice with no room must grow.
func TestAppendJSONAllocsViolation(t *testing.T) {
	if allocs := appendAllocs(routed(t), 8); allocs < 1 {
		t.Errorf("AppendJSON into a slice of capacity 8 allocates %v times, want 1 or more", allocs)
	}
}

// TestSinkFunc hands the observation to the function once, by value.
func TestSinkFunc(t *testing.T) {
	var got []observe.Observation
	var sink observe.Sink = observe.SinkFunc(func(o observe.Observation) { got = append(got, o) })
	o := routed(t)
	sink.Observe(o)
	if len(got) != 1 || got[0] != o {
		t.Errorf("the sink received %v, want one observation equal to %v", got, o)
	}
}
