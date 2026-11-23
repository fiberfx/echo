// SPDX-License-Identifier: MIT

package reqid_test

import (
	"fmt"
	"testing"

	"github.com/fiberfx/mx-echo/v7/reqid"
)

var sinkRead reqid.CorrelationRead

// reqOf builds the text of a valid eid of kind req from system mxe.
func reqOf(t testing.TB) string {
	t.Helper()
	sys, _ := reqid.ParseCode("mxe")
	kind, _ := reqid.ParseCode("req")
	id, err := reqid.EIDFromParts(sys, kind, 1795271400123, 895287301, 0)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// refusedAs derives from the valid text s, by changing one byte, a text that ParseEID refuses with want.
func refusedAs(t testing.TB, s string, want reqid.Refusal) string {
	t.Helper()
	candidates := reqid.Alphabet + "-A"
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(candidates); j++ {
			b := []byte(s)
			b[i] = candidates[j]
			if _, err := reqid.ParseEID(string(b)); err == want {
				return string(b)
			}
		}
	}
	t.Fatalf("no one-byte change of %q is refused as %q", s, want)
	return ""
}

// escaped writes every byte of s as %XX.
func escaped(s string) string {
	out := make([]byte, 0, 3*len(s))
	for i := 0; i < len(s); i++ {
		out = fmt.Appendf(out, "%%%02x", s[i])
	}
	return string(out)
}

func bag(value string) []string { return []string{"other=1, bcs.correlation.id=" + value + ";p=1"} }

func TestReadCorrelation(t *testing.T) {
	_, mxe := corOf(t, "mxe")
	_, mxe2 := corOf(t, "3ab")
	_, ecd := corOf(t, "ecd")
	req := reqOf(t)
	refusals := map[reqid.Refusal]string{}
	for _, r := range []reqid.Refusal{reqid.ErrSeparator, reqid.ErrAlphabet, reqid.ErrPad, reqid.ErrReserved, reqid.ErrSystemReserved, reqid.ErrKind} {
		refusals[r] = refusedAs(t, mxe, r)
	}
	refusals[reqid.ErrLength] = mxe[:len(mxe)-1]

	type carrier struct {
		name   string
		value  string
		reason reqid.CorrelationReason
	}
	// Each carrier value and the reason it must give; Baggage values are given through the member, the header as is.
	values := []carrier{
		{"ok", mxe, reqid.ReasonOK},
		{"ok detached", mxe2, reqid.ReasonOK},
		{"cor-kind", req, reqid.ReasonCorKind},
		{"cor-system", ecd, reqid.ReasonCorSystem},
	}
	for r, text := range refusals {
		values = append(values, carrier{string(r), text, reqid.CorrelationReason(r)})
	}
	for _, name := range []string{"length", "separator", "alphabet", "pad", "reserved", "system-reserved", "kind"} {
		if refusals[reqid.Refusal(name)] == "" {
			t.Fatalf("no text for the refusal %q", name)
		}
	}

	for _, v := range values {
		for _, c := range []string{v.value, escaped(v.value)} {
			got := reqid.ReadCorrelation(bag(c), "")
			if got.Baggage != v.reason || got.Header != reqid.ReasonAbsent {
				t.Errorf("baggage %s %q: reasons (%s, %s), want (%s, absent)", v.name, c, got.Baggage, got.Header, v.reason)
			}
			wantCor, wantSrc := "", reqid.CorrelationFromNone
			if v.reason == reqid.ReasonOK {
				wantCor, wantSrc = v.value, reqid.CorrelationFromBaggage
			}
			if got.Correlation.String() != wantCor || got.Source != wantSrc {
				t.Errorf("baggage %s %q: (%q, %s), want (%q, %s)", v.name, c, got.Correlation, got.Source, wantCor, wantSrc)
			}
		}
		got := reqid.ReadCorrelation(nil, v.value)
		if got.Header != v.reason || got.Baggage != reqid.ReasonAbsent {
			t.Errorf("header %s %q: reasons (%s, %s), want (absent, %s)", v.name, v.value, got.Baggage, got.Header, v.reason)
		}
		wantCor, wantSrc := "", reqid.CorrelationFromNone
		if v.reason == reqid.ReasonOK {
			wantCor, wantSrc = v.value, reqid.CorrelationFromHeader
		}
		if got.Correlation.String() != wantCor || got.Source != wantSrc {
			t.Errorf("header %s %q: (%q, %s), want (%q, %s)", v.name, v.value, got.Correlation, got.Source, wantCor, wantSrc)
		}
		// CorrelationOf is the same COR.
		if of := reqid.CorrelationOf(bag(v.value), v.value); of != reqid.ReadCorrelation(bag(v.value), v.value).Correlation {
			t.Errorf("%s: CorrelationOf %q differs from ReadCorrelation's", v.name, of)
		}
	}
}

