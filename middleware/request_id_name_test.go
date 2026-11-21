// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// requestIDSeen is every place one request's id showed up: the response's X-Request-Id, the value given to
// RequestIDHandler, the value the request logger extracted, and the name the Context carries.
type requestIDSeen struct {
	header  string
	handled string
	logged  string
	name    reqid.ID
}

type discardObservations struct{}

func (discardObservations) Observe(observe.Observation) {}

// serveRequestID sends one request through the request logger and RequestID on e.
func serveRequestID(e *echo.Echo, clientID string) requestIDSeen {
	var seen requestIDSeen
	e.Use(RequestLoggerWithConfig(RequestLoggerConfig{
		LogRequestID: true,
		LogValuesFunc: func(c *echo.Context, v RequestLoggerValues) error {
			seen.logged = v.RequestID
			return nil
		},
	}))
	e.Use(RequestIDWithConfig(RequestIDConfig{
		RequestIDHandler: func(c *echo.Context, requestID string) { seen.handled = requestID },
	}))
	e.GET("/", func(c *echo.Context) error {
		seen.name = c.RequestID()
		return c.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if clientID != "" {
		req.Header.Set(echo.HeaderXRequestID, clientID)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	seen.header = rec.Header().Get(echo.HeaderXRequestID)
	return seen
}

// checkRequestID is the assertion. A named request carries its name everywhere and never the client's value.
// A request with no name carries the client's value, or a generated one when the client sent none, as at v6.1.0.
func checkRequestID(seen requestIDSeen, clientID string, named bool) error {
	want := clientID
	switch {
	case named && seen.name.IsZero():
		return errors.New("the request has no name")
	case named:
		want = seen.name.String()
	case !seen.name.IsZero():
		return fmt.Errorf("the request has the name %s with naming off", seen.name)
	case clientID == "":
		if len(seen.header) != 32 {
			return fmt.Errorf("the generated id is %q, want 32 characters", seen.header)
		}
		want = seen.header
	}
	if seen.header != want || seen.handled != want || seen.logged != want {
		return fmt.Errorf("the response carries %q, RequestIDHandler got %q, the logger %q; want %q in all three",
			seen.header, seen.handled, seen.logged, want)
	}
	return nil
}

// TestRequestIDUsesName is row I1: with an observer the request has one id, its name, in the response header, in
// RequestIDHandler and in the request logger, even when the client sent its own; with none, nothing changes.
func TestRequestIDUsesName(t *testing.T) {
	observed := func() *echo.Echo {
		return echo.NewWithConfig(echo.Config{Observer: observe.Observer{Sink: discardObservations{}}})
	}
	unnamed := func() *echo.Echo {
		return echo.NewWithConfig(echo.Config{Observer: observe.Observer{
			Sink: discardObservations{},
			Mint: func() reqid.ID { return reqid.ID{} },
		}})
	}
	tests := []struct {
		name     string
		build    func() *echo.Echo
		clientID string
		named    bool
	}{
		{"an observer, the client sent an id", observed, "client-chosen", true},
		{"an observer, the client sent none", observed, "", true},
		{"no observer, the client sent an id", echo.New, "client-chosen", false},
		{"no observer, the client sent none", echo.New, "", false},
		{"an observer that names nothing, the client sent an id", unnamed, "client-chosen", false},
		{"an observer that names nothing, the client sent none", unnamed, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkRequestID(serveRequestID(tc.build(), tc.clientID), tc.clientID, tc.named); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestRequestIDUsesNameViolation feeds the assertion the client's id winning with naming on, and two ids for
// one request.
func TestRequestIDUsesNameViolation(t *testing.T) {
	name := reqid.New(1, 1)
	clientWins := requestIDSeen{header: "client-chosen", handled: "client-chosen", logged: "client-chosen", name: name}
	if checkRequestID(clientWins, "client-chosen", true) == nil {
		t.Error("the client's id winning with naming on passed the check")
	}
	twoIDs := requestIDSeen{header: name.String(), handled: name.String(), logged: "client-chosen", name: name}
	if checkRequestID(twoIDs, "client-chosen", true) == nil {
		t.Error("a logger that kept the client's id passed the check")
	}
	nameWins := requestIDSeen{header: name.String(), handled: name.String(), logged: name.String()}
	if checkRequestID(nameWins, "client-chosen", false) == nil {
		t.Error("a request with no name that dropped the client's id passed the check")
	}
}
