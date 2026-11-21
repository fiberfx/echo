// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/middleware"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/outcome"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// observations keeps every observation an Echo made. Every string in an Observation may be kept.
type observations struct {
	seen []observe.Observation
}

func (s *observations) Observe(o observe.Observation) { s.seen = append(s.seen, o) }

// observeOne serves one request and returns the response with the one observation the request produced.
func observeOne(t *testing.T, e *echo.Echo, sink *observations, req *http.Request) (*httptest.ResponseRecorder, observe.Observation) {
	t.Helper()
	before := len(sink.seen)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if got := len(sink.seen) - before; got != 1 {
		t.Fatalf("%s %s: %d observations, want 1", req.Method, req.URL.Path, got)
	}
	return rec, sink.seen[before]
}

type admissionWant struct {
	admission observe.Admission
	pattern   string
	handler   string
	kind      outcome.Kind
	status    int
}

// checkAdmission is the assertion of TestAdmission: where routing ended, with which pattern and handler.
func checkAdmission(o observe.Observation, want admissionWant) error {
	if o.ID.IsZero() {
		return errors.New("the request has no name")
	}
	if o.Admission != want.admission || o.Pattern != want.pattern || o.Handler != want.handler {
		return fmt.Errorf("admission %s, pattern %q, handler %q; want %s, %q, %q",
			o.Admission, o.Pattern, o.Handler, want.admission, want.pattern, want.handler)
	}
	if o.Kind != want.kind || o.Status != want.status {
		return fmt.Errorf("kind %s, status %d; want %s, %d", o.Kind, o.Status, want.kind, want.status)
	}
	return nil
}

