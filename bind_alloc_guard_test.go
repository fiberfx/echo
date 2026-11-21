// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Allocations per bind, measured on the stand at 2db3c81 (v6.5.0), before the engine moved to
// internal/bindengine.
const (
	bindStructAllocs = 2 // stand run l-bind-1 (20261121T102323Z), 9a681f9: "2 allocations per bind"
	bindMapAllocs    = 6 // stand run l-bind-1 (20261121T102323Z), 9a681f9: "6 allocations per bind"
)

// checkBindAllocs is the assertion: an error unless a bind allocated exactly want.
func checkBindAllocs(t *testing.T, got float64, want int) {
	t.Helper()
	t.Logf("%v allocations per bind", got)
	if got != float64(want) {
		t.Errorf("%v allocations per bind, want %d", got, want)
	}
}

func TestBindAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	e := New()
	req := httptest.NewRequest(http.MethodGet, "/q?id=7&name=jo&tags=a&tags=b", nil)
	c := e.NewContext(req, httptest.NewRecorder())

	t.Run("DefaultBinder.Bind struct", func(t *testing.T) {
		var dest struct {
			ID   int      `query:"id"`
			Name string   `query:"name"`
			Tags []string `query:"tags"`
		}
		b := &DefaultBinder{}
		// The first bind fills the field cache and the context's query values.
		if err := b.Bind(c, &dest); err != nil {
			t.Fatal(err)
		}
		got := testing.AllocsPerRun(1000, func() {
			_ = b.Bind(c, &dest)
		})
		checkBindAllocs(t, got, bindStructAllocs)
	})
	t.Run("BindQueryParams map", func(t *testing.T) {
		dest := map[string]string{}
		if err := BindQueryParams(c, &dest); err != nil {
			t.Fatal(err)
		}
		got := testing.AllocsPerRun(1000, func() {
			_ = BindQueryParams(c, &dest)
		})
		checkBindAllocs(t, got, bindMapAllocs)
	})
}
