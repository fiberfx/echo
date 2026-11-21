// SPDX-License-Identifier: MIT

package reqid_test

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

// The contract's epoch, and 2026-11-21T00:00:00Z, the clock of the minter's vector COR0SF1bsgvgpc (node 1, seq 0).
const (
	brandedEpochMS = 1704067200000
	nov21MS        = 1795219200000
)

var (
	sinkNS        string
	sinkSnowflake uint64
	sinkClass     string
)

// The vectors of echo_data native (branded_id.h:7-11, branded_id.rs:223-242), and a COR from the record
// (specs/ecd_core/stories/ecd_core.13.aus.md:65), with their snowflakes.
var brandedVectors = []struct {
	ns        string
	snowflake uint64
	text      string
}{
	{"USR", 274557032793636864, "USR0KHTOWnGLuC"},
	{"USR", 320636799581945856, "USR0NgWEfAEJfs"},
	{"USR", 0, "USR00000000000"},
	{"USR", 1<<63 - 1, "USRAzL8n0Y58m7"},
	{"COR", 381738025748656129, "COR0SCMa0vxXFZ"},
	{"COR", 382319198208004096, "COR0SF1bsgvgpc"},
	{"KAN", 367496124424073250, "KAN0R98RM39MWY"},
}

func TestEncodeBranded(t *testing.T) {
	for _, v := range brandedVectors {
		got, ok := reqid.EncodeBranded(v.ns, v.snowflake)
		if !ok || got != v.text {
			t.Errorf("EncodeBranded(%q, %d) = (%q, %t), want (%q, true)", v.ns, v.snowflake, got, ok, v.text)
		}
	}
	refused := []struct {
		name      string
		ns        string
		snowflake uint64
	}{
		{"2^63", "USR", 1 << 63},
		{"the largest uint64", "USR", 1<<64 - 1},
		{"a lowercase namespace", "usr", 1},
		{"a namespace of 2 bytes", "US", 1},
		{"a namespace of 4 bytes", "USRX", 1},
		{"a digit in the namespace", "U5R", 1},
		{"an empty namespace", "", 1},
	}
	for _, c := range refused {
		if got, ok := reqid.EncodeBranded(c.ns, c.snowflake); ok || got != "" {
			t.Errorf("%s: EncodeBranded(%q, %d) = (%q, %t), want (\"\", false)", c.name, c.ns, c.snowflake, got, ok)
		}
	}
	// The largest snowflake ends in the payload ceiling.
	if got, _ := reqid.EncodeBranded("USR", 1<<63-1); !strings.HasSuffix(got, "AzL8n0Y58m7") {
		t.Errorf("EncodeBranded(\"USR\", 2^63-1) = %q, want it to end in AzL8n0Y58m7", got)
	}
}

// checkDecodeBranded runs the table of DecodeBranded against decode and reports the first row it gets wrong.
func checkDecodeBranded(decode func(string) (string, uint64, string)) error {
	for _, v := range brandedVectors {
		ns, snowflake, class := decode(v.text)
		if ns != v.ns || snowflake != v.snowflake || class != "" {
			return fmt.Errorf("DecodeBranded(%q) = (%q, %d, %q), want (%q, %d, \"\")", v.text, ns, snowflake, class, v.ns, v.snowflake)
		}
	}
	refused := []struct {
		name  string
		in    string
		class string
	}{
		{"range", "USRzzzzzzzzzzz", "range"},
		{"one above the ceiling", "USRAzL8n0Y58m8", "range"},
		{"namespace", "usr0KHTOWnGLuC", "namespace"},
		{"a digit in the namespace", "U5R0KHTOWnGLuC", "namespace"},
		{"charset", "USR0KHTOWnGLu*", "charset"},
		{"a dash in the payload", "COR0000000000-", "charset"},
		{"a byte that is not ASCII", "USR0KHTOWnGLé", "charset"},
		{"13 bytes", "USR0KHTOWnGLu", "length"},
		{"15 bytes", "USR0KHTOWnGLuCx", "length"},
		{"empty", "", "length"},
		{"length before namespace", "usr0KHTOWnGLu", "length"},
		{"namespace before charset", "usr0KHTOWnGLu*", "namespace"},
		{"charset before range", "USRzzzzzzzzzz*", "charset"},
	}
	for _, c := range refused {
		ns, snowflake, class := decode(c.in)
		if class != c.class || ns != "" || snowflake != 0 {
			return fmt.Errorf("%s: DecodeBranded(%q) = (%q, %d, %q), want (\"\", 0, %q)", c.name, c.in, ns, snowflake, class, c.class)
		}
	}
	return nil
}

