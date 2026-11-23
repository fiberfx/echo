// SPDX-License-Identifier: MIT

package fibervisor_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/fiberfx/mx-echo/v7/fibervisor"
	"github.com/fiberfx/mx-echo/v7/observe"
	"github.com/fiberfx/mx-echo/v7/reqid"
)

// The traces and paths of fv's own tests (fiberfx/fv/src/id.rs in jonnify, the helpers trace and key and the test
// pin_node_ids_and_hex_text). The node ids that test asserts for them are the literals in TestAttempt.
const fvTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
const fvOtherTrace = "5bf92f3577b34da6a3ce929d0e0e4736"
const fvRequest = "q00000000000100000007"
const fvAttempt = "q00000000000100000007/run/a1"
const fvDeepRun = "q01934a5b6c7d00000007/run/a12/q00000000000500000007/run"
const fvNested = "q00000000000100000007/run/a1/q00000000000200000007/run/a3"

// env is the three variables fv gives a leaf. An empty field is a variable that is not set.
type env struct{ traceparent, path, trace string }

// set puts e in the environment for the rest of the test.
func (e env) set(t *testing.T) {
	t.Helper()
	for name, value := range map[string]string{"TRACEPARENT": e.traceparent, "FV_PATH": e.path, "FV_TRACE": e.trace} {
		t.Setenv(name, value)
		if value != "" {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

// derive is the test's own copy of fv's NodeId::derive. TestAttempt holds it to fv's answers before it is used.
func derive(trace, path string) string {
	sum := sha256.Sum256([]byte("fv.node/1\x00" + trace + "\x00" + path))
	return hex.EncodeToString(sum[:8])
}

// header is the traceparent fv writes for a leaf: version 00, sampled.
func header(trace, span string) string {
	return "00-" + trace + "-" + span + "-01"
}

// leaf is the environment fv's door gives the leaf of the attempt path of trace.
func leaf(trace, path string) env {
	return env{traceparent: header(trace, derive(trace, path)), path: path, trace: trace}
}

// checkAttempt returns an error unless got is what Attempt owes for e: FV_PATH when the attempt is shown, else none.
func checkAttempt(e env, shown bool, got string) error {
	want := observe.None
	if shown {
		want = e.path
	}
	if got != want {
		return fmt.Errorf("Attempt() = %q, want %q, with TRACEPARENT=%q FV_PATH=%q FV_TRACE=%q",
			got, want, e.traceparent, e.path, e.trace)
	}
	return nil
}

func TestAttempt(t *testing.T) {
	// The test's derivation is fv's: the three node ids and the header that id.rs pins.
	for _, pin := range []struct{ trace, path, id string }{
		{fvTrace, fvRequest, "49c98335cd3dc4ef"},
		{fvTrace, fvDeepRun, "3d669ece721af88e"},
		{fvOtherTrace, fvAttempt, "fd578c945e0aac89"},
	} {
		if got := derive(pin.trace, pin.path); got != pin.id {
			t.Fatalf("derive(%q, %q) = %s, and fv pins %s", pin.trace, pin.path, got, pin.id)
		}
	}
	if got, want := header(fvTrace, derive(fvTrace, fvRequest)), "00-4bf92f3577b34da6a3ce929d0e0e4736-49c98335cd3dc4ef-01"; got != want {
		t.Fatalf("header = %q, and fv pins %q", got, want)
	}

	valid := leaf(fvTrace, fvAttempt)
	span := derive(fvTrace, fvAttempt)
	tests := []struct {
		name  string
		env   env
		shown bool
	}{
		// The literal span is fv's, not this test's: the door's rule is checked against fv's own vector.
		{"fv's own vector for an attempt", env{header(fvOtherTrace, "fd578c945e0aac89"), fvAttempt, fvOtherTrace}, true},
		{"a triple the test derives", valid, true},
		{"an attempt of a nested call", leaf(fvTrace, fvNested), true},
		{"the largest attempt number", leaf(fvTrace, fvRequest+"/run/a4294967295"), true},
		{"not sampled", env{"00-" + fvTrace + "-" + span + "-00", fvAttempt, fvTrace}, true},

		{"nothing set", env{}, false},
		{"FV_PATH alone", env{path: fvAttempt}, false},
		{"FV_PATH and FV_TRACE", env{path: fvAttempt, trace: fvTrace}, false},
		{"TRACEPARENT alone", env{traceparent: valid.traceparent}, false},
		{"no FV_TRACE", env{traceparent: valid.traceparent, path: fvAttempt}, false},
		{"no FV_PATH", env{traceparent: valid.traceparent, trace: fvTrace}, false},

		{"a TRACEPARENT of another trace", env{leaf(fvOtherTrace, fvAttempt).traceparent, fvAttempt, fvTrace}, false},
		{"a TRACEPARENT of another trace with this trace's span", env{header(fvOtherTrace, span), fvAttempt, fvTrace}, false},
		{"an FV_TRACE of another trace", env{valid.traceparent, fvAttempt, fvOtherTrace}, false},

		// In each of these the span is the id derived from the path's text, so only the path is at fault.
		{"a request's path, with fv's own vector", env{header(fvTrace, "49c98335cd3dc4ef"), fvRequest, fvTrace}, false},
		{"a run's path", leaf(fvTrace, fvRequest+"/run"), false},
		{"a call's path under an attempt", leaf(fvTrace, fvAttempt+"/q00000000000200000007"), false},
		{"attempt 0", leaf(fvTrace, fvRequest+"/run/a0"), false},
		{"an attempt number with a leading zero", leaf(fvTrace, fvRequest+"/run/a01"), false},
		{"an attempt number above 32 bits", leaf(fvTrace, fvRequest+"/run/a4294967296"), false},
		{"an attempt number with a sign", leaf(fvTrace, fvRequest+"/run/a+1"), false},
		{"an attempt with no number", leaf(fvTrace, fvRequest+"/run/a"), false},
		{"an attempt directly under a request", leaf(fvTrace, fvRequest+"/a1"), false},
		{"a path with no request", leaf(fvTrace, "run/a1"), false},
		{"a trailing slash", leaf(fvTrace, fvAttempt+"/"), false},
		{"a leading space", leaf(fvTrace, " "+fvAttempt), false},
		{"an uppercase segment", leaf(fvTrace, "Q00000000000100000007/run/a1"), false},
		{"a key of 19 digits", leaf(fvTrace, "q0000000000010000000/run/a1"), false},
		{"a key that is not hex", leaf(fvTrace, "q0000000000010000000g/run/a1"), false},

		{"a sibling attempt's span", env{header(fvTrace, derive(fvTrace, fvRequest+"/run/a2")), fvAttempt, fvTrace}, false},
		{"the run's span", env{header(fvTrace, derive(fvTrace, fvRequest+"/run")), fvAttempt, fvTrace}, false},
		{"a foreign span", env{header(fvTrace, "00f067aa0ba902b7"), fvAttempt, fvTrace}, false},

		{"version 01", env{"01-" + fvTrace + "-" + span + "-01", fvAttempt, fvTrace}, false},
		{"version 01 with a suffix", env{"01-" + fvTrace + "-" + span + "-01-extra", fvAttempt, fvTrace}, false},
		{"version 00 with a suffix", env{valid.traceparent + "-extra", fvAttempt, fvTrace}, false},
		{"an uppercase trace", env{header(strings.ToUpper(fvTrace), span), fvAttempt, strings.ToUpper(fvTrace)}, false},
		{"no flags", env{"00-" + fvTrace + "-" + span, fvAttempt, fvTrace}, false},
		// fv's door trims a TRACEPARENT before it parses it, and fv never writes one that needs it. This package is
		// stricter than the door here: a padded value is not claimed.
		{"a TRACEPARENT padded with a space", env{" " + valid.traceparent, fvAttempt, fvTrace}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)
			if err := checkAttempt(tt.env, tt.shown, fibervisor.Attempt()); err != nil {
				t.Error(err)
			}
			s, ok := fibervisor.Read()
			if err := checkAgree(s, ok, fibervisor.Attempt()); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestAttemptViolation(t *testing.T) {
	alone := env{path: fvAttempt}
	if checkAttempt(alone, false, alone.path) == nil {
		t.Fatal("an attempt claimed from FV_PATH alone passed the check")
	}
	if checkAttempt(leaf(fvTrace, fvAttempt), true, observe.None) == nil {
		t.Fatal("a shown attempt reported as none passed the check")
	}
}

func TestAttemptReadOnce(t *testing.T) {
	leaf(fvTrace, fvAttempt).set(t)
	a := fibervisor.Attempt()
	o := observe.Observer{Attempt: a}

	other := fvRequest + "/run/a2"
	t.Setenv("FV_PATH", other)
	if a != fvAttempt || o.Attempt != fvAttempt {
		t.Fatalf("the kept value is %q and the observer's %q, want %q for both", a, o.Attempt, fvAttempt)
	}
	// The package kept nothing either: a second call reads the changed environment, where the span no longer fits.
	if got := fibervisor.Attempt(); got != observe.None {
		t.Fatalf("Attempt() after FV_PATH changed = %q, want %q", got, observe.None)
	}
	leaf(fvTrace, other).set(t)
	if got := fibervisor.Attempt(); got != other {
		t.Fatalf("Attempt() in the next attempt's environment = %q, want %q", got, other)
	}
}

// checkBuild returns an error unless got is none, or a module path, "@" and what follows.
func checkBuild(got string) error {
	if got == observe.None {
		return nil
	}
	path, _, ok := strings.Cut(got, "@")
	if !ok || path == "" || strings.TrimSpace(got) != got {
		return fmt.Errorf("Build() = %q, want %q or PATH@VERSION", got, observe.None)
	}
	return nil
}

func TestBuild(t *testing.T) {
	got := fibervisor.Build()
	if err := checkBuild(got); err != nil {
		t.Fatal(err)
	}
	if again := fibervisor.Build(); again != got {
		t.Fatalf("Build() = %q, then %q", got, again)
	}
	t.Logf("this test binary states the build %q", got)
}

func TestBuildViolation(t *testing.T) {
	for _, bad := range []string{"", "@v6.5.0", "v6.5.0"} {
		if checkBuild(bad) == nil {
			t.Fatalf("the build %q passed the check", bad)
		}
	}
}

// checkLines returns an error unless out is want whole lines, each a JSON object with a name no other line has.
func checkLines(out []byte, want int) error {
	if len(out) == 0 || out[len(out)-1] != '\n' {
		return fmt.Errorf("the output does not end with a newline: %d bytes", len(out))
	}
	rows := bytes.Split(out[:len(out)-1], []byte("\n"))
	if len(rows) != want {
		return fmt.Errorf("%d lines, want %d", len(rows), want)
	}
	seen := make(map[string]bool, len(rows))
	for i, row := range rows {
		var line struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(row, &line); err != nil {
			return fmt.Errorf("line %d is not one JSON object: %w: %q", i+1, err, row)
		}
		if _, ok := reqid.Parse(line.ID); !ok {
			return fmt.Errorf("line %d has the id %q, which is not a name", i+1, line.ID)
		}
		if seen[line.ID] {
			return fmt.Errorf("line %d repeats the id %q", i+1, line.ID)
		}
		seen[line.ID] = true
	}
	return nil
}

func TestLines(t *testing.T) {
	const writers, each = 16, 50
	// A plain buffer: the sink's own lock is the only one, so the race detector sees a sink that does not hold it.
	var out bytes.Buffer
	sink := fibervisor.Lines(&out)
	var wg sync.WaitGroup
	for g := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				sink.Observe(observe.Observation{ID: reqid.New(int64(g+1), uint32(i)), Method: "GET"})
			}
		}()
	}
	wg.Wait()
	if err := checkLines(out.Bytes(), writers*each); err != nil {
		t.Fatal(err)
	}
}

func TestLinesViolation(t *testing.T) {
	a := observe.Observation{ID: reqid.New(1, 1), Method: "GET"}.AppendJSON(nil)
	b := observe.Observation{ID: reqid.New(1, 2), Method: "GET"}.AppendJSON(nil)
	whole := string(a) + "\n" + string(b) + "\n"
	if err := checkLines([]byte(whole), 2); err != nil {
		t.Fatalf("two whole lines did not pass the check: %v", err)
	}
	for _, bad := range []struct{ name, out string }{
		{"a torn line", string(a[:8]) + string(b) + "\n" + string(a[8:]) + "\n"},
		{"a repeated name", string(a) + "\n" + string(a) + "\n"},
		{"a missing line", string(a) + "\n"},
		{"an unfinished line", string(a) + "\n" + string(b)},
	} {
		if checkLines([]byte(bad.out), 2) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
}

// writes keeps what each Write was given, and fails each Write with err when it is set.
type writes struct {
	calls [][]byte
	err   error
}

func (w *writes) Write(p []byte) (int, error) {
	w.calls = append(w.calls, bytes.Clone(p))
	if w.err != nil {
		return 0, w.err
	}
	return len(p), nil
}

func TestLinesOneWriteEach(t *testing.T) {
	// A long line and then a short one: the sink's buffer is used again and must not carry the first line's tail.
	observations := []observe.Observation{
		{ID: reqid.New(1, 1), Method: strings.Repeat("X", 300)},
		{ID: reqid.New(1, 2), Method: "GET"},
		{ID: reqid.New(1, 3), Method: "a \"quoted\"\nmethod"},
	}
	for _, failing := range []error{nil, errors.New("the writer is closed")} {
		w := &writes{err: failing}
		sink := fibervisor.Lines(w)
		for _, o := range observations {
			sink.Observe(o)
		}
		// A failed Write is dropped: every observation still reaches the writer once.
		if len(w.calls) != len(observations) {
			t.Fatalf("%d writes for %d observations, with the writer's error %v", len(w.calls), len(observations), failing)
		}
		for i, call := range w.calls {
			if err := checkLines(call, 1); err != nil {
				t.Errorf("write %d is not one whole line: %v", i+1, err)
			}
			if want := append(observations[i].AppendJSON(nil), '\n'); !bytes.Equal(call, want) {
				t.Errorf("write %d = %q, want %q", i+1, call, want)
			}
		}
	}
}
