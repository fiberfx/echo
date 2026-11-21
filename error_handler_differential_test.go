// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// This file proves that DefaultHTTPErrorHandler and ProblemDetailsHTTPErrorHandler, rewritten over
// outcome.Decide, answer every error as they did before. legacyDefault and legacyProblem are the two handlers
// of commit 70c2771 (echo.go:470-519, rfc9457.go:68-129), copied whole. Do not edit them: they are the reference.

func legacyDefault(exposeError bool) HTTPErrorHandler {
	return func(c *Context, err error) {
		if r, _ := UnwrapResponse(c.response); r != nil && r.Committed {
			return
		}

		code := http.StatusInternalServerError
		var sc HTTPStatusCoder
		if errors.As(err, &sc) {
			if tmp := sc.StatusCode(); tmp != 0 {
				code = tmp
			}
		}

		var result any
		switch m := sc.(type) {
		case json.Marshaler: // this type knows how to format itself to JSON
			result = m
		case *HTTPError:
			sText := m.Message
			if sText == "" {
				sText = http.StatusText(code)
			}
			msg := map[string]any{"message": sText}
			if exposeError {
				if wrappedErr := m.Unwrap(); wrappedErr != nil {
					msg["error"] = wrappedErr.Error()
				}
			}
			result = msg
		default:
			msg := map[string]any{"message": http.StatusText(code)}
			if exposeError {
				msg["error"] = err.Error()
			}
			result = msg
		}

		clearRepresentationHeaders(c.Response().Header())
		var cErr error
		if c.Request().Method == http.MethodHead { // Issue #608
			cErr = c.NoContent(code)
		} else {
			cErr = c.JSON(code, result)
		}
		if cErr != nil {
			c.Logger().Error("echo default error handler failed to send error to client", "error", cErr) // truly rare case. ala client already disconnected
		}
	}
}

func legacyProblem(exposeError bool) HTTPErrorHandler {
	return func(c *Context, err error) {
		if r, _ := UnwrapResponse(c.response); r != nil && r.Committed {
			return
		}

		pe, hasProblem := errors.AsType[*ProblemError](err)
		var pder ProblemErrorer
		switch {
		case hasProblem:
		case errors.As(err, &pder):
			if pe = pder.ProblemError(); pe == nil {
				pe = &ProblemError{}
			}
		default:
			pe = &ProblemError{Status: StatusCode(err)}

			if he, ok := errors.AsType[*HTTPError](err); ok {
				pe.Detail = he.Message
				if exposeError {
					if wrapped := he.Unwrap(); wrapped != nil {
						if pe.Detail == "" {
							pe.Detail = wrapped.Error()
						} else {
							pe.Detail = fmt.Sprintf("%s: %s", pe.Detail, wrapped.Error())
						}
					}
				}
			} else if exposeError {
				pe.Detail = err.Error()
			}
		}

		// The problem can be a value the application reuses, for example a package
		// level sentinel error, so the defaults are applied to a copy. Writing them
		// back would race between requests and would permanently alter the error.
		problem := *pe
		if problem.Status == 0 {
			problem.Status = http.StatusInternalServerError
		}
		if problem.Type == "" {
			problem.Type = "about:blank"
		}
		if problem.Title == "" {
			problem.Title = http.StatusText(problem.Status)
		}

		header := c.Response().Header()
		clearRepresentationHeaders(header)
		header.Set(HeaderContentType, MIMEApplicationProblemJSON)

		var cErr error
		if c.Request().Method == http.MethodHead { // Issue #608
			cErr = c.NoContent(problem.Status)
		} else {
			cErr = c.JSON(problem.Status, &problem)
		}
		if cErr != nil {
			c.Logger().Error("echo RFC 9457 error handler failed to send error to client", "error", cErr) // truly rare case. ala client already disconnected
		}
	}
}

// diffCoder states a status and is neither an *HTTPError nor a json.Marshaler.
type diffCoder struct {
	code int
}

