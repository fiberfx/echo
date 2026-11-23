// SPDX-License-Identifier: MIT

package stdhttp_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v7/observe"
	"github.com/fiberfx/mx-echo/v7/observe/stdhttp"
	"github.com/fiberfx/mx-echo/v7/outcome"
	"github.com/fiberfx/mx-echo/v7/reqid"
)

const (
	savePattern = "PUT /api/responses/{key}"
	savePath    = "/api/responses/premises%3Aa%2Fb%20c"
	savedKey    = "premises:a/b c"
	mintMilli   = 1700000000000
	w3cHeader   = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
)

var (
	errBoom = errors.New("boom")
	errGone = errors.New("write: broken pipe")
)

// sink keeps every observation it is given.
type sink struct{ seen []observe.Observation }

func (s *sink) Observe(o observe.Observation) { s.seen = append(s.seen, o) }

// counter mints the names 1, 2, 3 and so on at one millisecond, so a test knows the name of each request.
func counter() func() reqid.ID {
	var n uint32
	return func() reqid.ID {
		n++
		return reqid.New(mintMilli, n)
	}
}

// routes is a standard mux with the handlers these tests ask for.
func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(savePattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Name", reqid.FromContext(r.Context()).String())
		w.Header().Set("X-Key", r.PathValue("key"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /quiet", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /body", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /flush", func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.Flush()
	})
	mux.HandleFunc("GET /early", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusInternalServerError) // superfluous: the answer is already 201
	})
	mux.HandleFunc("GET /refused", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusConflict)
	})
	mux.HandleFunc("GET /failed", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	})
	mux.HandleFunc("GET /deadline", func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic(errBoom) })
	mux.HandleFunc("/tree/{id}/", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/quiet", http.StatusTemporaryRedirect)
	})
	return mux
}

// expect is the observation of the request counter named n when the service stated nothing and the mux
// reported no pattern: a test sets what its request adds.
func expect(n uint32, method string) observe.Observation {
	return observe.Observation{
		ID:      reqid.New(mintMilli, n),
		Attempt: observe.None,
		Build:   observe.None,
		Method:  method,
		Pattern: observe.None,
		Handler: observe.None,
		Status:  http.StatusOK,
	}
}

// routed returns o as the mux reports a match of pattern.
func routed(o observe.Observation, pattern string) observe.Observation {
	o.Admission, o.Pattern = observe.Routed, pattern
	return o
}

// check returns an error unless got is want in every field, and its pattern is not the request's own path.
func check(got, want observe.Observation, requestPath string) error {
	if got.Pattern == requestPath {
		return fmt.Errorf("the pattern is the concrete path %q", requestPath)
	}
	if got != want {
		return fmt.Errorf("the observation is\n  %s\nwant\n  %s", got.AppendJSON(nil), want.AppendJSON(nil))
	}
	return nil
}

// only returns the one observation s holds.
func only(t *testing.T, s *sink) observe.Observation {
	t.Helper()
	if len(s.seen) != 1 {
		t.Fatalf("%d observations, want 1", len(s.seen))
	}
	return s.seen[0]
}

