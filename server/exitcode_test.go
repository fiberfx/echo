// SPDX-License-Identifier: MIT

package server_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/fiberfx/mx-echo/v7/server"
)

// checkExitCode runs the table of ExitCode against code and reports the first row it gets wrong: 0 only for a stop
// that was requested and drained with no error, 2 for one requested and forced, 1 for every other end, the zero
// report among them.
func checkExitCode(code func(server.StopReport, error) int) error {
	errListen := errors.New("listen tcp 127.0.0.1:8080: bind: address already in use")
	cases := []struct {
		name   string
		report server.StopReport
		err    error
		want   int
	}{
		{"a drained stop", server.StopReport{Requested: true}, nil, 0},
		{"a forced stop", server.StopReport{Requested: true, Forced: true}, nil, 2},
		{"a forced stop that abandoned work", server.StopReport{Requested: true, Forced: true, Abandoned: 2}, nil, 2},
		{"the zero report, as Start leaves it without OnStop", server.StopReport{}, nil, 1},
		{"a Serve that ended on its own and was forced", server.StopReport{Forced: true}, nil, 1},
		{"a failed start", server.StopReport{}, errListen, 1},
		{"a requested stop and an error", server.StopReport{Requested: true}, errListen, 1},
		{"a forced stop and an error", server.StopReport{Requested: true, Forced: true}, errListen, 1},
	}
	for _, c := range cases {
		if got := code(c.report, c.err); got != c.want {
			return fmt.Errorf("%s: ExitCode of %+v and error %v = %d, want %d", c.name, c.report, c.err, got, c.want)
		}
	}
	return nil
}

func TestExitCode(t *testing.T) {
	if err := checkExitCode(server.StopReport.ExitCode); err != nil {
		t.Error(err)
	}
	if got := (server.StopReport{}).ExitCode(nil); got == 0 {
		t.Errorf("the zero report with no error gives %d: a drain that cannot be shown is claimed", got)
	}
}

func TestExitCodeViolation(t *testing.T) {
	refuseAll(t, []violation{
		{"0 unless err", checkExitCode(func(_ server.StopReport, err error) int {
			if err != nil {
				return 1
			}
			return 0
		})},
		{"forced read before requested", checkExitCode(func(r server.StopReport, err error) int {
			switch {
			case err != nil:
				return 1
			case r.Forced:
				return 2
			case !r.Requested:
				return 1
			}
			return 0
		})},
		{"the error ignored", checkExitCode(func(r server.StopReport, _ error) int {
			switch {
			case !r.Requested:
				return 1
			case r.Forced:
				return 2
			}
			return 0
		})},
		{"forced as 1", checkExitCode(func(r server.StopReport, err error) int {
			if err == nil && r.Requested && !r.Forced {
				return 0
			}
			return 1
		})},
	})
}