func (e *diffCoder) Error() string   { return "diff coder" }
func (e *diffCoder) StatusCode() int { return e.code }

// diffMarshalCoder states a status and writes its own JSON, or fails to.
type diffMarshalCoder struct {
	message string
	code    int
	fail    bool
}

func (e *diffMarshalCoder) Error() string   { return e.message }
func (e *diffMarshalCoder) StatusCode() int { return e.code }

func (e *diffMarshalCoder) MarshalJSON() ([]byte, error) {
	if e.fail {
		return nil, errors.New("diff marshal failed")
	}
	return fmt.Appendf(nil, `{"x":%q}`, e.message), nil
}

// diffProblemErrorer gives a problem and states no status of its own.
type diffProblemErrorer struct {
	pe *ProblemError
}

func (e *diffProblemErrorer) Error() string               { return "diff problem errorer" }
func (e *diffProblemErrorer) ProblemError() *ProblemError { return e.pe }

// diffProblemCoder gives a problem and states another status as a coder.
type diffProblemCoder struct {
	pe   *ProblemError
	code int
}

func (e *diffProblemCoder) Error() string               { return "diff problem coder" }
func (e *diffProblemCoder) ProblemError() *ProblemError { return e.pe }
func (e *diffProblemCoder) StatusCode() int             { return e.code }

// diffCountingCoder gives a problem and counts how often it is asked for a status.
type diffCountingCoder struct {
	pe    *ProblemError
	asked int
}

func (e *diffCountingCoder) Error() string               { return "diff counting coder" }
func (e *diffCountingCoder) ProblemError() *ProblemError { return e.pe }

func (e *diffCountingCoder) StatusCode() int {
	e.asked++
	return http.StatusTeapot
}

// diffUnwrappingWriter is a response wrapper that unwraps to the Context's own Response.
type diffUnwrappingWriter struct {
	http.ResponseWriter
}