func TestWrap(t *testing.T) {
	t.Run("a match", func(t *testing.T) {
		var s sink
		h := stdhttp.Wrap(routes(), observe.Observer{
			Sink: &s, Mint: counter(), Attempt: "q00000000000100000007/run/a1", Build: "example.com/svc@v1.2.3",
		})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, savePath, strings.NewReader("v")))

		want := routed(expect(1, http.MethodPut), savePattern)
		want.Attempt, want.Build = "q00000000000100000007/run/a1", "example.com/svc@v1.2.3"
		want.Kind, want.Status, want.Committed = outcome.OK, http.StatusNoContent, true
		got := only(t, &s)
		if err := check(got, want, savePath); err != nil {
			t.Error(err)
		}
		if got.Pattern == "/api/responses/"+savedKey {
			t.Errorf("the pattern is the decoded path %q", got.Pattern)
		}
		if rec.Code != http.StatusNoContent {
			t.Errorf("status %d, want 204", rec.Code)
		}
		// The handler read the same name from the request's context, and the mux's own path value.
		if name := rec.Header().Get("X-Name"); name != want.ID.String() {
			t.Errorf("the handler read the name %q, want %q", name, want.ID)
		}
		if key := rec.Header().Get("X-Key"); key != savedKey {
			t.Errorf("the handler read the key %q, want %q", key, savedKey)
		}
	})

	cases := []struct {
		name      string
		method    string
		target    string
		pattern   string // the registered pattern, or "" when the mux reports none
		status    int
		committed bool
		kind      outcome.Kind
	}{
		{"an unknown path", http.MethodGet, "/nope", "", http.StatusNotFound, true, outcome.AdmissionRefusal},
		{"a method that is not allowed", http.MethodDelete, savePath, "", http.StatusMethodNotAllowed, true, outcome.AdmissionRefusal},
		{"a handler that writes nothing", http.MethodGet, "/quiet", "GET /quiet", http.StatusOK, false, outcome.OK},
		{"a body with no status", http.MethodGet, "/body", "GET /body", http.StatusOK, true, outcome.OK},
		{"a flush with no status", http.MethodGet, "/flush", "GET /flush", http.StatusOK, true, outcome.OK},
		{"an early hint, then the answer", http.MethodGet, "/early", "GET /early", http.StatusCreated, true, outcome.OK},
		{"a handler's refusal", http.MethodGet, "/refused", "GET /refused", http.StatusConflict, true, outcome.BusinessRefusal},
		{"a handler's failure", http.MethodGet, "/failed", "GET /failed", http.StatusBadGateway, true, outcome.WorkerFailure},
		// The mux's own redirects, against the redirect of a registered handler, which is routed.
		{"the mux's trailing-slash redirect", http.MethodGet, "/tree/42", "", http.StatusTemporaryRedirect, true, outcome.OK},
		{"the mux's cleaned-path redirect", http.MethodGet, "//quiet", "", http.StatusTemporaryRedirect, true, outcome.OK},
		{"a handler's own redirect", http.MethodGet, "/moved", "GET /moved", http.StatusTemporaryRedirect, true, outcome.OK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s sink
			h := stdhttp.Wrap(routes(), observe.Observer{Sink: &s, Mint: counter()})
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(c.method, c.target, nil))

			want := expect(1, c.method)
			if c.pattern != "" {
				want = routed(want, c.pattern)
			}
			want.Kind, want.Status, want.Committed = c.kind, c.status, c.committed
			if err := check(only(t, &s), want, c.target); err != nil {
				t.Error(err)
			}
		})
	}

	t.Run("a panic is observed and not recovered", func(t *testing.T) {
		var s sink
		h := stdhttp.Wrap(routes(), observe.Observer{Sink: &s, Mint: counter()})
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
		}()
		if recovered != errBoom {
			t.Errorf("the test recovered %v, want the handler's own panic value", recovered)
		}
		want := routed(expect(1, http.MethodGet), "GET /boom")
		want.Kind, want.Panicked = outcome.WorkerFailure, true
		if err := check(only(t, &s), want, "/boom"); err != nil {
			t.Error(err)
		}
	})

	t.Run("no sink returns next itself", func(t *testing.T) {
		mux := routes()
		if h := stdhttp.Wrap(mux, observe.Observer{}); h != http.Handler(mux) {
			t.Errorf("Wrap with the zero Observer returned %T, want next itself", h)
		}
		if h := stdhttp.Wrap(mux, observe.Observer{Mint: counter(), Attempt: "a", Build: "b"}); h != http.Handler(mux) {
			t.Errorf("Wrap with no sink returned %T, want next itself", h)
		}
	})

	t.Run("the default name is the clock's and differs per request", func(t *testing.T) {
		var s sink
		h := stdhttp.Wrap(routes(), observe.Observer{Sink: &s})
		before := time.Now().UnixMilli()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/quiet", nil))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/quiet", nil))
		after := time.Now().UnixMilli()
		if len(s.seen) != 2 {
			t.Fatalf("%d observations, want 2", len(s.seen))
		}
		first, second := s.seen[0].ID, s.seen[1].ID
		if first.IsZero() || second.IsZero() || first == second {
			t.Errorf("two requests were named %s and %s, want two names that are not zero", first, second)
		}
		if at := first.UnixMilli(); at < before || at > after {
			t.Errorf("the name's time %d is outside the request's %d to %d", at, before, after)
		}
		if s.seen[0].Attempt != observe.None || s.seen[0].Build != observe.None {
			t.Errorf("attempt %q and build %q, want none for both", s.seen[0].Attempt, s.seen[0].Build)
		}
	})

	t.Run("an incoming traceparent is a link, never the name", func(t *testing.T) {
		var s sink
		h := stdhttp.Wrap(routes(), observe.Observer{Sink: &s, Mint: counter()})
		req := httptest.NewRequest(http.MethodGet, "/quiet", nil)
		req.Header.Set("Traceparent", w3cHeader)
		h.ServeHTTP(httptest.NewRecorder(), req)
		bad := httptest.NewRequest(http.MethodGet, "/quiet", nil)
		bad.Header.Set("Traceparent", strings.ToUpper(w3cHeader))
		h.ServeHTTP(httptest.NewRecorder(), bad)
		if len(s.seen) != 2 {
			t.Fatalf("%d observations, want 2", len(s.seen))
		}

		link, ok := reqid.ParseTraceparent(w3cHeader)
		if !ok {
			t.Fatal("the W3C example did not parse")
		}
		want := routed(expect(1, http.MethodGet), "GET /quiet")
		want.Link, want.Kind = link, outcome.OK
		if err := check(s.seen[0], want, "/quiet"); err != nil {
			t.Error(err)
		}
		// A header that does not parse is dropped.
		want = routed(expect(2, http.MethodGet), "GET /quiet")
		want.Kind = outcome.OK
		if err := check(s.seen[1], want, "/quiet"); err != nil {
			t.Error(err)
		}
	})

	t.Run("a client that went away is lost contact", func(t *testing.T) {
		var s sink
		h := stdhttp.Wrap(routes(), observe.Observer{Sink: &s, Mint: counter()})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/quiet", nil).WithContext(ctx))
		want := routed(expect(1, http.MethodGet), "GET /quiet")
		want.Kind = outcome.LostContact
		if err := check(only(t, &s), want, "/quiet"); err != nil {
			t.Error(err)
		}
	})

	t.Run("the writer below is reached through Unwrap", func(t *testing.T) {
		var s sink
		h := stdhttp.Wrap(routes(), observe.Observer{Sink: &s, Mint: counter()})
		below := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		h.ServeHTTP(below, httptest.NewRequest(http.MethodGet, "/deadline", nil))
		if below.deadlines != 1 {
			t.Errorf("the writer below was given %d deadlines, want 1", below.deadlines)
		}
		want := routed(expect(1, http.MethodGet), "GET /deadline")
		want.Kind, want.Status, want.Committed = outcome.OK, http.StatusNoContent, true
		if err := check(only(t, &s), want, "/deadline"); err != nil {
			t.Error(err)
		}
	})

	// The mux reports a pattern for its own redirects, though no registered handler answers them (net/http/server.go,
	// findHandler, at Go 1.27.4): a CONNECT request redirected to a trailing slash carries the redirect's concrete
	// path, any other method the pattern the redirect's target matches. The edge reports none for each.
	for _, c := range []struct {
		method, target, reported string
	}{
		{http.MethodConnect, "/tree/42", "/tree/42/"},
		{http.MethodGet, "/tree/42", "/tree/{id}/"},
		{http.MethodGet, "//quiet", "GET /quiet"},
		{http.MethodGet, "/a/../quiet", "GET /quiet"},
	} {
		t.Run("the mux's redirect of "+c.method+" "+c.target+" reports no pattern", func(t *testing.T) {
			var s sink
			mux := routes()
			var reported string
			spy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mux.ServeHTTP(w, r)
				reported = r.Pattern
			})
			h := stdhttp.Wrap(spy, observe.Observer{Sink: &s, Mint: counter()})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, nil))
			if reported != c.reported {
				t.Fatalf("the mux reported the pattern %q for its redirect, not %q: its rule changed, and "+
					"registeredPattern and this case need another look", reported, c.reported)
			}
			if rec.Code != http.StatusTemporaryRedirect {
				t.Errorf("status %d, want the mux's redirect", rec.Code)
			}
			want := expect(1, c.method)
			want.Kind, want.Status, want.Committed = outcome.OK, rec.Code, true
			if err := check(only(t, &s), want, c.target); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestWrapFlush(t *testing.T) {
	cases := []struct {
		name      string
		below     http.ResponseWriter
		want      error // nil for a flush that worked
		committed bool
	}{
		{"a writer that flushes", httptest.NewRecorder(), nil, true},
		{"a writer that cannot flush", &plainWriter{header: http.Header{}}, http.ErrNotSupported, false},
		{"a client that has gone", &goneWriter{ResponseRecorder: httptest.NewRecorder()}, errGone, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s sink
			var flushed error
			mux := http.NewServeMux()
			mux.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
				flushed = http.NewResponseController(w).Flush()
			})
			h := stdhttp.Wrap(mux, observe.Observer{Sink: &s, Mint: counter()})
			h.ServeHTTP(c.below, httptest.NewRequest(http.MethodGet, "/stream", nil))
			if err := checkFlush(flushed, c.want); err != nil {
				t.Error(err)
			}
			want := routed(expect(1, http.MethodGet), "GET /stream")
			want.Kind, want.Committed = outcome.OK, c.committed
			if err := check(only(t, &s), want, "/stream"); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestWrapFlushViolation(t *testing.T) {
	// The success the recorder reported before it had FlushError, for writers that cannot flush.
	if checkFlush(nil, http.ErrNotSupported) == nil {
		t.Error("a flush that reported success passed for a writer that cannot flush")
	}
	if checkFlush(nil, errGone) == nil {
		t.Error("a flush that reported success passed for a client that has gone")
	}
	if checkFlush(errGone, nil) == nil {
		t.Error("a flush that failed passed for a writer that flushes")
	}
}

// checkFlush returns an error unless the handler's flush returned nil when want is nil, and an error that is
// want otherwise.
func checkFlush(got, want error) error {
	if want == nil && got != nil {
		return fmt.Errorf("the flush returned %v, want nil", got)
	}
	if want != nil && !errors.Is(got, want) {
		return fmt.Errorf("the flush returned %v, want %v", got, want)
	}
	return nil
}

func TestWrapViolation(t *testing.T) {
	want := routed(expect(1, http.MethodPut), savePattern)
	want.Kind, want.Status, want.Committed = outcome.OK, http.StatusNoContent, true

	// The concrete path as the pattern, even when the expectation was written with the same mistake.
	concrete := routed(want, savePath)
	if check(concrete, want, savePath) == nil {
		t.Error("the concrete path passed as the pattern")
	}
	if check(concrete, concrete, savePath) == nil {
		t.Error("the concrete path passed as the pattern when the expectation held it too")
	}
	// A handler attributed, an unmatched request called routed, a panic not reported, another request's name.
	for name, change := range map[string]func(*observe.Observation){
		"a handler":         func(o *observe.Observation) { o.Handler = "PUT:/api/responses/{key}" },
		"admission unknown": func(o *observe.Observation) { o.Admission = observe.AdmissionUnknown },
		"a panic":           func(o *observe.Observation) { o.Panicked = true },
		"another name":      func(o *observe.Observation) { o.ID = reqid.New(mintMilli, 2) },
		"another status":    func(o *observe.Observation) { o.Status = http.StatusOK },
	} {
		got := want
		change(&got)
		if check(got, want, savePath) == nil {
			t.Errorf("an observation with %s passed", name)
		}
	}
}

// deadlineWriter is a writer with a method the edge's own writer does not have: it is reached only through
// Unwrap.
type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadlines int
}

func (w *deadlineWriter) SetWriteDeadline(time.Time) error {
	w.deadlines++
	return nil
}

// plainWriter is a writer with none of the optional methods: it cannot flush, and has no Unwrap.
type plainWriter struct{ header http.Header }

func (w *plainWriter) Header() http.Header         { return w.header }
func (w *plainWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *plainWriter) WriteHeader(int)             {}

// goneWriter is a writer whose flush fails, as the server's does when the client has gone.
type goneWriter struct {
	*httptest.ResponseRecorder
}

func (w *goneWriter) FlushError() error { return errGone }
