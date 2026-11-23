// SPDX-License-Identifier: MIT

// Package fibervisor lets a service that fv holds state the attempt it runs in, and write its observations as
// JSON lines. fv, the Fibervisor's supervisor, starts the whole server process as one leaf and gives it three
// variables: TRACEPARENT, FV_PATH and FV_TRACE. Attempt reads them, and Read keeps the trace and the attempt's
// node id beside the path; Build reads the binary's own build information; Lines writes where the service says,
// which under fv is the output the leaf inherits, and LinesOf adds the trace and the node to each line, so that
// fv's own commands can be run from it. StopSignals names the signals fv stops a leaf with.
//
// What this package is not. It writes no fv record and reaches fv over nothing: an observation is the leaf's own
// report about itself, kept beside fv's records and never among them, and a request is not a node of fv's tree.
// It starts nothing: no process, no goroutine, no init. It is imported by a service, never by the root package,
// by middleware or by a pure package, so a service that does not import it carries none of it.
package fibervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	"github.com/fiberfx/mx-echo/v7/observe"
	"github.com/fiberfx/mx-echo/v7/reqid"
)

// nodeDomain is the first bytes of the input fv hashes for a node's id.
const nodeDomain = "fv.node/1\x00"

// A version 00 traceparent is 55 bytes: "00-", 32 hex digits of trace, "-", 16 of span, "-", 2 of flags.
const (
	traceparentLen = 55
	traceStart     = 3
	traceEnd       = 35
	spanStart      = 36
	spanEnd        = 52
)

// Attempt returns FV_PATH when TRACEPARENT, FV_PATH and FV_TRACE describe one fv attempt, else observe.None.
// It reads the environment when called: a service calls it once at start and keeps the value.
//
// The three describe one attempt when TRACEPARENT is a version 00 traceparent, FV_TRACE is its trace, FV_PATH is
// the path of an attempt (it ends in a<n>), and TRACEPARENT's span is the id fv derives from that trace and that
// path. A path that only looks like an attempt is not claimed: without the derived span nothing shows that fv
// wrote it.
//
// The test is never laxer than fv's own door, which takes a call made from this process as that attempt's child
// on the same path and span. It is stricter in two things, neither of which fv's own leaf can fail: FV_TRACE must
// be TRACEPARENT's trace, where the door does not read FV_TRACE, and TRACEPARENT must be the 55 bytes fv writes,
// where the door trims white space around it first.
func Attempt() string {
	s, ok := Read()
	if !ok {
		return observe.None
	}
	return s.Path
}

// oneAttempt reports whether the three values are what fv gives the leaf of one attempt.
func oneAttempt(traceparent, path, trace string) bool {
	if _, ok := reqid.ParseTraceparent(traceparent); !ok {
		return false
	}
	// fv writes and reads version 00 only; ParseTraceparent also reads a later version, which is 55 bytes or more.
	if len(traceparent) != traceparentLen || !strings.HasPrefix(traceparent, "00-") {
		return false
	}
	return traceparent[traceStart:traceEnd] == trace &&
		isAttemptPath(path) &&
		traceparent[spanStart:spanEnd] == nodeID(trace, path)
}

// isAttemptPath reports whether path is the name of an attempt in fv's tree: segments q<key>, run, a<n> in that
// rotation, one or more times, so that the last is a<n>.
func isAttemptPath(path string) bool {
	segs := strings.Split(path, "/")
	if len(segs)%3 != 0 {
		return false
	}
	for i, seg := range segs {
		var fits bool
		switch i % 3 {
		case 0:
			fits = isCall(seg)
		case 1:
			fits = seg == "run"
		default:
			fits = isAttemptNumber(seg)
		}
		if !fits {
			return false
		}
	}
	return true
}

// isCall reports whether seg is q and a call's key: 20 lowercase hex digits.
func isCall(seg string) bool {
	return len(seg) == 21 && seg[0] == 'q' && isHex(seg[1:], 20)
}

// isHex reports whether s is n lowercase hex digits.
func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// isAttemptNumber reports whether seg is a and an attempt's number: decimal digits with no leading zero, from 1
// to the largest 32-bit unsigned number.
func isAttemptNumber(seg string) bool {
	if len(seg) < 2 || seg[0] != 'a' || seg[1] == '0' {
		return false
	}
	for i := 1; i < len(seg); i++ {
		if seg[i] < '0' || seg[i] > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(seg[1:], 10, 32)
	return err == nil
}

// nodeID returns the id fv derives for the node path of trace, as 16 lowercase hex digits: the first 8 bytes of
// SHA-256 over the domain, the trace's text, a zero byte and the path's text. An all-zero result is not a valid
// span id, so its last byte is set to 1.
func nodeID(trace, path string) string {
	input := make([]byte, 0, len(nodeDomain)+len(trace)+1+len(path))
	input = append(input, nodeDomain...)
	input = append(input, trace...)
	input = append(input, 0)
	input = append(input, path...)
	sum := sha256.Sum256(input)
	var id, zero [8]byte
	copy(id[:], sum[:])
	if id == zero {
		id[7] = 1
	}
	return hex.EncodeToString(id[:])
}

// Build returns the main module's path, version and sum from the binary's build information, else observe.None.
// The text is the path, "@" and the version, then a space and the sum when the binary records one. It is
// observe.None when the binary holds no build information or names no main module.
func Build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path == "" {
		return observe.None
	}
	build := info.Main.Path + "@" + info.Main.Version
	if info.Main.Sum != "" {
		build += " " + info.Main.Sum
	}
	return build
}

// Lines returns a sink that writes each observation as one JSON line to w: one Write per line, one line at a time.
// The sink holds its own lock, so lines from concurrent requests do not interleave whatever w is. A sink has no
// way to return an error, so a failed Write is dropped: that line is lost and the next is still written. w must
// not be nil.
//
// The Write runs on the request's goroutine, under that lock: while w blocks, such as on a full pipe, every
// request that ends waits behind it. Give Lines a writer that returns quickly. Standard error, where fv leaves a
// leaf's output, is one only while whatever reads it keeps reading; a service that cannot rely on that gives a
// writer that buffers and does not block, and that writer guards its own buffer, since the sink's lock covers
// the Write alone.
func Lines(w io.Writer) observe.Sink {
	return &lines{w: w}
}

// lines is the sink Lines and LinesOf return.
type lines struct {
	mu   sync.Mutex
	w    io.Writer
	buf  []byte
	tail []byte // LinesOf's keys and the closing brace that replace the line's own brace; nil for Lines
}

// Observe writes o as one line.
func (l *lines) Observe(o observe.Observation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = o.AppendJSON(l.buf[:0])
	if l.tail != nil {
		l.buf = append(l.buf[:len(l.buf)-1], l.tail...)
	}
	l.buf = append(l.buf, '\n')
	_, _ = l.w.Write(l.buf)
}
