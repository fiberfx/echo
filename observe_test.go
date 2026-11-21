// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fiberfx/mx-echo/v6/observe"
)

// Allocations per routed request with an observer whose sink keeps nothing, measured on the stand.
const observedAllocs = 0 // stand runs spike-2 and spike-full (20261121T085411Z), a5eaffa: static and param pass at 0

// countingSink keeps nothing of an observation and counts its calls.
type countingSink struct {
	calls int
}

func (s *countingSink) Observe(observe.Observation) { s.calls++ }

// allocatingSink keeps one allocation per observation on the heap, in allocSink.
type allocatingSink struct{}

func (allocatingSink) Observe(observe.Observation) { allocSink = new([64]byte) }

type observerAllocCase struct {
	name  string
	route string
	path  string
	want  int
}

// observerAllocCases are the two requests of TestServeHTTPAllocs with its two constants.
func observerAllocCases() []observerAllocCase {
	return []observerAllocCase{
		{name: "static", route: "/users/profile", path: "/users/profile", want: staticAllocs},
		{name: "param", route: "/users/:id/books/:bid", path: "/users/42/books/7", want: paramAllocs},
	}
}

// checkAllocs is the assertion: an error when a request allocated more than want.
func checkAllocs(got float64, want int) error {
	if got > float64(want) {
		return fmt.Errorf("%v allocations per request, want at most %d", got, want)
	}
	return nil
}

// routedAllocs registers one route on e and measures a request to it.
func routedAllocs(e *Echo, tc observerAllocCase) float64 {
	e.GET(tc.route, func(c *Context) error { return c.NoContent(http.StatusOK) })
	return serveAllocs(e, httptest.NewRequest(http.MethodGet, tc.path, nil))
}

// TestObserverOffAllocs is the falsifier of the hook: with no observer a routed request allocates what it
// allocated before the hook existed, whichever way the Echo was made.
func TestObserverOffAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	makers := []struct {
		name  string
		build func() *Echo
	}{
		{"New", New},
		{"NewWithConfig, a zero Observer", func() *Echo { return NewWithConfig(Config{}) }},
		{"NewWithConfig, an Observer with no sink", func() *Echo {
			return NewWithConfig(Config{Observer: observe.Observer{Attempt: "q0/run/a1", Mint: mintRequestID}})
		}},
	}
	for _, maker := range makers {
		for _, tc := range observerAllocCases() {
			t.Run(maker.name+"/"+tc.name, func(t *testing.T) {
				e := maker.build()
				if e.observer != nil {
					t.Fatal("an Echo with no sink has an observer")
				}
				got := routedAllocs(e, tc)
				t.Logf("%s: %v allocations per request with the observer off", tc.name, got)
				if err := checkAllocs(got, tc.want); err != nil {
					t.Errorf("%s: %v", tc.name, err)
				}
			})
		}
	}
}

// TestObservedAllocs measures the same two requests with an observer whose sink keeps nothing.
func TestObservedAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	for _, tc := range observerAllocCases() {
		t.Run(tc.name, func(t *testing.T) {
			sink := &countingSink{}
			e := NewWithConfig(Config{Observer: observe.Observer{Sink: sink}})
			got := routedAllocs(e, tc)
			t.Logf("%s: %v allocations per request with the observer on", tc.name, got)
			if err := checkAllocs(got, observedAllocs); err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			if sink.calls < 1000 {
				t.Errorf("%s: the sink was called %d times for at least 1000 requests", tc.name, sink.calls)
			}
		})
	}
}

// TestObserverAllocsViolation feeds the assertion one allocation too many, and shows that the meter sees an
// allocation made on the observed path.
func TestObserverAllocsViolation(t *testing.T) {
	if checkAllocs(float64(staticAllocs)+1, staticAllocs) == nil {
		t.Fatal("one allocation more than the constant passed the check")
	}
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	tc := observerAllocCases()[0]
	quiet := routedAllocs(NewWithConfig(Config{Observer: observe.Observer{Sink: &countingSink{}}}), tc)
	loud := routedAllocs(NewWithConfig(Config{Observer: observe.Observer{Sink: allocatingSink{}}}), tc)
	t.Logf("a sink that keeps nothing %v, a sink that allocates %v allocations per request", quiet, loud)
	if loud < quiet+1 {
		t.Errorf("the meter missed an allocation on the observed path: %v with it, %v without", loud, quiet)
	}
}
