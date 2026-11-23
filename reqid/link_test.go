// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package reqid_test

import (
	"encoding"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v7/reqid"
)

var _ encoding.TextAppender = reqid.Link{}

var _ encoding.TextMarshaler = reqid.Link{}

var _ fmt.Stringer = reqid.Link{}

// The example of the W3C Trace Context recommendation, and its parts.
const (
	w3cTrace   = "4bf92f3577b34da6a3ce929d0e0e4736"
	w3cSpan    = "00f067aa0ba902b7"
	w3cExample = "00-" + w3cTrace + "-" + w3cSpan + "-01"
	zeroTrace  = "00000000000000000000000000000000"
	zeroSpan   = "0000000000000000"
)

// checkParseTraceparent runs the table of ParseTraceparent against parse and reports the first row it gets wrong.
func checkParseTraceparent(parse func(string) (reqid.Link, bool)) error {
	accepted := []struct {
		name string
		in   string
		want string
	}{
		{"the W3C example", w3cExample, w3cExample},
		{"flags 00", "00-" + w3cTrace + "-" + w3cSpan + "-00", "00-" + w3cTrace + "-" + w3cSpan + "-00"},
		{"flags ff", "00-" + w3cTrace + "-" + w3cSpan + "-ff", "00-" + w3cTrace + "-" + w3cSpan + "-ff"},
		{"one bit of trace and of span", "00-00000000000000000000000000000001-8000000000000000-00", "00-00000000000000000000000000000001-8000000000000000-00"},
		{"every digit", "00-0123456789abcdef0123456789abcdef-fedcba9876543210-a9", "00-0123456789abcdef0123456789abcdef-fedcba9876543210-a9"},
		{"version 01 with a suffix", "01-" + w3cTrace + "-" + w3cSpan + "-01-suffix", w3cExample},
		{"version 01 with an empty suffix", "01-" + w3cTrace + "-" + w3cSpan + "-01-", w3cExample},
		{"version 01 of 55 bytes", "01-" + w3cTrace + "-" + w3cSpan + "-01", w3cExample},
		{"version fe", "fe-" + w3cTrace + "-" + w3cSpan + "-01", w3cExample},
	}
	for _, c := range accepted {
		l, ok := parse(c.in)
		if !ok {
			return fmt.Errorf("%s: ParseTraceparent(%q) was refused", c.name, c.in)
		}
		if l.IsZero() {
			return fmt.Errorf("%s: ParseTraceparent(%q) gave the zero Link and true", c.name, c.in)
		}
		if got := l.String(); got != c.want {
			return fmt.Errorf("%s: ParseTraceparent(%q) prints back %q, want %q", c.name, c.in, got, c.want)
		}
		got, err := l.AppendText([]byte("link="))
		if err != nil {
			return fmt.Errorf("%s: AppendText returned the error %v", c.name, err)
		}
		if string(got) != "link="+c.want {
			return fmt.Errorf("%s: AppendText onto %q = %q, want %q", c.name, "link=", got, "link="+c.want)
		}
	}
	refused := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"54 bytes", w3cExample[:54]},
		{"uppercase hex in the trace", "00-" + strings.ToUpper(w3cTrace) + "-" + w3cSpan + "-01"},
		{"uppercase hex in the span", "00-" + w3cTrace + "-" + strings.ToUpper(w3cSpan) + "-01"},
		{"uppercase hex in the flags", "00-" + w3cTrace + "-" + w3cSpan + "-0A"},
		{"uppercase hex in the version", "0A-" + w3cTrace + "-" + w3cSpan + "-01"},
		{"an all-zero trace", "00-" + zeroTrace + "-" + w3cSpan + "-01"},
		{"an all-zero span", "00-" + w3cTrace + "-" + zeroSpan + "-01"},
		{"version ff", "ff-" + w3cTrace + "-" + w3cSpan + "-01"},
		{"version ff with a suffix", "ff-" + w3cTrace + "-" + w3cSpan + "-01-suffix"},
		{"version 00 with a suffix", w3cExample + "-suffix"},
		{"version 00 with a trailing dash", w3cExample + "-"},
		{"version 01 with no dash at 55", "01-" + w3cTrace + "-" + w3cSpan + "-010"},
		{"a letter after f in the trace", "00-" + w3cTrace[:31] + "g-" + w3cSpan + "-01"},
		{"a letter after f in the version", "0g-" + w3cTrace + "-" + w3cSpan + "-01"},
		{"a dash one byte late", "00-" + w3cTrace + "0-" + w3cSpan[1:] + "-01"},
		{"underscores for dashes", "00_" + w3cTrace + "_" + w3cSpan + "_01"},
		{"a leading space", " " + w3cExample},
		{"a trailing space", w3cExample + " "},
		{"only the trace", w3cTrace},
	}
	for _, c := range refused {
		l, ok := parse(c.in)
		if ok {
			return fmt.Errorf("%s: ParseTraceparent(%q) was accepted", c.name, c.in)
		}
		if !l.IsZero() {
			return fmt.Errorf("%s: ParseTraceparent(%q) was refused with the non-zero Link %v", c.name, c.in, l)
		}
	}
	return nil
}

