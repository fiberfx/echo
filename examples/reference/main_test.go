// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v7/fibervisor"
	"github.com/fiberfx/mx-echo/v7/observe"
	"github.com/fiberfx/mx-echo/v7/observe/stdhttp"
	"github.com/fiberfx/mx-echo/v7/reqid"
	"github.com/fiberfx/mx-echo/v7/server"
)

const (
	childVar = "REFERENCE_CHILD" // "1" makes the test binary the service
	addrVar  = "REFERENCE_ADDR"  // the address the child listens on

	// wait bounds every step that waits for the child. It is generous: the box that runs this has one CPU.
	wait = 60 * time.Second

	fvTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	fvPath  = "q00000000000100000007/run/a1"

	savePattern = "PUT /api/responses/{key}"
	readPattern = "GET /api/responses/{key}"
	encodedKey  = "/api/responses/premises%3Aa%2Fb%20c"
)

// TestMain makes the test binary the service itself when REFERENCE_CHILD is 1. TestReferenceService starts it
// that way, so the child is the code under test, with main's own signal handling, and not a second build.
func TestMain(m *testing.M) {
	if os.Getenv(childVar) == "1" {
		os.Exit(serve(os.Getenv(addrVar)))
	}
	os.Exit(m.Run())
}

// fvNode is the node id fv derives from fvTrace and fvPath (fiberfx/fv/src/id.rs in jonnify; package
// fibervisor's tests pin it to fv's own vectors).
func fvNode() string {
	sum := sha256.Sum256([]byte("fv.node/1\x00" + fvTrace + "\x00" + fvPath))
	return hex.EncodeToString(sum[:8])
}

// fvEnv is the three variables fv's door gives the leaf of the attempt fvPath of fvTrace: the span is the
// attempt's node id.
func fvEnv() []string {
	return []string{
		"TRACEPARENT=00-" + fvTrace + "-" + fvNode() + "-01",
		"FV_PATH=" + fvPath,
		"FV_TRACE=" + fvTrace,
	}
}

// output collects what a child writes to one stream, and tells when its first line is complete.
type output struct {
	mu    sync.Mutex
	text  []byte
	once  sync.Once
	first chan struct{} // closed when the first newline arrives
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.text = append(o.text, p...)
	if bytes.IndexByte(p, '\n') >= 0 {
		o.once.Do(func() { close(o.first) })
	}
	return len(p), nil
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.text)
}

// child is the service running as a process of its own.
type child struct {
	cmd    *exec.Cmd
	stdout *output
	stderr *output
	exited chan struct{} // closed when the process has exited and both streams are complete
}

// start runs the test binary as the service on addr, with env added to an environment that holds none of
// fv's variables. The child is killed when the test ends, if it is still running.
func start(t *testing.T, addr string, env []string) *child {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stop is a SIGTERM, which this test cannot send on Windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &child{
		cmd:    exec.Command(exe),
		stdout: &output{first: make(chan struct{})},
		stderr: &output{first: make(chan struct{})},
		exited: make(chan struct{}),
	}
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "TRACEPARENT", "FV_PATH", "FV_TRACE", childVar, addrVar:
		default:
			c.cmd.Env = append(c.cmd.Env, kv)
		}
	}
	c.cmd.Env = append(c.cmd.Env, childVar+"=1", addrVar+"="+addr)
	c.cmd.Env = append(c.cmd.Env, env...)
	c.cmd.Stdout, c.cmd.Stderr = c.stdout, c.stderr
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = c.cmd.Wait()
		close(c.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-c.exited:
		default:
			_ = c.cmd.Process.Kill()
			<-c.exited
		}
	})
	return c
}

// address waits for the child's first line on standard output: the address it bound.
func (c *child) address(t *testing.T) string {
	t.Helper()
	select {
	case <-c.stdout.first:
	case <-c.exited:
		t.Fatalf("the child ended before it printed an address: %v\nstdout: %s\nstderr: %s",
			c.cmd.ProcessState, c.stdout, c.stderr)
	case <-time.After(wait):
		t.Fatalf("no address from the child in %v", wait)
	}
	addr, _, _ := strings.Cut(c.stdout.String(), "\n")
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatalf("the child's first line %q is not an address: %v", addr, err)
	}
	return addr
}

