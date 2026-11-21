# MX Echo roadmap

## v6.7.1

A patch from the second train's review. `LinesOf` keeps v6.6.0's output for a hand-built supervision, a percent-encoded COR in Baggage is read, and the internal MCP rules refuse a legacy initialize no served revision can answer.

## v6.7.0

A request's identity can cross processes. [reqid](reqid/) mints and reads the COR value, a branded id holding a time, a node and a sequence, beside the request's own name. Two internal packages prepare an MCP endpoint; neither is in the request path yet.

## v6.6.2

A second patch that a service does not see. The reflection engine of binding lives in one internal package with its own tests, and `bind.go` goes from 592 to 166 lines.

## v6.6.1

A patch that a service does not see. The client IP and scheme extraction lives in one internal package with its own tests, and the root package is about 150 lines smaller.

## v6.6.0

An observation line names its attempt the way fv's commands do. [fibervisor](fibervisor/) reads the attempt's trace, path and node id once, writes the trace and the node id into each line, and names the signals fv stops a leaf with; the [reference service](examples/reference/) uses all three. The root package still imports none of it, and a test now holds that for every package the root imports.

## v6.5.0

The join with a supervisor is a package a service opts into. [fibervisor](fibervisor/) reads the attempt fv gave the process and writes observations as JSON lines; [observe/stdhttp](observe/stdhttp/) gives any `http.Handler` the same named, observed request; the [reference service](examples/reference/) shows both on the standard mux with an exit status a supervisor can read. The root package imports neither. Not yet shown: the reference service under a running fv.

## v6.4.0

A stop is reported, not assumed. A server started with `OnStop` reports whether its stop drained or was forced, after the listener closed, and a [server-scoped owner](server/) holds work that outlives a request until the stop. `Echo.Freeze` makes the route table and the middleware chains fixed once registration is done. Each is opt-in. The middleware's decisions are pure functions in one internal package.

## v6.3.0

A request can be named and observed. `Config.Observer` is the one opt-in: it names each request before the first middleware and hands a sink one [observation](observe/) after the error handler ran, with the [name](reqid/), where routing ended, the registered pattern and how the request ended. Without it the request path is unchanged. The framework records nothing durable and restarts nothing: an observation is the service's own report.

## v6.2.0

The first reusable subsystems are [typed binding](bind/) and [HTTP server lifecycle](server/). Each can be imported without the root Echo package. Existing applications can continue to use the root API. The Fibervisor responses service is the compatibility consumer for listener startup and graceful shutdown.

Two pure packages join them: [the route pattern grammar](route/pattern/) and [request outcomes](outcome/). The root's route registration, `RouteInfo.Reverse` and built-in error handlers are adapters over them, and answer as they did in v6.1.0.

## v6.1.0

MX Echo uses `github.com/fiberfx/mx-echo/v6`. Applications migrating from the original v6.0.0 release must update imports in dependent packages together.

## v6.0.0

The first FiberFX version was released under the module path `github.com/fiberfx/echo/v6` and requires Go 1.27.4. The root package, middleware and test helpers share one release. The [contributor guide](README.md#open-the-workspace) explains how to open the module and test an application against a local checkout.

The [v6.0.0 release](https://github.com/fiberfx/mx-echo/releases/tag/v6.0.0) contains the framework, middleware and test helpers. Existing LabStack v4 and v5 releases retain their upstream module paths. Applications that expose Echo types through another package must migrate that package as well.

## Under consideration

- Explore startup through an MX `mx.yml` job and integration with the ecd runtime.
- Evaluate opt-in automatic `HEAD` handling for `GET` routes.
- Expose retry and quota information from the rate limiter.
- Review `Forwarded` handling and proxy authorization header behavior.

Changes to routing, middleware or application lifecycle need a consumer test before they become part of the v6 contract.
