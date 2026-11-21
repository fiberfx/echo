# MX Echo v6

MX Echo is a Go HTTP framework derived from LabStack Echo v5.4.6 with the v5.4.7 fixes adapted to v6. Its router, middleware and request-test helpers ship together as one module, `github.com/fiberfx/mx-echo/v6`. The minimum Go version is **1.27.4**.

## Open the workspace

Open the directory containing [go.mod](go.mod) in GoLand or another Go editor. Run Go commands from that directory. The same module defines all three public packages.

| Import | Responsibility |
| --- | --- |
| `github.com/fiberfx/mx-echo/v6` | Route registration, request context, binding, responses and server lifecycle |
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

To install v6.1.0 in an application module, run `go get github.com/fiberfx/mx-echo/v6@v6.1.0`.

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

## Origins and contributions

MX Echo builds on the work of Vishal Rana, Nitin Rana, Martti T., Roland Lammel, Pablo Andres Fuente and the [LabStack Echo contributors](https://github.com/labstack/echo/graphs/contributors). The [changelog](CHANGELOG.md) retains the upstream history. See the [roadmap](ROADMAP.md) for the FiberFX line and [LICENSE](LICENSE) for the MIT terms.
