// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v7/server"
)

// wait bounds every receive of a test, and is the grace of a stop that must drain: a passing test never
// reaches it.
const wait = 5 * time.Second

// grace is the grace of a stop that must expire. What outlasts it is held by a channel, not by a clock.
const grace = 50 * time.Millisecond

// recv returns the next value of ch, or fails the test when none comes within wait.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(wait):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// drain returns what ch holds now, without waiting.
func drain[T any](ch <-chan T) []T {
	var all []T
	for {
		select {
		case v := <-ch:
			all = append(all, v)
		default:
			return all
		}
	}
}

// violation is an input a check must refuse, with what the check answered.
type violation struct {
	name string
	err  error
}

// refuseAll fails the test for each violation its check let through.
func refuseAll(t *testing.T, violations []violation) {
	t.Helper()
	for _, v := range violations {
		if v.err == nil {
			t.Errorf("%s passed the check", v.name)
		}
	}
}

// started is a server under test: where it listens, and what Start returned.
type started struct {
	addr string
	done chan error
}

// start runs config on a free local port and returns once it listens.
func start(t *testing.T, ctx context.Context, config server.StartConfig, h http.Handler) started {
	t.Helper()
	addrs := make(chan string, 1)
	ready := config.ListenerAddrFunc
	config.Address = "127.0.0.1:0"
	config.HidePort = true
	config.Logger = slog.New(slog.DiscardHandler)
	config.ListenerAddrFunc = func(addr net.Addr) {
		if ready != nil {
			ready(addr)
		}
		addrs <- addr.String()
	}
	s := started{done: make(chan error, 1)}
	go func() { s.done <- config.Start(ctx, h) }()
	select {
	case s.addr = <-addrs:
	case err := <-s.done:
		t.Fatalf("Start returned before listening: %v", err)
	case <-time.After(wait):
		t.Fatal("the server did not listen")
	}
	return s
}

// result is how one request ended for its client.
type result struct {
	status int
	err    error
}

// get sends one request on a connection of its own and delivers how it ended.
func get(addr string) <-chan result {
	out := make(chan result, 1)
	go func() {
		transport := &http.Transport{DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: wait}
		resp, err := client.Get("http://" + addr + "/")
		if err != nil {
			out <- result{err: err}
			return
		}
		resp.Body.Close()
		out <- result{status: resp.StatusCode}
	}()
	return out
}

// answered reports an error unless the client got the held handler's 204.
func answered(res result) error {
	if res.err != nil {
		return fmt.Errorf("the request was not answered: %w", res.err)
	}
	if res.status != http.StatusNoContent {
		return fmt.Errorf("the request was answered with %d, want 204", res.status)
	}
	return nil
}

// held is a handler for one request. It blocks until it is released or its request's context ends, says
// which, and answers 204.
type held struct {
	entered chan struct{}
	release chan struct{}
	ctxErrs chan error
}

func newHeld() *held {
	return &held{entered: make(chan struct{}), release: make(chan struct{}), ctxErrs: make(chan error, 1)}
}

func (h *held) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	close(h.entered)
	select {
	case <-h.release:
	case <-r.Context().Done():
	}
	h.ctxErrs <- r.Context().Err()
	w.WriteHeader(http.StatusNoContent)
}

// oneReport returns the single report OnStop was given by the time Start returned.
func oneReport(startErr error, reports []server.StopReport) (server.StopReport, error) {
	if startErr != nil {
		return server.StopReport{}, fmt.Errorf("Start returned %v after a stop, want nil", startErr)
	}
	if len(reports) != 1 {
		return server.StopReport{}, fmt.Errorf("OnStop was called %d times before Start returned, want once", len(reports))
	}
	return reports[0], nil
}

