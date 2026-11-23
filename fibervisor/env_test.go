// SPDX-License-Identifier: MIT

package fibervisor_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v7/fibervisor"
	"github.com/fiberfx/mx-echo/v7/reqid"
)

// childBase is a base a service gives Env: entries of its own, a stale entry for three of fv's names, and an
// entry with no '='.
var childBase = []string{"PATH=/bin", "TRACEPARENT=" + staleParent, "A=1", "FV_PATH=" + fvRequest + "/run/a9", "NOEQ", "HOME=/old"}

// childState sets the state this process has for the rest of the test: FV_STATE and HOME set, XDG_STATE_HOME not,
// and FV_CRASH set, which no child may inherit through Env.
func childState(t *testing.T) {
	t.Helper()
	t.Setenv("FV_STATE", "/state")
	t.Setenv("HOME", "/home/svc")
	t.Setenv("XDG_STATE_HOME", "")
	if err := os.Unsetenv("XDG_STATE_HOME"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FV_CRASH", "1")
}

// lookup returns the values env gives name, in order.
func lookup(env []string, name string) []string {
	var values []string
	for _, kv := range env {
		if n, v, ok := strings.Cut(kv, "="); ok && n == name {
			values = append(values, v)
		}
	}
	return values
}

// checkFrame returns an error unless env places a child as fv's frame says: TRACEPARENT once with traceparent,
// FV_PATH once with path and FV_TRACE once with trace, each absent when its want is "", and no FV_CRASH.
func checkFrame(env []string, traceparent, path, trace string) error {
	for _, v := range []struct{ name, want string }{{"TRACEPARENT", traceparent}, {"FV_PATH", path}, {"FV_TRACE", trace}} {
		got := lookup(env, v.name)
		if v.want == "" && len(got) != 0 {
			return fmt.Errorf("%s=%q, want it absent", v.name, got)
		}
		if v.want != "" && (len(got) != 1 || got[0] != v.want) {
			return fmt.Errorf("%s=%q, want [%q]", v.name, got, v.want)
		}
	}
	if got := lookup(env, "FV_CRASH"); len(got) != 0 {
		return fmt.Errorf("FV_CRASH=%q reached the child", got)
	}
	return nil
}

func TestEnv(t *testing.T) {
	leaf(fvTrace, fvAttempt).set(t)
	childState(t)
	s, ok := fibervisor.Read()
	if !ok {
		t.Fatal("Read() is false under fv's leaf environment")
	}
	// Values are Read's: a later change of the environment does not reach FV_PATH, FV_TRACE or TRACEPARENT.
	t.Setenv("FV_PATH", fvRequest+"/run/a2")
	t.Setenv("FV_TRACE", fvOtherTrace)
	base := slices.Clone(childBase)
	attempt := header(fvTrace, derive(fvTrace, fvAttempt))

	got := s.Env(base)
	want := []string{"PATH=/bin", "A=1", "NOEQ",
		"TRACEPARENT=" + attempt, "FV_PATH=" + fvAttempt, "FV_TRACE=" + fvTrace, "FV_STATE=/state", "HOME=/home/svc"}
	if !slices.Equal(got, want) {
		t.Errorf("Env(%q) =\n%q, want\n%q", base, got, want)
	}
	if err := checkFrame(got, attempt, fvAttempt, fvTrace); err != nil {
		t.Errorf("Env: %v", err)
	}
	if !slices.Equal(base, childBase) {
		t.Errorf("Env changed base to %q", base)
	}
}

func TestLinkEnv(t *testing.T) {
	leaf(fvTrace, fvAttempt).set(t)
	childState(t)
	s, _ := fibervisor.Read()
	base := slices.Clone(childBase)
	node, _ := s.RequestNode(fvRequestID)
	linked := header(fvTrace, node)

	got := s.LinkEnv(base, fvRequestID)
	want := []string{"PATH=/bin", "A=1", "NOEQ", "TRACEPARENT=" + linked, "FV_STATE=/state", "HOME=/home/svc"}
	if !slices.Equal(got, want) {
		t.Errorf("LinkEnv(%q) =\n%q, want\n%q", base, got, want)
	}
	if err := checkFrame(got, linked, "", ""); err != nil {
		t.Errorf("LinkEnv: %v", err)
	}
	if !slices.Equal(base, childBase) {
		t.Errorf("LinkEnv changed base to %q", base)
	}
}

// TestEnvNotSupervised holds the zero Supervision, and a request with no name for LinkEnv, to removing fv's three
// names from base and adding nothing.
func TestEnvNotSupervised(t *testing.T) {
	childState(t)
	leaf(fvTrace, fvAttempt).set(t)
	held, _ := fibervisor.Read()
	want := []string{"PATH=/bin", "A=1", "NOEQ", "HOME=/old"}
	for _, tt := range []struct {
		name string
		got  func([]string) []string
	}{
		{"Env, zero Supervision", fibervisor.Supervision{}.Env},
		{"LinkEnv, zero Supervision", func(b []string) []string { return fibervisor.Supervision{}.LinkEnv(b, fvRequestID) }},
		{"LinkEnv, zero ID", func(b []string) []string { return held.LinkEnv(b, reqid.ID{}) }},
		{"Env, hand-built", fibervisor.Supervision{Path: fvAttempt}.Env},
	} {
		base := slices.Clone(childBase)
		got := tt.got(base)
		if !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", tt.name, got, want)
		}
		if err := checkFrame(got, "", "", ""); err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		if !slices.Equal(base, childBase) {
			t.Errorf("%s changed base to %q", tt.name, base)
		}
	}
	// A non-nil empty base stays empty: the zero Supervision adds nothing to it.
	if got := (fibervisor.Supervision{}).Env([]string{}); got == nil || len(got) != 0 {
		t.Errorf("Env([]string{}) = %#v for the zero Supervision, want an empty slice", got)
	}
}

