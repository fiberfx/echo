// SPDX-License-Identifier: MIT

package server

import (
	stdContext "context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
)

// StopReport says how a started server stopped.
type StopReport struct {
	Requested bool // the start context ended and the graceful stop ran
	Forced    bool // the grace expired with a request or owned work still running; the connections were closed
	Abandoned int  // owned functions still running when the grace expired
}

// Owner owns work that outlives a request. The zero Owner is ready. A server given it cancels its context when
// the stop begins and waits for its functions within GracefulTimeout.
//
// An Owner is stopped once and stays stopped: it serves one start, and when two servers share it the first stop
// ends it for both. A start that fails before serving (a listen error, a BeforeServeFunc error) does not stop it.
type Owner struct {
	mu      sync.Mutex
	ctx     stdContext.Context
	cancel  stdContext.CancelFunc
	running int
	idle    chan struct{} // made by stop; closed when the owner is stopped and no function is running
}

// Go runs f on a new goroutine with the owner's context and reports true; after the stop began it runs nothing
// and reports false.
func (o *Owner) Go(f func(ctx stdContext.Context)) bool {
	o.mu.Lock()
	if o.idle != nil {
		o.mu.Unlock()
		return false
	}
	if o.ctx == nil {
		o.ctx, o.cancel = stdContext.WithCancel(stdContext.Background())
	}
	ctx := o.ctx
	o.running++
	o.mu.Unlock()

	go func() {
		defer o.done()
		f(ctx)
	}()
	return true
}

func (o *Owner) done() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.running--
	if o.running == 0 && o.idle != nil {
		close(o.idle)
	}
}

// stop ends the owner's context and refuses later work. The channel it returns is closed when no function runs.
func (o *Owner) stop() <-chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.idle == nil {
		o.idle = make(chan struct{})
		if o.cancel != nil {
			o.cancel()
		}
		if o.running == 0 {
			close(o.idle)
		}
	}
	return o.idle
}

// wait stops the owner and waits for its functions until ctx ends. It returns how many are still running.
func (o *Owner) wait(ctx stdContext.Context) int {
	select {
	case <-o.stop():
		return 0
	case <-ctx.Done():
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.running
}

// errServeEnded is the cause given to the graceful goroutine's context when Serve returned on its own, so that
// a stop the start context asked for can be told from one it did not.
var errServeEnded = errors.New("server: serve ended")

// beginStop starts the report of a stop and ends the owner's context, before the listener closes.
func (sc *StartConfig) beginStop(shutdownCtx stdContext.Context) StopReport {
	if sc.Owner != nil {
		sc.Owner.stop()
	}
	return StopReport{Requested: stdContext.Cause(shutdownCtx) != errServeEnded}
}

// endGrace completes the report after Shutdown returned err. It waits for the owner's functions until graceCtx
// ends. When the grace expired with a request still running, it closes the connections Shutdown left open, only
// if OnStop is set: a forced report without the close would be untrue, and a close without OnStop is not v6.1.0.
func (sc *StartConfig) endGrace(graceCtx stdContext.Context, server *http.Server, err error, report *StopReport) {
	expired := errors.Is(err, stdContext.DeadlineExceeded)
	if sc.Owner != nil {
		report.Abandoned = sc.Owner.wait(graceCtx)
	}
	report.Forced = expired || report.Abandoned > 0
	if expired && sc.OnStop != nil {
		_ = server.Close()
	}
}

// stopped runs last in start, after the graceful goroutine ended and before Start returns. With a negative
// GracefulTimeout no graceful stop ran: the owner is stopped here with no wait and no connection is closed.
func (sc *StartConfig) stopped(report *StopReport, logger *slog.Logger) {
	if sc.Owner != nil && sc.GracefulTimeout < 0 {
		expired, cancel := stdContext.WithCancel(stdContext.Background())
		cancel()
		report.Abandoned = sc.Owner.wait(expired)
		report.Forced = report.Abandoned > 0
	}
	if sc.OnStop != nil {
		sc.OnStop(*report)
		return
	}
	if report.Abandoned > 0 {
		logger.Error("owned work still running after the graceful timeout", "abandoned", report.Abandoned)
	}
}