// checkDrained holds FC-9's first half: a stop within its grace is reported as requested and not forced, its
// in-flight request was answered, and nothing listens once Start has returned.
func checkDrained(startErr error, reports []server.StopReport, res result, dialErr error) error {
	report, err := oneReport(startErr, reports)
	if err != nil {
		return err
	}
	if want := (server.StopReport{Requested: true}); report != want {
		return fmt.Errorf("a stop that drained was reported as %+v, want %+v", report, want)
	}
	if err := answered(res); err != nil {
		return fmt.Errorf("the request in flight when the stop began: %w", err)
	}
	if dialErr == nil {
		return errors.New("a dial after Start returned was accepted: the listener is still open")
	}
	return nil
}

func TestStopDrained(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h := newHeld()
	began := make(chan struct{})
	reports := make(chan server.StopReport, 4)
	s := start(t, ctx, server.StartConfig{
		GracefulTimeout: wait,
		OnStop:          func(r server.StopReport) { reports <- r },
		BeforeServeFunc: func(hs *http.Server) error {
			hs.RegisterOnShutdown(func() { close(began) })
			return nil
		},
	}, h)

	answer := get(s.addr)
	recv(t, h.entered, "the request to reach its handler")
	cancel()
	recv(t, began, "the stop to begin") // the request is now in flight inside the grace
	close(h.release)
	res := recv(t, answer, "the response")
	startErr := recv(t, s.done, "Start to return")

	conn, dialErr := net.DialTimeout("tcp", s.addr, time.Second)
	if dialErr == nil {
		conn.Close()
	}
	if err := checkDrained(startErr, drain(reports), res, dialErr); err != nil {
		t.Fatal(err)
	}
}

func TestStopDrainedViolation(t *testing.T) {
	drained := []server.StopReport{{Requested: true}}
	ok := result{status: http.StatusNoContent}
	refused := errors.New("connection refused")
	if err := checkDrained(nil, drained, ok, refused); err != nil {
		t.Fatalf("the valid case is refused: %v", err)
	}
	refuseAll(t, []violation{
		{"a forced stop where a drained one is claimed", checkDrained(nil, []server.StopReport{{Requested: true, Forced: true}}, ok, refused)},
		{"a stop nobody requested", checkDrained(nil, []server.StopReport{{}}, ok, refused)},
		{"no report", checkDrained(nil, nil, ok, refused)},
		{"two reports", checkDrained(nil, append(drained, drained...), ok, refused)},
		{"a listener still open", checkDrained(nil, drained, ok, nil)},
		{"a request cut off", checkDrained(nil, drained, result{err: errors.New("EOF")}, refused)},
		{"an error from Start", checkDrained(errors.New("boom"), drained, ok, refused)},
	})
}

// checkForced holds FC-9's second half: a stop whose grace expired with a request still running is reported
// as forced, and that request's connection was closed by the server, not left open or given up by the client.
func checkForced(startErr error, reports []server.StopReport, res result, ctxErr error) error {
	report, err := oneReport(startErr, reports)
	if err != nil {
		return err
	}
	if want := (server.StopReport{Requested: true, Forced: true}); report != want {
		return fmt.Errorf("a stop past its grace was reported as %+v, want %+v", report, want)
	}
	if res.err == nil {
		return fmt.Errorf("the request past the grace was answered with %d: its connection was not closed", res.status)
	}
	var timeout net.Error
	if errors.As(res.err, &timeout) && timeout.Timeout() {
		return fmt.Errorf("the client gave up waiting: the connection was not closed (%w)", res.err)
	}
	if ctxErr == nil {
		return errors.New("the request's context had not ended when its handler returned")
	}
	return nil
}

func TestStopForced(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h := newHeld()
	defer close(h.release)
	reports := make(chan server.StopReport, 4)
	s := start(t, ctx, server.StartConfig{
		GracefulTimeout: grace,
		OnStop:          func(r server.StopReport) { reports <- r },
		OnShutdownError: func(error) {},
	}, h)

	answer := get(s.addr)
	recv(t, h.entered, "the request to reach its handler")
	cancel()
	startErr := recv(t, s.done, "Start to return")
	got := drain(reports)
	res := recv(t, answer, "the request to end")
	ctxErr := recv(t, h.ctxErrs, "the handler to return")

	if err := checkForced(startErr, got, res, ctxErr); err != nil {
		t.Fatal(err)
	}
}

