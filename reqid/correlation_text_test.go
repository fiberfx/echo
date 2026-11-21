// SPDX-License-Identifier: MIT

package reqid_test

import (
	"encoding"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"testing"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

var _ encoding.TextUnmarshaler = (*reqid.Correlation)(nil)

// checkUnmarshalText reports the first case unmarshal gets wrong: every valid COR is read as ParseCorrelation reads
// it, and every text that is not one returns an error and leaves the Correlation as it was.
func checkUnmarshalText(unmarshal func(*reqid.Correlation, []byte) error) error {
	old, _ := reqid.ParseCorrelation("COR0SF1bsgvgpc")
	for _, s := range []string{"COR0SCMa0vxXFZ", "COR00000000000", "CORAzL8n0Y58m7"} {
		c := old
		want, _ := reqid.ParseCorrelation(s)
		if err := unmarshal(&c, []byte(s)); err != nil || c != want {
			return fmt.Errorf("UnmarshalText(%q) = (%q, %v), want %q and no error", s, c, err, s)
		}
	}
	refused := []struct{ name, in string }{
		{"empty, which ParseCorrelation reads as absent", ""},
		{"13 bytes", "COR0000000000"},
		{"a lowercase namespace", "cOR00000000000"},
		{"brand USR", "USR0KHTOWnGLuC"},
		{"charset", "COR0000000000-"},
		{"range", "CORAzL8n0Y58m8"},
	}
	for _, r := range refused {
		c := old
		if err := unmarshal(&c, []byte(r.in)); err == nil || c != old {
			return fmt.Errorf("%s: UnmarshalText(%q) left %q and returned %v, want %q and an error", r.name, r.in, c, err, old)
		}
	}
	return nil
}

func TestCorrelationUnmarshalText(t *testing.T) {
	if err := checkUnmarshalText((*reqid.Correlation).UnmarshalText); err != nil {
		t.Error(err)
	}
}

func TestCorrelationUnmarshalTextViolation(t *testing.T) {
	planted := []struct {
		name      string
		unmarshal func(*reqid.Correlation, []byte) error
	}{
		{"bad text zeroes c", func(c *reqid.Correlation, text []byte) error {
			parsed, ok := reqid.ParseCorrelation(string(text))
			*c = parsed
			if !ok {
				return errors.New("not a COR")
			}
			return nil
		}},
		{"bad text returns no error", func(c *reqid.Correlation, text []byte) error {
			if parsed, ok := reqid.ParseCorrelation(string(text)); ok {
				*c = parsed
			}
			return nil
		}},
		{"good text leaves c as it was", func(_ *reqid.Correlation, text []byte) error {
			if _, ok := reqid.ParseCorrelation(string(text)); !ok {
				return errors.New("not a COR")
			}
			return nil
		}},
	}
	for _, p := range planted {
		if checkUnmarshalText(p.unmarshal) == nil {
			t.Errorf("%s passed the check", p.name)
		}
	}
}

// A Correlation goes through encoding/json and flag.TextVar and comes back the same.
func TestCorrelationTextRoundTrip(t *testing.T) {
	type record struct {
		Correlation reqid.Correlation `json:"correlation"`
	}
	in := record{}
	in.Correlation, _ = reqid.ParseCorrelation("COR0SCMa0vxXFZ")
	data, err := json.Marshal(in)
	if err != nil || string(data) != `{"correlation":"COR0SCMa0vxXFZ"}` {
		t.Fatalf("json.Marshal = (%s, %v), want the COR as a string", data, err)
	}
	var out record
	if err := json.Unmarshal(data, &out); err != nil || out != in {
		t.Errorf("json.Unmarshal(%s) = (%+v, %v), want %+v", data, out, err, in)
	}
	kept := out
	if err := json.Unmarshal([]byte(`{"correlation":"cOR0SCMa0vxXFZ"}`), &out); err == nil || out != kept {
		t.Errorf("json.Unmarshal of a lowercase namespace = (%+v, %v), want %+v and an error", out, err, kept)
	}

	flags := flag.NewFlagSet("cor", flag.ContinueOnError)
	var c reqid.Correlation
	flags.TextVar(&c, "cor", reqid.Correlation{}, "the COR id")
	if err := flags.Parse([]string{"-cor", "COR0SF1bsgvgpc"}); err != nil || c.String() != "COR0SF1bsgvgpc" {
		t.Errorf("flag.TextVar read (%q, %v), want COR0SF1bsgvgpc", c, err)
	}
}
