// SPDX-License-Identifier: MIT

package reqid

import (
	"os"
	"strings"
	"testing"
)

// unescapeField decodes "\t" and "\\" of a field of correlation_of.tsv; with splitBar it also splits on an unescaped "|"
// and decodes "\|", as the baggage column defines.
func unescapeField(t *testing.T, s string, splitBar bool) []string {
	t.Helper()
	parts := []string{}
	var cur []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == 't':
			cur, i = append(cur, '\t'), i+1
		case c == '\\' && i+1 < len(s) && s[i+1] == '\\':
			cur, i = append(cur, '\\'), i+1
		case c == '\\' && splitBar && i+1 < len(s) && s[i+1] == '|':
			cur, i = append(cur, '|'), i+1
		case c == '\\':
			t.Fatalf("unknown escape at %d of %q", i, s)
		case c == '|' && splitBar:
			parts, cur = append(parts, string(cur)), nil
		default:
			cur = append(cur, c)
		}
	}
	return append(parts, string(cur))
}

func TestCorrelationVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/correlation/correlation_of.tsv")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	names := map[string]bool{}
	header := true
	for n, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			t.Fatalf("line %d: %d fields, want 7", n+1, len(f))
		}
		if header {
			header = false
			if strings.Join(f, " ") != "name baggage header want member baggage_reason header_reason" {
				t.Fatalf("line %d: unexpected columns %q", n+1, f)
			}
			continue
		}
		name := f[0]
		if names[name] {
			t.Errorf("row name %q is not unique", name)
		}
		names[name] = true
		var baggage []string
		if f[1] != "-" {
			baggage = unescapeField(t, f[1], true)
		}
		hdr, want, member := "", "", ""
		if f[2] != "-" {
			hdr = unescapeField(t, f[2], false)[0]
		}
		if f[3] != "-" {
			want = unescapeField(t, f[3], false)[0]
		}
		if f[4] != "-" {
			member = unescapeField(t, f[4], false)[0]
		}
		wantBag, wantHdr := CorrelationReason(f[5]), CorrelationReason(f[6])

		got := ReadCorrelation(baggage, hdr)
		if got.Correlation.String() != want {
			t.Errorf("%s: COR %q, want %q", name, got.Correlation, want)
		}
		if got.Baggage != wantBag || got.Header != wantHdr {
			t.Errorf("%s: reasons (%s, %s), want (%s, %s)", name, got.Baggage, got.Header, wantBag, wantHdr)
		}
		wantSrc := CorrelationFromNone
		if wantBag == ReasonOK {
			wantSrc = CorrelationFromBaggage
		} else if wantHdr == ReasonOK {
			wantSrc = CorrelationFromHeader
		}
		if got.Source != wantSrc {
			t.Errorf("%s: source %s, want %s", name, got.Source, wantSrc)
		}
		if of := CorrelationOf(baggage, hdr); of != got.Correlation {
			t.Errorf("%s: CorrelationOf gave %q, want ReadCorrelation's %q", name, of, got.Correlation)
		}

		value := baggageValue(baggage, CorrelationBaggageKey)
		if f[4] == "-" {
			var b [TextLen]byte
			if _, ok := percentDecode(&b, value); value != "" && ok {
				t.Errorf("%s: member is -, but the value %q is not empty and decodes", name, value)
			}
		} else {
			decoded := value
			if indexByte(value, '%') >= 0 {
				var b [TextLen]byte
				size, ok := percentDecode(&b, value)
				if !ok {
					t.Errorf("%s: the value %q has a malformed escape, member is %q", name, value, member)
					decoded = ""
				} else if size != len(member) || string(b[:min(size, len(b))]) != member[:min(size, len(b))] {
					t.Errorf("%s: %q decodes to %d bytes starting %q, want member %q", name, value, size, b[:min(size, len(b))], member)
					decoded = ""
				} else {
					decoded = member
				}
			}
			if decoded != member {
				t.Errorf("%s: member %q, want %q", name, decoded, member)
			}
		}
		checked++
	}
	if checked != 47 {
		t.Errorf("%d rows checked, want 47", checked)
	}
}
