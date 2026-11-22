package mxdb

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestClient(t *testing.T, f *fakeServer) *Client {
	t.Helper()
	c := New(Config{Addr: f.ln.Addr().String(), Timeout: 3 * time.Second})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func mustPut(t *testing.T, c *Client, db, key, val string) uint64 {
	t.Helper()
	res, err := c.Update(ctx(t), Update{Ops: []Op{Put(db, key, []byte(val))}})
	if err != nil || res.Outcome != OutcomeOk {
		t.Fatalf("put %s/%s: %v, %+v", db, key, err, res)
	}
	return res.CommitSeq
}

func mustGet(t *testing.T, c *Client, db, key string, at uint64) (string, bool) {
	t.Helper()
	v, found, err := c.Get(ctx(t), db, key, at)
	if err != nil {
		t.Fatalf("get %s/%s: %v", db, key, err)
	}
	return string(v), found
}

func TestHelloOncePerConnection(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	for i := 0; i < 3; i++ {
		if err := c.Health(ctx(t)); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hellos) != 1 || f.hellos[0] != Version || f.conns != 1 {
		t.Fatalf("hellos %v, conns %d: want one hello of version %d on one connection", f.hellos, f.conns, Version)
	}
}

func TestUpdateWireGolden(t *testing.T) {
	body, err := encodeUpdate(Update{
		Pre: []Cond{Absent("a", "k1")},
		Ops: []Op{Put("a", "k1", []byte("v"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "02" + "00000001" + "0000000161" + // update, one db "a"
		"00000001" + "02" + "00000000" + "000000026b31" + // one pre: absent, db 0, "k1"
		"00000001" + "01" + "00000000" + "000000026b31" + "0000000176" // one op: put, db 0, "k1", "v"
	if got := hex.EncodeToString(body); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
	if got := hex.EncodeToString(appendFrame(body))[:8]; got != "0000002d" {
		t.Fatalf("frame length prefix %s, want 0000002d", got)
	}
}

func TestUpdateOkAndGet(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k1", []byte("v1")), Put("a", "k2", []byte("v2"))}})
	if err != nil || res.Outcome != OutcomeOk || res.CommitSeq != 1 {
		t.Fatalf("update: %v, %+v", err, res)
	}
	if v, ok := mustGet(t, c, "a", "k2", 0); !ok || v != "v2" {
		t.Fatalf("get k2 = %q, %v", v, ok)
	}
	if _, ok := mustGet(t, c, "a", "nope", 0); ok {
		t.Fatal("get of a missing key found it")
	}
	seq, err := c.Snapshot(ctx(t))
	if err != nil || seq != 1 {
		t.Fatalf("snapshot = %d, %v", seq, err)
	}
}

func TestGetAtCommitSequence(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	s1 := mustPut(t, c, "a", "k", "old")
	mustPut(t, c, "a", "k", "new")
	if v, _ := mustGet(t, c, "a", "k", s1); v != "old" {
		t.Fatalf("get at %d = %q, want old", s1, v)
	}
	if v, _ := mustGet(t, c, "a", "k", 0); v != "new" {
		t.Fatalf("get latest = %q, want new", v)
	}
	if _, _, err := c.Get(ctx(t), "a", "k", 99); !errors.Is(err, ErrRejected) {
		t.Fatalf("get at an unknown sequence: %v, want Rejected", err)
	}
}

func TestPreconditions(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	mustPut(t, c, "a", "k", "v")

	// Absent on an existing key aborts and commits nothing.
	res, err := c.Update(ctx(t), Update{Pre: []Cond{Absent("a", "k")}, Ops: []Op{Put("a", "other", []byte("x"))}})
	var ab *AbortedError
	if !errors.As(err, &ab) || !errors.Is(err, ErrAborted) || res.Outcome != OutcomeAborted || ab.FailedPre != 0 || res.FailedPre != 0 {
		t.Fatalf("absent on existing: %v, %+v", err, res)
	}
	if _, ok := mustGet(t, c, "a", "other", 0); ok {
		t.Fatal("an aborted update committed")
	}
	if seq, _ := c.Snapshot(ctx(t)); seq != 1 {
		t.Fatalf("commit sequence %d after an abort, want 1", seq)
	}

	// The second precondition fails: its index is reported.
	res, err = c.Update(ctx(t), Update{
		Pre: []Cond{Holds("a", "k", []byte("v")), Holds("a", "k", []byte("stale"))},
		Ops: []Op{Put("a", "k", []byte("w"))},
	})
	if !errors.Is(err, ErrAborted) || res.FailedPre != 1 {
		t.Fatalf("holds with a stale value: %v, %+v, want Aborted at 1", err, res)
	}

	// Both hold: the update commits.
	res, err = c.Update(ctx(t), Update{
		Pre: []Cond{Holds("a", "k", []byte("v")), Absent("a", "fresh")},
		Ops: []Op{Put("a", "fresh", []byte("f"))},
	})
	if err != nil || res.Outcome != OutcomeOk || res.CommitSeq != 2 {
		t.Fatalf("holding preconditions: %v, %+v", err, res)
	}
}

func TestDeleteAndDeleteRange(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	for _, k := range []string{"k1", "k2", "k3", "k4", "k5"} {
		mustPut(t, c, "a", k, "v")
	}
	_, err := c.Update(ctx(t), Update{Ops: []Op{Delete("a", "k1"), DeleteRange("a", "k2", "k4")}})
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]bool{"k1": false, "k2": false, "k3": false, "k4": true, "k5": true} {
		if _, ok := mustGet(t, c, "a", k, 0); ok != want {
			t.Fatalf("%s present = %v, want %v", k, ok, want)
		}
	}
}

func TestUpdateDeclaresEveryDatabase(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	_, err := c.Update(ctx(t), Update{
		Pre: []Cond{Absent("b", "k")},
		Ops: []Op{Put("a", "k", []byte("1")), Put("b", "k", []byte("2")), Put("a", "j", []byte("3"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	var first []byte
	f.with(func() { first = f.updates[0] })
	d := newDec(first)
	d.u8()
	if n := d.u32(); n != 2 {
		t.Fatalf("declared %d databases, want 2 (b, a once each)", n)
	}
	if v, _ := mustGet(t, c, "b", "k", 0); v != "2" {
		t.Fatalf("b/k = %q", v)
	}
}

func TestUpdateIsValidatedBeforeSending(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	for name, u := range map[string]Update{
		"no ops":       {},
		"empty db":     {Ops: []Op{Put("", "k", nil)}},
		"empty key":    {Ops: []Op{Delete("a", "")}},
		"empty pre db": {Pre: []Cond{Absent("", "k")}, Ops: []Op{Delete("a", "k")}},
		"zero op":      {Ops: []Op{{}}},
	} {
		res, err := c.Update(ctx(t), u)
		if !errors.Is(err, ErrNotSent) || errors.Is(err, ErrOutcomeUnknown) || res.Outcome != OutcomeNone {
			t.Fatalf("%s: %v, %+v, want NotSent", name, err, res)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conns != 0 {
		t.Fatalf("%d connections for requests that were never valid", f.conns)
	}
}

func TestOversizeRequestIsNotSent(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", make([]byte, MaxFrame))}})
	if !errors.Is(err, ErrNotSent) || !errors.Is(err, ErrTooLarge) || res.Outcome != OutcomeNone {
		t.Fatalf("oversize: %v, %+v", err, res)
	}
}

// The four outcomes are four distinct errors and four distinct Outcome values.
func TestOutcomeClassesDistinct(t *testing.T) {
	sentinels := []error{ErrAborted, ErrRejected, ErrOutcomeUnknown, ErrNotSent}
	cases := []struct {
		name  string
		setup func(f *fakeServer, c *Client)
		u     Update
		want  Outcome
		is    error // nil for Ok
	}{
		{"ok", nil, Update{Ops: []Op{Put("a", "k", []byte("v"))}}, OutcomeOk, nil},
		{"aborted", func(f *fakeServer, c *Client) { mustPut(t, c, "a", "k", "v") },
			Update{Pre: []Cond{Absent("a", "k")}, Ops: []Op{Delete("a", "k")}}, OutcomeAborted, ErrAborted},
		{"rejected", nil, Update{Ops: []Op{Put("readonly", "k", nil)}}, OutcomeRejected, ErrRejected},
		{"unknown, server-reported", func(f *fakeServer, c *Client) {
			f.with(func() { f.force = mkReply(stUnknown, func(e *enc) { e.str("sync failed") }) })
		}, Update{Ops: []Op{Put("a", "k", nil)}}, OutcomeUnknown, ErrOutcomeUnknown},
		{"unknown, connection dropped", func(f *fakeServer, c *Client) { f.with(func() { f.dropBefore = 1 }) },
			Update{Ops: []Op{Put("a", "k", nil)}}, OutcomeUnknown, ErrOutcomeUnknown},
	}
	seen := map[Outcome]string{}
	for _, tc := range cases {
		f := newFake(t)
		c := newTestClient(t, f)
		if tc.setup != nil {
			tc.setup(f, c)
		}
		res, err := c.Update(ctx(t), tc.u)
		if res.Outcome != tc.want {
			t.Errorf("%s: outcome %v, want %v", tc.name, res.Outcome, tc.want)
		}
		if tc.is == nil {
			if err != nil {
				t.Errorf("%s: error %v", tc.name, err)
			}
		} else {
			for _, s := range sentinels {
				if got := errors.Is(err, s); got != (s == tc.is) {
					t.Errorf("%s: errors.Is(%v, %q) = %v", tc.name, err, s, got)
				}
			}
		}
		if prev, dup := seen[res.Outcome]; dup && res.Outcome != OutcomeUnknown {
			t.Errorf("%s and %s share outcome %v", prev, tc.name, res.Outcome)
		}
		seen[res.Outcome] = tc.name
	}
	if len(seen) != 4 {
		t.Fatalf("%d distinct outcomes, want 4: %v", len(seen), seen)
	}
}

func TestRejectedCarriesReason(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	res, err := c.Update(ctx(t), Update{Ops: []Op{Put("readonly", "k", nil)}})
	var rj *RejectedError
	if !errors.As(err, &rj) || !strings.Contains(rj.Reason, "read-only") || res.Reason != rj.Reason || rj.Code != Other || res.Code != Other {
		t.Fatalf("%v, %+v", err, res)
	}
	res, err = c.Update(ctx(t), Update{Ops: []Op{Put("a", strings.Repeat("k", 65), nil)}})
	if !errors.As(err, &rj) || rj.Code != KeyTooLong || res.Code != KeyTooLong || res.Outcome != OutcomeRejected {
		t.Fatalf("a 65-byte key: %v, %+v, want Rejected KeyTooLong", err, res)
	}
	if _, _, err := c.Get(ctx(t), "a", "k", 99); !errors.As(err, &rj) || rj.Code != NoSuchCommit {
		t.Fatalf("get at an unknown commit: %v, want Rejected NoSuchCommit", err)
	}
}

// The reject codes are the wire values 0..8, in the order of the protocol.
func TestRejectCodes(t *testing.T) {
	names := []string{"KeyTooLong", "ValueTooLong", "TransactionTooLarge", "Poisoned", "Io", "LockLost", "NoSuchCommit", "DamagedTail", "Other"}
	codes := []RejectCode{KeyTooLong, ValueTooLong, TransactionTooLarge, Poisoned, Io, LockLost, NoSuchCommit, DamagedTail, Other}
	f := newFake(t)
	c := newTestClient(t, f)
	for i, want := range codes {
		if int(want) != i || want.String() != names[i] {
			t.Fatalf("code %d is %v, want %d %s", want, want, i, names[i])
		}
		f.with(func() { f.force = mkReply(stRejected, func(e *enc) { e.u8(byte(i)); e.str("why " + names[i]) }) })
		res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", nil)}})
		var rj *RejectedError
		if !errors.As(err, &rj) || rj.Code != want || res.Code != want || rj.Reason != "why "+names[i] || res.Outcome != OutcomeRejected {
			t.Fatalf("wire code %d: %v, %+v", i, err, res)
		}
	}
	if RejectCode(200).String() != "RejectCode(200)" {
		t.Fatal(RejectCode(200).String())
	}
}

// Status 4: the update is Rejected and nothing is committed, the error is
// ErrProtocol, and the connection is closed, never returned to the pool. An
// ordinary Rejected keeps its connection.
func TestProtocolErrorIsRejectedAndDropsTheConnection(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	idle := func() (n int) {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.idle)
	}

	f.with(func() { f.force = protoErr("bad frame") })
	res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", []byte("v"))}})
	var pe *ProtocolError
	if res.Outcome != OutcomeRejected || res.Reason != "bad frame" || !errors.As(err, &pe) ||
		!errors.Is(err, ErrProtocol) || !errors.Is(err, ErrRejected) || errors.Is(err, ErrOutcomeUnknown) || errors.Is(err, ErrAborted) {
		t.Fatalf("status 4: %v, %+v, want Rejected and ErrProtocol", err, res)
	}
	if n := idle(); n != 0 {
		t.Fatalf("%d pooled connections after a ProtocolError, want 0", n)
	}
	if seq, err := c.Snapshot(ctx(t)); err != nil || seq != 0 {
		t.Fatalf("snapshot = %d, %v: the refused update committed", seq, err)
	}
	if n := f.connCount(); n != 2 {
		t.Fatalf("%d connections, want 2: the next call dialled afresh", n)
	}

	// Violating contrast: an ordinary Rejected leaves its connection in the pool.
	c2 := newTestClient(t, f)
	f.with(func() { f.force = rejected(Other, "no") })
	if _, err := c2.Update(ctx(t), Update{Ops: []Op{Put("a", "k", nil)}}); !errors.Is(err, ErrRejected) || errors.Is(err, ErrProtocol) {
		t.Fatalf("plain Rejected: %v", err)
	}
	c2.mu.Lock()
	n := len(c2.idle)
	c2.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d pooled connections after an ordinary Rejected, want 1", n)
	}
}

func (f *fakeServer) connCount() (n int) {
	f.with(func() { n = f.conns })
	return n
}

// A connection that drops after the request was sent is OutcomeUnknown, not
// Aborted and not Rejected; the update did not run, and the caller can see it
// by reading back. On a fresh connection nothing is retried.
func TestUpdateDroppedConnIsUnknown(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	f.with(func() { f.dropBefore = 1 })

	res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", []byte("v"))}})
	if res.Outcome != OutcomeUnknown || !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("dropped before apply: %v, %+v, want OutcomeUnknown", err, res)
	}
	if errors.Is(err, ErrAborted) || errors.Is(err, ErrRejected) || errors.Is(err, ErrNotSent) {
		t.Fatalf("OutcomeUnknown is also another outcome: %v", err)
	}
	var un *UnknownError
	if !errors.As(err, &un) || un.Cause == nil {
		t.Fatalf("no cause: %v", err)
	}
	if n := f.updateCount(); n != 1 {
		t.Fatalf("the server saw %d updates: an update on a fresh connection was retried", n)
	}
	if _, ok := mustGet(t, c, "a", "k", 0); ok {
		t.Fatal("read-back found an update the server dropped")
	}
}

func (f *fakeServer) updateCount() (n int) {
	f.with(func() { n = len(f.updates) })
	return n
}

// flakyConn is a connection that lets okWrites writes through (-1: all), then
// fails every Write after sending the first part bytes of it: a pooled socket
// the server has already closed.
type flakyConn struct {
	net.Conn
	mu       sync.Mutex
	okWrites int
	part     int
}

func (c *flakyConn) arm() {
	c.mu.Lock()
	c.okWrites = 0
	c.mu.Unlock()
}

func (c *flakyConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	ok := c.okWrites != 0
	if c.okWrites > 0 {
		c.okWrites--
	}
	part := c.part
	c.mu.Unlock()
	if ok {
		return c.Conn.Write(p)
	}
	n := 0
	if part > 0 {
		n, _ = c.Conn.Write(p[:part])
	}
	return n, errors.New("write: broken pipe")
}

// flakyClient dials the fake; its first connection is sound until armed, and
// every later one passes laterOK writes (the hello) and then fails.
func flakyClient(t *testing.T, f *fakeServer, part, laterOK int) (*Client, func() []*flakyConn) {
	t.Helper()
	var mu sync.Mutex
	var conns []*flakyConn
	c := New(Config{Timeout: 3 * time.Second, Dial: func(cx context.Context) (net.Conn, error) {
		var d net.Dialer
		cn, err := d.DialContext(cx, "tcp", f.ln.Addr().String())
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		fc := &flakyConn{Conn: cn, okWrites: laterOK, part: part}
		if len(conns) == 0 {
			fc.okWrites = -1
		}
		conns = append(conns, fc)
		return fc, nil
	}})
	t.Cleanup(func() { _ = c.Close() })
	return c, func() []*flakyConn {
		mu.Lock()
		defer mu.Unlock()
		return append([]*flakyConn{}, conns...)
	}
}

// A pooled connection that refuses the write (nothing written, or a torn
// frame) is replaced once, for an update too: the server cannot have read a
// whole frame, so it executes the update once.
func TestUpdateRetriedOnceWhenPooledWriteFails(t *testing.T) {
	for _, part := range []int{0, 3} {
		f := newFake(t)
		c, conns := flakyClient(t, f, part, -1)
		if err := c.Health(ctx(t)); err != nil { // leaves a pooled connection
			t.Fatal(err)
		}
		conns()[0].arm()
		res, err := c.Update(ctx(t), Update{Pre: []Cond{Absent("a", "k")}, Ops: []Op{Put("a", "k", []byte("v"))}})
		if err != nil || res.Outcome != OutcomeOk || res.CommitSeq != 1 {
			t.Fatalf("part %d: pooled write failed: %v, %+v, want one retry on a fresh connection and Ok", part, err, res)
		}
		if n := f.updateCount(); n != 1 {
			t.Fatalf("part %d: the server executed %d updates, want 1", part, n)
		}
	}
}

// Violating: the fresh connection fails its write too. One retry only; the
// request was never completely sent, so nothing was executed.
func TestUpdateWriteFailureIsRetriedOnlyOnce(t *testing.T) {
	for _, part := range []int{0, 3} {
		f := newFake(t)
		c, conns := flakyClient(t, f, part, 1) // later connections: the hello passes, the update write fails
		if err := c.Health(ctx(t)); err != nil {
			t.Fatal(err)
		}
		conns()[0].arm()
		res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", []byte("v"))}})
		want := OutcomeNone
		if part > 0 {
			want = OutcomeUnknown // bytes left the client: unknown, though the server cannot execute a torn frame
		}
		if res.Outcome != want || err == nil || errors.Is(err, ErrNotSent) != (part == 0) {
			t.Fatalf("part %d: %v, %+v, want outcome %v", part, err, res, want)
		}
		if n := len(conns()); n != 2 {
			t.Fatalf("part %d: %d connections, want 2: the update was tried more than twice", part, n)
		}
		if n := f.updateCount(); n != 0 {
			t.Fatalf("part %d: the server executed %d updates", part, n)
		}
	}
}

// The server reads the whole frame, then closes without a reply byte: the
// update may have been committed, so it is OutcomeUnknown and is not retried,
// on a pooled connection as on a fresh one.
func TestUpdateWholeFrameWrittenThenClosedIsUnknownAndNotRetried(t *testing.T) {
	for _, pooled := range []bool{true, false} {
		f := newFake(t)
		c := newTestClient(t, f)
		if pooled {
			if err := c.Health(ctx(t)); err != nil {
				t.Fatal(err)
			}
		}
		f.with(func() { f.dropAfter = 1 }) // executes the update, then closes without replying
		res, err := c.Update(ctx(t), Update{Pre: []Cond{Absent("a", "k")}, Ops: []Op{Put("a", "k", []byte("v"))}})
		if res.Outcome != OutcomeUnknown || !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("pooled=%v: %v, %+v, want OutcomeUnknown", pooled, err, res)
		}
		if n := f.updateCount(); n != 1 {
			t.Fatalf("pooled=%v: the server saw %d frames, want exactly 1", pooled, n)
		}
		if v, ok := mustGet(t, c, "a", "k", 0); !ok || v != "v" {
			t.Fatalf("pooled=%v: read-back = %q, %v: the update committed", pooled, v, ok)
		}
		if seq, _ := c.Snapshot(ctx(t)); seq != 1 {
			t.Fatalf("pooled=%v: commit sequence %d, want 1: the update ran twice", pooled, seq)
		}
	}
}

// Once a reply byte has arrived the update may have run: no retry.
func TestUpdateCutAfterFirstReplyByteIsNotRetried(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	if err := c.Health(ctx(t)); err != nil {
		t.Fatal(err)
	}
	f.with(func() { f.truncate = true })
	res, err := c.Update(ctx(t), Update{Pre: []Cond{Absent("a", "k")}, Ops: []Op{Put("a", "k", []byte("v"))}})
	if res.Outcome != OutcomeUnknown || !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("reply cut after two bytes: %v, %+v", err, res)
	}
	if n := f.updateCount(); n != 1 {
		t.Fatalf("the server saw %d update frames, want 1: a retry followed a partial reply", n)
	}
	if v, ok := mustGet(t, c, "a", "k", 0); !ok || v != "v" {
		t.Fatalf("read-back = %q, %v: the update did commit", v, ok)
	}
}

// The drop can also come after the commit: read-back finds it, so a blind
// retry would have applied it twice.
func TestUpdateDroppedAfterCommitIsUnknownAndReadBackFindsIt(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	f.with(func() { f.dropAfter = 1 })

	res, err := c.Update(ctx(t), Update{Pre: []Cond{Absent("a", "k")}, Ops: []Op{Put("a", "k", []byte("v"))}})
	if res.Outcome != OutcomeUnknown || !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("dropped after apply: %v, %+v", err, res)
	}
	if v, ok := mustGet(t, c, "a", "k", 0); !ok || v != "v" {
		t.Fatalf("read-back = %q, %v, want the committed value", v, ok)
	}
	// The retry the caller must not make blindly aborts on its own precondition.
	_, err = c.Update(ctx(t), Update{Pre: []Cond{Absent("a", "k")}, Ops: []Op{Put("a", "k", []byte("v"))}})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("retry after commit: %v, want Aborted", err)
	}
}

func TestMalformedUpdateReplyIsUnknown(t *testing.T) {
	for name, body := range map[string][]byte{
		"ok without a sequence": {stOK},
		"trailing bytes":        append(mkReply(stOK, func(e *enc) { e.u64(1) }), 0),
		"unknown status":        {9},
		"aborted, short":        {stAborted, 0},
	} {
		f := newFake(t)
		c := newTestClient(t, f)
		f.with(func() { f.force = body })
		res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", nil)}})
		if res.Outcome != OutcomeUnknown || !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, ErrProtocol) {
			t.Fatalf("%s: %v, %+v", name, err, res)
		}
	}
}

func TestCancelledOrExpiredMidUpdateIsUnknown(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	stall := make(chan struct{})
	f.with(func() { f.stall = stall })
	t.Cleanup(func() { close(stall) })

	cx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	res, err := c.Update(cx, Update{Ops: []Op{Put("a", "k", nil)}})
	if res.Outcome != OutcomeUnknown || !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline mid-update: %v, %+v", err, res)
	}
}

func TestUnreachableServerIsNotSent(t *testing.T) {
	boom := errors.New("connection refused")
	c := New(Config{Dial: func(context.Context) (net.Conn, error) { return nil, boom }})
	res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", nil)}})
	if !errors.Is(err, ErrNotSent) || !errors.Is(err, boom) || errors.Is(err, ErrOutcomeUnknown) || res.Outcome != OutcomeNone {
		t.Fatalf("%v, %+v, want NotSent", err, res)
	}
	if err := c.Health(ctx(t)); !errors.Is(err, boom) {
		t.Fatalf("health: %v", err)
	}
	_ = c.Close()
	if _, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", nil)}}); !errors.Is(err, ErrNotSent) || !errors.Is(err, ErrClosed) {
		t.Fatalf("closed client: %v", err)
	}
}

func TestCancelledBeforeSendIsNotSent(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	cx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := c.Update(cx, Update{Ops: []Op{Put("a", "k", nil)}})
	if !errors.Is(err, ErrNotSent) || !errors.Is(err, context.Canceled) || res.Outcome != OutcomeNone {
		t.Fatalf("%v, %+v", err, res)
	}
}

func TestHelloVersionMismatchRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		cn, err := ln.Accept()
		if err != nil {
			return
		}
		defer cn.Close()
		if _, err := readFrame(cn); err == nil {
			_, _ = cn.Write(appendFrame(mkReply(stOK, func(e *enc) { e.u32(7) })))
		}
	}()
	c := New(Config{Addr: ln.Addr().String(), Timeout: 2 * time.Second})
	t.Cleanup(func() { _ = c.Close() })
	res, err := c.Update(ctx(t), Update{Ops: []Op{Put("a", "k", nil)}})
	if !errors.Is(err, ErrNotSent) || !errors.Is(err, ErrProtocol) || res.Outcome != OutcomeNone {
		t.Fatalf("%v, %+v", err, res)
	}
}

