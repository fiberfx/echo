// SPDX-License-Identifier: MIT

// Package stdhttp makes the observation of package observe around any http.Handler: a service on the standard
// mux names and observes its requests with no Echo type.
//
// The pattern comes from the request the handler chain was given: http.ServeMux writes the pattern it matched
// to that request. A layer between Wrap and the mux that passes on a copy of the request (r.WithContext) keeps
// the pattern from Wrap, and the observation then says "none" and admission "unknown". A request the mux did
// not match (no route, a method that is not allowed) carries no pattern either: the standard mux does not tell
// the two apart, so the admission is "unknown" and the kind is the status's. The mux's own redirect (to the
// path with a trailing slash, or to the cleaned path) is answered by no registered handler, and is observed the
// same way, although the mux reports a pattern for it: the edge knows it by its 307 and its Location, which is
// the request's cleaned path or that path with a slash added. A registered handler that itself answers 307 to
// one of those two places is observed the same way too.
//
// The package is an effect package: the default name reads the clock and a random source. It is imported by a
// service, never by the root package.
package stdhttp

import (
	"math/rand/v2"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/outcome"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// Wrap names each request, puts the name in the request's context (reqid.FromContext), and gives o.Sink one
// observation after next returns: the pattern is the standard mux's r.Pattern, the handler is None.
//
// With o.Sink nil it returns next itself. A panic in next is observed and not recovered.
func Wrap(next http.Handler, o observe.Observer) http.Handler {
	if o.Sink == nil {
		return next
	}
	if o.Mint == nil {
		o.Mint = mint
	}
	if o.Attempt == "" {
		o.Attempt = observe.None
	}
	if o.Build == "" {
		o.Build = observe.None
	}
	return &edge{next: next, observer: o}
}

// mint is the default name: the clock's millisecond and a random number.
func mint() reqid.ID {
	return reqid.New(time.Now().UnixMilli(), rand.Uint32())
}

// edge is the handler Wrap returns.
type edge struct {
	next     http.Handler
	observer observe.Observer
}

// ServeHTTP is next's ServeHTTP with a name before and one observation after.
func (e *edge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := e.observer.Mint()
	named := r.WithContext(reqid.NewContext(r.Context(), id))
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	panicked := true // still true in the deferred call only when a panic is unwinding; it is not recovered
	defer func() { e.observer.Sink.Observe(e.observation(id, r, named, rec, panicked)) }()
	e.next.ServeHTTP(rec, named)
	panicked = false
}

// observation is the value for one finished request. r is the request ServeHTTP was given, whose context says
// whether the client went away; named is the request next was given, which carries the mux's pattern.
func (e *edge) observation(id reqid.ID, r, named *http.Request, rec *recorder, panicked bool) observe.Observation {
	admission, pattern := observe.AdmissionUnknown, observe.None
	if p := registeredPattern(named, rec); p != "" {
		admission, pattern = observe.Routed, p
	}
	link, _ := reqid.ParseTraceparent(r.Header.Get("Traceparent"))
	return observe.Observation{
		ID: id, Link: link, Attempt: e.observer.Attempt, Build: e.observer.Build,
		Method: r.Method, Admission: admission, Pattern: pattern, Handler: observe.None,
		Kind: outcome.Classify(outcome.Facts{Status: rec.status, Routed: admission == observe.Routed,
			Panicked: panicked, Gone: r.Context().Err() != nil}),
		Status: rec.status, Committed: rec.wrote, Panicked: panicked,
	}
}

// registeredPattern returns the registered pattern of the handler that answered r, or "" when no registered
// handler answered it. The mux reports a pattern for its own redirects too (net/http/server.go, findHandler,
// read at Go 1.27.4): for a CONNECT request redirected to the same path with a trailing slash, r.Pattern is the
// redirect's concrete path, and no registered pattern that matches a path is that path with a slash added; for
// any other method, r.Pattern is the pattern the redirect's target matches, and the answer is a 307 whose
// Location is the cleaned path, or that path with a slash added.
func registeredPattern(r *http.Request, rec *recorder) string {
	if r.Method == http.MethodConnect && r.Pattern == r.URL.Path+"/" {
		return ""
	}
	if r.Pattern != "" && rec.status == http.StatusTemporaryRedirect {
		target, _, _ := strings.Cut(rec.Header().Get("Location"), "?")
		if clean := cleanPath(r.URL.EscapedPath()); target == clean || target == clean+"/" {
			return ""
		}
	}
	return r.Pattern
}

// cleanPath is the path the standard mux matches for a request whose escaped path is p: rooted, with . and ..
// resolved and repeated slashes removed, and a trailing slash kept (net/http's cleanPath).
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	if p[len(p)-1] == '/' && np != "/" {
		np += "/"
	}
	return np
}

// recorder is the writer next is given. It records the status of the answer and whether it was written.
type recorder struct {
	http.ResponseWriter
	status int  // 200 until a final status is written
	wrote  bool // a final status line was written through this writer
}

// WriteHeader records the first final status. A 1xx status other than 101 is not the answer: more may follow.
func (w *recorder) WriteHeader(code int) {
	if !w.wrote && (code >= 200 || code == http.StatusSwitchingProtocols) {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write writes the body; net/http writes the status line first when none was written.
func (w *recorder) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// FlushError flushes the writer below and returns its error: one that wraps http.ErrNotSupported when it
// cannot flush, or the write's error when the client has gone. http.ResponseController asks for it first.
func (w *recorder) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err == nil {
		w.wrote = true
	}
	return err
}

// Flush keeps http.Flusher for a handler that asks the writer for it. It drops FlushError's error.
func (w *recorder) Flush() {
	_ = w.FlushError()
}

// Unwrap returns the writer Wrap's handler was given, for http.ResponseController.
func (w *recorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
