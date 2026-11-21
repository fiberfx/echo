// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/fibervisor"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/outcome"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// This file is the fit test of the observed request under an fv attempt (gate G10b of v6.5.0). It drives the
// router shape of the Fibervisor responses service, as observe_fit_test.go does, with the attempt the service
// reads once from the environment fv gives its leaf. The test, not the root, imports fibervisor.

const fvFitPattern = "/api/responses/:key"

// fvFitEnv is fv's own vector for an attempt, the one fibervisor/fibervisor_test.go pins from fiberfx/fv's
// id.rs: the span is the node id fv derives for this trace and path.
var fvFitEnv = map[string]string{
	"TRACEPARENT": "00-5bf92f3577b34da6a3ce929d0e0e4736-fd578c945e0aac89-01",
	"FV_PATH":     "q00000000000100000007/run/a1",
	"FV_TRACE":    "5bf92f3577b34da6a3ce929d0e0e4736",
}

// fvFitServed is what the four requests left behind.
type fvFitServed struct {
	seen     []observe.Observation
	statuses []int
}

// fvFitServe sends the four requests to the Fibervisor's router shape: a saved key, an encoded key, a foreign
// Host and an unknown path.
func fvFitServe(attempt string) fvFitServed {
	guard := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			host := c.Request().Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if host != "127.0.0.1" && host != "localhost" {
				return echo.NewHTTPError(http.StatusForbidden, "host is not 127.0.0.1 or localhost")
			}
			return next(c)
		}
	}
	refuse := func(c *echo.Context, err error) {
		if r, _ := echo.UnwrapResponse(c.Response()); r != nil && r.Committed {
			return
		}
		code := http.StatusInternalServerError
		var sc echo.HTTPStatusCoder
		if errors.As(err, &sc) && sc.StatusCode() != 0 {
			code = sc.StatusCode()
		}
		_ = c.JSON(code, map[string]string{"reason": http.StatusText(code)})
	}

	sink := &observations{}
	e := echo.NewWithConfig(echo.Config{
		Router:           echo.NewRouter(echo.RouterConfig{UnescapePathParamValues: true}),
		HTTPErrorHandler: refuse,
		Observer:         observe.Observer{Sink: sink, Attempt: attempt},
	})
	e.Pre(guard)
	e.PUT(fvFitPattern, func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	var served fvFitServed
	for _, r := range []struct{ host, target string }{
		{"127.0.0.1:4890", "/api/responses/premises:P3"},
		{"127.0.0.1:4890", "/api/responses/premises%3Aa%2Fb%20c"},
		{"rebound.example", "/api/responses/premises:P3"},
		{"localhost", "/api/unknown"},
	} {
		req := httptest.NewRequest(http.MethodPut, r.target, nil)
		req.Host = r.host
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		served.statuses = append(served.statuses, rec.Code)
	}
	served.seen = sink.seen
	return served
}

// fvFitCheck returns an error unless served holds four observations of attempt, with four distinct names,
// the pattern for the two routed requests, and refusals of admission with no handler for the 403 and the 404.
func fvFitCheck(served fvFitServed, attempt string) error {
	if len(served.seen) != 4 || len(served.statuses) != 4 {
		return fmt.Errorf("%d observations of %d requests, want 4 of 4", len(served.seen), len(served.statuses))
	}
	want := []struct {
		pattern string
		kind    outcome.Kind
		status  int
	}{
		{fvFitPattern, outcome.OK, http.StatusNoContent},
		{fvFitPattern, outcome.OK, http.StatusNoContent},
		{observe.None, outcome.AdmissionRefusal, http.StatusForbidden},
		{observe.None, outcome.AdmissionRefusal, http.StatusNotFound},
	}
	names := map[reqid.ID]bool{}
	for i, o := range served.seen {
		w := want[i]
		if o.Attempt == "" || o.Attempt != attempt {
			return fmt.Errorf("request %d: attempt %q, want %q", i, o.Attempt, attempt)
		}
		if o.ID.IsZero() || names[o.ID] {
			return fmt.Errorf("request %d: the name %s is zero or was given to an earlier request", i, o.ID)
		}
		names[o.ID] = true
		if o.Pattern != w.pattern {
			return fmt.Errorf("request %d: pattern %q, want %q", i, o.Pattern, w.pattern)
		}
		if w.kind == outcome.AdmissionRefusal && o.Handler != observe.None {
			return fmt.Errorf("request %d: handler %q for a refusal of admission, want %q", i, o.Handler, observe.None)
		}
		if o.Kind != w.kind {
			return fmt.Errorf("request %d: kind %s, want %s", i, o.Kind, w.kind)
		}
		if o.Status != w.status || o.Status != served.statuses[i] {
			return fmt.Errorf("request %d: observed status %d, the response's %d, want %d",
				i, o.Status, served.statuses[i], w.status)
		}
	}
	return nil
}

// fvFitUnset removes the three variables for the rest of the test; t.Setenv restores them after it.
func fvFitUnset(t *testing.T) {
	t.Helper()
	for name := range fvFitEnv {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFibervisorShapeUnderAttempt(t *testing.T) {
	t.Run("under an attempt", func(t *testing.T) {
		for name, value := range fvFitEnv {
			t.Setenv(name, value)
		}
		attempt := fibervisor.Attempt()
		if attempt != fvFitEnv["FV_PATH"] {
			t.Fatalf("fibervisor.Attempt() = %q under fv's own vector, want FV_PATH %q", attempt, fvFitEnv["FV_PATH"])
		}
		if err := fvFitCheck(fvFitServe(attempt), fvFitEnv["FV_PATH"]); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("with the variables unset", func(t *testing.T) {
		fvFitUnset(t)
		attempt := fibervisor.Attempt()
		if attempt != observe.None {
			t.Fatalf("fibervisor.Attempt() = %q with no fv environment, want %q", attempt, observe.None)
		}
		if err := fvFitCheck(fvFitServe(attempt), observe.None); err != nil {
			t.Fatal(err)
		}
	})
}

// TestFibervisorShapeUnderAttemptViolation feeds the check an empty attempt, an attempt that is not FV_PATH,
// the concrete path as the pattern, a handler named for a refusal, and one name for two requests.
func TestFibervisorShapeUnderAttemptViolation(t *testing.T) {
	path := fvFitEnv["FV_PATH"]
	violations := []struct {
		name   string
		change func(seen []observe.Observation)
	}{
		{"an empty attempt", func(seen []observe.Observation) { seen[0].Attempt = "" }},
		{"an attempt that is not FV_PATH", func(seen []observe.Observation) { seen[2].Attempt = observe.None }},
		{"the concrete path as the pattern", func(seen []observe.Observation) {
			seen[1].Pattern = "/api/responses/premises%3Aa%2Fb%20c"
		}},
		{"a handler named for the 404", func(seen []observe.Observation) { seen[3].Handler = "PUT:" + fvFitPattern }},
		{"one name for two requests", func(seen []observe.Observation) { seen[1].ID = seen[0].ID }},
	}
	for _, v := range violations {
		served := fvFitServe(path)
		v.change(served.seen)
		if fvFitCheck(served, path) == nil {
			t.Errorf("%s passed the check", v.name)
		}
	}
	if fvFitCheck(fvFitServe(observe.None), path) == nil {
		t.Error("observations with no attempt passed the check for FV_PATH")
	}
	if fvFitCheck(fvFitServe(path), "") == nil {
		t.Error("an empty expected attempt passed the check")
	}
}
