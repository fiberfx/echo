// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package reqid_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v7/reqid"
)

var _ encoding.TextAppender = reqid.ID{}

var _ encoding.TextMarshaler = reqid.ID{}

var _ fmt.Stringer = reqid.ID{}

// low48 masks the part of a time an ID keeps.
const low48 = 1<<48 - 1

var (
	sinkBytes []byte
	sinkID    reqid.ID
	sinkLink  reqid.Link
	sinkOK    bool
)

// checkLayout reports an error unless id prints as want, the same through String and AppendText.
func checkLayout(id reqid.ID, want string) error {
	if got := id.String(); got != want {
		return fmt.Errorf("String() = %q, want %q", got, want)
	}
	got, err := id.AppendText([]byte("id="))
	if err != nil {
		return fmt.Errorf("AppendText returned the error %v", err)
	}
	if string(got) != "id="+want {
		return fmt.Errorf("AppendText onto %q = %q, want %q", "id=", got, "id="+want)
	}
	return nil
}

func TestNew(t *testing.T) {
	cases := []struct {
		unixMilli int64
		random    uint32
		want      string
	}{
		{0x0123456789ab, 0xdeadbeef, "0123456789abdeadbeef"},
		{0, 0, "00000000000000000000"},
		{1, 0, "00000000000100000000"},
		{0, 1, "00000000000000000001"},
		{0xffffffffffff, 0xffffffff, "ffffffffffffffffffff"},
		{1795000000000, 0x00c0ffee, "01a1ee565e0000c0ffee"},
		{0x100000000002a, 7, "00000000002a00000007"},
		{-1, 0, "ffffffffffff00000000"},
	}
	for _, c := range cases {
		id := reqid.New(c.unixMilli, c.random)
		if err := checkLayout(id, c.want); err != nil {
			t.Errorf("New(%#x, %#x): %v", c.unixMilli, c.random, err)
		}
		if got, want := id.UnixMilli(), c.unixMilli&low48; got != want {
			t.Errorf("New(%#x, %#x).UnixMilli() = %#x, want %#x", c.unixMilli, c.random, got, want)
		}
		if got, want := id.IsZero(), c.unixMilli&low48 == 0 && c.random == 0; got != want {
			t.Errorf("New(%#x, %#x).IsZero() = %t, want %t", c.unixMilli, c.random, got, want)
		}
	}

	// Byte by byte: one byte set at each of the ten places, six of the time and four of the random number.
	for i := 0; i < 10; i++ {
		var unixMilli int64
		var random uint32
		if i < 6 {
			unixMilli = 0xa5 << (8 * (5 - i))
		} else {
			random = 0xa5 << (8 * (9 - i))
		}
		want := strings.Repeat("00", i) + "a5" + strings.Repeat("00", 9-i)
		if err := checkLayout(reqid.New(unixMilli, random), want); err != nil {
			t.Errorf("byte %d: New(%#x, %#x): %v", i, unixMilli, random, err)
		}
	}

	if !reqid.New(0, 0).IsZero() {
		t.Error("New(0, 0) is not the zero ID")
	}
	var zero reqid.ID
	if !zero.IsZero() || zero != reqid.New(0, 0) {
		t.Error("the zero value is not the zero ID")
	}

	// An ID is comparable: equal inputs give equal values, and it keys a map.
	seen := map[reqid.ID]bool{reqid.New(1795000000000, 1): true}
	if !seen[reqid.New(1795000000000, 1)] {
		t.Error("two IDs of the same time and random number are not equal")
	}
	if seen[reqid.New(1795000000000, 2)] || seen[reqid.New(1795000000001, 1)] {
		t.Error("two IDs of different inputs are equal")
	}
}

func TestNewViolation(t *testing.T) {
	// The same ten bytes, little-endian in each half: a layout that does not sort by time.
	if checkLayout(reqid.New(0x0123456789ab, 0xdeadbeef), "ab8967452301efbeadde") == nil {
		t.Fatal("a little-endian layout passed the check")
	}
	if checkLayout(reqid.New(0x0123456789ab, 0xdeadbeef), "0123456789ABDEADBEEF") == nil {
		t.Fatal("an uppercase text passed the check")
	}
}

