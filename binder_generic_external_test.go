// SPDX-License-Identifier: MIT

package echo_test

import (
	"errors"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v7"
	"github.com/fiberfx/mx-echo/v7/bind"
)

type dualDecoder string

func (d *dualDecoder) UnmarshalParam(value string) error {
	*d = dualDecoder("parameter " + value)
	return nil
}

func (d *dualDecoder) UnmarshalText(value []byte) error {
	*d = dualDecoder("text " + string(value))
	return nil
}

func TestBindingImportCompatibility(t *testing.T) {
	for _, api := range []struct {
		name          string
		parse         func(string, ...any) (int8, error)
		parseOr       func(string, int8, ...any) (int8, error)
		parseValues   func([]string, ...any) ([]int, error)
		parseValuesOr func([]string, []int, ...any) ([]int, error)
		decode        func(string, ...any) (dualDecoder, error)
	}{
		{"echo", echo.ParseValue[int8], echo.ParseValueOr[int8], echo.ParseValues[int], echo.ParseValuesOr[int], echo.ParseValue[dualDecoder]},
		{"bind", bind.ParseValue[int8], bind.ParseValueOr[int8], bind.ParseValues[int], bind.ParseValuesOr[int], bind.ParseValue[dualDecoder]},
	} {
		t.Run(api.name, func(t *testing.T) {
			if n, err := api.parse("128"); n != 0 || !errors.Is(err, strconv.ErrRange) {
				t.Fatalf("overflow returned %d, %v", n, err)
			}
			if n, err := api.parseOr("", 9); n != 9 || err != nil {
				t.Fatalf("empty value returned %d, %v", n, err)
			}
			if ns, err := api.parseValues([]string{"7", "8"}); !slices.Equal(ns, []int{7, 8}) || err != nil {
				t.Fatalf("valid slice returned %v, %v", ns, err)
			}
			if ns, err := api.parseValues([]string{"7", "x"}); ns != nil || !errors.Is(err, strconv.ErrSyntax) {
				t.Fatalf("malformed slice returned %v, %v", ns, err)
			}
			if ns, err := api.parseValuesOr(nil, []int{9}); !slices.Equal(ns, []int{9}) || err != nil {
				t.Fatalf("empty slice returned %v, %v", ns, err)
			}
			if v, err := api.decode("value"); v != "parameter value" || err != nil {
				t.Fatalf("custom unmarshal returned %q, %v", v, err)
			}
		})
	}
}

func TestBindingTimeOptionCompatibility(t *testing.T) {
	from := time.FixedZone("from", 3600)
	to := time.FixedZone("to", -3600)
	var layout echo.TimeLayout = bind.TimeLayout(time.DateOnly)
	var rootOpts echo.TimeOpts = bind.TimeOpts{Layout: layout, ParseInLocation: from, ToInLocation: to}
	var directOpts bind.TimeOpts = rootOpts
	for _, api := range []struct {
		name                   string
		parse                  func(string, ...any) (time.Time, error)
		opts                   any
		seconds, millis, nanos any
	}{
		{"echo", echo.ParseValue[time.Time], directOpts, bind.TimeLayoutUnixTime, bind.TimeLayoutUnixTimeMilli, bind.TimeLayoutUnixTimeNano},
		{"bind", bind.ParseValue[time.Time], rootOpts, echo.TimeLayoutUnixTime, echo.TimeLayoutUnixTimeMilli, echo.TimeLayoutUnixTimeNano},
	} {
		t.Run(api.name, func(t *testing.T) {
			got, err := api.parse("2020-01-01", api.opts)
			want := time.Date(2020, 1, 1, 0, 0, 0, 0, from)
			if err != nil || !got.Equal(want) || got.Location() != to {
				t.Fatalf("custom options returned %v, %v", got, err)
			}
			for _, tc := range []struct {
				value  string
				layout any
			}{
				{"7", api.seconds}, {"7000", api.millis}, {"7000000000", api.nanos},
			} {
				got, err := api.parse(tc.value, tc.layout)
				if err != nil || !got.Equal(time.Unix(7, 0)) {
					t.Fatalf("%v returned %v, %v", tc.layout, got, err)
				}
			}
		})
	}
}

func TestBindingQueryAdapterCompatibility(t *testing.T) {
	c := echo.NewContext(httptest.NewRequest("GET", "/?empty=&bad=x", nil), nil)
	if n, err := echo.QueryParam[int](c, "missing"); n != 0 || !errors.Is(err, echo.ErrNonExistentKey) {
		t.Fatalf("missing key returned %d, %v", n, err)
	}
	if n, err := echo.QueryParam[int](c, "empty"); n != 0 || err != nil {
		t.Fatalf("empty key returned %d, %v", n, err)
	}
	var bindingErr *echo.BindingError
	if _, err := echo.QueryParam[int](c, "bad"); !errors.As(err, &bindingErr) || bindingErr.Field != "bad" {
		t.Fatalf("invalid field returned %v", err)
	}
}
