// SPDX-License-Identifier: MIT

package reqid_test

import (
	"encoding"
	"fmt"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

var _ encoding.TextAppender = reqid.Correlation{}

var _ encoding.TextMarshaler = reqid.Correlation{}

var _ fmt.Stringer = reqid.Correlation{}

var sinkCorrelation reqid.Correlation

func TestCorrelationNames(t *testing.T) {
	names := []struct{ got, want string }{
		{reqid.CorrelationBaggageKey, "bcs.correlation.id"},
		{reqid.CorrelationHeader, "X-Correlation-ID"},
		{reqid.CorrelationEnv, "X_CORRELATION_ID"},
		{reqid.CorrelationMeta, "x-correlation-id"},
	}
	for _, n := range names {
		if n.got != n.want {
			t.Errorf("a carrier is named %q, want %q", n.got, n.want)
		}
	}
}

func TestParseCorrelation(t *testing.T) {
	for _, s := range []string{"COR0SCMa0vxXFZ", "COR00000000000", "CORAzL8n0Y58m7", "COR0SF1bsgvgpc"} {
		c, ok := reqid.ParseCorrelation(s)
		if !ok || c.IsZero() || c.String() != s {
			t.Errorf("ParseCorrelation(%q) = (%q, %t), want the same text and true", s, c, ok)
		}
		_, snowflake, _ := reqid.DecodeBranded(s)
		if n, ok := reqid.NewCorrelation(snowflake); !ok || n != c {
			t.Errorf("NewCorrelation(%d) = (%q, %t), want ParseCorrelation's %q", snowflake, n, ok, s)
		}
	}
	refused := []struct{ name, in string }{
		{"empty, which is absent", ""},
		{"brand USR", "USR0KHTOWnGLuC"},
		{"brand KAN", "KAN0R98RM39MWY"},
		{"length", "COR0000000000"},
		{"namespace", "cOR00000000000"},
		{"charset", "COR0000000000-"},
		{"range", "CORAzL8n0Y58m8"},
		{"two joined by a comma", "COR0SF1bsgvgpc, COR0SCMa0vxXFZ"},
	}
	for _, r := range refused {
		if c, ok := reqid.ParseCorrelation(r.in); ok || !c.IsZero() {
			t.Errorf("%s: ParseCorrelation(%q) = (%q, %t), want the zero Correlation and false", r.name, r.in, c, ok)
		}
	}
}

func TestNewCorrelation(t *testing.T) {
	c, ok := reqid.NewCorrelation(0)
	if !ok || c.IsZero() || c.String() != "COR00000000000" {
		t.Errorf("NewCorrelation(0) = (%q, %t), want COR00000000000, not zero, and true", c, ok)
	}
	for _, s := range []uint64{1 << 63, 1<<64 - 1} {
		if c, ok := reqid.NewCorrelation(s); ok || !c.IsZero() {
			t.Errorf("NewCorrelation(%d) = (%q, %t), want the zero Correlation and false", s, c, ok)
		}
	}
}

func TestCorrelationText(t *testing.T) {
	c, _ := reqid.ParseCorrelation("COR0SCMa0vxXFZ")
	var zero reqid.Correlation
	for _, row := range []struct {
		c    reqid.Correlation
		want string
	}{{c, "COR0SCMa0vxXFZ"}, {zero, ""}} {
		if got := row.c.String(); got != row.want {
			t.Errorf("String() = %q, want %q", got, row.want)
		}
		got, err := row.c.AppendText([]byte("cor="))
		if err != nil || string(got) != "cor="+row.want {
			t.Errorf("AppendText onto %q = (%q, %v), want %q", "cor=", got, err, "cor="+row.want)
		}
		text, err := row.c.MarshalText()
		if err != nil || string(text) != row.want {
			t.Errorf("MarshalText() = (%q, %v), want %q", text, err, row.want)
		}
		if err := checkJSONText(row.c, row.want); err != nil {
			t.Error(err)
		}
	}
	if !zero.IsZero() || c.IsZero() {
		t.Errorf("IsZero: the zero value %t, a parsed COR %t", zero.IsZero(), c.IsZero())
	}
}

// checkCorrelationOf runs the table of CorrelationOf against of and reports the first row it gets wrong.
func checkCorrelationOf(of func([]string, string) reqid.Correlation) error {
	cases := []struct {
		name    string
		baggage []string
		header  string
		want    string
	}{
		{"a member among others, with a property", []string{"other=1, bcs.correlation.id=COR0SF1bsgvgpc;p=1"}, "", "COR0SF1bsgvgpc"},
		{"baggage and header both valid", []string{"bcs.correlation.id=COR0SF1bsgvgpc"}, "COR0SCMa0vxXFZ", "COR0SF1bsgvgpc"},
		{"an invalid member gives way to the header", []string{"bcs.correlation.id=USR0KHTOWnGLuC"}, "COR00000000000", "COR00000000000"},
		{"nothing", nil, "", ""},
		{"an empty value and a garbage header", []string{""}, "garbage", ""},
		{"a longer key", []string{"bcs.correlation.idx=COR0SF1bsgvgpc"}, "", ""},
		{"spaces and tabs around the key and the value", []string{" \tbcs.correlation.id \t=\t COR0SF1bsgvgpc \t;p"}, "", "COR0SF1bsgvgpc"},
		{"the second Baggage value", []string{"a=1", "b=2,bcs.correlation.id=COR0SF1bsgvgpc"}, "", "COR0SF1bsgvgpc"},
		{"the first member with the key decides", []string{"bcs.correlation.id=garbage, bcs.correlation.id=COR0SF1bsgvgpc"}, "", ""},
		{"a member with no value", []string{"bcs.correlation.id"}, "COR0SCMa0vxXFZ", "COR0SCMa0vxXFZ"},
		{"the header alone", nil, "COR0SCMa0vxXFZ", "COR0SCMa0vxXFZ"},
		{"a header of another brand", nil, "USR0KHTOWnGLuC", ""},
		{"a header of two joined by a comma", nil, "COR0SF1bsgvgpc, COR0SCMa0vxXFZ", ""},
		{"empty members around the one", []string{",, bcs.correlation.id=COR0SF1bsgvgpc ,"}, "", "COR0SF1bsgvgpc"},
	}
	for _, c := range cases {
		if got := of(c.baggage, c.header).String(); got != c.want {
			return fmt.Errorf("%s: CorrelationOf(%q, %q) = %q, want %q", c.name, c.baggage, c.header, got, c.want)
		}
	}
	return nil
}

func TestCorrelationOf(t *testing.T) {
	if err := checkCorrelationOf(reqid.CorrelationOf); err != nil {
		t.Error(err)
	}
}

func TestCorrelationOfViolation(t *testing.T) {
	wrong := []struct {
		row string
		of  func([]string, string) reqid.Correlation
	}{
		{"baggage and header both valid", func(baggage []string, header string) reqid.Correlation {
			// The header first: a rule that inverts the platform's precedence.
			if c, ok := reqid.ParseCorrelation(header); ok {
				return c
			}
			return reqid.CorrelationOf(baggage, "")
		}},
		{"spaces and tabs around the key and the value", func(baggage []string, header string) reqid.Correlation {
			// Spaces trimmed and tabs kept: a member padded with a tab is missed.
			for _, list := range baggage {
				for _, member := range strings.Split(list, ",") {
					if v, ok := strings.CutPrefix(strings.Trim(member, " "), "bcs.correlation.id="); ok {
						v, _, _ = strings.Cut(v, ";")
						if c, ok := reqid.ParseCorrelation(v); ok {
							return c
						}
					}
				}
			}
			return reqid.CorrelationOf(nil, header)
		}},
	}
	for _, c := range wrong {
		err := checkCorrelationOf(c.of)
		if err == nil {
			t.Errorf("a rule wrong on the row %q passed the table", c.row)
			continue
		}
		if !strings.HasPrefix(err.Error(), c.row+":") {
			t.Errorf("a rule wrong on the row %q was caught on another: %v", c.row, err)
		}
	}
}

func TestCorrelationAllocs(t *testing.T) {
	err := checkNoAllocs(func() {
		sinkCorrelation, sinkOK = reqid.ParseCorrelation("COR0SCMa0vxXFZ")
	})
	if err != nil {
		t.Errorf("ParseCorrelation: %v", err)
	}
	if !sinkOK || sinkCorrelation.String() != "COR0SCMa0vxXFZ" {
		t.Errorf("ParseCorrelation gave (%q, %t)", sinkCorrelation, sinkOK)
	}

	baggage := []string{"other=1", " k=v;p, bcs.correlation.id = COR0SF1bsgvgpc ;p=1"}
	if err := checkNoAllocs(func() { sinkCorrelation = reqid.CorrelationOf(baggage, "COR0SCMa0vxXFZ") }); err != nil {
		t.Errorf("CorrelationOf: %v", err)
	}
	if sinkCorrelation.String() != "COR0SF1bsgvgpc" {
		t.Errorf("CorrelationOf gave %q", sinkCorrelation)
	}

	buf := make([]byte, 0, 64)
	if err := checkNoAllocs(func() { buf, _ = sinkCorrelation.AppendText(buf[:0]) }); err != nil {
		t.Errorf("Correlation.AppendText into a slice of capacity 64: %v", err)
	}
	if string(buf) != "COR0SF1bsgvgpc" {
		t.Errorf("Correlation.AppendText wrote %q", buf)
	}
}

// TestCorrelationOfPercentDecoded: W3C Baggage lets a producer percent-encode a byte that needs no escape, so an
// encoded COR is the same COR; a malformed escape or a value that does not decode to 14 bytes is refused, and the
// header then gives the COR.
func TestCorrelationOfPercentDecoded(t *testing.T) {
	const header = "COR0SCMa0vxXFZ"
	for _, c := range []struct {
		name, value, want string
	}{
		{"the first byte encoded", "%43OR0SF1bsgvgpc", "COR0SF1bsgvgpc"},
		{"two bytes encoded, lowercase hex", "%43%4fR0SF1bsgvgpc", "COR0SF1bsgvgpc"},
		{"plain", "COR0SF1bsgvgpc", "COR0SF1bsgvgpc"},
		{"an escape cut short", "COR0SF1bsgvgp%4", header},
		{"a bad hex digit", "%4GOR0SF1bsgvgpc", header},
		{"15 bytes after decoding", "%43OR0SF1bsgvgpc0", header},
		{"13 bytes after decoding", "%43OR0SF1bsgvgp", header},
		{"an encoded byte that is not Base62", "COR0SF1bsgvg%2Fc", header},
	} {
		got := reqid.CorrelationOf([]string{"bcs.correlation.id=" + c.value}, header)
		if got.String() != c.want {
			t.Errorf("%s: CorrelationOf(%q) = %q, want %q", c.name, c.value, got, c.want)
		}
	}
	baggage := []string{"bcs.correlation.id=%43OR0SF1bsgvgpc"}
	if err := checkNoAllocs(func() { sinkCorrelation = reqid.CorrelationOf(baggage, "") }); err != nil {
		t.Errorf("CorrelationOf of a percent-encoded value: %v", err)
	}
	if sinkCorrelation.String() != "COR0SF1bsgvgpc" {
		t.Errorf("CorrelationOf of a percent-encoded value gave %q", sinkCorrelation)
	}
}
