# FiberFX Echo roadmap

## v6.0.0

The first FiberFX version uses the module path `github.com/fiberfx/echo/v6` and requires Go 1.27.4. The root package, middleware and test helpers share one release. The [contributor guide](README.md#open-the-workspace) explains how to open the module and test an application against a local checkout.

The [v6.0.0 release](https://github.com/fiberfx/echo/releases/tag/v6.0.0) contains the framework, middleware and test helpers. Existing LabStack v4 and v5 releases retain their upstream module paths. Applications that expose Echo types through another package must migrate that package as well.

## Under consideration

- Explore startup through an MX `mx.yml` job and integration with the ecd runtime.
- Evaluate opt-in automatic `HEAD` handling for `GET` routes.
- Expose retry and quota information from the rate limiter.
- Review `Forwarded` handling and proxy authorization header behavior.

Changes to routing, middleware or application lifecycle need a consumer test before they become part of the v6 contract.
