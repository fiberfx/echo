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
	for _, s := range []string{"mxe_cor_23obzcbapvupu2272222", "mxe_cor_22222222222222222222", "zzz_cor_bzzzzzzzzzzzzzzzzzzw", "mxe_cor_23obzcbapvupu2272226", "3ab_cor_23obzcbapv5prncp2222"} {
		c, ok := reqid.ParseCorrelation(s)
		if !ok || c.IsZero() || c.String() != s {
			t.Errorf("ParseCorrelation(%q) = (%q, %t), want the same text and true", s, c, ok)
		}
		if n, ok := reqid.NewCorrelation(c.EID()); !ok || n != c {
			t.Errorf("NewCorrelation(%q) = (%q, %t), want ParseCorrelation's %q", s, n, ok, s)
		}
	}
	refused := []struct{ name, in string }{
		{"empty, which is absent", ""},
		{"kind req", "mxe_req_23obzcbapvupu2272222"},
		{"kind job of another system", "emq_job_23obzcbapvupu2272222"},
		{"length", "mxe_cor_2222222222222222222"},
		{"separator", "mxe-cor_23obzcbapvupu2272222"},
		{"uppercase", "MXE_COR_23OBZCBAPVUPU2272222"},
		{"alphabet", "mxe_cor_23obzcbapvupu227222-"},
		{"pad", "mxe_cor_23obzcbapvupu2272223"},
		{"system-reserved", "2xe_cor_23obzcbapvupu2272222"},
		{"two joined by a comma", "mxe_cor_23obzcbapvupu2272226, mxe_cor_23obzcbapvupu2272222"},
	}
	for _, r := range refused {
		if c, ok := reqid.ParseCorrelation(r.in); ok || !c.IsZero() {
			t.Errorf("%s: ParseCorrelation(%q) = (%q, %t), want the zero Correlation and false", r.name, r.in, c, ok)
		}
	}
}

func TestNewCorrelation(t *testing.T) {
	cor, _ := reqid.ParseCode("cor")
	job, _ := reqid.ParseCode("job")
	mxe, _ := reqid.ParseCode("mxe")
	id, err := reqid.EIDFromParts(mxe, cor, 1795271400123, 895287301, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := reqid.NewCorrelation(id)
	if !ok || c.IsZero() || c.String() != "mxe_cor_23obzcbapvupu2272222" || c.EID() != id {
		t.Errorf("NewCorrelation(%v) = (%q, %t), want mxe_cor_23obzcbapvupu2272222, not zero, and true", id, c, ok)
	}
	other, _ := reqid.EIDFromParts(mxe, job, 1795271400123, 895287301, 0)
	for name, bad := range map[string]reqid.EID{"the zero EID": {}, "a kind other than cor": other} {
		if c, ok := reqid.NewCorrelation(bad); ok || !c.IsZero() {
			t.Errorf("NewCorrelation(%s) = (%q, %t), want the zero Correlation and false", name, c, ok)
		}
	}
	if (reqid.Correlation{}).EID() != (reqid.EID{}) {
		t.Error("the zero Correlation holds a nonzero EID")
	}
}

func TestCorrelationText(t *testing.T) {
	c, _ := reqid.ParseCorrelation("mxe_cor_23obzcbapvupu2272222")
	var zero reqid.Correlation
	for _, row := range []struct {
		c    reqid.Correlation
		want string
	}{{c, "mxe_cor_23obzcbapvupu2272222"}, {zero, ""}} {
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
		{"a member among others, with a property", []string{"other=1, bcs.correlation.id=mxe_cor_23obzcbapvupu2272226;p=1"}, "", "mxe_cor_23obzcbapvupu2272226"},
		{"baggage and header both valid", []string{"bcs.correlation.id=mxe_cor_23obzcbapvupu2272226"}, "mxe_cor_23obzcbapvupu2272222", "mxe_cor_23obzcbapvupu2272226"},
		{"an invalid member gives way to the header", []string{"bcs.correlation.id=mxe_req_23obzcbapvupu2272222"}, "mxe_cor_22222222222222222222", "mxe_cor_22222222222222222222"},
		{"nothing", nil, "", ""},
		{"an empty value and a garbage header", []string{""}, "garbage", ""},
		{"a longer key", []string{"bcs.correlation.idx=mxe_cor_23obzcbapvupu2272226"}, "", ""},
		{"spaces and tabs around the key and the value", []string{" \tbcs.correlation.id \t=\t mxe_cor_23obzcbapvupu2272226 \t;p"}, "", "mxe_cor_23obzcbapvupu2272226"},
		{"the second Baggage value", []string{"a=1", "b=2,bcs.correlation.id=mxe_cor_23obzcbapvupu2272226"}, "", "mxe_cor_23obzcbapvupu2272226"},
		{"the first member with the key decides", []string{"bcs.correlation.id=garbage, bcs.correlation.id=mxe_cor_23obzcbapvupu2272226"}, "", ""},
		{"a member with no value", []string{"bcs.correlation.id"}, "mxe_cor_23obzcbapvupu2272222", "mxe_cor_23obzcbapvupu2272222"},
		{"the header alone", nil, "mxe_cor_23obzcbapvupu2272222", "mxe_cor_23obzcbapvupu2272222"},
		{"a header of another brand", nil, "mxe_req_23obzcbapvupu2272222", ""},
		{"a header of two joined by a comma", nil, "mxe_cor_23obzcbapvupu2272226, mxe_cor_23obzcbapvupu2272222", ""},
		{"empty members around the one", []string{",, bcs.correlation.id=mxe_cor_23obzcbapvupu2272226 ,"}, "", "mxe_cor_23obzcbapvupu2272226"},
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
		sinkCorrelation, sinkOK = reqid.ParseCorrelation("mxe_cor_23obzcbapvupu2272222")
	})
	if err != nil {
		t.Errorf("ParseCorrelation: %v", err)
	}
	if !sinkOK || sinkCorrelation.String() != "mxe_cor_23obzcbapvupu2272222" {
		t.Errorf("ParseCorrelation gave (%q, %t)", sinkCorrelation, sinkOK)
	}

	baggage := []string{"other=1", " k=v;p, bcs.correlation.id = mxe_cor_23obzcbapvupu2272226 ;p=1"}
	if err := checkNoAllocs(func() { sinkCorrelation = reqid.CorrelationOf(baggage, "mxe_cor_23obzcbapvupu2272222") }); err != nil {
		t.Errorf("CorrelationOf: %v", err)
	}
	if sinkCorrelation.String() != "mxe_cor_23obzcbapvupu2272226" {
		t.Errorf("CorrelationOf gave %q", sinkCorrelation)
	}

	buf := make([]byte, 0, 64)
	if err := checkNoAllocs(func() { buf, _ = sinkCorrelation.AppendText(buf[:0]) }); err != nil {
		t.Errorf("Correlation.AppendText into a slice of capacity 64: %v", err)
	}
	if string(buf) != "mxe_cor_23obzcbapvupu2272226" {
		t.Errorf("Correlation.AppendText wrote %q", buf)
	}
}