func (w *diffUnwrappingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// diffFailingWriter takes the status line and refuses every body, as a client that went away does.
type diffFailingWriter struct {
	http.ResponseWriter
}

func (w *diffFailingWriter) Write([]byte) (int, error) { return 0, errors.New("diff client went away") }

// differentialError makes one error of the table. Each handler gets its own value, so a handler that changed the
// error it was given could not hide behind the other.
type differentialError struct {
	name  string
	build func() error
}

func differentialErrors() []differentialError {
	return []differentialError{
		{"an HTTPError with a message", func() error { return NewHTTPError(http.StatusTeapot, "my_error") }},
		{"an HTTPError with no message", func() error { return &HTTPError{Code: http.StatusTeapot} }},
		{"an HTTPError that states no status", func() error { return &HTTPError{Message: "no status"} }},
		{"an HTTPError wrapping an error", func() error {
			return NewHTTPError(http.StatusBadRequest, "bad").Wrap(errors.New("internal_error"))
		}},
		{"an HTTPError with no message wrapping an error", func() error {
			return HTTPError{Code: http.StatusBadRequest}.Wrap(errors.New("internal_error"))
		}},
		{"an HTTPError wrapping an HTTPError of another status", func() error {
			return NewHTTPError(http.StatusTooEarly, "outer").Wrap(NewHTTPError(http.StatusTeapot, "inner"))
		}},
		{"an HTTPError 404 wrapping a ProblemError 409", wrappedProblem},
		{"a sentinel wrapping an error", func() error { return ErrBadRequest.Wrap(errors.New("internal_error")) }},
		{"an error wrapping an HTTPError", func() error {
			return fmt.Errorf("outer: %w", NewHTTPError(http.StatusForbidden, "no"))
		}},
		{"a ProblemError with every field", func() error {
			return &ProblemError{
				Type:     "https://example.com/probs/out-of-credit",
				Title:    "You do not have enough credit.",
				Status:   http.StatusForbidden,
				Detail:   "Your current balance is 30, but that costs 50.",
				Instance: "/account/12345/msgs/abc",
			}
		}},
		{"a zero ProblemError", func() error { return &ProblemError{} }},
		{"an error wrapping a ProblemError", func() error {
			return fmt.Errorf("outer: %w", &ProblemError{Status: http.StatusConflict})
		}},
		{"a ProblemErrorer with no status", func() error {
			return &diffProblemErrorer{pe: &ProblemError{Title: "Custom", Detail: "no status"}}
		}},
		{"a ProblemErrorer with no problem", func() error { return &diffProblemErrorer{} }},
		{"a ProblemErrorer that is a coder of another status", func() error {
			return &diffProblemCoder{pe: &ProblemError{Status: http.StatusConflict}, code: http.StatusUnprocessableEntity}
		}},
		{"a coder that is a json.Marshaler", func() error {
			return &diffMarshalCoder{message: "custom error msg", code: http.StatusTeapot}
		}},
		{"a coder that is a json.Marshaler and states no status", func() error {
			return &diffMarshalCoder{message: "custom error msg"}
		}},
		{"a coder whose MarshalJSON fails", func() error {
			return &diffMarshalCoder{message: "unsendable", code: http.StatusTeapot, fail: true}
		}},
		{"a coder that is neither", func() error { return &diffCoder{code: http.StatusUnavailableForLegalReasons} }},
		{"an error wrapping a coder", func() error {
			return fmt.Errorf("outer: %w", &diffCoder{code: http.StatusGone})
		}},
		{"ErrNotFound", func() error { return ErrNotFound }},
		{"an error joined with ErrNotFound", func() error { return errors.Join(errors.New("outer"), ErrNotFound) }},
		{"a plain error", func() error { return errors.New("plain") }},
		{"an error wrapping a plain error", func() error { return fmt.Errorf("outer: %w", errors.New("plain")) }},
		{"a plain error joined with an HTTPError", func() error {
			return errors.Join(errors.New("plain"), NewHTTPError(http.StatusConflict, "joined"))
		}},
		{"a nil *HTTPError", func() error { return (*HTTPError)(nil) }},
		{"a nil *ProblemError", func() error { return (*ProblemError)(nil) }},
		{"no error", func() error { return nil }},
	}
}

// wrappedProblem is the row on which the two handlers differ from each other: the default handler answers with the
// outermost coder's 404, the problem handler with the first problem's 409.
func wrappedProblem() error {
	return NewHTTPError(http.StatusNotFound, "missing").Wrap(&ProblemError{Status: http.StatusConflict, Title: "Conflict"})
}

// differentialResponse says what the response is when the handler is called.
type differentialResponse struct {
	name      string
	committed bool
	wrapped   bool
	foreign   bool
	failing   bool
}

func differentialResponses() []differentialResponse {
	return []differentialResponse{
		{name: "a fresh response"},
		{name: "a committed response", committed: true},
		{name: "a wrapped response", wrapped: true},
		{name: "a wrapped, committed response", committed: true, wrapped: true},
		{name: "a writer that does not unwrap to the Response", foreign: true},
		{name: "a writer that refuses the body", failing: true},
	}
}

// handledError is everything a caller can see of one call of an error handler.
type handledError struct {
	header   http.Header
	body     string
	logged   string
	panicked string
	status   int
}

func dropLogTime(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

func handleError(handler HTTPErrorHandler, method string, response differentialResponse, err error) (got handledError) {
	logged := new(bytes.Buffer)
	e := New()
	e.Logger = slog.New(slog.NewTextHandler(logged, &slog.HandlerOptions{ReplaceAttr: dropLogTime}))

	req := httptest.NewRequest(method, "/", nil)
	rec := httptest.NewRecorder()
	var w http.ResponseWriter = rec
	if response.failing {
		w = &diffFailingWriter{ResponseWriter: rec}
	}
	c := e.NewContext(req, w)

	// the headers of the representation the error replaces, and one that must stay
	h := c.Response().Header()
	h.Set(HeaderContentEncoding, "br")
	h.Set(HeaderContentLength, "5000")
	h.Set(HeaderContentDisposition, `attachment; filename="report.csv"`)
	h.Set("ETag", `"v1"`)

	if response.committed {
		c.Response().WriteHeader(http.StatusAccepted)
		_, _ = c.Response().Write([]byte("sent"))
	}
	if response.wrapped {
		c.SetResponse(&diffUnwrappingWriter{ResponseWriter: c.Response()})
	}
	if response.foreign {
		c.SetResponse(rec)
	}

	defer func() {
		if r := recover(); r != nil {
			got.panicked = fmt.Sprint(r)
		}
		got.status = rec.Code
		got.header = rec.Header().Clone()
		got.body = rec.Body.String()
		got.logged = logged.String()
	}()
	handler(c, err)
	return got
}

// sameHandled is the assertion: nil when the two calls cannot be told apart.
func sameHandled(want, got handledError) error {
	if got.panicked != want.panicked {
		return fmt.Errorf("panicked with %q, want %q", got.panicked, want.panicked)
	}
	if got.status != want.status {
		return fmt.Errorf("status %d, want %d", got.status, want.status)
	}
	if !reflect.DeepEqual(got.header, want.header) {
		return fmt.Errorf("headers %v, want %v", got.header, want.header)
	}
	if got.body != want.body {
		return fmt.Errorf("body %q, want %q", got.body, want.body)
	}
	if got.logged != want.logged {
		return fmt.Errorf("logged %q, want %q", got.logged, want.logged)
	}
	return nil
}

func TestErrorHandlersUnchanged(t *testing.T) {
	handlers := []struct {
		name    string
		legacy  func(exposeError bool) HTTPErrorHandler
		current func(exposeError bool) HTTPErrorHandler
	}{
		{"default", legacyDefault, DefaultHTTPErrorHandler},
		{"problem", legacyProblem, ProblemDetailsHTTPErrorHandler},
	}

	// what the table reached, so that a table that compares nothing with nothing cannot pass
	var compared, answered, bodies, silent, logged, panicked int
	for _, handler := range handlers {
		for _, tc := range differentialErrors() {
			for _, response := range differentialResponses() {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					for _, exposeError := range []bool{false, true} {
						want := handleError(handler.legacy(exposeError), method, response, tc.build())
						got := handleError(handler.current(exposeError), method, response, tc.build())
						if err := sameHandled(want, got); err != nil {
							t.Errorf("%s handler, %s, %s, %s, exposeError=%v: %v",
								handler.name, tc.name, response.name, method, exposeError, err)
						}

						compared++
						switch {
						case want.panicked != "":
							panicked++
						case response.committed:
							if want.status == http.StatusAccepted && want.body == "sent" {
								silent++
							}
						case want.status >= 400:
							answered++
						}
						if !response.committed && want.body != "" {
							bodies++
						}
						if want.logged != "" {
							logged++
						}
					}
				}
			}
		}
	}

	if compared == 0 || answered == 0 || bodies == 0 || silent == 0 || logged == 0 || panicked == 0 {
		t.Fatalf("the table is hollow: compared %d, answered with an error status %d, with a body %d, "+
			"left a committed response alone %d, logged a failed send %d, panicked %d",
			compared, answered, bodies, silent, logged, panicked)
	}
}

// TestErrorHandlersUnchangedViolation feeds the assertion two responses that differ and fails when it passes
// them. The first pair is real: on an *HTTPError 404 wrapping a *ProblemError 409 the default handler answers
// 404 and the problem handler 409, before the change and after it.
func TestErrorHandlersUnchangedViolation(t *testing.T) {
	fresh := differentialResponses()[0]
	pairs := []struct {
		name    string
		byError HTTPErrorHandler
		byFirst HTTPErrorHandler
	}{
		{"legacy", legacyDefault(false), legacyProblem(false)},
		{"current", DefaultHTTPErrorHandler(false), ProblemDetailsHTTPErrorHandler(false)},
	}
	for _, pair := range pairs {
		byError := handleError(pair.byError, http.MethodGet, fresh, wrappedProblem())
		byFirst := handleError(pair.byFirst, http.MethodGet, fresh, wrappedProblem())
		if byError.status != http.StatusNotFound || byFirst.status != http.StatusConflict {
			t.Fatalf("%s handlers: the default handler answered %d and the problem handler %d, want 404 and 409",
				pair.name, byError.status, byFirst.status)
		}
		if sameHandled(byError, byFirst) == nil {
			t.Fatalf("%s handlers: a 404 and a 409 passed the comparison", pair.name)
		}
	}

	base := handleError(legacyDefault(false), http.MethodGet, fresh, wrappedProblem())
	differing := map[string]func(*handledError){
		"header":   func(h *handledError) { h.header.Set(HeaderContentEncoding, "br") },
		"body":     func(h *handledError) { h.body += " " },
		"logged":   func(h *handledError) { h.logged = "a line" },
		"panicked": func(h *handledError) { h.panicked = "a value" },
		"status":   func(h *handledError) { h.status++ },
	}
	for field, change := range differing {
		other := base
		other.header = base.header.Clone()
		change(&other)
		if sameHandled(base, other) == nil {
			t.Errorf("two responses that differ in %s passed the comparison", field)
		}
	}
	if err := sameHandled(base, base); err != nil {
		t.Errorf("a response differs from itself: %v", err)
	}
}

// TestErrorHandlersLeaveCommittedErrorUnread pins what the comparison found worth a name of its own: once the
// response is committed neither handler looks at the error, so an error that cannot be asked for its status
// (a nil pointer in an error value) is passed over in silence, as at v6.1.0.
func TestErrorHandlersLeaveCommittedErrorUnread(t *testing.T) {
	committed := differentialResponses()[1]
	handlers := map[string]HTTPErrorHandler{
		"default": DefaultHTTPErrorHandler(false),
		"problem": ProblemDetailsHTTPErrorHandler(false),
	}
	for name, handler := range handlers {
		got := handleError(handler, http.MethodGet, committed, (*HTTPError)(nil))
		if got.panicked != "" || got.status != http.StatusAccepted || got.body != "sent" {
			t.Errorf("%s handler: status %d, body %q, panic %q; want the committed 202 left alone",
				name, got.status, got.body, got.panicked)
		}
	}
}

// TestProblemHandlerAsksTheFirstCoder names the one difference the comparison cannot see. For an error that
// holds a problem the handler of 70c2771 never asked a coder for its status; the handler over outcome.Decide
// asks the first coder in the chain once, before it knows the error holds a problem, and does not use the
// answer. The responses are the same.
func TestProblemHandlerAsksTheFirstCoder(t *testing.T) {
	fresh := differentialResponses()[0]
	legacy := &diffCountingCoder{pe: &ProblemError{Status: http.StatusConflict}}
	current := &diffCountingCoder{pe: &ProblemError{Status: http.StatusConflict}}

	want := handleError(legacyProblem(false), http.MethodGet, fresh, legacy)
	got := handleError(ProblemDetailsHTTPErrorHandler(false), http.MethodGet, fresh, current)
	if err := sameHandled(want, got); err != nil {
		t.Errorf("the responses differ: %v", err)
	}
	if got.status != http.StatusConflict {
		t.Errorf("status %d, want the problem's 409 and not the coder's 418", got.status)
	}
	if legacy.asked != 0 || current.asked != 1 {
		t.Errorf("the coder was asked %d times by the legacy handler and %d by the current one, want 0 and 1",
			legacy.asked, current.asked)
	}
}
