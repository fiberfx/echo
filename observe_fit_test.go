// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/outcome"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// This file is the fit test of the observed request (gate G10b). It drives the router shape of the Fibervisor
// responses service: the raw-path router that decodes a parameter once, a Pre guard on the Host, a custom error
// handler that answers {"reason": ...}, and PUT /api/responses/:key, with a map in place of the store. It asks
// for what v6.1.0 could not give: one name per request, the status after the error handler wrote, where
// routing ended, and the registered pattern in place of the path.

const (
	fitPattern = "/api/responses/:key"
	fitAttempt = "q0/run/a1"
)

// fitLoopbackHost refuses a Host that is not a loopback name, before routing.
func fitLoopbackHost(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		host := c.Request().Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" {
			return echo.NewHTTPError(http.StatusForbidden, fmt.Sprintf("host %q is not 127.0.0.1 or localhost", c.Request().Host))
		}
		return next(c)
	}
}

// fitRefuse answers every error as JSON {"reason": ...} with the status the error states, else 500.
func fitRefuse(c *echo.Context, err error) {
	if r, _ := echo.UnwrapResponse(c.Response()); r != nil && r.Committed {
		return
	}
	code := http.StatusInternalServerError
	var sc echo.HTTPStatusCoder
	if errors.As(err, &sc) && sc.StatusCode() != 0 {
		code = sc.StatusCode()
	}
	reason := http.StatusText(code)
	var he *echo.HTTPError
	if errors.As(err, &he) && he.Message != "" {
		reason = he.Message
	}
	if c.Request().Method == http.MethodHead {
		_ = c.NoContent(code)
		return
	}
	_ = c.JSON(code, map[string]string{"reason": reason})
}

// fitServed is what the four requests of the fit test left behind.
type fitServed struct {
	seen     []observe.Observation
	statuses []int
	bodies   []string
	keys     []string
}

// serveFibervisorShape sends the four requests: a saved key, an encoded key, a foreign Host and an unknown path.
func serveFibervisorShape(attempt string) fitServed {
	sink := &observations{}
	var served fitServed
	e := echo.NewWithConfig(echo.Config{
		Router:           echo.NewRouter(echo.RouterConfig{UnescapePathParamValues: true}),
		HTTPErrorHandler: fitRefuse,
		Observer:         observe.Observer{Sink: sink, Attempt: attempt},
	})
	e.Pre(fitLoopbackHost)
	e.PUT(fitPattern, func(c *echo.Context) error {
		served.keys = append(served.keys, c.Param("key"))
		return c.NoContent(http.StatusNoContent)
	})

	requests := []struct {
		host   string
		target string
	}{
		{"127.0.0.1:4890", "/api/responses/premises:P3"},
		{"127.0.0.1:4890", "/api/responses/premises%3Aa%2Fb%20c"},
		{"rebound.example", "/api/responses/premises:P3"},
		{"localhost", "/api/unknown"},
	}
	for _, r := range requests {
		req := httptest.NewRequest(http.MethodPut, r.target, nil)
		req.Host = r.host
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		served.statuses = append(served.statuses, rec.Code)
		served.bodies = append(served.bodies, rec.Body.String())
	}
	served.seen = sink.seen
	return served
}

