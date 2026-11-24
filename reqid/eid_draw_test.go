// SPDX-License-Identifier: MIT

package reqid_test

import (
	"testing"

	"github.com/fiberfx/mx-echo/v7/reqid"
)

// The detached code draw of spec/lease.md#detached, as ecd-id v0.8.0 makes it: one 16-bit draw r; r >= 61440 is drawn
// again, at most 64 times; the code is 1024 + r mod 5120.

// drawChunks returns the script of the given 16-bit draws, big-endian, followed by the writer 7.
func drawChunks(draws ...uint16) [][]byte {
	var chunks [][]byte
	for _, d := range draws {
		chunks = append(chunks, []byte{byte(d >> 8), byte(d)})
	}
	return append(chunks, []byte{0, 0, 0, 7})
}

func drawnSystem(t testing.TB, random *scriptedRandom) reqid.Code {
	t.Helper()
	clock := &testClock{}
	clock.ms.Store(1795271400000)
	m, err := reqid.NewDetached(random, clock, newPid(7))
	if err != nil {
		t.Fatalf("NewDetached: %v", err)
	}
	if l := m.Lease(); l.System.Class() != reqid.Detached || l.Writer != 7 {
		t.Fatalf("lease %+v: want a detached system and writer 7", l)
	}
	return m.Lease().System
}

func TestDetachedDrawOneRejection(t *testing.T) {
	random := &scriptedRandom{chunks: drawChunks(0xF000, 0x0000)}
	if got := drawnSystem(t, random); got.String() != "322" || got != 1024 {
		t.Errorf("code %s (%d), want 322 (1024)", got, uint16(got))
	}
	if random.calls != 3 {
		t.Errorf("%d draws, want 3 (two codes, one writer)", random.calls)
	}
}

func TestDetachedDrawLastAccepted(t *testing.T) {
	if got := drawnSystem(t, &scriptedRandom{chunks: drawChunks(0xEFFF)}); got.String() != "7zz" || got != 6143 {
		t.Errorf("code %s (%d), want 7zz (6143)", got, uint16(got))
	}
}

func TestDetachedDrawAfterSixtyThreeRejections(t *testing.T) {
	draws := make([]uint16, 0, 64)
	for i := 0; i < 63; i++ {
		draws = append(draws, 0xFFFF)
	}
	draws = append(draws, 5)
	if got := drawnSystem(t, &scriptedRandom{chunks: drawChunks(draws...)}); got.String() != "327" || got != 1029 {
		t.Errorf("code %s (%d), want 327 (1029)", got, uint16(got))
	}
}

func TestDetachedDrawSixtyFourRejectionsRefused(t *testing.T) {
	draws := make([]uint16, 64)
	for i := range draws {
		draws[i] = 0xFFFF
	}
	random := &scriptedRandom{chunks: drawChunks(draws...)}
	clock := &testClock{}
	clock.ms.Store(1795271400000)
	m, err := reqid.NewDetached(random, clock, newPid(7))
	if m != nil || reason(err) != "random" {
		t.Errorf("NewDetached = (%v, %q), want nil and random", m, reason(err))
	}
	if random.calls != 64 {
		t.Errorf("%d draws, want 64 (no writer draw)", random.calls)
	}
}

func TestDetachedDrawSourceFailureReturnsAtOnce(t *testing.T) {
	random := &scriptedRandom{fail: true}
	clock := &testClock{}
	clock.ms.Store(1795271400000)
	m, err := reqid.NewDetached(random, clock, newPid(7))
	if m != nil || reason(err) != "random" || random.calls != 1 {
		t.Errorf("NewDetached = (%v, %q) after %d draws, want nil and random after 1", m, reason(err), random.calls)
	}
}

func TestDetachedDrawEveryCodeTwelveTimes(t *testing.T) {
	var seen [1 << 15]int
	for r := 0; r < 61440; r++ {
		seen[drawnSystem(t, &scriptedRandom{chunks: drawChunks(uint16(r))})]++
	}
	for c := range seen {
		want := 0
		if c >= 1024 && c < 6144 {
			want = 12
		}
		if seen[c] != want {
			t.Fatalf("code %s drawn %d times, want %d", reqid.Code(c), seen[c], want)
		}
	}
}