// timeoutError is what a client reports when it stops waiting for an answer.
type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestStopForcedViolation(t *testing.T) {
	forced := []server.StopReport{{Requested: true, Forced: true}}
	closed := result{err: errors.New("EOF")}
	if err := checkForced(nil, forced, closed, context.Canceled); err != nil {
		t.Fatalf("the valid case is refused: %v", err)
	}
	refuseAll(t, []violation{
		{"a forced stop reported as drained", checkForced(nil, []server.StopReport{{Requested: true}}, closed, context.Canceled)},
		{"owned work reported where there was none", checkForced(nil, []server.StopReport{{Requested: true, Forced: true, Abandoned: 1}}, closed, context.Canceled)},
		{"no report", checkForced(nil, nil, closed, context.Canceled)},
		{"a forced report with the request answered", checkForced(nil, forced, result{status: http.StatusNoContent}, context.Canceled)},
		{"a forced report with the connection open", checkForced(nil, forced, result{err: timeoutError{}}, context.Canceled)},
		{"a request context that never ended", checkForced(nil, forced, closed, nil)},
	})
}

// checkLeftRunning holds C-14: with OnStop nil, a request longer than the grace is still running, on an open
// connection, when Start returns nil, and OnShutdownError is told of the expired grace as at v6.1.0.
func checkLeftRunning(startErr error, shutdownErrs []error, ctxErr error, res result) error {
	if startErr != nil {
		return fmt.Errorf("Start returned %v after a stop, want nil", startErr)
	}
	if len(shutdownErrs) != 1 || !errors.Is(shutdownErrs[0], context.DeadlineExceeded) {
		return fmt.Errorf("OnShutdownError got %v, want one deadline error", shutdownErrs)
	}
	if ctxErr != nil {
		return fmt.Errorf("the request's context ended before its handler was released, after Start returned: %w", ctxErr)
	}
	if err := answered(res); err != nil {
		return fmt.Errorf("the request left running when Start returned: %w", err)
	}
	return nil
}

func TestStopWithoutOnStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h := newHeld()
	shutdownErrs := make(chan error, 4)
	s := start(t, ctx, server.StartConfig{
		GracefulTimeout: grace,
		OnShutdownError: func(err error) { shutdownErrs <- err },
	}, h)

	answer := get(s.addr)
	recv(t, h.entered, "the request to reach its handler")
	cancel()
	startErr := recv(t, s.done, "Start to return")
	got := drain(shutdownErrs)
	close(h.release) // only now: the handler ran through the whole stop
	ctxErr := recv(t, h.ctxErrs, "the handler to go on")
	res := recv(t, answer, "the response")

	if err := checkLeftRunning(startErr, got, ctxErr, res); err != nil {
		t.Fatal(err)
	}
}

func TestStopWithoutOnStopViolation(t *testing.T) {
	expired := []error{context.DeadlineExceeded}
	ok := result{status: http.StatusNoContent}
	if err := checkLeftRunning(nil, expired, nil, ok); err != nil {
		t.Fatalf("the valid case is refused: %v", err)
	}
	refuseAll(t, []violation{
		{"a Close on the default path, seen by the client", checkLeftRunning(nil, expired, nil, result{err: errors.New("EOF")})},
		{"a Close on the default path, seen by the handler", checkLeftRunning(nil, expired, context.Canceled, ok)},
		{"an expired grace nobody was told of", checkLeftRunning(nil, nil, nil, ok)},
		{"an error from Start", checkLeftRunning(context.DeadlineExceeded, expired, nil, ok)},
	})
}

