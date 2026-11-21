// SPDX-License-Identifier: MIT

package fibervisor_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/fiberfx/mx-echo/v6/fibervisor"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// fvAttemptNode is the node id fv's id.rs pins for fvOtherTrace and fvAttempt (TestAttempt holds derive to it).
const fvAttemptNode = "fd578c945e0aac89"

// fvAttemptRequest is the path of a call made from fvAttempt: a request's path, whose node is not the attempt's.
const fvAttemptRequest = fvAttempt + "/q00000000000200000007"

// fvOwn is the attempt of fv's own vector, as Read owes it.
var fvOwn = fibervisor.Supervision{Trace: fvOtherTrace, Path: fvAttempt, AttemptNode: fvAttemptNode}

// attemptOf is the Supervision Read owes for the attempt path of trace.
func attemptOf(trace, path string) fibervisor.Supervision {
	return fibervisor.Supervision{Trace: trace, Path: path, AttemptNode: derive(trace, path)}
}

// checkRead returns an error unless s and ok are what Read owes when want is the attempt: want and true, or the
// zero Supervision and false when want is zero. A shown attempt has no empty field.
func checkRead(want, s fibervisor.Supervision, ok bool) error {
	shown := want != fibervisor.Supervision{}
	if s != want || ok != shown {
		return fmt.Errorf("Read() = %+v, %v, want %+v, %v", s, ok, want, shown)
	}
	if ok && (s.Trace == "" || s.Path == "" || s.AttemptNode == "") {
		return fmt.Errorf("Read() = %+v, true: an empty field passed off as fv's", s)
	}
	return nil
}

// checkAgree returns an error unless attempt, what Attempt returned, is s.Path when Read reported ok, else none.
func checkAgree(s fibervisor.Supervision, ok bool, attempt string) error {
	want := observe.None
	if ok {
		want = s.Path
	}
	if attempt != want {
		return fmt.Errorf("Attempt() = %q, and Read() = %+v, %v", attempt, s, ok)
	}
	return nil
}