func TestReadCorrelationAbsent(t *testing.T) {
	_, mxe := corOf(t, "mxe")
	for _, b := range [][]string{nil, {""}, {"other=1"}, {"bcs.correlation.id="}, {"bcs.correlation.id = \t ;p=1"}, {"x=" + mxe}} {
		got := reqid.ReadCorrelation(b, "")
		if got.Baggage != reqid.ReasonAbsent || got.Header != reqid.ReasonAbsent || !got.Correlation.IsZero() || got.Source != reqid.CorrelationFromNone {
			t.Errorf("ReadCorrelation(%q, \"\") = %+v, want absent, absent, none", b, got)
		}
	}
}

func TestReadCorrelationEscapeAndLength(t *testing.T) {
	_, mxe := corOf(t, "mxe")
	cases := []struct {
		name, value string
		want        reqid.CorrelationReason
	}{
		{"bad hex digit", "%zz" + mxe[1:], reqid.ReasonEscape},
		{"truncated at the end", mxe + "%4", reqid.ReasonEscape},
		{"lone percent at the end", mxe + "%", reqid.ReasonEscape},
		{"bad escape after the length is exceeded", mxe + "ab%zz", reqid.ReasonEscape},
		{"bad escape in a short value", "%4", reqid.ReasonEscape},
		{"decoded one byte too long", escaped(mxe) + "2", "length"},
		{"decoded one byte too long by an escape", escaped(mxe) + "%32", "length"},
		{"decoded one byte too short", escaped(mxe[1:]), "length"},
		{"decoded much too long", escaped(mxe) + escaped(mxe), "length"},
		{"percent only", "%", reqid.ReasonEscape},
		{"decoded to nothing useful", "%20%20", "length"},
		{"escaped separator", mxe[:3] + "%2d" + mxe[4:], "separator"},
		{"decoded ok", "%" + fmt.Sprintf("%02x", mxe[0]) + mxe[1:], reqid.ReasonOK},
	}
	for _, c := range cases {
		got := reqid.ReadCorrelation(bag(c.value), "")
		if got.Baggage != c.want {
			t.Errorf("%s: %q gave %q, want %q", c.name, c.value, got.Baggage, c.want)
		}
	}
}

func TestReadCorrelationCarriers(t *testing.T) {
	_, mxe := corOf(t, "mxe")
	_, other := corOf(t, "3ab")
	_, ecd := corOf(t, "ecd")
	req := reqOf(t)
	cases := []struct {
		name, baggage, header string
		wantCor, wantSrc      string
		wantBag, wantHdr      reqid.CorrelationReason
	}{
		{"baggage ok, header ok: baggage wins", mxe, other, mxe, "baggage", reqid.ReasonOK, reqid.ReasonOK},
		{"baggage ok, header invalid", mxe, mxe[:10], mxe, "baggage", reqid.ReasonOK, "length"},
		{"baggage ok, header cor-system", mxe, ecd, mxe, "baggage", reqid.ReasonOK, reqid.ReasonCorSystem},
		{"baggage ok, header absent", mxe, "", mxe, "baggage", reqid.ReasonOK, reqid.ReasonAbsent},
		{"baggage invalid, header ok", mxe[:10], mxe, mxe, "header", "length", reqid.ReasonOK},
		{"baggage cor-kind, header ok", req, mxe, mxe, "header", reqid.ReasonCorKind, reqid.ReasonOK},
		{"baggage cor-system, header ok", ecd, other, other, "header", reqid.ReasonCorSystem, reqid.ReasonOK},
		{"baggage escape, header ok", "%zz", mxe, mxe, "header", reqid.ReasonEscape, reqid.ReasonOK},
		{"baggage invalid, header invalid", mxe[:10], req, "", "none", "length", reqid.ReasonCorKind},
	}
	for _, c := range cases {
		got := reqid.ReadCorrelation(bag(c.baggage), c.header)
		if got.Correlation.String() != c.wantCor || got.Source.String() != c.wantSrc || got.Baggage != c.wantBag || got.Header != c.wantHdr {
			t.Errorf("%s: got (%q, %s, %s, %s), want (%q, %s, %s, %s)", c.name,
				got.Correlation, got.Source, got.Baggage, got.Header, c.wantCor, c.wantSrc, c.wantBag, c.wantHdr)
		}
	}
}

func TestReadCorrelationSourceNames(t *testing.T) {
	for s, want := range map[reqid.CorrelationSource]string{reqid.CorrelationFromNone: "none", reqid.CorrelationFromBaggage: "baggage", reqid.CorrelationFromHeader: "header", 9: "none"} {
		if s.String() != want {
			t.Errorf("CorrelationSource(%d).String() = %q, want %q", uint8(s), s.String(), want)
		}
	}
}

func TestReadCorrelationAllocs(t *testing.T) {
	_, mxe := corOf(t, "mxe")
	_, ecd := corOf(t, "ecd")
	cases := []struct {
		name    string
		baggage []string
		header  string
	}{
		{"plain", bag(mxe), mxe},
		{"escaped", bag(escaped(mxe)), ecd},
		{"bad escape", bag("%zz"), ""},
		{"wrong length", bag(escaped(mxe) + "2"), "short"},
		{"absent", nil, ""},
	}
	for _, c := range cases {
		if err := checkNoAllocs(func() { sinkRead = reqid.ReadCorrelation(c.baggage, c.header) }); err != nil {
			t.Errorf("ReadCorrelation %s: %v", c.name, err)
		}
	}
}