// stop asks the child to stop as a supervisor does, and returns its exit status.
func (c *child) stop(t *testing.T) int {
	t.Helper()
	if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	return c.status(t)
}

// status waits for the child to exit and returns its exit status.
func (c *child) status(t *testing.T) int {
	t.Helper()
	select {
	case <-c.exited:
	case <-time.After(wait):
		t.Fatalf("the child did not exit in %v\nstdout: %s\nstderr: %s", wait, c.stdout, c.stderr)
	}
	return c.cmd.ProcessState.ExitCode()
}

// line is one observation as the service writes it. Trace and AttemptNode are written under fv only.
type line struct {
	ID          string `json:"id"`
	Link        string `json:"link"`
	Attempt     string `json:"attempt"`
	Build       string `json:"build"`
	Method      string `json:"method"`
	Admission   string `json:"admission"`
	Pattern     string `json:"pattern"`
	Handler     string `json:"handler"`
	Kind        string `json:"kind"`
	Status      int    `json:"status"`
	Committed   bool   `json:"committed"`
	Panicked    bool   `json:"panicked"`
	Trace       string `json:"trace"`
	AttemptNode string `json:"attempt_node"`
}

// parse reads what the service wrote to standard error: every line is one observation and nothing else.
func parse(stderr string) ([]line, error) {
	if stderr == "" {
		return nil, nil
	}
	if !strings.HasSuffix(stderr, "\n") {
		return nil, fmt.Errorf("the last line is not complete: %q", stderr)
	}
	var lines []line
	for i, text := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		dec := json.NewDecoder(strings.NewReader(text))
		dec.DisallowUnknownFields()
		var l line
		if err := dec.Decode(&l); err != nil {
			return nil, fmt.Errorf("line %d is not an observation: %v: %q", i+1, err, text)
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// owed is what one request owes in its observation.
type owed struct {
	method    string
	admission string
	pattern   string
	kind      string
	status    int
}

// checkLines returns an error unless lines are the observations of the requests in wants, in order: all made
// in the one attempt, each with a name of its own, none with a handler, a panic or an empty field.
func checkLines(lines []line, attempt string, wants []owed) error {
	if len(lines) != len(wants) {
		return fmt.Errorf("%d observations, want %d", len(lines), len(wants))
	}
	names := map[string]bool{}
	for i, l := range lines {
		if l.Attempt != attempt {
			return fmt.Errorf("observation %d states the attempt %q, want %q", i+1, l.Attempt, attempt)
		}
		if id, ok := reqid.Parse(l.ID); !ok || id.IsZero() || names[l.ID] {
			return fmt.Errorf("observation %d is named %q, which is not a name of its own", i+1, l.ID)
		}
		names[l.ID] = true
		if got := (owed{l.Method, l.Admission, l.Pattern, l.Kind, l.Status}); got != wants[i] {
			return fmt.Errorf("observation %d is %+v, want %+v", i+1, got, wants[i])
		}
		if l.Handler != observe.None || l.Link != observe.None || l.Build == "" || l.Panicked {
			return fmt.Errorf("observation %d has handler %q, link %q, build %q, panicked %v",
				i+1, l.Handler, l.Link, l.Build, l.Panicked)
		}
	}
	return nil
}

// checkFollowed returns an error unless every line carries trace and node, the attempt's node id that `fv show`
// takes; "" for both is a service not under fv, whose lines carry neither key.
func checkFollowed(lines []line, trace, node string) error {
	for i, l := range lines {
		if l.Trace != trace || l.AttemptNode != node {
			return fmt.Errorf("observation %d has the trace %q and the attempt node %q, want %q and %q",
				i+1, l.Trace, l.AttemptNode, trace, node)
		}
	}
	return nil
}

// fourRequests is what the four requests of TestReferenceService owe: a saved key, an encoded key, a foreign
// Host and an unknown path.
var fourRequests = []owed{
	{http.MethodPut, "routed", savePattern, "ok", http.StatusNoContent},
	{http.MethodPut, "routed", savePattern, "ok", http.StatusNoContent},
	{http.MethodPut, "unknown", observe.None, "admission-refusal", http.StatusForbidden},
	{http.MethodGet, "unknown", observe.None, "admission-refusal", http.StatusNotFound},
}

// send makes one request on a connection of its own and returns the status and the body.
func send(t *testing.T, method, url, host, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: wait}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(answer)
}

func TestReferenceService(t *testing.T) {
	t.Run("four requests in one attempt, then a drained stop", func(t *testing.T) {
		c := start(t, "127.0.0.1:0", fvEnv())
		base := "http://" + c.address(t)
		for i, r := range []struct{ method, path, host, body string }{
			{http.MethodPut, "/api/responses/premises", "", "v1"},
			{http.MethodPut, encodedKey, "", "v2"},
			{http.MethodPut, "/api/responses/premises", "example.com", "v3"},
			{http.MethodGet, "/api/unknown", "", ""},
		} {
			if status, _ := send(t, r.method, base+r.path, r.host, r.body); status != fourRequests[i].status {
				t.Errorf("request %d was answered %d, want %d", i+1, status, fourRequests[i].status)
			}
		}
		if status := c.stop(t); status != exitDrained {
			t.Errorf("exit status %d after a drained stop, want %d\nstdout: %s", status, exitDrained, c.stdout)
		}
		lines, err := parse(c.stderr.String())
		if err != nil {
			t.Fatal(err)
		}
		if err := checkLines(lines, fvPath, fourRequests); err != nil {
			t.Errorf("%v\nstderr: %s", err, c.stderr)
		}
	})

	t.Run("a taken port is a failed start", func(t *testing.T) {
		held, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		c := start(t, held.Addr().String(), fvEnv())
		if status := c.status(t); status != exitFailed {
			t.Errorf("exit status %d when the port is taken, want %d", status, exitFailed)
		}
		if text := c.stderr.String(); text != "" {
			t.Errorf("a service that never served wrote to standard error: %s", text)
		}
		if text := c.stdout.String(); !strings.Contains(text, "the service could not serve") {
			t.Errorf("standard output does not say why the service ended: %q", text)
		}
	})

	t.Run("with no fv variables the attempt is none", func(t *testing.T) {
		c := start(t, "127.0.0.1:0", nil)
		status, answer := send(t, http.MethodGet, "http://"+c.address(t)+"/api/healthz", "", "")
		if status != http.StatusOK || answer != `{"ok":true}` {
			t.Errorf("the health route answered %d %q", status, answer)
		}
		if status := c.stop(t); status != exitDrained {
			t.Errorf("exit status %d after a drained stop, want %d\nstdout: %s", status, exitDrained, c.stdout)
		}
		lines, err := parse(c.stderr.String())
		if err != nil {
			t.Fatal(err)
		}
		want := []owed{{http.MethodGet, "routed", "GET /api/healthz", "ok", http.StatusOK}}
		if err := checkLines(lines, observe.None, want); err != nil {
			t.Errorf("%v\nstderr: %s", err, c.stderr)
		}
		if err := checkFollowed(lines, "", ""); err != nil {
			t.Errorf("%v\nstderr: %s", err, c.stderr)
		}
	})

	// fv's holder forwards an interrupt as it got it, as it does SIGTERM: the service drains on either.
	t.Run("under fv a line carries the attempt's node, and an interrupt drains", func(t *testing.T) {
		c := start(t, "127.0.0.1:0", fvEnv())
		if status, _ := send(t, http.MethodGet, "http://"+c.address(t)+"/api/healthz", "", ""); status != http.StatusOK {
			t.Errorf("the health route answered %d", status)
		}
		if err := c.cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		if status := c.status(t); status != exitDrained {
			t.Errorf("exit status %d after an interrupt, want %d\nstdout: %s", status, exitDrained, c.stdout)
		}
		lines, err := parse(c.stderr.String())
		if err != nil {
			t.Fatal(err)
		}
		want := []owed{{http.MethodGet, "routed", "GET /api/healthz", "ok", http.StatusOK}}
		if err := checkLines(lines, fvPath, want); err != nil {
			t.Errorf("%v\nstderr: %s", err, c.stderr)
		}
		if err := checkFollowed(lines, fvTrace, fvNode()); err != nil {
			t.Errorf("%v\nstderr: %s", err, c.stderr)
		}
	})

	// A save whose body never arrives is still running when the grace expires. The service's "100 Continue"
	// tells the test that the handler is reading the body, so the stop is sent with the request in flight.
	t.Run("a request past the grace is a forced stop", func(t *testing.T) {
		c := start(t, "127.0.0.1:0", fvEnv())
		conn, err := net.DialTimeout("tcp", c.address(t), wait)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(wait)); err != nil {
			t.Fatal(err)
		}
		const head = "PUT /api/responses/slow HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\n"
		if _, err := io.WriteString(conn, head); err != nil {
			t.Fatal(err)
		}
		if interim, err := bufio.NewReader(conn).ReadString('\n'); err != nil || !strings.HasPrefix(interim, "HTTP/1.1 100") {
			t.Fatalf("the service did not ask for the body: %q, %v", interim, err)
		}
		asked := time.Now()
		if status := c.stop(t); status != exitForced {
			t.Errorf("exit status %d after a forced stop, want %d\nstdout: %s", status, exitForced, c.stdout)
		}
		if took := time.Since(asked); took < grace {
			t.Errorf("the service ended %v after the stop was asked, before its grace of %v expired", took, grace)
		}
	})
}

