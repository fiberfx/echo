// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/bind"
	"github.com/fiberfx/mx-echo/v6/fibervisor"
	"github.com/fiberfx/mx-echo/v6/middleware"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/observe/stdhttp"
	"github.com/fiberfx/mx-echo/v6/server"
)

// The Go blocks of README.md, each body verbatim, so that go vet and go test compile them. The examples have no
// Output comment: they are compiled and not run, since each one serves until it is stopped.

// Example_quickStart is the body of main in the README's quick start.
func Example_quickStart() {
	e := echo.New()
	e.Use(middleware.RequestLogger(), middleware.Recover())
	e.GET("/hello/:name", func(c *echo.Context) error {
		return c.String(http.StatusOK, "Hello, "+c.Param("name")+"!")
	})
	if err := e.Start(":8080"); err != nil {
		log.Fatal(err)
	}
}

// readmeServe is Serve of the README's package service, on the standard mux with bind and server.
func readmeServe(ctx context.Context, address string) error {
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

// Example_standaloneServer calls the README's Serve as its own program would.
func Example_standaloneServer() {
	if err := readmeServe(context.Background(), ":8080"); err != nil {
		log.Fatal(err)
	}
}

// readmeRun is run of the README's supervised service; its main is os.Exit(run()).
func readmeRun() int {
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

// Example_supervisedService is the README's main of the supervised service.
func Example_supervisedService() {
	os.Exit(readmeRun())
}