func TestDecodeBranded(t *testing.T) {
	if err := checkDecodeBranded(reqid.DecodeBranded); err != nil {
		t.Error(err)
	}
	// The record's COR holds ts 91013437688, node 31 and seq 1, minted at 2026-11-19T09:30:37.688Z.
	_, s, _ := reqid.DecodeBranded("COR0SCMa0vxXFZ")
	if ts, node, seq := s>>22, s>>12&0x3ff, s&0xfff; ts != 91013437688 || node != 31 || seq != 1 {
		t.Errorf("COR0SCMa0vxXFZ holds ts %d, node %d, seq %d; want 91013437688, 31, 1", ts, node, seq)
	}
}

func TestDecodeBrandedViolation(t *testing.T) {
	lenient := []struct {
		row    string
		decode func(string) (string, uint64, string)
	}{
		{"range", func(s string) (string, uint64, string) {
			ns, snowflake, class := reqid.DecodeBranded(s)
			if class == "range" {
				return s[:3], snowflake, ""
			}
			return ns, snowflake, class
		}},
		{"charset before range", func(s string) (string, uint64, string) {
			if len(s) == 14 && s[:3] == "USR" && s[3:] > "AzL8n0Y58m7" {
				return "", 0, "range"
			}
			return reqid.DecodeBranded(s)
		}},
	}
	for _, c := range lenient {
		err := checkDecodeBranded(c.decode)
		if err == nil {
			t.Errorf("a decoder wrong on the row %q passed the table", c.row)
			continue
		}
		if !strings.HasPrefix(err.Error(), c.row+":") {
			t.Errorf("a decoder wrong on the row %q was caught on another: %v", c.row, err)
		}
	}
}

func TestDecodeBrandedRoundTrip(t *testing.T) {
	// The round trip of branded_id.rs's property test, over 100000 snowflakes of its xorshift.
	s := uint64(0x6a09e667f3bcc909)
	for i := 0; i < 100000; i++ {
		s ^= s << 13
		s ^= s >> 7
		s ^= s << 17
		x := s & (1<<63 - 1)
		text, ok := reqid.EncodeBranded("CRS", x)
		ns, back, class := reqid.DecodeBranded(text)
		if !ok || ns != "CRS" || back != x || class != "" {
			t.Fatalf("snowflake %d: EncodeBranded = (%q, %t), DecodeBranded = (%q, %d, %q)", x, text, ok, ns, back, class)
		}
	}
}

func TestBrandedUnixMilli(t *testing.T) {
	cases := []struct {
		snowflake uint64
		want      int64
	}{
		{320636799581945856, 1780512970164},
		{381738025748656129, 1795080637688}, // COR0SCMa0vxXFZ: 2026-11-19T09:30:37.688Z, as brd14's brd.sh date prints
		{382319198208004096, nov21MS},
		{0, brandedEpochMS},
		{1<<22 - 1, brandedEpochMS},
		{1 << 22, brandedEpochMS + 1},
	}
	for _, c := range cases {
		if got := reqid.BrandedUnixMilli(c.snowflake); got != c.want {
			t.Errorf("BrandedUnixMilli(%d) = %d, want %d", c.snowflake, got, c.want)
		}
	}
}

// encode62 and base62 are copied from echo-core's bid (jonnify go/echo-core/internal/bid/bid.go:13,43-50). bid's epoch
// is another, so its time-to-text is not the contract; its text codec agrees for one snowflake.
const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func encode62(n uint64) string {
	var b [11]byte
	for i := 10; i >= 0; i-- {
		b[i] = base62[n%62]
		n /= 62
	}
	return string(b[:])
}

func TestBidAgreement(t *testing.T) {
	const s = 274557032793636864
	got, ok := reqid.EncodeBranded("USR", s)
	if want := "USR" + encode62(s); !ok || got != want {
		t.Errorf("EncodeBranded(\"USR\", %d) = (%q, %t), bid writes %q", uint64(s), got, ok, want)
	}
}