// Reads are repeatable, so a stale pooled connection is replaced silently.
func TestReadSurvivesServerDroppingPooledConnection(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	mustPut(t, c, "a", "k", "v")
	f.dropConns()
	if v, ok := mustGet(t, c, "a", "k", 0); !ok || v != "v" {
		t.Fatalf("get after the server dropped the connection = %q, %v", v, ok)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conns != 2 {
		t.Fatalf("%d connections, want 2 (one redial)", f.conns)
	}
}

func TestScanPagesContinueAfterKeyExclusive(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	var ops []Op
	for i := 0; i < 25; i++ {
		ops = append(ops, Put("a", fmt.Sprintf("k%02d", i), []byte(fmt.Sprintf("v%02d", i))))
	}
	if _, err := c.Update(ctx(t), Update{Ops: ops}); err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "b", "other", "x") // another database does not leak in

	p1, err := c.Scan(ctx(t), ScanRequest{DB: "a", Limit: 10})
	if err != nil || len(p1.Items) != 10 || !p1.More || p1.Next != "k09" || p1.Items[0].Key != "k00" {
		t.Fatalf("page 1: %v, %+v", err, p1)
	}
	p2, err := c.Scan(ctx(t), ScanRequest{DB: "a", After: p1.Next, Limit: 10, At: p1.At})
	if err != nil || len(p2.Items) != 10 || p2.Items[0].Key != "k10" || !p2.More {
		t.Fatalf("page 2: %v, %+v (after is exclusive)", err, p2)
	}
	p3, err := c.Scan(ctx(t), ScanRequest{DB: "a", After: p2.Next, Limit: 10, At: p2.At})
	if err != nil || len(p3.Items) != 5 || p3.More || p3.Items[4].Key != "k24" || string(p3.Items[4].Value) != "v24" {
		t.Fatalf("page 3: %v, %+v", err, p3)
	}
	if p1.At != p2.At || p2.At != p3.At {
		t.Fatalf("pages read at %d, %d, %d", p1.At, p2.At, p3.At)
	}

	var keys []string
	if err := c.ScanAll(ctx(t), "a", 7, func(it Item) error { keys = append(keys, it.Key); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 25 || keys[0] != "k00" || keys[24] != "k24" {
		t.Fatalf("ScanAll = %v", keys)
	}
	if err := c.ScanAll(ctx(t), "a", 7, func(Item) error { return errors.New("stop") }); err == nil || err.Error() != "stop" {
		t.Fatalf("ScanAll did not stop on the callback's error: %v", err)
	}
	empty, err := c.Scan(ctx(t), ScanRequest{DB: "missing"})
	if err != nil || len(empty.Items) != 0 || empty.More {
		t.Fatalf("scan of an empty db: %v, %+v", err, empty)
	}
}

func TestScanPinnedToSnapshotIgnoresLaterWrites(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	for i := 0; i < 6; i++ {
		mustPut(t, c, "a", fmt.Sprintf("k%d", i), "old")
	}
	p1, err := c.Scan(ctx(t), ScanRequest{DB: "a", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "a", "k4", "new")  // overwrite ahead of the cursor
	mustPut(t, c, "a", "zz", "late") // insert ahead of the cursor

	pinned, err := c.Scan(ctx(t), ScanRequest{DB: "a", After: p1.Next, Limit: 10, At: p1.At})
	if err != nil || len(pinned.Items) != 3 || string(pinned.Items[1].Value) != "old" || pinned.At != p1.At {
		t.Fatalf("pinned: %v, %+v", err, pinned)
	}
	live, err := c.Scan(ctx(t), ScanRequest{DB: "a", After: p1.Next, Limit: 10})
	if err != nil || len(live.Items) != 4 || live.Items[3].Key != "zz" || string(live.Items[1].Value) != "new" {
		t.Fatalf("live: %v, %+v", err, live)
	}
}

func TestScanRefusesACursorThatDoesNotAdvance(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		cn, err := ln.Accept()
		if err != nil {
			return
		}
		defer cn.Close()
		for {
			body, err := readFrame(cn)
			if err != nil {
				return
			}
			var r []byte
			if body[0] == opHello {
				r = mkReply(stOK, func(e *enc) { e.u32(Version) })
			} else { // a scan page that says "more" and repeats the cursor it was given
				r = mkReply(stOK, func(e *enc) {
					e.u64(1)
					e.u32(0)
					e.u8(1)
					e.str("a")
				})
			}
			if _, err := cn.Write(appendFrame(r)); err != nil {
				return
			}
		}
	}()
	c := New(Config{Addr: ln.Addr().String(), Timeout: 2 * time.Second})
	t.Cleanup(func() { _ = c.Close() })
	err = c.ScanAll(ctx(t), "d", 10, func(Item) error { return nil })
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("a cursor that repeats: %v, want a protocol error rather than a loop", err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	const n = 16
	var wg sync.WaitGroup
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprintf("k%02d", i)
			if _, err := c.Update(context.Background(), Update{Pre: []Cond{Absent("a", k)}, Ops: []Op{Put("a", k, []byte(k))}}); err != nil {
				errc <- err
				return
			}
			v, found, err := c.Get(context.Background(), "a", k, 0)
			if err != nil || !found || string(v) != k {
				errc <- fmt.Errorf("get %s = %q, %v, %v", k, v, found, err)
			}
		}(i)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}
	cnt := 0
	if err := c.ScanAll(ctx(t), "a", 5, func(Item) error { cnt++; return nil }); err != nil || cnt != n {
		t.Fatalf("scan found %d of %d: %v", cnt, n, err)
	}
}

func fillScan(t *testing.T, c *Client) {
	t.Helper()
	var ops []Op
	for i := 0; i < 25; i++ {
		ops = append(ops, Put("a", fmt.Sprintf("k%02d", i), []byte("v")))
	}
	if _, err := c.Update(ctx(t), Update{Ops: ops}); err != nil {
		t.Fatal(err)
	}
}

// A pinned scan whose commit is gone says NoSuchCommit; Scan reports it, and
// ScanAll goes on at At 0 from the last key it delivered, once per page.
func TestScanAllRestartsAtLatestWhenPinnedCommitIsGone(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	fillScan(t, c)

	p1, err := c.Scan(ctx(t), ScanRequest{DB: "a", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	f.with(func() { f.forget = 1 << 62 })
	var rj *RejectedError
	if _, err := c.Scan(ctx(t), ScanRequest{DB: "a", After: p1.Next, Limit: 10, At: p1.At}); !errors.As(err, &rj) || rj.Code != NoSuchCommit {
		t.Fatalf("Scan at a forgotten commit: %v, want Rejected NoSuchCommit", err)
	}

	f.with(func() { f.forget = 0 })
	var keys []string
	err = c.ScanAll(ctx(t), "a", 10, func(it Item) error {
		keys = append(keys, it.Key)
		if len(keys) == 10 {
			f.with(func() { f.forget = 1 << 62 }) // the server drops its commits after page 1
		}
		return nil
	})
	if err != nil || len(keys) != 25 {
		t.Fatalf("ScanAll: %v, %d keys, want 25", err, len(keys))
	}
	for i, k := range keys {
		if k != fmt.Sprintf("k%02d", i) {
			t.Fatalf("key %d is %s: a key repeated or was skipped: %v", i, k, keys)
		}
	}
}

// Violating: only NoSuchCommit restarts a scan. Any other rejection ends it.
func TestScanAllDoesNotRestartOnOtherRejections(t *testing.T) {
	f := newFake(t)
	c := newTestClient(t, f)
	fillScan(t, c)
	var keys []string
	err := c.ScanAll(ctx(t), "a", 10, func(it Item) error {
		keys = append(keys, it.Key)
		if len(keys) == 10 {
			f.with(func() { f.scanFail = true })
		}
		return nil
	})
	var rj *RejectedError
	if !errors.As(err, &rj) || rj.Code != Io || len(keys) != 10 {
		t.Fatalf("ScanAll: %v, %d keys, want Rejected Io after 10", err, len(keys))
	}
	f.with(func() {
		if f.scans != 2 {
			t.Errorf("%d scans, want 2: the failed page was retried", f.scans)
		}
	})
}
