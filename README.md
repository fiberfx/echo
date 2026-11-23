# MX Echo v6

[![Go Reference](https://pkg.go.dev/badge/github.com/fiberfx/mx-echo/v6.svg)](https://pkg.go.dev/github.com/fiberfx/mx-echo/v6)
[![Go 1.27.4](https://img.shields.io/badge/go-1.27.4-00ADD8)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

MX Echo is a Go HTTP framework derived from LabStack Echo v5.4.6, with the v5.4.7 fixes adapted to v6. Its router, middleware and request-test helpers ship together as one module, `github.com/fiberfx/mx-echo/v6`. Beside the framework, the module holds standalone packages for typed binding, server lifecycle, route patterns, request outcomes, request names and observation. They use no Echo type, so any `http.Handler` can use them. A service that the fv supervisor holds can opt into the `fibervisor` package to state its attempt, name its requests in fv's terms and report how it stopped. Each feature added since v6.3.0 is opt-in: with none of them used, the request path and every default stay as they were.

The minimum Go version is **1.27.4**. The latest release is **v6.11.0**.

## Install

```sh
go get github.com/fiberfx/mx-echo/v6@v6.11.0
```

After adding imports such as `middleware`, run `go mod tidy` in the application to record the dependencies its packages use.

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

Run the program with `go run .`. A request to `http://localhost:8080/hello/Ada` returns `Hello, Ada!`. Pre-routing middleware registered with `e.Pre` runs before the router. Once a route matches, Echo calls its middleware and handler. Returned errors go to `HTTPErrorHandler`. Handlers have the signature `func(*echo.Context) error`, and `Context` is a concrete struct.

## Compose a handler without the framework root

This example uses the standalone packages. The caller supplies a context for shutdown. The request handler uses the same integer conversion as an Echo handler.

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

Fibervisor's responses service composes an Echo router with the same lifecycle through `echo.StartConfig`. The supervisor starts one executable, probes its HTTP readiness, and cancels it by process identity. A listener error should reach the command's exit status. [HTTP handler and shutdown contracts](https://pkg.go.dev/net/http#Server.Shutdown)

## Packages

Eight standalone packages were added from v6.2.0 to v6.5.0, and `ownerstore` in v6.11.0. They share the framework's module and version, and [examples/reference](examples/reference/) composes them with no Echo type.

| Import | Responsibility |
| --- | --- |
| `github.com/fiberfx/mx-echo/v6` | Route registration, request context, binding, responses and server lifecycle |
| `github.com/fiberfx/mx-echo/v6/bind` | Convert strings and slices with pure functions, including defaults and typed time options |
| `github.com/fiberfx/mx-echo/v6/server` | Serve any `http.Handler` with a caller-owned cancellation context, listener and shutdown policy, report how the stop ended and own work that outlives a request |
| `github.com/fiberfx/mx-echo/v6/route/pattern` | Read a route pattern into its static, parameter and wildcard parts with pure functions |
| `github.com/fiberfx/mx-echo/v6/outcome` | Classify how a request ended and decide the status of an error response with pure functions |
| `github.com/fiberfx/mx-echo/v6/reqid` | Name a request with a small value ordered by time, parse an incoming `traceparent`, and mint and read Ecd IDs (eids), the COR correlation value among them |
| `github.com/fiberfx/mx-echo/v6/ownerstore` | Keep a service's state in one SQLite file under Ecd ID keys, with the four outcomes of a write (committed, conflict, refused, unknown), a changefeed, durability classes D1 and D2 and one owner at a time |
| `github.com/fiberfx/mx-echo/v6/observe` | Describe one finished request as a value, write it as JSON and hand it to a sink |
| `github.com/fiberfx/mx-echo/v6/observe/stdhttp` | Name and observe the requests of any `http.Handler`, with the standard mux's pattern |
| `github.com/fiberfx/mx-echo/v6/fibervisor` | Read the attempt the fv supervisor gave the process, state the build, and write observations as JSON lines |
| `github.com/fiberfx/mx-echo/v6/middleware` | HTTP middleware, including request logging, recovery and compression |
| `github.com/fiberfx/mx-echo/v6/echotest` | Request contexts, response recorders and fixtures for handler tests |

The root package imports neither `fibervisor` nor `observe/stdhttp`, and reads no fv environment. A test holds that rule for every package the root imports.

## Run under the fv supervisor (opt-in)

`fibervisor` is for a service that fv holds. A service that does not import it carries none of it. The package writes no fv record and reaches fv over nothing: it starts no process, no goroutine and no `init`. Under fv, a service reads its supervision once at start:

```go
package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"

	"github.com/fiberfx/mx-echo/v6/fibervisor"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/observe/stdhttp"
	"github.com/fiberfx/mx-echo/v6/server"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), fibervisor.StopSignals()...)
	defer cancel()

	sup, _ := fibervisor.Read() // the zero Supervision when fv does not hold the process
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	h := stdhttp.Wrap(mux, observe.Observer{
		Sink:    fibervisor.LinesOf(os.Stderr, sup),
		Attempt: sup.Path,
		Build:   fibervisor.Build(),
	})

	var stop server.StopReport
	err := (server.StartConfig{
		Address: "127.0.0.1:8080",
		OnStop:  func(r server.StopReport) { stop = r },
	}).Start(ctx, h)
	return stop.ExitCode(err)
}
```

What each piece gives:

- `fibervisor.Read` returns the attempt as a `Supervision`: the trace, the attempt's path and `AttemptNode`, the node id that `fv show` and `fv stop` accept. When fv does not hold the process it returns the zero `Supervision` and false.
- `fibervisor.LinesOf` writes one JSON line per observation, with `trace` and `attempt_node` added, so `fv show` can be run from any line. For the zero `Supervision`, or a value not in fv's shapes, it writes exactly what `fibervisor.Lines` writes.
- `fibervisor.StopSignals` names the signals fv stops a leaf with: SIGTERM and an interrupt. The package installs no handler.
- `server.StopReport.ExitCode(err)` is the exit status of `main`: 0 after a requested, drained stop; 2 after a requested, forced one; 1 otherwise. It exits nothing. With `OnStop` unset, the report is zero and the status is 1.
- `Supervision.RequestNode(id)` returns the node id fv would derive for a request under the attempt. It is derived, not recorded, so `fv show` refuses it.
- `Supervision.Transport(next)` sends each outgoing request whose context names a request with a `traceparent` naming that node. Use it as `&http.Client{Transport: sup.Transport(nil)}`.
- `Supervision.Env(base)` gives a child process the environment under which `fv run` records it as a child of the attempt. `Supervision.LinkEnv(base, id)` gives one under which fv records a new root in the same trace, linked to the request. A nil base is `os.Environ()`, as with `os/exec`.
- `Supervision.Grace` returns the stop grace fv gives the attempt, from `FV_GRACE_MS`. fv does not set that variable until its train 2b, so until then `Grace` reports false: the grace is unknown, not zero.

Each `Supervision` method returns a new value and changes nothing it is given.

**Checked under a real fv** (the stand's fv row): `fv run` starts the reference service; `fv show` given the `attempt_node` of one of its lines exits 0 and names the service's pid; `fv show` refuses a request's id; after `fv stop` the service drains, exits 0 and leaves no process of its group.

**Not yet run under a real fv:** `Env`, `LinkEnv`, `Transport` and a forced stop's exit status. `Grace` waits for fv to set `FV_GRACE_MS`.

## Compatibility

- **v6 stays source-compatible from v6.1.0 on.** Identifiers that moved into a standalone package remain in the root as aliases, such as `TimeOpts` and `ErrInvalidCertOrKeyType`, and `echo.StartConfig` remains available. Programs that inspect the defining package of `TimeOpts` through reflection see `github.com/fiberfx/mx-echo/v6/bind` since v6.2.0. Each patch release from v6.6.1 to v6.7.1 keeps every exported identifier with the same type, and adds or moves nothing.
- **New behaviour is opt-in.** `Config.Observer`, `OnStop`, `server.Owner`, `Echo.Freeze` and the `fibervisor` methods change nothing until a service uses them.
- **Defaults are unchanged.** With none of the v6.8.0 additions used, the request path and every default are those of v6.7.1.
- **No cost when off.** With no observer set, the request path is the one of v6.1.0, and a routed request allocates nothing more.

MX Echo follows semantic versioning under the `/v6` module path. Minor releases add opt-in API. Patch releases fix behaviour and change no exported identifier.

### Upgrading

The published v6.0.0 tag declares the historical module path `github.com/fiberfx/echo/v6`. When upgrading from v6.0.0, update the imports of the framework, middleware and test helpers together. The Go package name remains `echo`.

For an application moving from v5, change Echo imports in handlers, middleware and tests together. Go treats types from the two module paths as distinct. A dependency that accepts `*echo.Context` or returns `echo.MiddlewareFunc` must use the same module path as its caller. Standard `http.Handler` implementations can be mounted with `echo.WrapHandler`.

## Open the workspace

Open the directory containing [go.mod](go.mod) in GoLand or another Go editor. Run Go commands from that directory.

```sh
go version
go list ./...
go test ./...
go vet ./...
go test -race ./...
```

The `go` directive sets the minimum supported toolchain. To repeat the contributor checks on this exact patch release, use `GOTOOLCHAIN=go1.27.4 make check`. `make init` downloads the module dependencies and installs the pinned linter. Put the Go binary installation directory on your `PATH` before running `make check`.

| Target | What it runs |
| --- | --- |
| `make check` | The contributor checks: `lint`, `vet` and `race` (the default target) |
| `make init` | Download module dependencies and install golangci-lint v2.15.0 |
| `make lint` | golangci-lint with the repository's [configuration](.golangci.yaml) |
| `make vet` | `go vet ./...` |
| `make test` | Short tests, `go test -short ./...` |
| `make race` | The full suite with the race detector |
| `make benchmark` | Benchmarks with allocation reports |
| `make test_version` | `vet` and `race` in a `golang:1.27.4` container |
| `make help` | List the targets |

`GOWORK=off go mod tidy -diff` checks the dependency manifest independently of a contributor workspace.

### Develop with an application

While developing MX Echo and an application together, put their checkouts under a common parent. For an application module in `myapp`, run the following from the parent directory. If a workspace already exists there, use `go work use ./mx-echo ./myapp` to add the modules.

```sh
go work init ./mx-echo ./myapp
go -C myapp test ./...
```

The workspace selects the local MX Echo checkout.

## Contributing

Run `make check` before proposing a change. Use `go test -race ./...` when changing request handling or shared state. Preserve the order of pre-routing, group and route middleware when changing the request path. Keep package imports under the v6 module path and retain upstream attribution in inherited files. [CLAUDE.md](CLAUDE.md) maps each responsibility to its source files.

## Changes and roadmap

The [changelog](CHANGELOG.md) records every release and retains the upstream history. The [roadmap](ROADMAP.md) describes the FiberFX line.

## Origins and license

MX Echo builds on the work of Vishal Rana, Nitin Rana, Martti T., Roland Lammel, Pablo Andres Fuente and the [LabStack Echo contributors](https://github.com/labstack/echo/graphs/contributors). It is released under the MIT terms in [LICENSE](LICENSE).
