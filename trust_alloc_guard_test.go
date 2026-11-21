// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Allocations per call of the IP and scheme extractors, measured on the stand at 2db3c81 before their bodies
// moved to internal/trust. A count that differs after the move is red.
const (
	trustXFFAllocs           = -1
	trustRealIPAllocs        = -1
	trustSchemeAllocs        = -1
	trustContextRealIPAllocs = -1
	trustContextDirectAllocs = -1
	trustContextSchemeAllocs = -1
)

// trustAllocSink keeps each extracted value alive.
var trustAllocSink string

func TestTrustAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	newReq := func(header, value string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/who", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set(header, value)
		return req
	}
	xffReq := newReq(HeaderXForwardedFor, "198.51.100.7, 10.0.0.2")
	realIPReq := newReq(HeaderXRealIP, "198.51.100.7")
	schemeReq := newReq(HeaderXForwardedProto, "https")

	xff := ExtractIPFromXFFHeader()
	realIP := ExtractIPFromRealIPHeader()
	scheme := ExtractSchemeFromHeaders()

	e := New()
	e.IPExtractor = xff
	xffCtx := e.NewContext(xffReq, &nopResponseWriter{})
	direct := New()
	directCtx := direct.NewContext(xffReq, &nopResponseWriter{})
	schemeCtx := direct.NewContext(schemeReq, &nopResponseWriter{})

	tests := []struct {
		name string
		call func() string
		want int
	}{
		{name: "xff", call: func() string { return xff(xffReq) }, want: trustXFFAllocs},
		{name: "realip", call: func() string { return realIP(realIPReq) }, want: trustRealIPAllocs},
		{name: "scheme", call: func() string { return scheme(schemeReq) }, want: trustSchemeAllocs},
		{name: "context realip", call: xffCtx.RealIP, want: trustContextRealIPAllocs},
		{name: "context direct", call: directCtx.RealIP, want: trustContextDirectAllocs},
		{name: "context scheme", call: schemeCtx.Scheme, want: trustContextSchemeAllocs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := testing.AllocsPerRun(1000, func() {
				trustAllocSink = tt.call()
			})
			t.Logf("%s: %v allocations per call", tt.name, got)
			if got != float64(tt.want) {
				t.Errorf("%s: %v allocations per call, want %d", tt.name, got, tt.want)
			}
		})
	}
}