// TestAdmission is row FC-3: a request refused before a handler was attributed can be told from one that was
// routed, and its observation names no handler.
func TestAdmission(t *testing.T) {
	sink := &observations{}
	e := echo.NewWithConfig(echo.Config{Observer: observe.Observer{Sink: sink}})
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if c.Request().Host == "foreign.example" {
				return echo.NewHTTPError(http.StatusForbidden, "foreign host")
			}
			return next(c)
		}
	})
	passThrough := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	found := func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) }
	e.GET("/users/:id", found)
	e.Group("/api", passThrough).GET("/things", found)
	if _, err := e.AddRoute(echo.Route{Method: http.MethodGet, Path: "/named", Name: "the-named-route", Handler: found}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		method string
		host   string
		path   string
		want   admissionWant
	}{
		{"a Pre refusal", http.MethodGet, "foreign.example", "/users/42", admissionWant{observe.BeforeRouting, observe.None, observe.None, outcome.AdmissionRefusal, http.StatusForbidden}},
		{"an unknown path", http.MethodGet, "", "/nowhere", admissionWant{observe.NotFound, observe.None, observe.None, outcome.AdmissionRefusal, http.StatusNotFound}},
		{"a wrong method", http.MethodPost, "", "/users/42", admissionWant{observe.MethodNotAllowed, "/users/:id", observe.None, outcome.AdmissionRefusal, http.StatusMethodNotAllowed}},
		{"an OPTIONS request the router answers itself", http.MethodOptions, "", "/users/42", admissionWant{observe.MethodNotAllowed, "/users/:id", observe.None, outcome.OK, http.StatusNoContent}},
		{"an unknown path under a group with middleware", http.MethodGet, "", "/api/unknown", admissionWant{observe.NotFound, "/api/*", observe.None, outcome.AdmissionRefusal, http.StatusNotFound}},
		{"a match", http.MethodGet, "", "/users/42", admissionWant{observe.Routed, "/users/:id", "GET:/users/:id", outcome.OK, http.StatusNoContent}},
		{"a match under the group", http.MethodGet, "", "/api/things", admissionWant{observe.Routed, "/api/things", "GET:/api/things", outcome.OK, http.StatusNoContent}},
		{"a match of a named route", http.MethodGet, "", "/named", admissionWant{observe.Routed, "/named", "the-named-route", outcome.OK, http.StatusNoContent}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.host != "" {
				req.Host = tc.host
			}
			rec, o := observeOne(t, e, sink, req)
			if rec.Code != tc.want.status {
				t.Errorf("the response's status is %d, want %d", rec.Code, tc.want.status)
			}
			if err := checkAdmission(o, tc.want); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestAdmissionViolation feeds the assertion the observations a hollow hook would make of an unknown path
// under a group with middleware: the group's not-found route reported as a handler.
func TestAdmissionViolation(t *testing.T) {
	want := admissionWant{observe.NotFound, "/api/*", observe.None, outcome.AdmissionRefusal, http.StatusNotFound}
	good := observe.Observation{
		ID:        reqid.New(1, 1),
		Admission: observe.NotFound,
		Pattern:   "/api/*",
		Handler:   observe.None,
		Kind:      outcome.AdmissionRefusal,
		Status:    http.StatusNotFound,
	}
	if err := checkAdmission(good, want); err != nil {
		t.Fatalf("the true observation was refused: %v", err)
	}

	asHandler := good
	asHandler.Handler = "echo_route_not_found:/api/*"
	asRouted := asHandler
	asRouted.Admission = observe.Routed
	asRouted.Kind = outcome.BusinessRefusal
	unnamed := good
	unnamed.ID = reqid.ID{}
	violations := []struct {
		name string
		o    observe.Observation
	}{
		{"the not-found route named as the handler", asHandler},
		{"the not-found route taken for a match", asRouted},
		{"a request with no name", unnamed},
	}
	for _, v := range violations {
		if checkAdmission(v.o, want) == nil {
			t.Errorf("%s passed the check", v.name)
		}
	}
}

type kindWant struct {
	status   int
	kind     outcome.Kind
	panicked bool
}

// checkKind is the assertion of TestKinds: how the request ended.
func checkKind(o observe.Observation, want kindWant) error {
	if o.Kind != want.kind || o.Panicked != want.panicked || o.Status != want.status {
		return fmt.Errorf("kind %s, panicked %v, status %d; want %s, %v, %d",
			o.Kind, o.Panicked, o.Status, want.kind, want.panicked, want.status)
	}
	return nil
}

// TestKinds is row FC-6: an admission refusal, a business refusal, a worker failure and a lost contact are
// four kinds, and a panic that nothing recovers still reaches the caller.
func TestKinds(t *testing.T) {
	errBoom := errors.New("boom")
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	routes := func(e *echo.Echo) {
		e.GET("/refused", func(c *echo.Context) error {
			return echo.NewHTTPError(http.StatusBadRequest, "refused")
		})
		e.GET("/panics", func(c *echo.Context) error { panic(errBoom) })
		e.GET("/gone", func(c *echo.Context) error {
			stop() // the client goes away while the handler runs
			return c.NoContent(http.StatusOK)
		})
	}

	sink := &observations{}
	e := echo.NewWithConfig(echo.Config{Observer: observe.Observer{Sink: sink}})
	routes(e)

	recoveringSink := &observations{}
	recovering := echo.NewWithConfig(echo.Config{Observer: observe.Observer{Sink: recoveringSink}})
	recovering.Use(middleware.Recover())
	routes(recovering)

	t.Run("an unknown path", func(t *testing.T) {
		_, o := observeOne(t, e, sink, httptest.NewRequest(http.MethodGet, "/nowhere", nil))
		if err := checkKind(o, kindWant{kind: outcome.AdmissionRefusal, status: http.StatusNotFound}); err != nil {
			t.Error(err)
		}
	})
	t.Run("a handler's 400", func(t *testing.T) {
		_, o := observeOne(t, e, sink, httptest.NewRequest(http.MethodGet, "/refused", nil))
		if err := checkKind(o, kindWant{kind: outcome.BusinessRefusal, status: http.StatusBadRequest}); err != nil {
			t.Error(err)
		}
	})
	t.Run("a panic under middleware.Recover", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/panics", nil)
		rec, o := observeOne(t, recovering, recoveringSink, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("the response's status is %d, want 500", rec.Code)
		}
		if err := checkKind(o, kindWant{kind: outcome.WorkerFailure, status: http.StatusInternalServerError}); err != nil {
			t.Error(err)
		}
	})
	t.Run("a panic that nothing recovers", func(t *testing.T) {
		before := len(sink.seen)
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panics", nil))
		}()
		if recovered != errBoom {
			t.Fatalf("the caller recovered %v, want the handler's own panic value", recovered)
		}
		if got := len(sink.seen) - before; got != 1 {
			t.Fatalf("%d observations of the panic, want 1", got)
		}
		o := sink.seen[before]
		if err := checkKind(o, kindWant{kind: outcome.WorkerFailure, panicked: true, status: http.StatusOK}); err != nil {
			t.Error(err)
		}
		if o.Committed {
			t.Error("the observation says a status line was written; the handler wrote none")
		}
	})
	t.Run("a client that goes away", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/gone", nil).WithContext(ctx)
		_, o := observeOne(t, e, sink, req)
		if err := checkKind(o, kindWant{kind: outcome.LostContact, status: http.StatusOK}); err != nil {
			t.Error(err)
		}
	})
}

