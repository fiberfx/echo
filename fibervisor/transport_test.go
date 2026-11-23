// SPDX-License-Identifier: MIT

package fibervisor_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/fiberfx/mx-echo/v7/fibervisor"
	"github.com/fiberfx/mx-echo/v7/reqid"
)

// staleParent is a traceparent a caller set before Transport saw the request.
const staleParent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

// recorder is a RoundTripper that keeps the request it was given and answers 204.
type recorder struct{ got *http.Request }

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.got = req
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
}

// outgoing returns a request with two headers, one of them staleParent, whose context names id unless id is zero.
func outgoing(t *testing.T, id reqid.ID) *http.Request {
	t.Helper()
	ctx := context.Background()
	if !id.IsZero() {
		ctx = reqid.NewContext(ctx, id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://downstream.test/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Traceparent", staleParent)
	req.Header.Set("X-Kept", "a")
	return req
}

// checkSent returns an error unless sent, what next was given for caller, is what Transport owes: caller itself
// when want is "", else a clone whose one traceparent is want and whose other headers are before's. Either way
// caller's headers are still before's.
func checkSent(caller *http.Request, before http.Header, sent *http.Request, want string) error {
	if !reflect.DeepEqual(caller.Header, before) {
		return fmt.Errorf("the caller's headers are %v after RoundTrip, were %v", caller.Header, before)
	}
	if want == "" {
		if sent != caller {
			return fmt.Errorf("a request with no name was not passed unchanged: next got %p for %p", sent, caller)
		}
		return nil
	}
	if sent == caller {
		return fmt.Errorf("the caller's request was sent, not a clone")
	}
	if got := sent.Header.Values("Traceparent"); len(got) != 1 || got[0] != want {
		return fmt.Errorf("traceparent sent %q, want [%q]", got, want)
	}
	rest, wantRest := sent.Header.Clone(), before.Clone()
	rest.Del("Traceparent")
	wantRest.Del("Traceparent")
	if !reflect.DeepEqual(rest, wantRest) {
		return fmt.Errorf("the other headers sent are %v, want %v", rest, wantRest)
	}
	return nil
}

func TestTransport(t *testing.T) {
	node, _ := fvOwn.RequestNode(fvRequestID)
	named := header(fvOtherTrace, node)
	for _, tt := range []struct {
		name string
		s    fibervisor.Supervision
		id   reqid.ID
		want string // "" when the request passes unchanged
	}{
		{"a named request replaces the caller's traceparent", fvOwn, fvRequestID, named},
		{"a request with no name", fvOwn, reqid.ID{}, ""},
		{"a named request under the zero Supervision", fibervisor.Supervision{}, fvRequestID, ""},
		{"a named request under a hand-built Supervision", fibervisor.Supervision{Path: fvAttempt}, fvRequestID, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			next := &recorder{}
			caller := outgoing(t, tt.id)
			before := caller.Header.Clone()
			resp, err := tt.s.Transport(next).RoundTrip(caller)
			if err != nil || resp.StatusCode != http.StatusNoContent {
				t.Fatalf("RoundTrip = %v, %v, want next's 204", resp, err)
			}
			if err := checkSent(caller, before, next.got, tt.want); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestTransportNoHeader holds a named request whose Header is nil to a clone with the traceparent, not a panic.
func TestTransportNoHeader(t *testing.T) {
	next := &recorder{}
	caller := &http.Request{Method: http.MethodGet, URL: outgoing(t, fvRequestID).URL}
	caller = caller.WithContext(reqid.NewContext(context.Background(), fvRequestID))
	if _, err := fvOwn.Transport(next).RoundTrip(caller); err != nil {
		t.Fatal(err)
	}
	node, _ := fvOwn.RequestNode(fvRequestID)
	if caller.Header != nil || next.got.Header.Get("Traceparent") != header(fvOtherTrace, node) {
		t.Errorf("caller's Header %v, sent %v", caller.Header, next.got.Header)
	}
}

// TestTransportFresh holds Transport to a new value per call, and nil to http.DefaultTransport's use.
func TestTransportFresh(t *testing.T) {
	if fvOwn.Transport(nil) == fvOwn.Transport(nil) {
		t.Error("two calls of Transport returned the same RoundTripper")
	}
}

// TestTransportTwoServers sends a named request from one in-process server to another through Transport over
// http.DefaultTransport: the traceparent the downstream reads, the link it would keep, names the upstream's
// RequestNode in the upstream's trace.
func TestTransportTwoServers(t *testing.T) {
	links := make(chan reqid.Link, 1)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		link, _ := reqid.ParseTraceparent(r.Header.Get("Traceparent"))
		links <- link
		w.WriteHeader(http.StatusNoContent)
	}))
	defer down.Close()

	leaf(fvTrace, fvAttempt).set(t)
	s, ok := fibervisor.Read()
	if !ok {
		t.Fatal("Read() is false under fv's leaf environment")
	}
	client := &http.Client{Transport: s.Transport(nil)}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := reqid.NewContext(r.Context(), fvRequestID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, down.URL, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))
	defer up.Close()

	resp, err := http.Get(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("upstream answered %d", resp.StatusCode)
	}
	link := <-links
	node, _ := s.RequestNode(fvRequestID)
	if want := header(fvTrace, node); link.String() != want {
		t.Errorf("the downstream's link is %q, want %q", link.String(), want)
	}
}

// inPlace stands in for a wrong Transport: it calls set on the caller's request and sends that request to next.
type inPlace struct {
	next *recorder
	set  func(*http.Request)
}

func (p inPlace) RoundTrip(req *http.Request) (*http.Response, error) {
	p.set(req)
	return p.next.RoundTrip(req)
}

// cloning stands in for a wrong Transport that clones: it calls set on a clone and sends the clone to next.
type cloning struct {
	next *recorder
	set  func(*http.Request)
}

func (c cloning) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	c.set(req)
	return c.next.RoundTrip(req)
}

