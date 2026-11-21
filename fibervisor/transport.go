// SPDX-License-Identifier: MIT

package fibervisor

import (
	"net/http"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

// Transport returns a RoundTripper that sends each request through next (http.DefaultTransport when nil) with
// traceparent 00-<Trace>-<RequestNode(id)>-01 when the request's context names a request (reqid.FromContext); a
// request with no name, or under the zero Supervision, passes unchanged. It clones a request before setting the header.
//
// The traceparent replaces one the request already has: this request is its parent. Each call returns a new value.
func (s Supervision) Transport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &transport{s: s, next: next}
}

// transport is the RoundTripper Transport returns.
type transport struct {
	s    Supervision
	next http.RoundTripper
}

// RoundTrip sends r through next, a clone of r with the request's traceparent when r's context names a request.
func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	node, ok := t.s.RequestNode(reqid.FromContext(r.Context()))
	if !ok {
		return t.next.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	r.Header.Set("Traceparent", t.s.traceparent(node))
	return t.next.RoundTrip(r)
}
