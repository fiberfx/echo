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
// it, empty text is read as the zero Correlation, and every other text that is not a COR returns an error and leaves
// the Correlation as it was.
func checkUnmarshalText(unmarshal func(*reqid.Correlation, []byte) error) error {
	old, _ := reqid.ParseCorrelation("mxe_cor_23obzcbapvupu2272226")
	for _, s := range []string{"mxe_cor_23obzcbapvupu2272222", "mxe_cor_22222222222222222222", "3zz_cor_bzzzzzzzzzzzzzzzzzzw", ""} {
		c := old
		want, _ := reqid.ParseCorrelation(s)
		if err := unmarshal(&c, []byte(s)); err != nil || c != want {
			return fmt.Errorf("UnmarshalText(%q) = (%q, %v), want %q and no error", s, c, err, s)
		}
	}
	refused := []struct{ name, in string }{
		{"27 bytes", "mxe_cor_2222222222222222222"},
		{"an uppercase text", "MXE_COR_23OBZCBAPVUPU2272222"},
		{"kind req", "mxe_req_23obzcbapvupu2272222"},
		{"alphabet", "mxe_cor_23obzcbapvupu227222-"},
		{"pad", "mxe_cor_23obzcbapvupu2272223"},
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
		{"empty text refused", func(c *reqid.Correlation, text []byte) error {
			parsed, ok := reqid.ParseCorrelation(string(text))
			if !ok {
				return errors.New("not a COR")
			}
			*c = parsed
			return nil
		}},
		{"empty text leaves c as it was", func(c *reqid.Correlation, text []byte) error {
			if len(text) == 0 {
				return nil
			}
			return c.UnmarshalText(text)
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
	in.Correlation, _ = reqid.ParseCorrelation("mxe_cor_23obzcbapvupu2272222")
	data, err := json.Marshal(in)
	if err != nil || string(data) != `{"correlation":"mxe_cor_23obzcbapvupu2272222"}` {
		t.Fatalf("json.Marshal = (%s, %v), want the COR as a string", data, err)
	}
	var out record
	if err := json.Unmarshal(data, &out); err != nil || out != in {
		t.Errorf("json.Unmarshal(%s) = (%+v, %v), want %+v", data, out, err, in)
	}
	var zero record
	data, err = json.Marshal(zero)
	if err != nil || string(data) != `{"correlation":""}` {
		t.Fatalf("json.Marshal of the zero Correlation = (%s, %v), want an empty string", data, err)
	}
	back := in
	if err := json.Unmarshal(data, &back); err != nil || back != zero {
		t.Errorf("json.Unmarshal(%s) = (%+v, %v), want the zero Correlation", data, back, err)
	}
	kept := out
	if err := json.Unmarshal([]byte(`{"correlation":"MXE_COR_23OBZCBAPVUPU2272222"}`), &out); err == nil || out != kept {
		t.Errorf("json.Unmarshal of a lowercase namespace = (%+v, %v), want %+v and an error", out, err, kept)
	}

	flags := flag.NewFlagSet("cor", flag.ContinueOnError)
	var c reqid.Correlation
	flags.TextVar(&c, "cor", reqid.Correlation{}, "the COR id")
	if err := flags.Parse([]string{"-cor", "mxe_cor_23obzcbapvupu2272226"}); err != nil || c.String() != "mxe_cor_23obzcbapvupu2272226" {
		t.Errorf("flag.TextVar read (%q, %v), want mxe_cor_23obzcbapvupu2272226", c, err)
	}
}
