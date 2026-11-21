// SPDX-License-Identifier: MIT

// Command reference is a small service on http.ServeMux that a supervisor such as fv can hold.
//
// What it shows: server, observe/stdhttp, fibervisor and outcome compose with no Echo type; every request is
// named and observed as one JSON line on standard error, with the attempt fv gave the process; and the exit
// status says how the service ended: 0 after a drained stop, 2 after a forced one, 1 when it could not serve.
//
// What it does not show: it writes no fv record and reaches fv over nothing; its values live in memory and
// are lost at exit; and its tests set the variables fv's code writes, they do not run it under a real fv.
//
// Standard output holds the bound address as the first line, then the server's own log. Standard error holds
// the observations and nothing else.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/fiberfx/mx-echo/v6/fibervisor"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/observe/stdhttp"
	"github.com/fiberfx/mx-echo/v6/outcome"
	"github.com/fiberfx/mx-echo/v6/server"
)

// The exit statuses: the one report a supervisor reads.
const (
	exitDrained = 0 // a stop was asked for, and every request ended within the grace
	exitFailed  = 1 // the service could not serve, or stopped serving without being asked
	exitForced  = 2 // a stop was asked for, and the grace expired with a request still running
)

// grace is how long a stop waits for requests that are running. A supervisor's own grace must be longer, or
// its kill lands in the middle of the drain: fv's default is 500 ms, so run this under fv with --grace 4000.
const grace = 3 * time.Second

// maxValue is the largest value a key may hold.
const maxValue = 1 << 20

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "the address to listen on")
	flag.Parse()
	os.Exit(serve(*addr))
}

// serve runs the service until SIGTERM or an interrupt and returns the exit status.
func serve(addr string) int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	return run(ctx, addr, os.Stdout, os.Stderr)
}

// run serves on addr until ctx ends and returns the exit status. It reads the attempt and the build once,
// before the first request: an observation states the attempt the process started in, whatever the environment
// says later.
func run(ctx context.Context, addr string, stdout, stderr io.Writer) int {
	h := stdhttp.Wrap(newService(), observe.Observer{
		Sink:    fibervisor.Lines(stderr),
		Attempt: fibervisor.Attempt(),
		Build:   fibervisor.Build(),
	})
	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	var stop server.StopReport
	sc := server.StartConfig{
		Address:         addr,
		Logger:          logger,
		HideBanner:      true,
		HidePort:        true,
		GracefulTimeout: grace,
		// Called once the listener is bound: the address is the service's word that it accepts connections.
		ListenerAddrFunc: func(bound net.Addr) { fmt.Fprintln(stdout, bound.String()) },
		// Called on this goroutine, after the listener closed and the stop ended, before Start returns.
		OnStop: func(report server.StopReport) { stop = report },
	}
	err := sc.Start(ctx, h)
	if err != nil {
		logger.Error("the service could not serve", "error", err.Error())
	}
	return exitStatus(err, stop)
}

// exitStatus maps how Start ended to the exit status. Start returns nil after a forced stop too, so the report
// decides: only a stop that was asked for and not forced is a drained one.
func exitStatus(err error, stop server.StopReport) int {
	switch {
	case err != nil:
		return exitFailed
	case stop.Forced:
		return exitForced
	case !stop.Requested:
		return exitFailed
	}
	return exitDrained
}

// newService returns the service's handler: the routes behind the Host guard.
func newService() http.Handler {
	s := &store{values: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.Handle("GET /api/healthz", route(health))
	mux.Handle("PUT /api/responses/{key}", route(s.save))
	mux.Handle("GET /api/responses/{key}", route(s.read))
	return hostGuard(mux)
}

// refusal is an error that states the status it is answered with.
type refusal struct {
	status int
	reason string
}

func (r refusal) Error() string   { return r.reason }
func (r refusal) StatusCode() int { return r.status }

// route is a route's function: it answers, or it returns the error that says why it did not. It returns an
// error only before it has written anything.
type route func(w http.ResponseWriter, r *http.Request) error

// ServeHTTP runs the route and answers its error.
func (f route) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := f(w, r); err != nil {
		refuse(w, err)
	}
}

// refuse is the service's one error rule: the status is outcome's decision over the status err states, 500
// for an error that states none, and the body says why. Nothing is committed when it is called.
func refuse(w http.ResponseWriter, err error) {
	d := outcome.Decide(outcome.StatusOf(err), false)
	reason := err.Error()
	if d.Status >= http.StatusInternalServerError {
		reason = http.StatusText(d.Status) // an error that is not a refusal is not shown to the client
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(d.Status)
	_ = json.NewEncoder(w).Encode(map[string]string{"reason": reason})
}

// hostGuard refuses a request whose Host is not this machine's loopback name, before any route is looked up.
func hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		if host != "127.0.0.1" && host != "localhost" {
			refuse(w, refusal{http.StatusForbidden, "host not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// health answers that the service is serving.
func health(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true}`)
	return nil
}

// store holds the saved values in memory.
type store struct {
	mu     sync.Mutex
	values map[string][]byte
}

// save keeps the request's body under the key in its path.
func (s *store) save(w http.ResponseWriter, r *http.Request) error {
	value, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxValue))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return refusal{http.StatusRequestEntityTooLarge, "the value is larger than 1 MiB"}
		}
		return refusal{http.StatusBadRequest, "the value could not be read"}
	}
	s.mu.Lock()
	s.values[r.PathValue("key")] = value
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// read answers with the value saved under the key in its path.
func (s *store) read(w http.ResponseWriter, r *http.Request) error {
	s.mu.Lock()
	value, ok := s.values[r.PathValue("key")]
	s.mu.Unlock()
	if !ok {
		return refusal{http.StatusNotFound, "no value is saved under this key"}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(value)
	return nil
}