// checkNodeFrom runs the table of NodeFrom against nodeFrom and reports the first row it gets wrong.
func checkNodeFrom(nodeFrom func(string, int) uint16) error {
	cases := []struct {
		host string
		pid  int
		want uint16
	}{
		{"hosta", 100, 585}, // snowflake.ex, the node_from doctest
		{"", 0, 175},
		{"hosta", 0, 590},
		{"win-120", 4242, 61},
		{"m5.local", -1, 668},
	}
	for _, c := range cases {
		if got := nodeFrom(c.host, c.pid); got != c.want {
			return fmt.Errorf("NodeFrom(%q, %d) = %d, want %d", c.host, c.pid, got, c.want)
		}
	}
	return nil
}

// fnvNode is NodeFrom written with hash/fnv and strconv: FNV-32a over host + strconv.Itoa(pid), low 10 bits.
func fnvNode(host string, pid int) uint16 {
	h := fnv.New32a()
	h.Write([]byte(host + strconv.Itoa(pid)))
	return uint16(h.Sum32() & 0x3ff)
}

func TestNodeFrom(t *testing.T) {
	if err := checkNodeFrom(reqid.NodeFrom); err != nil {
		t.Error(err)
	}
	if err := checkNodeFrom(fnvNode); err != nil {
		t.Errorf("hash/fnv disagrees with the table: %v", err)
	}
	hosts := []string{"", "a", "hosta", "kanbox", "win-120", "m5.local", strings.Repeat("x", 300)}
	pids := []int{0, 1, 9, 10, 99, 100, 4242, 65535, 1 << 31, -1, -100, 1<<63 - 1, -1 << 63}
	for _, host := range hosts {
		for _, pid := range pids {
			if got, want := reqid.NodeFrom(host, pid), fnvNode(host, pid); got != want {
				t.Errorf("NodeFrom(%q, %d) = %d, hash/fnv gives %d", host, pid, got, want)
			}
		}
	}
}

func TestNodeFromViolation(t *testing.T) {
	// FNV-1 multiplies before it xors: not the contract's hash.
	fnv1 := func(host string, pid int) uint16 {
		h := fnv.New32()
		h.Write([]byte(host + strconv.Itoa(pid)))
		return uint16(h.Sum32() & 0x3ff)
	}
	if checkNodeFrom(fnv1) == nil {
		t.Fatal("FNV-1 passed the table of FNV-32a")
	}
}

// fields returns the ts, node and seq fields of a snowflake.
func fields(s uint64) (ts, node, seq uint64) {
	return s >> 22, s >> 12 & 0x3ff, s & 0xfff
}

func TestMinterLayout(t *testing.T) {
	// A fresh Minter of node 1 at 2026-11-21T00:00:00Z mints the design's vector: ts 91152000000, node 1, seq 0.
	m := reqid.NewMinter(1, func() int64 { return nov21MS })
	if got := m.Next(); got != 382319198208004096 {
		t.Errorf("the first Next() = %d, want 382319198208004096", got)
	}
	if got := reqid.NewMinter(1, func() int64 { return nov21MS }).Correlation().String(); got != "COR0SF1bsgvgpc" {
		t.Errorf("the first Correlation() = %q, want COR0SF1bsgvgpc", got)
	}

	// The node keeps its low 10 bits.
	for _, c := range []struct{ node, want uint16 }{{0, 0}, {585, 585}, {1023, 1023}, {1024 + 31, 31}, {0xffff, 1023}} {
		_, node, _ := fields(reqid.NewMinter(c.node, func() int64 { return nov21MS }).Next())
		if node != uint64(c.want) {
			t.Errorf("NewMinter(%d): the node field is %d, want %d", c.node, node, c.want)
		}
	}

	// The clock is read once per Next, and never by NewMinter.
	calls := 0
	m = reqid.NewMinter(7, func() int64 { calls++; return nov21MS })
	if calls != 0 {
		t.Errorf("NewMinter called the clock %d times", calls)
	}
	m.Next()
	m.Correlation()
	if calls != 2 {
		t.Errorf("Next and Correlation called the clock %d times, want 2", calls)
	}
}