// TestKindsViolation feeds the assertion a cancelled request classified as a worker failure, and a panic
// reported as none.
func TestKindsViolation(t *testing.T) {
	lost := kindWant{kind: outcome.LostContact, status: http.StatusOK}
	if checkKind(observe.Observation{Kind: outcome.WorkerFailure, Status: http.StatusOK}, lost) == nil {
		t.Error("a cancelled request classified worker-failure passed the check")
	}
	failed := kindWant{kind: outcome.WorkerFailure, panicked: true, status: http.StatusOK}
	if checkKind(observe.Observation{Kind: outcome.WorkerFailure, Status: http.StatusOK}, failed) == nil {
		t.Error("a panic reported as none passed the check")
	}
	if err := checkKind(observe.Observation{Kind: outcome.LostContact, Status: http.StatusOK}, lost); err != nil {
		t.Errorf("the true observation was refused: %v", err)
	}
}

// TestObservedRouteName pins how the observation comes to name a matched route. With no observer the route a
// request matched has the name it was registered with, empty for an unnamed route, as at v6.1.0. With an
// observer an unnamed route is given the name Routes reports for it, and a route added through Router()
// directly keeps what it has.
func TestObservedRouteName(t *testing.T) {
	found := func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) }
	matchedName := func(e *echo.Echo) (registered, listed, matched string) {
		ri := e.GET("/users/:id", func(c *echo.Context) error {
			matched = c.RouteInfo().Name
			return c.NoContent(http.StatusNoContent)
		})
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/42", nil))
		return ri.Name, e.Router().Routes()[0].Name, matched
	}

	registered, listed, matched := matchedName(echo.New())
	if registered != "GET:/users/:id" || listed != "GET:/users/:id" || matched != "" {
		t.Errorf("no observer: registered %q, listed %q, matched %q; want GET:/users/:id twice and an empty name",
			registered, listed, matched)
	}

	sink := &observations{}
	e := echo.NewWithConfig(echo.Config{Observer: observe.Observer{Sink: sink}})
	registered, listed, matched = matchedName(e)
	if registered != "GET:/users/:id" || listed != "GET:/users/:id" || matched != "GET:/users/:id" {
		t.Errorf("an observer: registered %q, listed %q, matched %q; want GET:/users/:id three times",
			registered, listed, matched)
	}

	if _, err := e.Router().Add(echo.Route{Method: http.MethodGet, Path: "/direct", Handler: found}); err != nil {
		t.Fatal(err)
	}
	_, o := observeOne(t, e, sink, httptest.NewRequest(http.MethodGet, "/direct", nil))
	if o.Admission != observe.Routed || o.Pattern != "/direct" || o.Handler != observe.None {
		t.Errorf("a route added through Router(): admission %s, pattern %q, handler %q; want routed, /direct, none",
			o.Admission, o.Pattern, o.Handler)
	}
}

// namedRequest is the name each layer read for one request.
type namedRequest struct {
	pre          reqid.ID
	use          reqid.ID
	handler      reqid.ID
	errorHandler reqid.ID
	observed     reqid.ID
}

