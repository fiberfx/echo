// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Allocations per routed request, measured on the stand at 70c2771.
const (
	staticAllocs = 0 // stand run guard-probe (20261121T083635Z), a493622: "static: 0"
	paramAllocs  = 0 // stand run guard-probe (20261121T083635Z), a493622: "param: 0"
)

// allocSink keeps the violating middleware's allocation on the heap.
var allocSink *[64]byte

func serveAllocs(e *Echo, req *http.Request) float64 {
	w := &nopResponseWriter{}
	return testing.AllocsPerRun(1000, func() {
		w.h = nil
		e.ServeHTTP(w, req)
	})
}

func TestServeHTTPAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	tests := []struct {
		name  string
		route string
		path  string
		want  int
	}{
		{name: "static", route: "/users/profile", path: "/users/profile", want: staticAllocs},
		{name: "param", route: "/users/:id/books/:bid", path: "/users/42/books/7", want: paramAllocs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New()
			e.GET(tt.route, func(c *Context) error { return c.NoContent(http.StatusOK) })
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			got := serveAllocs(e, req)
			t.Logf("%s: %v allocations per request", tt.name, got)
			if got > float64(tt.want) {
				t.Errorf("%s: %v allocations per request, want at most %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestAllocMeterViolation(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	handler := func(c *Context) error { return c.NoContent(http.StatusOK) }
	req := httptest.NewRequest(http.MethodGet, "/users/profile", nil)

	e := New()
	e.GET("/users/profile", handler)
	base := serveAllocs(e, req)

	e = New()
	e.Use(func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			allocSink = new([64]byte)
			return next(c)
		}
	})
	e.GET("/users/profile", handler)
	got := serveAllocs(e, req)

	t.Logf("without the middleware %v, with it %v allocations per request", base, got)
	if got < base+1 {
		t.Errorf("the meter missed an allocation: %v with the middleware, %v without", got, base)
	}
}