// checkFibervisorShape is the assertion of the fit test.
func checkFibervisorShape(served fitServed, attempt string) error {
	if len(served.seen) != 4 || len(served.statuses) != 4 {
		return fmt.Errorf("%d observations of %d requests, want 4 of 4", len(served.seen), len(served.statuses))
	}
	want := []struct {
		admission observe.Admission
		pattern   string
		handler   string
		kind      outcome.Kind
		status    int
	}{
		{observe.Routed, fitPattern, "PUT:" + fitPattern, outcome.OK, http.StatusNoContent},
		{observe.Routed, fitPattern, "PUT:" + fitPattern, outcome.OK, http.StatusNoContent},
		{observe.BeforeRouting, observe.None, observe.None, outcome.AdmissionRefusal, http.StatusForbidden},
		{observe.NotFound, observe.None, observe.None, outcome.AdmissionRefusal, http.StatusNotFound},
	}
	names := map[reqid.ID]bool{}
	for i, o := range served.seen {
		w := want[i]
		if o.Attempt != attempt {
			return fmt.Errorf("request %d: attempt %q, want %q", i, o.Attempt, attempt)
		}
		if o.ID.IsZero() || names[o.ID] {
			return fmt.Errorf("request %d: the name %s is zero or was given to an earlier request", i, o.ID)
		}
		names[o.ID] = true
		if o.Method != http.MethodPut {
			return fmt.Errorf("request %d: method %q, want PUT", i, o.Method)
		}
		if o.Admission != w.admission || o.Pattern != w.pattern || o.Handler != w.handler {
			return fmt.Errorf("request %d: admission %s, pattern %q, handler %q; want %s, %q, %q",
				i, o.Admission, o.Pattern, o.Handler, w.admission, w.pattern, w.handler)
		}
		if o.Kind != w.kind {
			return fmt.Errorf("request %d: kind %s, want %s", i, o.Kind, w.kind)
		}
		// the status is the one the custom error handler wrote, read after it ran
		if o.Status != w.status || o.Status != served.statuses[i] || !o.Committed {
			return fmt.Errorf("request %d: observed status %d (committed %v), the response's %d, want %d",
				i, o.Status, o.Committed, served.statuses[i], w.status)
		}
		if line := string(o.AppendJSON(nil)); strings.Contains(line, "premises") || strings.Contains(line, `""`) {
			return fmt.Errorf("request %d: the line %s holds the request's path or an empty string", i, line)
		}
	}
	return nil
}

func TestFibervisorShape(t *testing.T) {
	served := serveFibervisorShape(fitAttempt)
	if err := checkFibervisorShape(served, fitAttempt); err != nil {
		t.Fatal(err)
	}
	// the shape itself still behaves: the key is decoded once, and the refusals carry the custom handler's body
	if len(served.keys) != 2 || served.keys[0] != "premises:P3" || served.keys[1] != "premises:a/b c" {
		t.Errorf("the handler saw the keys %q, want premises:P3 and premises:a/b c", served.keys)
	}
	if !strings.Contains(served.bodies[2], `"reason":"host`) || !strings.Contains(served.bodies[3], `"reason":"Not Found"`) {
		t.Errorf("the refusals answered %q and %q, want the custom handler's reasons", served.bodies[2], served.bodies[3])
	}
}

// TestFibervisorShapeViolation feeds the assertion what a renaming would produce: the concrete path as the
// pattern, a handler named for a refusal, one name for two requests, an attempt that is not the service's.
func TestFibervisorShapeViolation(t *testing.T) {
	violations := []struct {
		name   string
		change func(seen []observe.Observation)
	}{
		{"the concrete path as the pattern", func(seen []observe.Observation) {
			seen[1].Pattern = "/api/responses/premises%3Aa%2Fb%20c"
		}},
		{"a handler named for the 403", func(seen []observe.Observation) { seen[2].Handler = "PUT:" + fitPattern }},
		{"a handler named for the 404", func(seen []observe.Observation) { seen[3].Handler = "echo_route_not_found" }},
		{"the 404 taken for a business refusal", func(seen []observe.Observation) {
			seen[3].Kind = outcome.BusinessRefusal
		}},
		{"one name for two requests", func(seen []observe.Observation) { seen[1].ID = seen[0].ID }},
		{"a request with no name", func(seen []observe.Observation) { seen[0].ID = reqid.ID{} }},
		{"an attempt that is not the service's", func(seen []observe.Observation) { seen[3].Attempt = observe.None }},
		{"a status that is not the response's", func(seen []observe.Observation) { seen[2].Status = http.StatusOK }},
		{"an observation missing", func(seen []observe.Observation) { seen[3] = seen[2] }},
	}
	for _, v := range violations {
		served := serveFibervisorShape(fitAttempt)
		v.change(served.seen)
		if checkFibervisorShape(served, fitAttempt) == nil {
			t.Errorf("%s passed the check", v.name)
		}
	}
	served := serveFibervisorShape(fitAttempt)
	served.seen = served.seen[:3]
	if checkFibervisorShape(served, fitAttempt) == nil {
		t.Error("three observations of four requests passed the check")
	}
}
