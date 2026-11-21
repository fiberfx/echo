# Working on FiberFX Echo

Open the module root in GoLand or use the Go tools from that directory. The module is `github.com/fiberfx/echo/v6`. Its minimum Go version is 1.27.4. The [README](README.md) includes a tested server example and the steps for developing Echo and an application in the same Go workspace.

## Development commands

- `go list ./...` lists the framework, middleware and `echotest` packages.
- `go test ./...` runs the tests. Use `go test -race ./...` when changing request handling or shared state.
- `go vet ./...` checks the module. `make check` also runs the configured linter and race suite.
- `go test -run='^$' -bench=. -benchmem ./...` runs the benchmarks.
- `GOWORK=off go mod tidy -diff` checks the dependency manifest independently of a contributor workspace.

## Where to make a change

| Responsibility | Source |
| --- | --- |
| Route registration, middleware composition, request dispatch | `echo.go`, `group.go`, `router.go` |
| Request and response state | `context.go`, `response.go` |
| Body and parameter binding | `bind.go`, `binder.go`, `binder_generic.go` |
| Middleware implementations | `middleware/` |
| Public testing helpers | `echotest/` |

Handlers have the signature `func(*echo.Context) error`. Middleware wraps an `echo.HandlerFunc`. Preserve the order of pre-routing, group and route middleware when changing the request path. Keep package imports under the v6 module path and retain upstream attribution in inherited files.