// TestCorrelationOfPercentDecoded: W3C Baggage lets a producer percent-encode a byte that needs no escape, so an
// encoded COR is the same COR; a malformed escape or a value that does not decode to 28 bytes is refused, and the
// header then gives the COR.
func TestCorrelationOfPercentDecoded(t *testing.T) {
	const header = "mxe_cor_23obzcbapvupu2272222"
	for _, c := range []struct {
		name, value, want string
	}{
		{"the first byte encoded", "%6dxe_cor_23obzcbapvupu2272226", "mxe_cor_23obzcbapvupu2272226"},
		{"two bytes encoded, lowercase hex", "%6d%78e_cor_23obzcbapvupu2272226", "mxe_cor_23obzcbapvupu2272226"},
		{"plain", "mxe_cor_23obzcbapvupu2272226", "mxe_cor_23obzcbapvupu2272226"},
		{"an escape cut short", "mxe_cor_23obzcbapvupu227222%3", header},
		{"a bad hex digit", "%6Gxe_cor_23obzcbapvupu2272226", header},
		{"29 bytes after decoding", "%6dxe_cor_23obzcbapvupu22722262", header},
		{"27 bytes after decoding", "%6dxe_cor_23obzcbapvupu227222", header},
		{"an encoded byte outside the alphabet", "mxe_cor_23obzcbapvupu22722%2D", header},
	} {
		got := reqid.CorrelationOf([]string{"bcs.correlation.id=" + c.value}, header)
		if got.String() != c.want {
			t.Errorf("%s: CorrelationOf(%q) = %q, want %q", c.name, c.value, got, c.want)
		}
	}
	baggage := []string{"bcs.correlation.id=%6dxe_cor_23obzcbapvupu2272226"}
	if err := checkNoAllocs(func() { sinkCorrelation = reqid.CorrelationOf(baggage, "") }); err != nil {
		t.Errorf("CorrelationOf of a percent-encoded value: %v", err)
	}
	if sinkCorrelation.String() != "mxe_cor_23obzcbapvupu2272226" {
		t.Errorf("CorrelationOf of a percent-encoded value gave %q", sinkCorrelation)
	}
}