// checkParse runs the table of Parse against parse and reports the first row it gets wrong.
func checkParse(parse func(string) (reqid.ID, bool)) error {
	accepted := []string{
		"0123456789abdeadbeef",
		"00000000000000000000",
		"ffffffffffffffffffff",
		"01a1ee565e0000c0ffee",
	}
	for _, in := range accepted {
		id, ok := parse(in)
		if !ok {
			return fmt.Errorf("Parse(%q) was refused", in)
		}
		if got := id.String(); got != in {
			return fmt.Errorf("Parse(%q) prints back %q", in, got)
		}
	}
	refused := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"19 digits", "0123456789abdeadbee"},
		{"21 digits", "0123456789abdeadbeef0"},
		{"uppercase", "0123456789ABDEADBEEF"},
		{"one uppercase digit", "0123456789abdeadbeeF"},
		{"a letter after f", "0123456789abdeadbeeg"},
		{"a 0x prefix", "0x23456789abdeadbeef"},
		{"a leading space", " 123456789abdeadbeef"},
		{"a trailing newline", "0123456789abdeadbee\n"},
		{"a sign", "-123456789abdeadbeef"},
		{"20 bytes that are not ASCII", "0123456789abdeadbeé"},
		{"a dash inside", "0123456789ab-eadbeef"},
	}
	for _, c := range refused {
		id, ok := parse(c.in)
		if ok {
			return fmt.Errorf("%s: Parse(%q) was accepted", c.name, c.in)
		}
		if !id.IsZero() {
			return fmt.Errorf("%s: Parse(%q) was refused with the non-zero ID %v", c.name, c.in, id)
		}
	}
	return nil
}

func TestParse(t *testing.T) {
	if err := checkParse(reqid.Parse); err != nil {
		t.Error(err)
	}
	ids := []reqid.ID{
		reqid.New(0, 0),
		reqid.New(1, 0),
		reqid.New(0, 1),
		reqid.New(0x0123456789ab, 0xdeadbeef),
		reqid.New(1795000000000, 0x00c0ffee),
		reqid.New(0xffffffffffff, 0xffffffff),
	}
	for _, id := range ids {
		got, ok := reqid.Parse(id.String())
		if !ok || got != id {
			t.Errorf("Parse(%q) = (%v, %t), want the same ID and true", id.String(), got, ok)
		}
	}
}

func TestParseViolation(t *testing.T) {
	lenient := []struct {
		row   string
		parse func(string) (reqid.ID, bool)
	}{
		{"uppercase", func(s string) (reqid.ID, bool) {
			return reqid.Parse(strings.ToLower(s))
		}},
		{"19 digits", func(s string) (reqid.ID, bool) {
			if len(s) == 19 {
				s += "0"
			}
			return reqid.Parse(s)
		}},
	}
	for _, c := range lenient {
		err := checkParse(c.parse)
		if err == nil {
			t.Errorf("a parser that accepts the row %q passed the table", c.row)
			continue
		}
		if !strings.HasPrefix(err.Error(), c.row+":") {
			t.Errorf("a parser that accepts the row %q was caught on another: %v", c.row, err)
		}
	}
}

// checkOrdered reports an error unless earlier sorts before later as text.
func checkOrdered(earlier, later reqid.ID) error {
	a, b := earlier.String(), later.String()
	if a >= b {
		return fmt.Errorf("%q does not sort before %q", a, b)
	}
	return nil
}

// orderedTimes are milliseconds whose successor carries into each byte of the time, and one of November 2026.
var orderedTimes = []int64{
	0,
	1,
	0x09,
	0x0f,
	0xff,
	0xffff,
	0xffffff,
	0xffffffff,
	0xffffffffff,
	0xfffffffffffe,
	1795000000000,
}

func TestTimeOrdered(t *testing.T) {
	// The earlier ID has the largest random half and the later the smallest: the time alone decides.
	for _, ms := range orderedTimes {
		if err := checkOrdered(reqid.New(ms, 0xffffffff), reqid.New(ms+1, 0)); err != nil {
			t.Errorf("%#x then %#x: %v", ms, ms+1, err)
		}
	}
}