func TestReferenceServiceViolation(t *testing.T) {
	good := func() []line {
		var lines []line
		for i, w := range fourRequests {
			lines = append(lines, line{
				ID: reqid.New(1700000000000, uint32(i+1)).String(), Link: observe.None, Attempt: fvPath,
				Build: observe.None, Method: w.method, Admission: w.admission, Pattern: w.pattern,
				Handler: observe.None, Kind: w.kind, Status: w.status, Committed: true,
			})
		}
		return lines
	}
	if err := checkLines(good(), fvPath, fourRequests); err != nil {
		t.Fatalf("the four good lines do not pass: %v", err)
	}
	for name, change := range map[string]func([]line) []line{
		"the concrete path as the pattern": func(l []line) []line { l[1].Pattern = encodedKey; return l },
		"a second attempt":                 func(l []line) []line { l[3].Attempt = "q00000000000100000007/run/a2"; return l },
		"no attempt":                       func(l []line) []line { l[0].Attempt = observe.None; return l },
		"one name for two requests":        func(l []line) []line { l[2].ID = l[0].ID; return l },
		"a request with no name":           func(l []line) []line { l[0].ID = observe.None; return l },
		"a handler for a refused request":  func(l []line) []line { l[2].Handler = savePattern; return l },
		"a refusal called business":        func(l []line) []line { l[3].Kind = "business-refusal"; return l },
		"an empty build":                   func(l []line) []line { l[0].Build = ""; return l },
		"three lines":                      func(l []line) []line { return l[:3] },
	} {
		if checkLines(change(good()), fvPath, fourRequests) == nil {
			t.Errorf("%s passed the check", name)
		}
	}
	followed := func() []line {
		lines := good()
		for i := range lines {
			lines[i].Trace, lines[i].AttemptNode = fvTrace, fvNode()
		}
		return lines
	}
	if err := checkFollowed(followed(), fvTrace, fvNode()); err != nil {
		t.Fatalf("the four followed lines do not pass: %v", err)
	}
	if err := checkFollowed(good(), "", ""); err != nil {
		t.Fatalf("the four lines of a service not under fv do not pass: %v", err)
	}
	for name, bad := range map[string]struct {
		lines       []line
		trace, node string
	}{
		"a line with no attempt node":      {func() []line { l := followed(); l[2].AttemptNode = ""; return l }(), fvTrace, fvNode()},
		"the path as the attempt node":     {func() []line { l := followed(); l[0].AttemptNode = fvPath; return l }(), fvTrace, fvNode()},
		"no keys under fv":                 {good(), fvTrace, fvNode()},
		"keys from a service not under fv": {followed(), "", ""},
	} {
		if checkFollowed(bad.lines, bad.trace, bad.node) == nil {
			t.Errorf("%s passed the check", name)
		}
	}
	if _, err := parse(`{"id":"0000000000000000000a"}` + "\n" + "not an observation\n"); err == nil {
		t.Error("a line that is not JSON passed as an observation")
	}
	if _, err := parse(`{"level":"ERROR","msg":"the service could not serve"}` + "\n"); err == nil {
		t.Error("a log line passed as an observation")
	}
}