// serveNamed sends one request through a Pre middleware, a Use middleware, a handler that returns an error
// and an error handler, and returns the name each of them read with the name the observation carries.
func serveNamed(t *testing.T, observer observe.Observer) namedRequest {
	t.Helper()
	sink := &observations{}
	observer.Sink = sink
	var named namedRequest
	e := echo.NewWithConfig(echo.Config{
		Observer: observer,
		HTTPErrorHandler: func(c *echo.Context, err error) {
			named.errorHandler = c.RequestID()
			echo.DefaultHTTPErrorHandler(false)(c, err)
		},
	})
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			named.pre = c.RequestID()
			return next(c)
		}
	})
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			named.use = c.RequestID()
			return next(c)
		}
	})
	e.POST("/orders", func(c *echo.Context) error {
		named.handler = c.RequestID()
		return echo.NewHTTPError(http.StatusBadRequest, "refused")
	})

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"same":"body"}`))
	_, o := observeOne(t, e, sink, req)
	named.observed = o.ID
	return named
}

// checkNamedOnce is the assertion of TestNamedOnce: every layer of a request read one name, the name is not
// zero, and no two requests have the same one.
func checkNamedOnce(requests []namedRequest) error {
	names := map[reqid.ID]bool{}
	for i, r := range requests {
		if r.pre.IsZero() {
			return fmt.Errorf("request %d has no name before the first middleware", i)
		}
		if r.use != r.pre || r.handler != r.pre || r.errorHandler != r.pre || r.observed != r.pre {
			return fmt.Errorf("request %d: Pre read %s, Use %s, the handler %s, the error handler %s, the observation %s",
				i, r.pre, r.use, r.handler, r.errorHandler, r.observed)
		}
		if names[r.pre] {
			return fmt.Errorf("request %d has the name of an earlier request, %s", i, r.pre)
		}
		names[r.pre] = true
	}
	return nil
}

// TestNamedOnce is row FC-1: a request is named once, before the first middleware, every layer reads that one
// name, and two requests that are the same in every byte get two names.
func TestNamedOnce(t *testing.T) {
	requests := []namedRequest{serveNamed(t, observe.Observer{}), serveNamed(t, observe.Observer{})}
	for range 100 {
		requests = append(requests, serveNamed(t, observe.Observer{}))
	}
	if err := checkNamedOnce(requests); err != nil {
		t.Error(err)
	}

	t.Run("no observer, no name", func(t *testing.T) {
		e := echo.New()
		named := true
		e.GET("/", func(c *echo.Context) error {
			named = !c.RequestID().IsZero()
			return c.NoContent(http.StatusNoContent)
		})
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		if named {
			t.Error("a request has a name with no observer set")
		}
	})

	t.Run("a Mint that returns the zero ID leaves the request unnamed", func(t *testing.T) {
		r := serveNamed(t, observe.Observer{Mint: func() reqid.ID { return reqid.ID{} }})
		if r != (namedRequest{}) {
			t.Errorf("a layer read a name the source did not give: %+v", r)
		}
		line := string(observe.Observation{ID: r.observed}.AppendJSON(nil))
		if !strings.Contains(line, `"id":"none"`) {
			t.Errorf("the unnamed request is written %s, want \"id\":\"none\"", line)
		}
	})
}

// TestNamedOnceViolation gives the Echo a source that returns one name for every request: "two requests, two
// names" must fail. It also feeds the assertion a layer that read another name.
func TestNamedOnceViolation(t *testing.T) {
	constant := observe.Observer{Mint: func() reqid.ID { return reqid.New(1, 1) }}
	if checkNamedOnce([]namedRequest{serveNamed(t, constant), serveNamed(t, constant)}) == nil {
		t.Error("two requests with one name passed the check")
	}
	one := serveNamed(t, observe.Observer{})
	one.handler = reqid.New(2, 2)
	if checkNamedOnce([]namedRequest{one}) == nil {
		t.Error("a handler that read another name passed the check")
	}
	if checkNamedOnce([]namedRequest{{}}) == nil {
		t.Error("a request with no name passed the check")
	}
}

// observedOnce is one request's observations with the response's status at the moment each was made.
type observedOnce struct {
	seen            []observe.Observation
	statusAtObserve []int
}

// checkOneObservation is the assertion of TestOneObservation: one observation, made after the response's
// status was written, with the registered pattern.
func checkOneObservation(got observedOnce, pattern string, status int) error {
	if len(got.seen) != 1 {
		return fmt.Errorf("%d observations, want 1", len(got.seen))
	}
	o := got.seen[0]
	if o.Pattern != pattern {
		return fmt.Errorf("pattern %q, want %q", o.Pattern, pattern)
	}
	if status != 0 && (got.statusAtObserve[0] != status || o.Status != status || !o.Committed) {
		return fmt.Errorf("observed status %d (committed %v) while the response had %d, want %d written before the observation",
			o.Status, o.Committed, got.statusAtObserve[0], status)
	}
	return nil
}

// TestOneObservation is row FC-13: the sink is called once per request, after the error handler wrote, with
// the registered pattern and never the path; a panic that nothing recovers is observed too.
func TestOneObservation(t *testing.T) {
	var rec *httptest.ResponseRecorder
	var got observedOnce
	sink := observe.SinkFunc(func(o observe.Observation) {
		got.seen = append(got.seen, o)
		got.statusAtObserve = append(got.statusAtObserve, rec.Code)
	})
	e := echo.NewWithConfig(echo.Config{Observer: observe.Observer{Sink: sink}})
	e.GET("/users/:id", func(c *echo.Context) error {
		switch c.Param("id") {
		case "refused":
			return echo.NewHTTPError(http.StatusBadRequest, "refused")
		case "panics":
			panic("boom")
		}
		return c.String(http.StatusOK, c.Param("id"))
	})

	tests := []struct {
		name    string
		path    string
		pattern string
		status  int
		panics  bool
	}{
		{"a match", "/users/42", "/users/:id", http.StatusOK, false},
		{"an error from the handler", "/users/refused", "/users/:id", http.StatusBadRequest, false},
		{"an unknown path", "/nowhere", observe.None, http.StatusNotFound, false},
		{"a panic that nothing recovers", "/users/panics", "/users/:id", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec = httptest.NewRecorder()
			got = observedOnce{}
			panicked := func() (panicked bool) {
				defer func() { panicked = recover() != nil }()
				e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
				return false
			}()
			if panicked != tc.panics {
				t.Fatalf("panicked %v, want %v", panicked, tc.panics)
			}
			if err := checkOneObservation(got, tc.pattern, tc.status); err != nil {
				t.Error(err)
			}
			if tc.panics && len(got.seen) == 1 && !got.seen[0].Panicked {
				t.Error("the observation of the panic does not say so")
			}
		})
	}
}

// TestOneObservationViolation feeds the assertion no observation for the panic, the path in place of the
// pattern, two observations, and an observation made before the status was written.
func TestOneObservationViolation(t *testing.T) {
	routed := observe.Observation{Pattern: "/users/:id", Status: http.StatusBadRequest, Committed: true}
	asPath := routed
	asPath.Pattern = "/users/42"
	violations := []struct {
		name string
		got  observedOnce
	}{
		{"no observation of the panic", observedOnce{}},
		{"the path as the pattern", observedOnce{[]observe.Observation{asPath}, []int{http.StatusBadRequest}}},
		{"two observations", observedOnce{[]observe.Observation{routed, routed}, []int{http.StatusBadRequest, http.StatusBadRequest}}},
		{"an observation before the error handler wrote", observedOnce{[]observe.Observation{routed}, []int{http.StatusOK}}},
	}
	for _, v := range violations {
		if checkOneObservation(v.got, "/users/:id", http.StatusBadRequest) == nil {
			t.Errorf("%s passed the check", v.name)
		}
	}
	good := observedOnce{[]observe.Observation{routed}, []int{http.StatusBadRequest}}
	if err := checkOneObservation(good, "/users/:id", http.StatusBadRequest); err != nil {
		t.Errorf("the true observation was refused: %v", err)
	}
}

// checkKindAndStatus is the assertion of TestKindAndStatus: the kind and the wire status, each as observed.
func checkKindAndStatus(o observe.Observation, kind outcome.Kind, status int) error {
	if o.Kind != kind || o.Status != status {
		return fmt.Errorf("kind %s with status %d, want %s with %d", o.Kind, o.Status, kind, status)
	}
	return nil
}

// TestKindAndStatus is row K5: the kind follows the error when one was returned and the wire status when none
// was, and the observation carries both, under an error handler of the service's own.
func TestKindAndStatus(t *testing.T) {
	sink := &observations{}
	e := echo.NewWithConfig(echo.Config{
		Observer: observe.Observer{Sink: sink},
		HTTPErrorHandler: func(c *echo.Context, err error) {
			if r, _ := echo.UnwrapResponse(c.Response()); r != nil && r.Committed {
				return
			}
			if errors.Is(err, echo.ErrNotFound) {
				_ = c.HTML(http.StatusOK, "<p>nothing here, and that is fine</p>") // a soft 404
				return
			}
			_ = c.NoContent(http.StatusInternalServerError)
		},
	})
	e.GET("/written-then-failed", func(c *echo.Context) error {
		if err := c.String(http.StatusOK, "written"); err != nil {
			return err
		}
		return errors.New("failed after the answer")
	})
	e.GET("/failed-in-silence", func(c *echo.Context) error {
		return c.NoContent(http.StatusInternalServerError)
	})

	tests := []struct {
		name   string
		path   string
		kind   outcome.Kind
		status int
	}{
		{"an error after a 200 was written", "/written-then-failed", outcome.WorkerFailure, http.StatusOK},
		{"no error and a 500 on the wire", "/failed-in-silence", outcome.WorkerFailure, http.StatusInternalServerError},
		{"an unknown path answered 200 by the error handler", "/nowhere", outcome.AdmissionRefusal, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, o := observeOne(t, e, sink, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.status {
				t.Errorf("the response's status is %d, want %d", rec.Code, tc.status)
			}
			if err := checkKindAndStatus(o, tc.kind, tc.status); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestKindAndStatusViolation feeds the assertion a kind that followed the wire status although an error was
// returned.
func TestKindAndStatusViolation(t *testing.T) {
	byStatus := observe.Observation{Kind: outcome.OK, Status: http.StatusOK}
	if checkKindAndStatus(byStatus, outcome.WorkerFailure, http.StatusOK) == nil {
		t.Error("an error after a 200 classified ok passed the check")
	}
	if checkKindAndStatus(byStatus, outcome.AdmissionRefusal, http.StatusOK) == nil {
		t.Error("a soft 404 classified ok passed the check")
	}
}

// checkUnaskable is the assertion of TestObservationOfAnUnaskableError: the request ended without a panic and
// was observed once, as a worker failure with the status that was written.
func checkUnaskable(recovered any, seen []observe.Observation) error {
	if recovered != nil {
		return fmt.Errorf("the request panicked: %v", recovered)
	}
	if len(seen) != 1 {
		return fmt.Errorf("%d observations, want 1", len(seen))
	}
	if o := seen[0]; o.Kind != outcome.WorkerFailure || o.Status != http.StatusOK || o.Panicked {
		return fmt.Errorf("kind %s, status %d, panicked %v; want worker-failure, 200, false", o.Kind, o.Status, o.Panicked)
	}
	return nil
}

// TestObservationOfAnUnaskableError: a handler that answers and then returns a nil pointer as its error is
// served in silence at v6.1.0, because the error handlers do not look at an error once the response is
// committed. Asking that error for its status panics. The observation must not turn the served request into a
// failed one: it reports an error that states no status.
func TestObservationOfAnUnaskableError(t *testing.T) {
	serve := func(e *echo.Echo) (recovered any) {
		e.GET("/", func(c *echo.Context) error {
			var failed *echo.HTTPError
			if err := c.String(http.StatusOK, "written"); err != nil {
				return err
			}
			return failed
		})
		defer func() { recovered = recover() }()
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		return nil
	}

	if recovered := serve(echo.New()); recovered != nil {
		t.Fatalf("with no observer the request panicked: %v", recovered)
	}
	sink := &observations{}
	recovered := serve(echo.NewWithConfig(echo.Config{Observer: observe.Observer{Sink: sink}}))
	if err := checkUnaskable(recovered, sink.seen); err != nil {
		t.Error(err)
	}
}

// TestObservationOfAnUnaskableErrorViolation feeds the assertion the panic an observation would raise if it
// asked the error without care, a request that was not observed, and one taken for a success.
func TestObservationOfAnUnaskableErrorViolation(t *testing.T) {
	failure := observe.Observation{Kind: outcome.WorkerFailure, Status: http.StatusOK}
	if checkUnaskable("invalid memory address or nil pointer dereference", []observe.Observation{failure}) == nil {
		t.Error("a request that panicked passed the check")
	}
	if checkUnaskable(nil, nil) == nil {
		t.Error("a request that was not observed passed the check")
	}
	success := observe.Observation{Kind: outcome.OK, Status: http.StatusOK}
	if checkUnaskable(nil, []observe.Observation{success}) == nil {
		t.Error("an error taken for a success passed the check")
	}
	if err := checkUnaskable(nil, []observe.Observation{failure}); err != nil {
		t.Errorf("the true observation was refused: %v", err)
	}
}
