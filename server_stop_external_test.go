// SPDX-License-Identifier: MIT

package echo_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/server"
)

// checkStopFieldsPassed holds that the root's StartConfig hands OnStop and Owner to the server package: the
// stop is reported once as drained, and the owner's function saw its context end before Start returned.
func checkStopFieldsPassed(startErr error, reports []server.StopReport, ownedEnded bool) error {
	if startErr != nil {
		return fmt.Errorf("Start returned %v after a stop, want nil", startErr)
	}
	if len(reports) != 1 {
		return fmt.Errorf("OnStop was called %d times before Start returned, want once: is the field copied?", len(reports))
	}
	if want := (server.StopReport{Requested: true}); reports[0] != want {
		return fmt.Errorf("the stop was reported as %+v, want %+v", reports[0], want)
	}
	if !ownedEnded {
		return errors.New("the owner's function had not returned when Start did: is the field copied?")
	}
	return nil
}

func TestStartConfigStop(t *testing.T) {
	const wait = 5 * time.Second
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	owner := &server.Owner{}
	ended := make(chan struct{})
	giveUp := make(chan struct{})
	defer close(giveUp)
	if !owner.Go(func(ctx context.Context) {
		select {
		case <-ctx.Done():
			close(ended)
		case <-giveUp:
		}
	}) {
		t.Fatal("Go refused work before the server started")
	}

	reports := make(chan server.StopReport, 4)
	listening := make(chan struct{}, 1)
	done := make(chan error, 1)
	e := echo.New()
	e.Logger = slog.New(slog.DiscardHandler)
	config := echo.StartConfig{
		Address:          "127.0.0.1:0",
		HideBanner:       true,
		HidePort:         true,
		ListenerAddrFunc: func(net.Addr) { listening <- struct{}{} },
		GracefulTimeout:  wait,
		OnStop:           func(r server.StopReport) { reports <- r },
		Owner:            owner,
	}
	go func() { done <- config.Start(ctx, e) }()
	select {
	case <-listening:
	case err := <-done:
		t.Fatalf("Start returned before listening: %v", err)
	case <-time.After(wait):
		t.Fatal("the server did not listen")
	}

	cancel()
	var startErr error
	select {
	case startErr = <-done:
	case <-time.After(wait):
		t.Fatal("Start did not return after the stop")
	}
	var got []server.StopReport
	for len(reports) > 0 {
		got = append(got, <-reports)
	}
	ownedEnded := false
	select {
	case <-ended:
		ownedEnded = true
	default:
	}

	if err := checkStopFieldsPassed(startErr, got, ownedEnded); err != nil {
		t.Fatal(err)
	}
}

func TestStartConfigStopViolation(t *testing.T) {
	drained := []server.StopReport{{Requested: true}}
	if err := checkStopFieldsPassed(nil, drained, true); err != nil {
		t.Fatalf("the valid case is refused: %v", err)
	}
	if checkStopFieldsPassed(nil, nil, true) == nil {
		t.Error("an OnStop the root dropped passed the check")
	}
	if checkStopFieldsPassed(nil, drained, false) == nil {
		t.Error("an Owner the root dropped passed the check")
	}
	if checkStopFieldsPassed(nil, []server.StopReport{{Requested: true, Forced: true}}, true) == nil {
		t.Error("a forced stop passed as drained")
	}
}