// TestEnvNilBase holds a nil base to os.Environ(), as a nil os/exec.Cmd.Env is, and a non-nil empty base to only
// the added names.
func TestEnvNilBase(t *testing.T) {
	leaf(fvTrace, fvAttempt).set(t)
	childState(t)
	t.Setenv("PATH", "/svc/bin")
	// A nil base is the whole environment, as os/exec's is, so FV_CRASH is the caller's to unset: this test does.
	if err := os.Unsetenv("FV_CRASH"); err != nil {
		t.Fatal(err)
	}
	s, _ := fibervisor.Read()
	node, _ := s.RequestNode(fvRequestID)
	attempt, linked := header(fvTrace, derive(fvTrace, fvAttempt)), header(fvTrace, node)
	for _, tt := range []struct {
		name                     string
		got                      func([]string) []string
		traceparent, path, trace string
		added                    []string
	}{
		{"Env", s.Env, attempt, fvAttempt, fvTrace, []string{"TRACEPARENT=" + attempt, "FV_PATH=" + fvAttempt, "FV_TRACE=" + fvTrace, "FV_STATE=/state", "HOME=/home/svc"}},
		{"LinkEnv", func(b []string) []string { return s.LinkEnv(b, fvRequestID) }, linked, "", "", []string{"TRACEPARENT=" + linked, "FV_STATE=/state", "HOME=/home/svc"}},
	} {
		got := tt.got(nil)
		if v := lookup(got, "PATH"); len(v) != 1 || v[0] != "/svc/bin" {
			t.Errorf("%s(nil): PATH=%q, want this process's [\"/svc/bin\"]", tt.name, v)
		}
		if !slices.Equal(got[len(got)-len(tt.added):], tt.added) {
			t.Errorf("%s(nil) ends %q, want %q", tt.name, got[len(got)-len(tt.added):], tt.added)
		}
		if err := checkFrame(got, tt.traceparent, tt.path, tt.trace); err != nil {
			t.Errorf("%s(nil): %v", tt.name, err)
		}
		if got := tt.got([]string{}); !slices.Equal(got, tt.added) {
			t.Errorf("%s([]string{}) = %q, want only the added names %q", tt.name, got, tt.added)
		}
	}
}

// TestEnvState holds a state variable this process does not have to absent, and one it has set empty to present.
func TestEnvState(t *testing.T) {
	leaf(fvTrace, fvAttempt).set(t)
	for _, name := range []string{"FV_STATE", "HOME"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_STATE_HOME", "")
	s, _ := fibervisor.Read()
	got := s.Env([]string{"FV_STATE=/stale", "HOME=/old"})
	if v := lookup(got, "FV_STATE"); len(v) != 0 {
		t.Errorf("FV_STATE=%q, want it absent", v)
	}
	if v := lookup(got, "HOME"); len(v) != 0 {
		t.Errorf("HOME=%q, want it absent", v)
	}
	if v := lookup(got, "XDG_STATE_HOME"); len(v) != 1 || v[0] != "" {
		t.Errorf("XDG_STATE_HOME=%q, want [\"\"]", v)
	}
}

func TestEnvViolation(t *testing.T) {
	attempt := header(fvTrace, derive(fvTrace, fvAttempt))
	request := fvAttempt + "/q" + fvRequestID.String()
	for _, bad := range []struct {
		name                     string
		env                      []string
		traceparent, path, trace string
	}{
		{"a per-request FV_PATH", []string{"TRACEPARENT=" + attempt, "FV_PATH=" + request, "FV_TRACE=" + fvTrace}, attempt, fvAttempt, fvTrace},
		{"a set FV_CRASH leaking through", []string{"TRACEPARENT=" + attempt, "FV_PATH=" + fvAttempt, "FV_TRACE=" + fvTrace, "FV_CRASH=1"}, attempt, fvAttempt, fvTrace},
		{"the zero Supervision leaving a stale TRACEPARENT", []string{"PATH=/bin", "TRACEPARENT=" + staleParent}, "", "", ""},
		{"a stale TRACEPARENT kept beside the new one", []string{"TRACEPARENT=" + staleParent, "TRACEPARENT=" + attempt, "FV_PATH=" + fvAttempt, "FV_TRACE=" + fvTrace}, attempt, fvAttempt, fvTrace},
		{"LinkEnv keeping FV_PATH", []string{"TRACEPARENT=" + attempt, "FV_PATH=" + fvAttempt}, attempt, "", ""},
		{"Env with no FV_TRACE", []string{"TRACEPARENT=" + attempt, "FV_PATH=" + fvAttempt}, attempt, fvAttempt, fvTrace},
	} {
		if checkFrame(bad.env, bad.traceparent, bad.path, bad.trace) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
}