// checkOwner holds FC-8: the report is the wanted one, every owned function that heeds its context had
// returned when Start did, and Go ran nothing once the stop had begun.
func checkOwner(startErr error, reports []server.StopReport, want server.StopReport, returned, heeding int, late bool) error {
	report, err := oneReport(startErr, reports)
	if err != nil {
		return err
	}
	if report != want {
		return fmt.Errorf("the stop was reported as %+v, want %+v", report, want)
	}
	if returned != heeding {
		return fmt.Errorf("%d of %d owned functions had returned when Start did", returned, heeding)
	}
	if late {
		return errors.New("Go accepted work after the stop began")
	}
	return nil
}

func TestOwner(t *testing.T) {
	t.Run("work that heeds its context has returned before Start does", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		owner := &server.Owner{}
		ended := make(chan struct{}, 2)
		giveUp := make(chan struct{})
		defer close(giveUp)
		work := func(ctx context.Context) {
			select {
			case <-ctx.Done():
				ended <- struct{}{}
			case <-giveUp:
			}
		}
		if !owner.Go(work) {
			t.Fatal("Go refused work before the server started")
		}
		reports := make(chan server.StopReport, 4)
		s := start(t, ctx, server.StartConfig{
			GracefulTimeout: wait,
			Owner:           owner,
			OnStop:          func(r server.StopReport) { reports <- r },
		}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if !owner.Go(work) { // the work outlives this request
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))

		if err := answered(recv(t, get(s.addr), "the response")); err != nil {
			t.Fatal(err)
		}
		if n := len(ended); n != 0 {
			t.Fatalf("the context of %d owned functions ended before the stop", n)
		}
		cancel()
		startErr := recv(t, s.done, "Start to return")
		returned := len(ended)
		late := owner.Go(func(context.Context) {})

		if err := checkOwner(startErr, drain(reports), server.StopReport{Requested: true}, returned, 2, late); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("work that ignores its context is counted as abandoned", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		owner := &server.Owner{}
		release := make(chan struct{})
		finished := make(chan struct{})
		if !owner.Go(func(context.Context) {
			<-release
			close(finished)
		}) {
			t.Fatal("Go refused work before the server started")
		}
		defer func() {
			close(release)
			recv(t, finished, "the abandoned function to return")
		}()
		reports := make(chan server.StopReport, 4)
		s := start(t, ctx, server.StartConfig{
			GracefulTimeout: grace,
			Owner:           owner,
			OnStop:          func(r server.StopReport) { reports <- r },
		}, http.NotFoundHandler())

		cancel()
		startErr := recv(t, s.done, "Start to return")
		late := owner.Go(func(context.Context) {})

		want := server.StopReport{Requested: true, Forced: true, Abandoned: 1}
		if err := checkOwner(startErr, drain(reports), want, 0, 0, late); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOwnerViolation(t *testing.T) {
	drained := server.StopReport{Requested: true}
	abandoned := server.StopReport{Requested: true, Forced: true, Abandoned: 1}
	if err := checkOwner(nil, []server.StopReport{drained}, drained, 2, 2, false); err != nil {
		t.Fatalf("the valid case is refused: %v", err)
	}
	refuseAll(t, []violation{
		{"a goroutine alive after a drained stop", checkOwner(nil, []server.StopReport{drained}, drained, 1, 2, false)},
		{"abandoned work reported as drained", checkOwner(nil, []server.StopReport{drained}, abandoned, 0, 0, false)},
		{"abandoned work reported as forced, uncounted", checkOwner(nil, []server.StopReport{{Requested: true, Forced: true}}, abandoned, 0, 0, false)},
		{"work accepted after the stop", checkOwner(nil, []server.StopReport{drained}, drained, 2, 2, true)},
		{"no report", checkOwner(nil, nil, drained, 2, 2, false)},
	})
}

func TestOwnerGoDuringStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner := &server.Owner{}
	var accepted, ended, refused atomic.Int64
	stopBegan := make(chan struct{})
	var once sync.Once
	work := func(ctx context.Context) {
		<-ctx.Done()
		once.Do(func() { close(stopBegan) })
		ended.Add(1)
	}
	reports := make(chan server.StopReport, 4)
	s := start(t, ctx, server.StartConfig{
		GracefulTimeout: wait,
		Owner:           owner,
		OnStop:          func(r server.StopReport) { reports <- r },
	}, http.NotFoundHandler())

	// Each caller hands work over in a burst, and the stop is asked for once every caller is under way, so it
	// begins beside their calls. A caller the stop has not reached by the end of its burst waits until an owned
	// function has seen its context end, and calls once more: that call is refused whatever the schedule.
	const callers, burst = 4, 200
	var calling sync.WaitGroup
	for range callers {
		calling.Go(func() {
			for range burst {
				if !owner.Go(work) {
					refused.Add(1)
					return
				}
				if accepted.Add(1) == callers {
					cancel()
				}
				runtime.Gosched()
			}
			select {
			case <-stopBegan:
			case <-time.After(wait):
				return
			}
			if owner.Go(work) {
				accepted.Add(1)
				return
			}
			refused.Add(1)
		})
	}
	startErr := recv(t, s.done, "Start to return")
	returned := int(ended.Load()) // read before the callers are waited for: Start must not return ahead of owned work
	calling.Wait()
	late := refused.Load() != callers || owner.Go(work)

	if err := checkOwner(startErr, drain(reports), server.StopReport{Requested: true}, returned, int(accepted.Load()), late); err != nil {
		t.Fatalf("%v (%d of %d callers were refused)", err, refused.Load(), callers)
	}
}

// errBroken is what a brokenListener's Accept returns.
var errBroken = errors.New("the listener broke")

// brokenListener is a bound listener whose Accept fails once fail is called: the way Serve ends on its own,
// with no stop requested.
type brokenListener struct {
	net.Listener
	broken chan struct{}
	once   sync.Once
}

func listenBroken(t *testing.T) *brokenListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &brokenListener{Listener: ln, broken: make(chan struct{})}
	t.Cleanup(l.fail)
	return l
}

func (l *brokenListener) Accept() (net.Conn, error) {
	<-l.broken
	return nil, errBroken
}

func (l *brokenListener) fail() { l.once.Do(func() { close(l.broken) }) }

// checkServeEnded holds what a report says when Serve ended on its own: Start returns Serve's error, OnStop is
// called once and never says the stop was requested, no owned function that heeds its context is left alive,
// and the owner takes no more work.
func checkServeEnded(startErr error, reports []server.StopReport, want server.StopReport, alive int, late bool) error {
	if !errors.Is(startErr, errBroken) {
		return fmt.Errorf("Start returned %v, want the listener's error", startErr)
	}
	if len(reports) != 1 {
		return fmt.Errorf("OnStop was called %d times when Serve ended on its own, want once", len(reports))
	}
	if reports[0].Requested {
		return fmt.Errorf("a stop nobody requested was reported as requested: %+v", reports[0])
	}
	if reports[0] != want {
		return fmt.Errorf("the stop was reported as %+v, want %+v", reports[0], want)
	}
	if alive != 0 {
		return fmt.Errorf("%d owned functions that heed their context were alive when Start returned", alive)
	}
	if late {
		return errors.New("Go accepted work after Serve ended")
	}
	return nil
}

func TestStopServeEnded(t *testing.T) {
	t.Run("within a grace the owner is cancelled and waited for", func(t *testing.T) {
		l := listenBroken(t)
		owner := &server.Owner{}
		ended := make(chan struct{}, 1)
		giveUp := make(chan struct{})
		defer close(giveUp)
		if !owner.Go(func(ctx context.Context) {
			select {
			case <-ctx.Done():
				ended <- struct{}{}
			case <-giveUp:
			}
		}) {
			t.Fatal("Go refused work before the server started")
		}
		reports := make(chan server.StopReport, 4)
		s := start(t, t.Context(), server.StartConfig{
			Listener:        l,
			GracefulTimeout: wait,
			Owner:           owner,
			OnStop:          func(r server.StopReport) { reports <- r },
		}, http.NotFoundHandler())

		l.fail() // the start context is still live: nobody asked for this stop
		startErr := recv(t, s.done, "Start to return")
		alive := 1 - len(ended)
		late := owner.Go(func(context.Context) {})

		if err := checkServeEnded(startErr, drain(reports), server.StopReport{}, alive, late); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("with a negative grace the owner is cancelled and not waited for", func(t *testing.T) {
		l := listenBroken(t)
		owner := &server.Owner{}
		release := make(chan struct{})
		finished := make(chan struct{})
		if !owner.Go(func(context.Context) {
			<-release
			close(finished)
		}) {
			t.Fatal("Go refused work before the server started")
		}
		defer func() {
			close(release)
			recv(t, finished, "the abandoned function to return")
		}()
		reports := make(chan server.StopReport, 4)
		s := start(t, t.Context(), server.StartConfig{
			Listener:        l,
			GracefulTimeout: -1,
			Owner:           owner,
			OnStop:          func(r server.StopReport) { reports <- r },
		}, http.NotFoundHandler())

		l.fail()
		startErr := recv(t, s.done, "Start to return")
		late := owner.Go(func(context.Context) {})

		want := server.StopReport{Forced: true, Abandoned: 1}
		if err := checkServeEnded(startErr, drain(reports), want, 0, late); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStopServeEndedViolation(t *testing.T) {
	ended := []server.StopReport{{}}
	if err := checkServeEnded(errBroken, ended, server.StopReport{}, 0, false); err != nil {
		t.Fatalf("the valid case is refused: %v", err)
	}
	requested := server.StopReport{Requested: true}
	refuseAll(t, []violation{
		{"a stop nobody requested reported as requested", checkServeEnded(errBroken, []server.StopReport{requested}, requested, 0, false)},
		{"the error of Serve swallowed", checkServeEnded(nil, ended, server.StopReport{}, 0, false)},
		{"no report when Serve ended on its own", checkServeEnded(errBroken, nil, server.StopReport{}, 0, false)},
		{"owned work left running and not counted", checkServeEnded(errBroken, ended, server.StopReport{Forced: true, Abandoned: 1}, 0, false)},
		{"owned work alive after Start returned", checkServeEnded(errBroken, ended, server.StopReport{}, 1, false)},
		{"work accepted after Serve ended", checkServeEnded(errBroken, ended, server.StopReport{}, 0, true)},
	})
}

// checkDial holds FC-11: something listens on addr at the moment of the call.
func checkDial(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return fmt.Errorf("nothing listens on %s: %w", addr, err)
	}
	return conn.Close()
}

func TestReadyAfterListen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dialed := make(chan error, 1)
	s := start(t, ctx, server.StartConfig{
		GracefulTimeout:  wait,
		ListenerAddrFunc: func(addr net.Addr) { dialed <- checkDial(addr.String()) },
	}, http.NotFoundHandler())

	if err := recv(t, dialed, "the dial inside ListenerAddrFunc"); err != nil {
		t.Fatalf("inside ListenerAddrFunc: %v", err)
	}
	cancel()
	if err := recv(t, s.done, "Start to return"); err != nil {
		t.Fatal(err)
	}
}

func TestReadyAfterListenViolation(t *testing.T) {
	// A callback before Listen is a callback with an address nobody listens on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := checkDial(addr); err != nil {
		t.Fatalf("the valid case is refused: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if checkDial(addr) == nil {
		t.Fatal("a dial to an address nobody listens on passed the check")
	}
}
