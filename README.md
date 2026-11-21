# MX Echo v6

MX Echo is a Go HTTP framework derived from LabStack Echo v5.4.6 with the v5.4.7 fixes adapted to v6. Its router, middleware and request-test helpers ship together as one module, `github.com/fiberfx/mx-echo/v6`. The minimum Go version is **1.27.4**.

## Open the workspace

Open the directory containing [go.mod](go.mod) in GoLand or another Go editor. Run Go commands from that directory. Eight standalone packages were added from v6.2.0 to v6.5.0. They share the framework’s module and version, and [examples/reference](examples/reference/) composes them with no Echo type.

| Import | Responsibility |
| --- | --- |
| `github.com/fiberfx/mx-echo/v6` | Route registration, request context, binding, responses and server lifecycle |
| `github.com/fiberfx/mx-echo/v6/bind` | Convert strings and slices with pure functions, including defaults and typed time options |
| `github.com/fiberfx/mx-echo/v6/server` | Serve any `http.Handler` with a caller-owned cancellation context, listener and shutdown policy, report how the stop ended and own work that outlives a request |
| `github.com/fiberfx/mx-echo/v6/route/pattern` | Read a route pattern into its static, parameter and wildcard parts with pure functions |
| `github.com/fiberfx/mx-echo/v6/outcome` | Classify how a request ended and decide the status of an error response with pure functions |
| `github.com/fiberfx/mx-echo/v6/reqid` | Name a request with a small value ordered by time, and parse an incoming `traceparent` |
| `github.com/fiberfx/mx-echo/v6/observe` | Describe one finished request as a value, write it as JSON and hand it to a sink |
| `github.com/fiberfx/mx-echo/v6/observe/stdhttp` | Name and observe the requests of any `http.Handler`, with the standard mux's pattern |
| `github.com/fiberfx/mx-echo/v6/fibervisor` | Read the attempt the fv supervisor gave the process, state the build, and write observations as JSON lines |
| `github.com/fiberfx/mx-echo/v6/middleware` | HTTP middleware, including request logging, recovery and compression |
| `github.com/fiberfx/mx-echo/v6/echotest` | Request contexts, response recorders and fixtures for handler tests |

```sh
go version
go list ./...
go test ./...
go vet ./...
go test -race ./...
```

The `go` directive sets the minimum supported toolchain. To repeat the contributor checks on this exact patch release, use `GOTOOLCHAIN=go1.27.4 make check`. The `lint` target uses the repository's [golangci-lint configuration](.golangci.yaml). `make init` installs its pinned linter. Put the Go binary installation directory on your `PATH` before running `make check`.

## Use v6 from an application

To install v6.5.0 in an application module, run `go get github.com/fiberfx/mx-echo/v6@v6.5.0`.

While developing MX Echo and an application together, put their checkouts under a common parent. For an application module in `myapp`, run the following from the parent directory. If a workspace already exists there, use `go work use ./mx-echo ./myapp` to add the modules.

```sh
go work init ./mx-echo ./myapp
go -C myapp test ./...
```

The workspace selects the local MX Echo checkout. The published v6.0.0 tag declares the historical module path `github.com/fiberfx/echo/v6`. When upgrading from v6.0.0, update the imports of all three packages together. After adding imports such as `middleware`, run `go mod tidy` in the application to record the dependencies its packages use.

For an application moving from v5, change Echo imports in handlers, middleware and tests together. Go treats types from the two module paths as distinct. A dependency that accepts `*echo.Context` or returns `echo.MiddlewareFunc` must use the same module path as its caller. Standard `http.Handler` implementations can be mounted with `echo.WrapHandler`.

## Serve a request

```go
package main

import (
	"log"
	"net/http"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/middleware"
)

func main() {
	e := echo.New()
	e.Use(middleware.RequestLogger(), middleware.Recover())
	e.GET("/hello/:name", func(c *echo.Context) error {
		return c.String(http.StatusOK, "Hello, "+c.Param("name")+"!")
	})
	if err := e.Start(":8080"); err != nil {
		log.Fatal(err)
	}
}
```

Run the program with `go run .`. A request to `http://localhost:8080/hello/Ada` returns `Hello, Ada!`. Pre-routing middleware registered with `e.Pre` runs before the router. Once a route matches, Echo calls its middleware and handler. Returned errors go to `HTTPErrorHandler`.

## Compose a handler without the framework root

This example uses the standalone packages from the checkout. The caller supplies a context for shutdown. The request handler uses the same integer conversion as an Echo handler.

```go
package service

import (
	"context"
	"fmt"
	"net/http"

	"github.com/fiberfx/mx-echo/v6/bind"
	"github.com/fiberfx/mx-echo/v6/server"
)

func Serve(ctx context.Context, address string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /count", func(w http.ResponseWriter, r *http.Request) {
		count, err := bind.ParseValueOr[int](r.URL.Query().Get("n"), 1)
		if err != nil {
			http.Error(w, "n must be an integer", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, count)
	})
	return (server.StartConfig{Address: address}).Start(ctx, mux)
}
```

Fibervisor’s responses service composes an Echo router with the same lifecycle through `echo.StartConfig`. The supervisor starts one executable, probes its HTTP readiness, and cancels it by process identity. A listener error should reach the command’s exit status. [HTTP handler and shutdown contracts](https://pkg.go.dev/net/http#Server.Shutdown)

## Origins and contributions

MX Echo builds on the work of Vishal Rana, Nitin Rana, Martti T., Roland Lammel, Pablo Andres Fuente and the [LabStack Echo contributors](https://github.com/labstack/echo/graphs/contributors). The [changelog](CHANGELOG.md) retains the upstream history. See the [roadmap](ROADMAP.md) for the FiberFX line and [LICENSE](LICENSE) for the MIT terms.