func TestTimeOrderedViolation(t *testing.T) {
	for _, ms := range orderedTimes {
		if checkOrdered(reqid.New(ms+1, 0), reqid.New(ms, 0xffffffff)) == nil {
			t.Errorf("%#x then %#x: a later millisecond sorted first and passed the check", ms+1, ms)
		}
	}
	if checkOrdered(reqid.New(1, 0), reqid.New(1, 0)) == nil {
		t.Error("an ID sorted before itself and passed the check")
	}
}

// checkNoAllocs reports an error when one run of f allocates.
func checkNoAllocs(f func()) error {
	if n := testing.AllocsPerRun(100, f); n != 0 {
		return fmt.Errorf("%v allocations per run, want 0", n)
	}
	return nil
}

func TestAppendTextAllocs(t *testing.T) {
	id := reqid.New(0x0123456789ab, 0xdeadbeef)
	buf := make([]byte, 0, 64)
	err := checkNoAllocs(func() {
		buf, _ = id.AppendText(buf[:0])
	})
	if err != nil {
		t.Errorf("ID.AppendText into a slice of capacity 64: %v", err)
	}
	if string(buf) != "0123456789abdeadbeef" {
		t.Errorf("ID.AppendText wrote %q", buf)
	}
}

func TestParseAllocs(t *testing.T) {
	err := checkNoAllocs(func() {
		sinkID, sinkOK = reqid.Parse("0123456789abdeadbeef")
	})
	if err != nil {
		t.Errorf("Parse: %v", err)
	}
	if !sinkOK || sinkID != reqid.New(0x0123456789ab, 0xdeadbeef) {
		t.Errorf("Parse gave (%v, %t)", sinkID, sinkOK)
	}
}

func TestAppendTextAllocsViolation(t *testing.T) {
	id := reqid.New(0x0123456789ab, 0xdeadbeef)
	err := checkNoAllocs(func() {
		sinkBytes, _ = id.AppendText(nil)
	})
	if err == nil {
		t.Fatal("AppendText onto a nil slice, kept, allocated nothing by the meter")
	}
}

// checkJSONText reports an error unless encoding/json and log/slog's JSON handler write v as the string want.
func checkJSONText(v any, want string) error {
	quoted := `"` + want + `"`
	got, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("json.Marshal(%T) returned the error %v", v, err)
	}
	if string(got) != quoted {
		return fmt.Errorf("json.Marshal(%T) = %s, want %s", v, got, quoted)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("named", "v", v)
	if !strings.Contains(buf.String(), `"v":`+quoted) {
		return fmt.Errorf("slog's JSON handler wrote %T as %s, want \"v\":%s in it", v, strings.TrimSpace(buf.String()), quoted)
	}
	return nil
}

func TestMarshalText(t *testing.T) {
	ids := []reqid.ID{
		reqid.New(0x0123456789ab, 0xdeadbeef),
		reqid.New(1795000000000, 0x00c0ffee),
		{},
	}
	for _, id := range ids {
		got, err := id.MarshalText()
		if err != nil {
			t.Errorf("%v: MarshalText returned the error %v", id, err)
		}
		if string(got) != id.String() {
			t.Errorf("%v: MarshalText = %q, want String's text", id, got)
		}
		if err := checkJSONText(id, id.String()); err != nil {
			t.Error(err)
		}
	}
	if err := checkJSONText(reqid.ID{}, "00000000000000000000"); err != nil {
		t.Errorf("the zero ID: %v", err)
	}
}

// bareID has the shape of an ID and no MarshalText: what encoding/json sees without the method.
type bareID struct{ b [10]byte }

func TestMarshalTextViolation(t *testing.T) {
	err := checkJSONText(bareID{b: [10]byte{1}}, "01000000000000000000")
	if err == nil {
		t.Fatal("a value with no MarshalText passed the check")
	}
	if !strings.Contains(err.Error(), "= {},") {
		t.Errorf("a value with no MarshalText was caught, but not as {}: %v", err)
	}
	if checkJSONText(reqid.New(0x0123456789ab, 0xdeadbeef), "0123456789ABDEADBEEF") == nil {
		t.Fatal("an uppercase text passed the check")
	}
}
