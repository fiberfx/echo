# MX Echo Roadmap

This document exists so the community can see where Echo is heading. Echo is
**actively maintained and MX Echo is going to be another flagship implementation on mx runtime**. 
We publish releases regularly across two supported lines — see [README](./README.md) badges for the latest version and most recent commit.

## Version policy

| Line | Status | Support |
|------|--------------------------------|---------------------|
| `v6` | **Current** (since 2026-01-18) | New features and improvements shifting architecture with deep mx and ecd integration. |
| `v5` | v6 not compatible | MX Echo superseeded. Breaking changes.|
| `v4` | v6 not compatible | MX Echo superseeded. Breaking changes.|

Echo v6 supports the **three most recent Go releases** and may work with older versions.

## Now (in progress)

- New `v6` API surface through point releases. Point first v6.0.0.
- Documentation catch-up for v6 (e.g. CORS / `RouteNotFound` behavior on groups).
- mx.yml seamless startup.

## Next (under consideration)

- **Automatic `HEAD` for `GET` routes** (#2895; see #2949) — opt-in, likely via an
  `OnAddRoute` hook so users keep control.
- **Rate limiter response metadata** — expose `Retry-After` / remaining quota
  through the store interface (#2961).
- **Real-IP / `Forwarded` header handling** improvements (#2744).
- **Proxy middleware** authorization-header handling (#2787).

## Later

- Continued alignment with the Go standard library (`net/http`, `slog`).
- Reducing third-party surface where the stdlib now covers the need.