// TestService runs the routes in this process: what each answers, and the kind its observation states.
func TestService(t *testing.T) {
	var stderr bytes.Buffer
	h := stdhttp.Wrap(newService(), observe.Observer{Sink: fibervisor.Lines(&stderr)})
	steps := []struct {
		method string
		target string
		host   string
		body   string
		answer string // the body owed, or "" when the test does not read it
		owed   owed
	}{
		{http.MethodGet, "/api/healthz", "127.0.0.1:8080", "", `{"ok":true}`,
			owed{http.MethodGet, "routed", "GET /api/healthz", "ok", http.StatusOK}},
		{http.MethodPut, encodedKey, "localhost", "v1", "",
			owed{http.MethodPut, "routed", savePattern, "ok", http.StatusNoContent}},
		{http.MethodGet, encodedKey, "localhost:8080", "", "v1",
			owed{http.MethodGet, "routed", readPattern, "ok", http.StatusOK}},
		{http.MethodGet, "/api/responses/absent", "127.0.0.1", "", `{"reason":"no value is saved under this key"}`,
			owed{http.MethodGet, "routed", readPattern, "business-refusal", http.StatusNotFound}},
		{http.MethodPut, "/api/responses/large", "127.0.0.1", strings.Repeat("x", maxValue+1), `{"reason":"the value is larger than 1 MiB"}`,
			owed{http.MethodPut, "routed", savePattern, "business-refusal", http.StatusRequestEntityTooLarge}},
		{http.MethodPut, "/api/responses/premises", "example.com", "v2", `{"reason":"host not allowed"}`,
			owed{http.MethodPut, "unknown", observe.None, "admission-refusal", http.StatusForbidden}},
		{http.MethodGet, "/api/unknown", "127.0.0.1", "", "",
			owed{http.MethodGet, "unknown", observe.None, "admission-refusal", http.StatusNotFound}},
		{http.MethodDelete, "/api/responses/premises", "127.0.0.1", "", "",
			owed{http.MethodDelete, "unknown", observe.None, "admission-refusal", http.StatusMethodNotAllowed}},
	}
	var wants []owed
	for i, s := range steps {
		req := httptest.NewRequest(s.method, s.target, strings.NewReader(s.body))
		req.Host = s.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != s.owed.status {
			t.Errorf("step %d was answered %d, want %d", i+1, rec.Code, s.owed.status)
		}
		if answer := strings.TrimSpace(rec.Body.String()); s.answer != "" && answer != s.answer {
			t.Errorf("step %d was answered %q, want %q", i+1, answer, s.answer)
		}
		wants = append(wants, s.owed)
	}
	lines, err := parse(stderr.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := checkLines(lines, observe.None, wants); err != nil {
		t.Errorf("%v\nstderr: %s", err, stderr.String())
	}
}

// checkExit returns an error unless status is what a supervisor is owed for how Start ended: 0 only after a
// stop that was asked for and drained, 1 when Start returned an error, 2 after a forced stop.
func checkExit(err error, stop server.StopReport, status int) error {
	drained := err == nil && stop.Requested && !stop.Forced
	switch {
	case drained && status != 0:
		return fmt.Errorf("exit %d after a drained stop", status)
	case !drained && status == 0:
		return fmt.Errorf("exit 0 for an end that was not a drained stop: error %v, report %+v", err, stop)
	case err != nil && status != 1:
		return fmt.Errorf("exit %d for a failed start, want 1", status)
	case err == nil && stop.Forced && status != 2:
		return fmt.Errorf("exit %d for a forced stop, want 2", status)
	}
	return nil
}

func TestExitStatus(t *testing.T) {
	errListen := errors.New("listen tcp 127.0.0.1:8080: bind: address already in use")
	for _, c := range []struct {
		err  error
		stop server.StopReport
	}{
		{nil, server.StopReport{Requested: true}},
		{nil, server.StopReport{Requested: true, Forced: true}},
		{nil, server.StopReport{Requested: true, Forced: true, Abandoned: 2}},
		{nil, server.StopReport{}}, // the server ended and no stop was asked for
		{errListen, server.StopReport{}},
		{errListen, server.StopReport{Requested: true}},
		{errListen, server.StopReport{Requested: true, Forced: true}},
	} {
		if err := checkExit(c.err, c.stop, exitStatus(c.err, c.stop)); err != nil {
			t.Error(err)
		}
	}
}

func TestExitStatusViolation(t *testing.T) {
	if checkExit(errors.New("listen"), server.StopReport{}, 0) == nil {
		t.Error("exit 0 on a failed start passed the check")
	}
	if checkExit(nil, server.StopReport{Requested: true, Forced: true}, 0) == nil {
		t.Error("exit 0 after a forced stop passed the check")
	}
	if checkExit(nil, server.StopReport{}, 0) == nil {
		t.Error("exit 0 for a server that ended unasked passed the check")
	}
	if checkExit(nil, server.StopReport{Requested: true}, 1) == nil {
		t.Error("exit 1 after a drained stop passed the check")
	}
}

func TestCommand(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"-help"}, {"-nope"}, {"-addr"}} {
		var stderr bytes.Buffer
		status := command(args, &stderr, func(addr string) int {
			t.Errorf("%q served on %q", args, addr)
			return exitDrained
		})
		if err := checkCommand(status, exitFailed); err != nil {
			t.Errorf("%q: %v", args, err)
		}
		if !strings.Contains(stderr.String(), "-addr") {
			t.Errorf("%q printed %q, want the usage", args, stderr.String())
		}
	}
	// A command line that parses hands its address to serve, and returns serve's status.
	for _, c := range []struct {
		args []string
		addr string
	}{
		{nil, "127.0.0.1:8080"},
		{[]string{"-addr", "127.0.0.1:0"}, "127.0.0.1:0"},
	} {
		var served string
		status := command(c.args, io.Discard, func(addr string) int {
			served = addr
			return exitForced
		})
		if err := checkCommand(status, exitForced); err != nil {
			t.Errorf("%q: %v", c.args, err)
		}
		if served != c.addr {
			t.Errorf("%q served on %q, want %q", c.args, served, c.addr)
		}
	}
}

func TestCommandViolation(t *testing.T) {
	// What flag.ExitOnError gave: 0 for -h, as after a drained stop, and 2 for a bad flag, as after a forced one.
	if checkCommand(exitDrained, exitFailed) == nil {
		t.Error("exit 0 for -h passed the check")
	}
	if checkCommand(exitForced, exitFailed) == nil {
		t.Error("exit 2 for a bad flag passed the check")
	}
}

// checkCommand returns an error unless command's status is want.
func checkCommand(status, want int) error {
	if status != want {
		return fmt.Errorf("exit %d, want %d", status, want)
	}
	return nil
}
