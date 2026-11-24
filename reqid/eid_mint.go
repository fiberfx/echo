// SPDX-License-Identifier: MIT

package reqid

import "sync/atomic"

// A Minter mints ids inside one lease with no round trip: the five lines of spec/lease.md#minter. The time it stamps
// never goes back, a burst of 2^18 ids in one millisecond borrows the next millisecond, a clock set back or slow
// stamps from the last id or the lease's start, and no id is stamped after the lease's end. A refused mint changes no
// state.
//
// A Minter is safe for concurrent use: its state is held under a short spin lock of sync/atomic, which no mint holds
// across a call that can block. It records the process id it was made under and refuses to mint under another
// ("fork"), so a child forked after the minter was made cannot mint on its parent's lease. The zero Minter is not
// usable: use NewMinter or NewDetached.
type Minter struct {
	lock    atomic.Uint32
	lease   Lease
	hasLast bool
	lastT   uint64
	count   uint32
	clock   Clock
	pids    PidSource
	pid     uint32
	random  RandomSource // set by NewDetached: the minter draws a fresh lease at half its term
}

// NewMinter returns a minter of lease, guarded by the process id pids reports now. It reads no clock.
func NewMinter(lease Lease, clock Clock, pids PidSource) *Minter {
	return &Minter{lease: lease, clock: clock, pids: pids, pid: pids.Pid()}
}

// NewDetached returns the minter of a process that holds no lease and can reach no allocator: it draws a detached
// system code (first character 3 to 7) and a random 30-bit writer from random, and mints inside a window of 24 hours
// from the clock. Two detached minters produce an equal id only if they share both draws and mint the same kind in the
// same millisecond at the same count. Past half its window the minter draws a new writer under the same code and
// switches to it. If random fails, NewDetached returns that failure as ErrRandom: there is no fallback to a process id
// or a time.
func NewDetached(random RandomSource, clock Clock, pids PidSource) (*Minter, error) {
	lease, err := detachedLease(random, clock, 0)
	if err != nil {
		return nil, err
	}
	m := NewMinter(lease, clock, pids)
	m.random = random
	return m, nil
}

// detachedLease draws a writer, and a detached system code unless keep is one, and returns a lease of 24 hours from the
// clock.
func detachedLease(random RandomSource, clock Clock, keep Code) (Lease, error) {
	system := keep
	if system == 0 {
		// Each draw reads one 16-bit big-endian value v. A value of 61440 (12 x 5120) or more is drawn again, at most
		// 64 times in all; otherwise the code is 1024 + v mod 5120, so each of the 5,120 detached codes (3 to 7 as
		// first character) is equally likely and a draw is rejected with probability 1/16.
		found := false
		for i := 0; i < 64 && !found; i++ {
			var b [2]byte
			if err := random.Fill(b[:]); err != nil {
				return Lease{}, ErrRandom
			}
			if v := uint16(b[0])<<8 | uint16(b[1]); v < 61440 {
				system = Code(1024 + v%5120)
				found = true
			}
		}
		if !found {
			return Lease{}, ErrRandom
		}
	}
	var w [4]byte
	if err := random.Fill(w[:]); err != nil {
		return Lease{}, ErrRandom
	}
	notBefore := clock.NowMs()
	if notBefore < 1 {
		notBefore = 1
	}
	if notBefore > TimeMax-leaseTermMax {
		notBefore = TimeMax - leaseTermMax
	}
	writer := (uint32(w[0])<<24 | uint32(w[1])<<16 | uint32(w[2])<<8 | uint32(w[3])) & WriterMax
	return NewLease(system, writer, notBefore, notBefore+leaseTermMax)
}

func (m *Minter) acquire() {
	for !m.lock.CompareAndSwap(0, 1) {
	}
}

func (m *Minter) release() { m.lock.Store(0) }

// Lease returns the lease in force, with NotBefore as the last switch raised it.
func (m *Minter) Lease() Lease {
	m.acquire()
	defer m.release()
	return m.lease
}

// Mint mints one id of kind. It refuses with ErrFork under another process id, ErrKind for a kind that is not three
// letters and ErrLeaseEnded when no id-time is left inside the lease.
func (m *Minter) Mint(kind Code) (EID, error) {
	m.acquire()
	defer m.release()
	if m.pids.Pid() != m.pid {
		return EID{}, ErrFork
	}
	if !kind.IsKind() {
		return EID{}, ErrKind
	}
	now := m.clock.NowMs()
	if m.random != nil && now >= m.lease.NotBefore+leaseTermMax/2 {
		// A failed draw keeps the lease in force; the refusal comes only when it ends.
		if fresh, err := detachedLease(m.random, m.clock, m.lease.System); err == nil {
			m.switchTo(fresh)
		}
	}
	t := now
	if t < m.lease.NotBefore {
		t = m.lease.NotBefore
	}
	if m.hasLast && t < m.lastT {
		t = m.lastT
	}
	var count uint32
	if m.hasLast && t == m.lastT {
		count = m.count + 1
	}
	if count == countMax {
		t, count = t+1, 0
	}
	if t > m.lease.NotAfter {
		return EID{}, ErrLeaseEnded
	}
	m.hasLast, m.lastT, m.count = true, t, count
	return packEID(m.lease.System, kind, t, m.lease.Writer, count), nil
}

// MintKind mints one id of the kind named by three letters.
func (m *Minter) MintKind(kind string) (EID, error) {
	k, ok := ParseCode(kind)
	if !ok {
		return EID{}, ErrKind
	}
	return m.Mint(k)
}

// Switch switches to a fresh lease of the same system. The minter keeps its last time and raises the new lease's
// NotBefore past it, so its ids of a kind never go backward in byte order, even when the new writer number is lower.
// A lease of another system is refused as lease-system, one that ends before the last time plus one as lease-ended,
// and a minter under another process id as fork.
func (m *Minter) Switch(lease Lease) error {
	m.acquire()
	defer m.release()
	if m.pids.Pid() != m.pid {
		return ErrFork
	}
	if lease.System != m.lease.System {
		return ErrLeaseSystem
	}
	next := lease
	if m.hasLast && next.NotBefore < m.lastT+1 {
		next.NotBefore = m.lastT + 1
	}
	if next.NotBefore > next.NotAfter {
		return ErrLeaseEnded
	}
	m.lease = next
	return nil
}

// switchTo is Switch for a lease the minter drew itself, with the lock held: a fresh detached lease begins at the
// clock, so only the raise past the last time can shorten it, and a lease that would end first is not taken.
func (m *Minter) switchTo(lease Lease) {
	if m.hasLast && lease.NotBefore < m.lastT+1 {
		lease.NotBefore = m.lastT + 1
	}
	if lease.NotBefore <= lease.NotAfter {
		m.lease = lease
	}
}

// Correlation mints a COR: an id of kind cor, from the minter's system, detached or registered. It returns the zero
// Correlation, which means none, when Mint refuses and when the minter's system may not issue a COR (a registered
// system without a live cor row); use Mint for the reason of the first.
func (m *Minter) Correlation() Correlation {
	id, err := m.Mint(corKind)
	if err != nil || !checkCorrelation(id) {
		return Correlation{}
	}
	return Correlation{id}
}