func TestTransportViolation(t *testing.T) {
	node, _ := fvOwn.RequestNode(fvRequestID)
	named := header(fvOtherTrace, node)
	setNamed := func(r *http.Request) { r.Header.Set("Traceparent", named) }
	for _, bad := range []struct {
		name string
		id   reqid.ID
		want string
		rt   func(next *recorder) http.RoundTripper
	}{
		{"the caller's request changed in place", fvRequestID, named, func(next *recorder) http.RoundTripper {
			return inPlace{next, setNamed}
		}},
		{"a traceparent added beside the caller's", fvRequestID, named, func(next *recorder) http.RoundTripper {
			return cloning{next, func(r *http.Request) { r.Header.Add("Traceparent", named) }}
		}},
		{"the attempt's node sent for the request", fvRequestID, named, func(next *recorder) http.RoundTripper {
			return cloning{next, func(r *http.Request) { r.Header.Set("Traceparent", header(fvOtherTrace, fvAttemptNode)) }}
		}},
		{"another header dropped from the clone", fvRequestID, named, func(next *recorder) http.RoundTripper {
			return cloning{next, func(r *http.Request) { setNamed(r); r.Header.Del("X-Kept") }}
		}},
		{"a request with no name given a header", reqid.ID{}, "", func(next *recorder) http.RoundTripper {
			return cloning{next, setNamed}
		}},
		{"a request with no name stripped of its traceparent", reqid.ID{}, "", func(next *recorder) http.RoundTripper {
			return inPlace{next, func(r *http.Request) { r.Header.Del("Traceparent") }}
		}},
	} {
		next := &recorder{}
		caller := outgoing(t, bad.id)
		before := caller.Header.Clone()
		if _, err := bad.rt(next).RoundTrip(caller); err != nil {
			t.Fatal(err)
		}
		if checkSent(caller, before, next.got, bad.want) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
}