func TestParseTraceparent(t *testing.T) {
	if w3cExample != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("the example is spelled %q", w3cExample)
	}
	if err := checkParseTraceparent(reqid.ParseTraceparent); err != nil {
		t.Error(err)
	}

	// A Link is comparable, and keeps the trace, the span and the flags: not the version, not the suffix.
	a, _ := reqid.ParseTraceparent(w3cExample)
	b, _ := reqid.ParseTraceparent("01-" + w3cTrace + "-" + w3cSpan + "-01-suffix")
	if a != b {
		t.Errorf("the same trace, span and flags under versions 00 and 01 are two Links: %v and %v", a, b)
	}
	c, _ := reqid.ParseTraceparent("00-" + w3cTrace + "-" + w3cSpan + "-00")
	if a == c {
		t.Error("two Links that differ in their flags are equal")
	}

	// The zero Link means none, and its text is never read back as a link.
	var zero reqid.Link
	if !zero.IsZero() {
		t.Error("the zero value is not the zero Link")
	}
	if l, ok := reqid.ParseTraceparent(zero.String()); ok || !l.IsZero() {
		t.Errorf("the zero Link's text %q parsed as a link", zero.String())
	}
}

func TestParseTraceparentViolation(t *testing.T) {
	lenient := []struct {
		row   string
		parse func(string) (reqid.Link, bool)
	}{
		{"an all-zero trace", func(s string) (reqid.Link, bool) {
			return reqid.ParseTraceparent(strings.Replace(s, "-"+zeroTrace+"-", "-"+w3cTrace+"-", 1))
		}},
		{"an all-zero span", func(s string) (reqid.Link, bool) {
			return reqid.ParseTraceparent(strings.Replace(s, "-"+zeroSpan+"-", "-"+w3cSpan+"-", 1))
		}},
		{"uppercase hex in the trace", func(s string) (reqid.Link, bool) {
			return reqid.ParseTraceparent(strings.ToLower(s))
		}},
		{"version ff", func(s string) (reqid.Link, bool) {
			return reqid.ParseTraceparent(strings.Replace(s, "ff-", "fe-", 1))
		}},
		{"54 bytes", func(s string) (reqid.Link, bool) {
			if len(s) == 54 {
				s += "1"
			}
			return reqid.ParseTraceparent(s)
		}},
		{"version 00 with a suffix", func(s string) (reqid.Link, bool) {
			if len(s) > 55 && s[55] == '-' {
				s = s[:55]
			}
			return reqid.ParseTraceparent(s)
		}},
	}
	for _, c := range lenient {
		err := checkParseTraceparent(c.parse)
		if err == nil {
			t.Errorf("a parser that accepts the row %q passed the table", c.row)
			continue
		}
		if !strings.HasPrefix(err.Error(), c.row+":") {
			t.Errorf("a parser that accepts the row %q was caught on another: %v", c.row, err)
		}
	}
}

func TestLinkAppendTextAllocs(t *testing.T) {
	l, ok := reqid.ParseTraceparent(w3cExample)
	if !ok {
		t.Fatal("the W3C example was refused")
	}
	buf := make([]byte, 0, 64)
	err := checkNoAllocs(func() {
		buf, _ = l.AppendText(buf[:0])
	})
	if err != nil {
		t.Errorf("Link.AppendText into a slice of capacity 64: %v", err)
	}
	if string(buf) != w3cExample {
		t.Errorf("Link.AppendText wrote %q", buf)
	}
}

func TestParseTraceparentAllocs(t *testing.T) {
	err := checkNoAllocs(func() {
		sinkLink, sinkOK = reqid.ParseTraceparent(w3cExample)
	})
	if err != nil {
		t.Errorf("ParseTraceparent: %v", err)
	}
	if !sinkOK || sinkLink.String() != w3cExample {
		t.Errorf("ParseTraceparent gave (%v, %t)", sinkLink, sinkOK)
	}
}

func TestLinkMarshalText(t *testing.T) {
	l, ok := reqid.ParseTraceparent(w3cExample)
	if !ok {
		t.Fatal("the W3C example was refused")
	}
	for _, l := range []reqid.Link{l, {}} {
		got, err := l.MarshalText()
		if err != nil {
			t.Errorf("%v: MarshalText returned the error %v", l, err)
		}
		if string(got) != l.String() {
			t.Errorf("%v: MarshalText = %q, want String's text", l, got)
		}
		if err := checkJSONText(l, l.String()); err != nil {
			t.Error(err)
		}
	}
	if err := checkJSONText(l, w3cExample); err != nil {
		t.Error(err)
	}
	if err := checkJSONText(reqid.Link{}, "00-"+zeroTrace+"-"+zeroSpan+"-00"); err != nil {
		t.Errorf("the zero Link: %v", err)
	}

	// A struct that holds both, as an observation does, is written with both texts.
	pair := struct {
		ID   reqid.ID
		Link reqid.Link
	}{reqid.New(0x0123456789ab, 0xdeadbeef), l}
	got, err := json.Marshal(pair)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"ID":"0123456789abdeadbeef","Link":"` + w3cExample + `"}`; string(got) != want {
		t.Errorf("json.Marshal of a struct with an ID and a Link = %s, want %s", got, want)
	}
}

// bareLink has the shape of a Link and no MarshalText: what encoding/json sees without the method.
type bareLink struct {
	trace [16]byte
	span  [8]byte
	flags byte
}

func TestLinkMarshalTextViolation(t *testing.T) {
	err := checkJSONText(bareLink{trace: [16]byte{1}, span: [8]byte{1}, flags: 1}, w3cExample)
	if err == nil {
		t.Fatal("a value with no MarshalText passed the check")
	}
	if !strings.Contains(err.Error(), "= {},") {
		t.Errorf("a value with no MarshalText was caught, but not as {}: %v", err)
	}
}
