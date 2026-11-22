// SPDX-License-Identifier: MIT

package reqid_test

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

// testClock is a Clock that moves only when told to, and counts its readings.
type testClock struct{ ms, reads atomic.Uint64 }

func (c *testClock) NowMs() uint64 {
	c.reads.Add(1)
	return c.ms.Load()
}

// testPid is a PidSource that changes only when told to.
type testPid struct{ v atomic.Uint32 }

func (p *testPid) Pid() uint32 { return p.v.Load() }

func newPid(v uint32) *testPid {
	p := &testPid{}
	p.v.Store(v)
	return p
}

var _ reqid.Clock = (*testClock)(nil)

var _ reqid.PidSource = (*testPid)(nil)

// expectation checks a result against a vector's expect column: a text, or !reason.
func expectation(id reqid.EID, err error, expect string) string {
	if want, refused := strings.CutPrefix(expect, "!"); refused {
		if got := reason(err); got != want {
			return "refused with " + strconv.Quote(got) + ", want " + strconv.Quote(want)
		}
		return ""
	}
	if err != nil || id.String() != expect {
		return "gave (" + id.String() + ", " + reason(err) + "), want " + expect
	}
	return ""
}

func TestEIDVectorsLease(t *testing.T) {
	n := 0
	for _, r := range vectorRows(t, "lease.tsv") {
		if r[0] == "writer" {
			continue // the allocator's writer numbers: reqid has no allocator
		}
		n++
		l, err := reqid.ParseLease(r[1])
		if want, refused := strings.CutPrefix(r[2], "!"); refused {
			if reason(err) != want || l != (reqid.Lease{}) {
				t.Errorf("%s: ParseLease(%q) = (%+v, %q), want the zero Lease and %q", r[0], r[1], l, reason(err), want)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: ParseLease(%q) refused with %q", r[0], r[1], reason(err))
		} else if l.String() != r[1] {
			t.Errorf("%s: ParseLease(%q).String() = %q", r[0], r[1], l.String())
		}
	}
	if n < 15 {
		t.Errorf("lease.tsv gave %d lease rows, want the whole file", n)
	}
}

func TestEIDVectorsMint(t *testing.T) {
	var (
		scenario string
		minter   *reqid.Minter
		clock    *testClock
		pid      = newPid(4242)
		rows     = vectorRows(t, "mint.tsv")
	)
	for _, r := range rows {
		name, op, at, arg, expect := r[0], r[1], r[2], r[3], r[4]
		if name != scenario {
			scenario, minter, clock = name, nil, &testClock{}
		}
		if op != "lease" && minter == nil {
			t.Fatalf("%s: %s before a lease", name, op)
		}
		if op != "lease" && op != "switch" {
			clock.ms.Store(parseUint(at))
		}
		switch op {
		case "lease":
			l, err := reqid.ParseLease(arg)
			if err != nil || expect != "ok" {
				t.Fatalf("%s: lease %q: %v", name, arg, err)
			}
			minter = reqid.NewMinter(l, clock, pid)
		case "switch":
			l, err := reqid.ParseLease(arg)
			if err != nil {
				t.Fatalf("%s: switch lease %q: %v", name, arg, err)
			}
			err = minter.Switch(l)
			if got := reason(err); (expect == "ok") != (err == nil) || (expect != "ok" && "!"+got != expect) {
				t.Errorf("%s: switch to %q refused with %q, want %s", name, arg, got, expect)
			}
		case "mint":
			id, err := minter.MintKind(arg)
			if msg := expectation(id, err, expect); msg != "" {
				t.Errorf("%s: mint %s at %s %s", name, arg, at, msg)
			}
		case "burst":
			kind, count, _ := strings.Cut(arg, "*")
			var id reqid.EID
			var err error
			for i := uint64(0); i < parseUint(count); i++ {
				if id, err = minter.MintKind(kind); err != nil {
					t.Fatalf("%s: burst %s: call %d refused with %q", name, arg, i, reason(err))
				}
			}
			if msg := expectation(id, err, expect); msg != "" {
				t.Errorf("%s: burst %s at %s %s", name, arg, at, msg)
			}
		default:
			t.Fatalf("%s: unknown op %q", name, op)
		}
	}
}

func testLease(t testing.TB, text string) reqid.Lease {
	t.Helper()
	l, err := reqid.ParseLease(text)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestMinterPidGuard(t *testing.T) {
	clock := &testClock{}
	clock.ms.Store(1795271400123)
	pid := newPid(100)
	m := reqid.NewMinter(testLease(t, "emq:895287301:1795271400000:1795357800000"), clock, pid)
	first, err := m.MintKind("job")
	if err != nil {
		t.Fatal(err)
	}
	pid.v.Store(101) // a child forked after the minter was made
	if id, err := m.MintKind("job"); reason(err) != "fork" || id != (reqid.EID{}) {
		t.Errorf("a mint under another pid gave (%v, %q), want the zero EID and fork", id, reason(err))
	}
	if err := m.Switch(testLease(t, "emq:5:1795271400000:1795357800000")); reason(err) != "fork" {
		t.Errorf("a switch under another pid gave %q, want fork", reason(err))
	}
	pid.v.Store(100)
	next, err := m.MintKind("job")
	if err != nil || next.Compare(first) <= 0 {
		t.Errorf("after the refusals the next id is (%v, %v), want one above %v: a refused mint changes no state", next, err, first)
	}
}

func TestMinterRefusalsChangeNoState(t *testing.T) {
	clock := &testClock{}
	clock.ms.Store(1795271400000)
	m := reqid.NewMinter(testLease(t, "emq:1:1795271400000:1795271400000"), clock, newPid(1))
	a, err := m.MintKind("job")
	if err != nil || a.Count() != 0 {
		t.Fatalf("the first id: (%v, %v)", a, err)
	}
	if _, err := m.MintKind("JOB"); reason(err) != "kind" {
		t.Errorf("a kind in uppercase: %q, want kind", reason(err))
	}
	b, err := m.MintKind("job")
	if err != nil || b.Count() != 1 {
		t.Errorf("after a refused kind the next id is (%v, %v), want count 1", b, err)
	}
}

func TestMinterAllocs(t *testing.T) {
	clock := &testClock{}
	clock.ms.Store(1795271400123)
	m := reqid.NewMinter(testLease(t, "emq:895287301:1795271400000:1795357800000"), clock, newPid(1))
	kind, _ := reqid.ParseCode("job")
	if err := checkNoAllocs(func() { sinkEID, sinkErr = m.Mint(kind) }); err != nil {
		t.Errorf("Minter.Mint: %v", err)
	}
	if sinkErr != nil {
		t.Errorf("Minter.Mint refused with %q", reason(sinkErr))
	}
}

func TestMinterConcurrent(t *testing.T) {
	const goroutines, each = 8, 10000
	clock := &testClock{}
	clock.ms.Store(1795271400123)
	m := reqid.NewMinter(testLease(t, "emq:3:1795271400000:1795357800000"), clock, newPid(1))
	minted := make([][]reqid.EID, goroutines)
	var wg sync.WaitGroup
	for g := range minted {
		wg.Add(1)
		go func() {
			defer wg.Done()
			own := make([]reqid.EID, each)
			for i := range own {
				own[i], _ = m.MintKind("job")
			}
			minted[g] = own
		}()
	}
	wg.Wait()
	seen := make(map[reqid.EID]bool, goroutines*each)
	for g, own := range minted {
		for i, id := range own {
			if id == (reqid.EID{}) {
				t.Fatalf("goroutine %d: call %d was refused", g, i)
			}
			if i > 0 && id.Compare(own[i-1]) <= 0 {
				t.Fatalf("goroutine %d: call %d gave %v after %v", g, i, id, own[i-1])
			}
			seen[id] = true
		}
	}
	if len(seen) != goroutines*each {
		t.Errorf("%d goroutines of %d mints gave %d distinct ids, want %d", goroutines, each, len(seen), goroutines*each)
	}
}

func TestMinterReadsTheClockOnlyWhenMinting(t *testing.T) {
	clock := &testClock{}
	clock.ms.Store(1795271400123)
	m := reqid.NewMinter(testLease(t, "emq:895287301:1795271400000:1795357800000"), clock, newPid(1))
	if n := clock.reads.Load(); n != 0 {
		t.Errorf("NewMinter read the clock %d times", n)
	}
	m.Correlation()
	_, _ = m.MintKind("job")
	if n := clock.reads.Load(); n != 2 {
		t.Errorf("Correlation and Mint read the clock %d times, want 2", n)
	}
}

func TestMinterCorrelation(t *testing.T) {
	clock := &testClock{}
	clock.ms.Store(1795271400123)
	for _, c := range []struct{ lease, first, second string }{
		{"emq:895287301:1795271400000:1795357800000", "emq_cor_23obzcbapvupu2272222", "emq_cor_23obzcbapvupu2272226"},
		{"3ab:123456789:1795271400000:1795357800000", "3ab_cor_23obzcbapv5prncp2222", "3ab_cor_23obzcbapv5prncp2226"},
	} {
		m := reqid.NewMinter(testLease(t, c.lease), clock, newPid(1))
		a, b := m.Correlation(), m.Correlation()
		if a.String() != c.first || b.String() != c.second {
			t.Errorf("%s: two Correlation() gave %q and %q, want %q and %q", c.lease, a, b, c.first, c.second)
		}
		if back, ok := reqid.ParseCorrelation(a.String()); !ok || back != a {
			t.Errorf("%s: a minted COR does not parse back: (%q, %t)", c.lease, back, ok)
		}
	}
	ended := reqid.NewMinter(testLease(t, "emq:895287301:1795271300000:1795271300000"), clock, newPid(1))
	if c := ended.Correlation(); !c.IsZero() {
		t.Errorf("a minter past its lease gave the COR %q, want the zero Correlation", c)
	}
}

// scriptedRandom is a RandomSource that fills from a script of chunks, one per call, and counts its calls.
type scriptedRandom struct {
	chunks [][]byte
	calls  int
	fail   bool
}

func (r *scriptedRandom) Fill(buf []byte) error {
	r.calls++
	if r.fail || len(r.chunks) == 0 {
		return errors.New("no entropy")
	}
	if len(r.chunks[0]) != len(buf) {
		return errors.New("the script's chunk is not the size asked")
	}
	copy(buf, r.chunks[0])
	r.chunks = r.chunks[1:]
	return nil
}

var (
	codeDrawn3ab  = []byte{0x04, 0xc7}             // the system code 3ab
	writerDrawn   = []byte{0x07, 0x5b, 0xcd, 0x15} // the writer 123456789
	writerRenewed = []byte{0x00, 0x00, 0x00, 0x2a} // the writer 42
	reservedDrawn = []byte{0x00, 0x00}             // the system code 222: reserved
	registeredHit = []byte{0x7f, 0xff}             // the system code zzz: registered
)

func TestDetachedMatchesTheVector(t *testing.T) {
	// The vector mint.tsv "detached": the lease 3ab:123456789:1795271400000:1795357800000 mints 3ab_run_23obzcbapv5prncp2222.
	clock := &testClock{}
	clock.ms.Store(1795271400000)
	random := &scriptedRandom{chunks: [][]byte{reservedDrawn, registeredHit, codeDrawn3ab, writerDrawn}}
	m, err := reqid.NewDetached(random, clock, newPid(7))
	if err != nil {
		t.Fatal(err)
	}
	want := reqid.Lease{System: mustCode(t, "3ab"), Writer: 123456789, NotBefore: 1795271400000, NotAfter: 1795271400000 + 86_400_000}
	if got := m.Lease(); got != want {
		t.Errorf("the detached lease is %+v, want %+v", got, want)
	}
	if m.Lease().System.Class() != reqid.Detached {
		t.Errorf("the detached system is of class %s", m.Lease().System.Class())
	}
	clock.ms.Store(1795271400123)
	id, err := m.MintKind("run")
	if err != nil || id.String() != "3ab_run_23obzcbapv5prncp2222" {
		t.Errorf("the first detached id is (%v, %q), want 3ab_run_23obzcbapv5prncp2222", id, reason(err))
	}
	if random.calls != 4 {
		t.Errorf("the random source was called %d times, want 4 (two refused codes, one code, one writer)", random.calls)
	}
}

func mustCode(t testing.TB, s string) reqid.Code {
	t.Helper()
	c, ok := reqid.ParseCode(s)
	if !ok {
		t.Fatalf("ParseCode(%q) refused", s)
	}
	return c
}

func TestDetachedRefusesWithoutRandom(t *testing.T) {
	clock := &testClock{}
	clock.ms.Store(1795271400000)
	var allReserved [][]byte
	for i := 0; i < 64; i++ {
		allReserved = append(allReserved, reservedDrawn)
	}
	for name, random := range map[string]*scriptedRandom{
		"a source that fails":            {fail: true},
		"a source that gives no detach":  {chunks: allReserved},
		"a source that fails the writer": {chunks: [][]byte{codeDrawn3ab}},
	} {
		m, err := reqid.NewDetached(random, clock, newPid(7))
		if m != nil || reason(err) != "random" {
			t.Errorf("%s: NewDetached = (%v, %q), want nil and random: no fallback to a pid or a time", name, m, reason(err))
		}
	}
}

func TestDetachedRenewsAtHalfItsWindow(t *testing.T) {
	const t0, half = 1795271400000, 43_200_000
	clock := &testClock{}
	clock.ms.Store(t0)
	random := &scriptedRandom{chunks: [][]byte{codeDrawn3ab, writerDrawn, writerRenewed}}
	m, err := reqid.NewDetached(random, clock, newPid(7))
	if err != nil {
		t.Fatal(err)
	}
	clock.ms.Store(t0 + 1)
	a, err := m.MintKind("run")
	if err != nil || a.Writer() != 123456789 || random.calls != 2 {
		t.Fatalf("before half the window: (%v, %q), %d draws; want writer 123456789 and 2 draws", a, reason(err), random.calls)
	}
	clock.ms.Store(t0 + half - 1)
	if b, err := m.MintKind("run"); err != nil || b.Writer() != 123456789 || random.calls != 2 {
		t.Errorf("just before half: (%v, %q), %d draws; want the same writer and no draw", b, reason(err), random.calls)
	}
	clock.ms.Store(t0 + half)
	c, err := m.MintKind("run")
	if err != nil || c.Writer() != 42 || c.System() != a.System() || c.Time() != t0+half || c.Compare(a) <= 0 {
		t.Errorf("at half the window: (%v, %q); want writer 42, the same system, time %d, above %v", c, reason(err), t0+half, a)
	}
	if got := m.Lease(); got.NotAfter != t0+half+86_400_000 || got.Writer != 42 {
		t.Errorf("the renewed lease is %+v, want a window of 24 hours from the clock", got)
	}
}

func TestDetachedKeepsItsLeaseWhenARenewalFails(t *testing.T) {
	const t0, half = 1795271400000, 43_200_000
	clock := &testClock{}
	clock.ms.Store(t0)
	random := &scriptedRandom{chunks: [][]byte{codeDrawn3ab, writerDrawn}} // nothing left for a renewal
	m, err := reqid.NewDetached(random, clock, newPid(7))
	if err != nil {
		t.Fatal(err)
	}
	clock.ms.Store(t0 + half + 5)
	if id, err := m.MintKind("run"); err != nil || id.Writer() != 123456789 {
		t.Errorf("a failed renewal: (%v, %q), want a mint on the lease in force", id, reason(err))
	}
	clock.ms.Store(t0 + 86_400_000 + 1)
	if id, err := m.MintKind("run"); reason(err) != "lease-ended" || id != (reqid.EID{}) {
		t.Errorf("past the window: (%v, %q), want lease-ended", id, reason(err))
	}
}
