# MX Echo roadmap

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
