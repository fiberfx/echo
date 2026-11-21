// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/outcome"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// RequestID returns the request's name, or the zero ID when Config.Observer is off.
func (c *Context) RequestID() reqid.ID {
	return c.requestID
}

// newObserver returns the observer an Echo keeps for config, with its defaults filled in, or nil when config
// has no sink: naming and observing are then off.
func newObserver(config observe.Observer) *observe.Observer {
	if config.Sink == nil {
		return nil
	}
	if config.Mint == nil {
		config.Mint = mintRequestID
	}
	if config.Attempt == "" {
		config.Attempt = observe.None
	}
	if config.Build == "" {
		config.Build = observe.None
	}
	return &config
}

// mintRequestID is the default source of a request's name: this process's clock and random numbers. The name
// is not a secret.
func mintRequestID() reqid.ID {
	return reqid.New(time.Now().UnixMilli(), rand.Uint32())
}

// serveObserved is serveHTTP's request path with a name before it and one observation after it. The
// observation is deferred, so a panic is observed too; the panic itself is not recovered here.
func (e *Echo) serveObserved(c *Context, r *http.Request) {
	c.requestID = e.observer.Mint()

	var err error
	panicked := true // still true in the deferred call only when a panic is unwinding
	defer func() {
		e.observer.Sink.Observe(e.observation(c, r, err, panicked))
	}()

	if e.premiddleware == nil {
		c.handler = e.router.Route(c)
		err = e.chain(c)
	} else {
		err = e.preChain(c)
	}
	if err != nil {
		e.HTTPErrorHandler(c, err)
	}
	panicked = false
}

// observation reads what happened to one request from the Context. r is the request serveHTTP was given, not
// c.Request(): a middleware may have replaced that one with a request whose context ends when the middleware
// returns.
func (e *Echo) observation(c *Context, r *http.Request, err error, panicked bool) observe.Observation {
	admission, routePattern, handler := observe.BeforeRouting, observe.None, observe.None
	if ri := c.route; ri != nil {
		if c.path != "" {
			routePattern = c.path // set by the router on any path match, a 405 included
		}
		switch {
		case ri == methodNotAllowedRouteInfo || ri.Name == MethodNotAllowedRouteName:
			admission = observe.MethodNotAllowed
		case ri == notFoundRouteInfo || ri.Name == NotFoundRouteName || ri.Method == RouteNotFound:
			admission = observe.NotFound // the last test is a registered not-found route, as a Group with middleware adds
		default:
			admission = observe.Routed
			if ri.Name != "" {
				handler = ri.Name
			}
		}
	}

	errStatus := 0
	if err != nil && !panicked {
		errStatus = statedStatus(err)
	}
	res := c.orgResponse // survives SetResponse; its Status is 200 until a status is written
	link, _ := reqid.ParseTraceparent(r.Header.Get("Traceparent"))
	facts := outcome.Facts{
		ErrStatus: errStatus,
		Status:    res.Status,
		Routed:    admission == observe.Routed,
		Panicked:  panicked,
		Gone:      r.Context().Err() != nil,
	}
	return observe.Observation{
		ID:        c.requestID,
		Link:      link,
		Attempt:   e.observer.Attempt,
		Build:     e.observer.Build,
		Method:    r.Method,
		Admission: admission,
		Pattern:   routePattern,
		Handler:   handler,
		Kind:      outcome.Classify(facts),
		Status:    res.Status,
		Committed: res.Committed,
		Panicked:  panicked,
	}
}

// statedStatus is the status err states, and 500 when it states none. An error that cannot be asked, such as a
// nil pointer in an error value, states none: the observation must not fail a request the framework served.
func statedStatus(err error) (status int) {
	defer func() {
		if recover() != nil {
			status = http.StatusInternalServerError
		}
	}()
	return outcome.Decide(StatusCode(err), false).Status
}
