// SPDX-License-Identifier: MIT

package server

// ExitCode returns the exit status of a server's main for this report and the error Start returned: 0 when err is
// nil and the stop was requested and drained; 2 when it was requested and forced; 1 otherwise: a failed start, a
// Serve that ended on its own, or a zero report, which is what Start leaves when OnStop was not set. It exits nothing.
func (r StopReport) ExitCode(err error) int {
	switch {
	case err != nil || !r.Requested:
		return 1
	case r.Forced:
		return 2
	}
	return 0
}