func TestRead(t *testing.T) {
	valid := leaf(fvTrace, fvAttempt)
	span := derive(fvTrace, fvAttempt)
	tests := []struct {
		name string
		env  env
		want fibervisor.Supervision // zero when the attempt is not shown
	}{
		// The node is fv's literal, not this test's derivation: Read is checked against fv's own vector.
		{"fv's own vector for an attempt", env{header(fvOtherTrace, fvAttemptNode), fvAttempt, fvOtherTrace}, fvOwn},
		{"a triple the test derives", valid, attemptOf(fvTrace, fvAttempt)},
		{"an attempt of a nested call", leaf(fvTrace, fvNested), attemptOf(fvTrace, fvNested)},

		{"nothing set", env{}, fibervisor.Supervision{}},
		{"no TRACEPARENT", env{path: fvAttempt, trace: fvTrace}, fibervisor.Supervision{}},
		{"no FV_PATH", env{traceparent: valid.traceparent, trace: fvTrace}, fibervisor.Supervision{}},
		{"no FV_TRACE", env{traceparent: valid.traceparent, path: fvAttempt}, fibervisor.Supervision{}},
		{"a TRACEPARENT that is not one", env{"not a traceparent", fvAttempt, fvTrace}, fibervisor.Supervision{}},
		{"a TRACEPARENT with no flags", env{"00-" + fvTrace + "-" + span, fvAttempt, fvTrace}, fibervisor.Supervision{}},
		{"a TRACEPARENT padded with a space", env{" " + valid.traceparent, fvAttempt, fvTrace}, fibervisor.Supervision{}},
		{"a run's path, with its own span", leaf(fvTrace, fvRequest+"/run"), fibervisor.Supervision{}},
		{"a request's path, with fv's own vector", env{header(fvTrace, "49c98335cd3dc4ef"), fvRequest, fvTrace}, fibervisor.Supervision{}},
		{"a sibling attempt's span", env{header(fvTrace, derive(fvTrace, fvRequest+"/run/a2")), fvAttempt, fvTrace}, fibervisor.Supervision{}},
		{"fv's attempt with its request's node", env{header(fvOtherTrace, derive(fvOtherTrace, fvAttemptRequest)), fvAttempt, fvOtherTrace}, fibervisor.Supervision{}},
		// Values whose JSON would need escaping never reach a Supervision: the path is not an attempt's, and the
		// trace is not TRACEPARENT's, which holds hex only.
		{"an FV_PATH with a quote and a newline", leaf(fvTrace, fvAttempt+"\"\n"), fibervisor.Supervision{}},
		{"an FV_TRACE with a quote", env{valid.traceparent, fvAttempt, fvTrace[:31] + `"`}, fibervisor.Supervision{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)
			s, ok := fibervisor.Read()
			if err := checkRead(tt.want, s, ok); err != nil {
				t.Errorf("%v, with TRACEPARENT=%q FV_PATH=%q FV_TRACE=%q", err, tt.env.traceparent, tt.env.path, tt.env.trace)
			}
			// Attempt is Read's path, or none: the two never disagree.
			shown := tt.want != fibervisor.Supervision{}
			if err := checkAttempt(tt.env, shown, fibervisor.Attempt()); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestReadViolation(t *testing.T) {
	for _, bad := range []struct {
		name string
		want fibervisor.Supervision
		got  fibervisor.Supervision
		ok   bool
	}{
		{"a path claimed with no trace and no node", fibervisor.Supervision{}, fibervisor.Supervision{Path: fvAttempt}, true},
		{"the zero Supervision claimed", fibervisor.Supervision{}, fibervisor.Supervision{}, true},
		{"a value returned with false", fibervisor.Supervision{}, fvOwn, false},
		{"a shown attempt with no node", fvOwn, fibervisor.Supervision{Trace: fvOtherTrace, Path: fvAttempt}, true},
		{"a shown attempt with another trace's node", fvOwn, attemptOf(fvTrace, fvAttempt), true},
		{"fv's attempt with its request's node", fvOwn, fibervisor.Supervision{Trace: fvOtherTrace, Path: fvAttempt, AttemptNode: derive(fvOtherTrace, fvAttemptRequest)}, true},
		{"a shown attempt reported as not shown", fvOwn, fibervisor.Supervision{}, false},
	} {
		if checkRead(bad.want, bad.got, bad.ok) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
	for _, bad := range []struct {
		name    string
		s       fibervisor.Supervision
		ok      bool
		attempt string
	}{
		{"none from Attempt where Read shows the attempt", fvOwn, true, observe.None},
		{"a path from Attempt where Read shows none", fibervisor.Supervision{}, false, fvAttempt},
		{"another attempt's path", fvOwn, true, fvRequest + "/run/a2"},
	} {
		if checkAgree(bad.s, bad.ok, bad.attempt) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
}

// followed is what LinesOf owes for plain, the line Lines writes for the same observation, and s: plain with
// "trace" and "attempt_node" spliced in before its closing brace.
func followed(plain []byte, s fibervisor.Supervision) string {
	return string(plain[:len(plain)-2]) + `,"trace":"` + s.Trace + `","attempt_node":"` + s.AttemptNode + `"}` + "\n"
}

// checkFollowed returns an error unless line is one JSON object that holds every key of plain, the line Lines
// writes for the same observation, with the same value, and besides them only trace and attempt_node, from s.
func checkFollowed(line, plain []byte, s fibervisor.Supervision) error {
	if err := checkLines(line, 1); err != nil {
		return err
	}
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal(line, &got); err != nil {
		return fmt.Errorf("the line is not one JSON object: %w: %q", err, line)
	}
	if err := json.Unmarshal(plain, &want); err != nil {
		return fmt.Errorf("Lines' line is not one JSON object: %w: %q", err, plain)
	}
	for key, value := range want {
		if !bytes.Equal(got[key], value) {
			return fmt.Errorf("the key %q is %s, and Lines writes %s", key, got[key], value)
		}
	}
	for key, value := range map[string]string{"trace": s.Trace, "attempt_node": s.AttemptNode} {
		var text string
		if err := json.Unmarshal(got[key], &text); err != nil || text != value {
			return fmt.Errorf("the key %q is %s, want %q", key, got[key], value)
		}
	}
	if len(got) != len(want)+2 {
		return fmt.Errorf("%d keys, want the %d Lines writes and 2: %q", len(got), len(want), line)
	}
	return nil
}

// sinkPair has LinesOf for s and Lines observe the same observations, and returns what each wrote, Write by Write.
func sinkPair(s fibervisor.Supervision, observations []observe.Observation) (of, plain [][]byte) {
	ofW, plainW := &writes{}, &writes{}
	ofSink, plainSink := fibervisor.LinesOf(ofW, s), fibervisor.Lines(plainW)
	for _, o := range observations {
		ofSink.Observe(o)
		plainSink.Observe(o)
	}
	return ofW.calls, plainW.calls
}

// observations are three lines: a long one, a short one that reuses the sink's buffer, and one that needs escaping.
var observations = []observe.Observation{
	{ID: reqid.New(1, 1), Method: strings.Repeat("X", 300)},
	{ID: reqid.New(1, 2), Method: "GET", Attempt: fvAttempt},
	{ID: reqid.New(1, 3), Method: "a \"quoted\"\nmethod"},
}

func TestLinesOf(t *testing.T) {
	// From fv's own vector to the line: what Read gives is what LinesOf writes.
	env{header(fvOtherTrace, fvAttemptNode), fvAttempt, fvOtherTrace}.set(t)
	s, ok := fibervisor.Read()
	if err := checkRead(fvOwn, s, ok); err != nil {
		t.Fatal(err)
	}
	of, plain := sinkPair(s, observations)
	if len(of) != len(observations) || len(plain) != len(observations) {
		t.Fatalf("%d and %d writes for %d observations", len(of), len(plain), len(observations))
	}
	for i := range observations {
		if err := checkFollowed(of[i], plain[i], s); err != nil {
			t.Errorf("write %d: %v", i+1, err)
		}
		if want := followed(plain[i], s); string(of[i]) != want {
			t.Errorf("write %d = %q, want %q", i+1, of[i], want)
		}
		// Up to "panicked" and its value, the line is Lines' own, byte for byte.
		if head := plain[i][:len(plain[i])-2]; !bytes.HasPrefix(of[i], head) {
			t.Errorf("write %d = %q, which does not begin with Lines' line %q", i+1, of[i], head)
		}
	}
}

func TestLinesOfNotSupervised(t *testing.T) {
	env{}.set(t)
	read, ok := fibervisor.Read()
	if ok {
		t.Fatalf("Read() = %+v, true with no fv variables", read)
	}
	for _, tt := range []struct {
		name string
		s    fibervisor.Supervision
	}{
		{"the zero Supervision", fibervisor.Supervision{}},
		{"Read with no fv variables", read},
	} {
		t.Run(tt.name, func(t *testing.T) {
			of, plain := sinkPair(tt.s, observations)
			if len(of) != len(observations) {
				t.Fatalf("%d writes for %d observations", len(of), len(observations))
			}
			for i := range observations {
				if !bytes.Equal(of[i], plain[i]) {
					t.Errorf("write %d = %q, and Lines writes %q", i+1, of[i], plain[i])
				}
			}
		})
	}
}

// TestLinesOfHandBuilt pins v6.6.0's rule: a Supervision built by hand gets the two keys only when both values have
// fv's shapes, and otherwise LinesOf writes exactly what Lines writes, as v6.6.0 (c712809) did.
func TestLinesOfHandBuilt(t *testing.T) {
	for _, tt := range []struct {
		name     string
		s        fibervisor.Supervision
		followed bool
	}{
		{"a local path alone", fibervisor.Supervision{Path: "local"}, false},
		{"a path alone", fibervisor.Supervision{Path: fvAttempt}, false},
		{"no node", fibervisor.Supervision{Trace: fvOtherTrace, Path: fvAttempt}, false},
		{"no trace", fibervisor.Supervision{Path: fvAttempt, AttemptNode: fvAttemptNode}, false},
		{"a trace that would end the line", fibervisor.Supervision{Trace: `x"}` + "\n" + `{"id":"forged`, Path: fvAttempt, AttemptNode: fvAttemptNode}, false},
		{"a node that would add a key", fibervisor.Supervision{Trace: fvOtherTrace, Path: fvAttempt, AttemptNode: `x","attempt":"forged`}, false},
		{"an uppercase node", fibervisor.Supervision{Trace: fvOtherTrace, Path: fvAttempt, AttemptNode: strings.ToUpper(fvAttemptNode)}, false},
		{"a node of 15 digits", fibervisor.Supervision{Trace: fvOtherTrace, Path: fvAttempt, AttemptNode: fvAttemptNode[:15]}, false},
		{"a trace of 33 digits", fibervisor.Supervision{Trace: fvOtherTrace + "0", Path: fvAttempt, AttemptNode: fvAttemptNode}, false},
		// The shapes are checked, not the derivation: only Read gives a node fv derived.
		{"a request's node set by hand", fibervisor.Supervision{Trace: fvOtherTrace, Path: fvAttempt, AttemptNode: derive(fvOtherTrace, fvAttemptRequest)}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			of, plain := sinkPair(tt.s, observations)
			if len(of) != len(observations) {
				t.Fatalf("%d writes for %d observations", len(of), len(observations))
			}
			for i := range observations {
				if !tt.followed {
					if !bytes.Equal(of[i], plain[i]) {
						t.Errorf("write %d = %q, and Lines writes %q", i+1, of[i], plain[i])
					}
					continue
				}
				if err := checkFollowed(of[i], plain[i], tt.s); err != nil {
					t.Errorf("write %d: %v", i+1, err)
				}
				if want := followed(plain[i], tt.s); string(of[i]) != want {
					t.Errorf("write %d = %q, want %q", i+1, of[i], want)
				}
			}
		})
	}
}

func TestLinesOfViolation(t *testing.T) {
	plain := append(observe.Observation{ID: reqid.New(1, 1), Method: "GET"}.AppendJSON(nil), '\n')
	head := string(plain[:len(plain)-2])
	good := followed(plain, fvOwn)
	if err := checkFollowed([]byte(good), plain, fvOwn); err != nil {
		t.Fatalf("the spliced line did not pass the check: %v", err)
	}
	for _, bad := range []struct{ name, line string }{
		{"Lines' line alone", string(plain)},
		{"no attempt_node", head + `,"trace":"` + fvTrace + `"}` + "\n"},
		{"another trace's node", followed(plain, attemptOf(fvTrace, fvAttempt))},
		{"the keys after the closing brace", string(plain[:len(plain)-1]) + `,"trace":"` + fvOtherTrace + `"}` + "\n"},
		{"a changed method", strings.Replace(good, `"GET"`, `"PUT"`, 1)},
		{"a third key", strings.TrimSuffix(good, "}\n") + `,"node":"` + fvAttemptNode + `"}` + "\n"},
		{"a torn line", good[:20] + "\n"},
	} {
		if checkFollowed([]byte(bad.line), plain, fvOwn) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
	// An absent node is written none, never "".
	noNode := fibervisor.Supervision{Trace: fvOtherTrace, AttemptNode: observe.None}
	if checkFollowed([]byte(followed(plain, fibervisor.Supervision{Trace: fvOtherTrace})), plain, noNode) == nil {
		t.Error(`an absent node written "" passed the check`)
	}
}

// checkNoAllocs returns an error unless sink, warm, writes the lines of observations with no allocation.
func checkNoAllocs(sink observe.Sink) error {
	n := testing.AllocsPerRun(100, func() {
		for _, o := range observations {
			sink.Observe(o)
		}
	})
	if n != 0 {
		return fmt.Errorf("%v allocations for %d lines when warm, want 0", n, len(observations))
	}
	return nil
}

// freshLines is a sink that builds each line in a new buffer: what LinesOf must not become.
type freshLines struct{ w io.Writer }

func (f freshLines) Observe(o observe.Observation) {
	_, _ = f.w.Write(append(o.AppendJSON(nil), '\n'))
}

func TestLinesOfAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	for _, tt := range []struct {
		name string
		sink observe.Sink
	}{
		{"Lines", fibervisor.Lines(io.Discard)},
		{"LinesOf", fibervisor.LinesOf(io.Discard, fvOwn)},
		{"LinesOf, a hand-built value", fibervisor.LinesOf(io.Discard, fibervisor.Supervision{Path: fvAttempt})},
	} {
		if err := checkNoAllocs(tt.sink); err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestLinesOfAllocsViolation(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	if checkNoAllocs(freshLines{io.Discard}) == nil {
		t.Error("a sink that allocates each line passed the check")
	}
}

// checkStopSignals returns an error unless got is SIGTERM then SIGINT, the two signals fv stops a leaf with.
func checkStopSignals(got []os.Signal) error {
	if want := []os.Signal{syscall.SIGTERM, syscall.SIGINT}; !slices.Equal(got, want) {
		return fmt.Errorf("the stop signals are %v, want %v", got, want)
	}
	return nil
}

// checkFresh returns an error unless changing what next returned, in place and by append, leaves its next result
// the stop signals still.
func checkFresh(next func() []os.Signal) error {
	first := next()
	first[0] = os.Kill
	grown := append(first[:1], os.Kill)
	if err := checkStopSignals(next()); err != nil {
		return fmt.Errorf("after the last result became %v: %w", grown, err)
	}
	return nil
}

func TestStopSignals(t *testing.T) {
	if err := checkStopSignals(fibervisor.StopSignals()); err != nil {
		t.Fatal(err)
	}
	if err := checkFresh(fibervisor.StopSignals); err != nil {
		t.Fatal(err)
	}
}

func TestStopSignalsViolation(t *testing.T) {
	for _, bad := range [][]os.Signal{
		nil,
		{syscall.SIGTERM},
		{syscall.SIGINT, syscall.SIGTERM},
		{syscall.SIGTERM, os.Kill},
		{syscall.SIGTERM, syscall.SIGINT, os.Kill},
	} {
		if checkStopSignals(bad) == nil {
			t.Errorf("the stop signals %v passed the check", bad)
		}
	}
	// One slice handed to every caller, as an exported var would be: the first caller's change reaches the next.
	shared := []os.Signal{syscall.SIGTERM, os.Interrupt}
	if checkFresh(func() []os.Signal { return shared }) == nil {
		t.Error("a shared slice passed the check")
	}
}