// checkBurst calls next 5000 times in one millisecond: each snowflake above the last, and seq 4095 carried into ts.
func checkBurst(next func() uint64) error {
	first := next()
	prev := first
	for i := 1; i < 5000; i++ {
		s := next()
		if s <= prev {
			return fmt.Errorf("burst: call %d gave %d after %d", i, s, prev)
		}
		prev = s
	}
	ts0, node0, _ := fields(first)
	if ts, node, seq := fields(prev); ts != ts0+1 || node != node0 || seq != 5000-4096-1 {
		return fmt.Errorf("burst: the last of 5000 holds ts %d, node %d, seq %d; want %d, %d, %d", ts, node, seq, ts0+1, node0, 5000-4096-1)
	}
	return nil
}

func TestMinterBurst(t *testing.T) {
	m := reqid.NewMinter(1, func() int64 { return nov21MS })
	if err := checkBurst(m.Next); err != nil {
		t.Error(err)
	}
}

func TestMinterBurstViolation(t *testing.T) {
	// A minter that takes the clock alone and drops last+1 mints one snowflake per millisecond, again and again.
	clockOnly := func() uint64 {
		return uint64(nov21MS-brandedEpochMS)<<22 | 1<<12
	}
	err := checkBurst(clockOnly)
	if err == nil {
		t.Fatal("a minter of the clock alone passed the burst")
	}
	if !strings.HasPrefix(err.Error(), "burst: call 1 ") {
		t.Errorf("a minter of the clock alone was caught late: %v", err)
	}
}

func TestMinterClockStepBack(t *testing.T) {
	ms := int64(nov21MS)
	m := reqid.NewMinter(1, func() int64 { return ms })
	a := m.Next()

	// The clock steps back 10 ms: the Minter stays on its logical clock, one seq above the last.
	ms -= 10
	b := m.Next()
	c := m.Next()
	if b <= a || c <= b {
		t.Fatalf("after a step back of 10 ms: %d, %d, %d are not increasing", a, b, c)
	}
	if ts, _, seq := fields(c); ts != nov21MS-brandedEpochMS || seq != 2 {
		t.Errorf("after a step back of 10 ms the snowflake holds ts %d, seq %d; want %d, 2", ts, seq, nov21MS-brandedEpochMS)
	}

	// The clock passes the logical clock again: the Minter follows it, seq 0.
	ms = nov21MS + 5
	d := m.Next()
	if d <= c || reqid.BrandedUnixMilli(d) != nov21MS+5 {
		t.Errorf("after the clock passed: %d (at %d) after %d, want the clock's millisecond %d", d, reqid.BrandedUnixMilli(d), c, nov21MS+5)
	}
	if _, _, seq := fields(d); seq != 0 {
		t.Errorf("after the clock passed: seq %d, want 0", seq)
	}
}

func TestMinterConcurrent(t *testing.T) {
	const goroutines, each = 8, 10000
	m := reqid.NewMinter(3, func() int64 { return nov21MS })
	minted := make([][]uint64, goroutines)
	var wg sync.WaitGroup
	for g := range minted {
		wg.Add(1)
		go func() {
			defer wg.Done()
			own := make([]uint64, each)
			for i := range own {
				own[i] = m.Next()
			}
			minted[g] = own
		}()
	}
	wg.Wait()
	seen := make(map[uint64]bool, goroutines*each)
	for g, own := range minted {
		for i, s := range own {
			if i > 0 && s <= own[i-1] {
				t.Fatalf("goroutine %d: call %d gave %d after %d", g, i, s, own[i-1])
			}
			seen[s] = true
		}
	}
	if len(seen) != goroutines*each {
		t.Errorf("%d goroutines of %d Next() gave %d distinct snowflakes, want %d", goroutines, each, len(seen), goroutines*each)
	}
}

func TestBrandedAllocs(t *testing.T) {
	err := checkNoAllocs(func() {
		sinkNS, sinkSnowflake, sinkClass = reqid.DecodeBranded("USR0NgWEfAEJfs")
	})
	if err != nil {
		t.Errorf("DecodeBranded: %v", err)
	}
	if sinkNS != "USR" || sinkSnowflake != 320636799581945856 || sinkClass != "" {
		t.Errorf("DecodeBranded gave (%q, %d, %q)", sinkNS, sinkSnowflake, sinkClass)
	}
	m := reqid.NewMinter(1, func() int64 { return nov21MS })
	if err := checkNoAllocs(func() { sinkSnowflake = m.Next() }); err != nil {
		t.Errorf("Minter.Next: %v", err)
	}
}
