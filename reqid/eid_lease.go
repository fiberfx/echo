// SPDX-License-Identifier: MIT

package reqid

// LeaseEnv is the name of the environment variable that passes a lease to a process, in the text form of
// spec/lease.md: EID_LEASE=emq:895287301:1795271400000:1795357800000. reqid reads no environment: the caller reads it
// and gives the text to ParseLease.
const LeaseEnv = "EID_LEASE"

// leaseTermMax is the longest lease window, 24 hours in milliseconds.
const leaseTermMax = 86_400_000

// Clock is a source of Unix milliseconds. reqid reads no clock of its own: a caller gives one to NewMinter.
type Clock interface {
	NowMs() uint64
}

// ClockFunc adapts a function, such as one that returns uint64(time.Now().UnixMilli()), to a Clock.
type ClockFunc func() uint64

// NowMs calls f.
func (f ClockFunc) NowMs() uint64 { return f() }

// PidSource is a source of the current process id, for the pid guard of a Minter.
type PidSource interface {
	Pid() uint32
}

// PidFunc adapts a function, such as one that returns uint32(os.Getpid()), to a PidSource.
type PidFunc func() uint32

// Pid calls f.
func (f PidFunc) Pid() uint32 { return f() }

// RandomSource is a source of random bytes, such as crypto/rand. A failure is never replaced by a weaker source.
type RandomSource interface {
	Fill(buf []byte) error
}

// RandomFunc adapts a function, such as crypto/rand.Read, to a RandomSource.
type RandomFunc func(buf []byte) error

// Fill calls f.
func (f RandomFunc) Fill(buf []byte) error { return f(buf) }

// Lease is the exclusive right to one writer number and to the id-times from NotBefore to NotAfter, inclusive.
// One lease goes to one process, and feeds one Minter.
type Lease struct {
	System    Code
	Writer    uint32
	NotBefore uint64
	NotAfter  uint64
}

// NewLease returns a lease, checked as ParseLease checks the text form.
func NewLease(system Code, writer uint32, notBefore, notAfter uint64) (Lease, error) {
	if system.Class() == Reserved {
		return Lease{}, ErrLeaseSystem
	}
	if writer > WriterMax {
		return Lease{}, ErrLeaseWriter
	}
	if notBefore == 0 || notAfter < notBefore || notAfter > TimeMax || notAfter-notBefore > leaseTermMax {
		return Lease{}, ErrLeaseWindow
	}
	return Lease{system, writer, notBefore, notAfter}, nil
}

// leaseDecimal reads a decimal number without sign, space or leading zero.
func leaseDecimal(p string) (uint64, error) {
	if p == "" || len(p) > 20 || (len(p) > 1 && p[0] == '0') {
		return 0, ErrLeaseSyntax
	}
	var v uint64
	for i := 0; i < len(p); i++ {
		d := uint64(p[i]) - '0'
		if d > 9 || v > (1<<64-1-d)/10 {
			return 0, ErrLeaseSyntax
		}
		v = v*10 + d
	}
	return v, nil
}

// ParseLease parses system:writer:not_before:not_after, the times in decimal Unix milliseconds. The parse is strict: a
// lease whose system is not three alphabet characters or whose numbers are not decimal is refused as lease-syntax; the
// other refusals are lease-system, lease-writer and lease-window.
func ParseLease(s string) (Lease, error) {
	var part [4]string
	n, start := 0, 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ':' {
			if n == 4 {
				return Lease{}, ErrLeaseSyntax
			}
			part[n] = s[start:i]
			n++
			start = i + 1
		}
	}
	if n != 4 {
		return Lease{}, ErrLeaseSyntax
	}
	system, ok := ParseCode(part[0])
	if !ok {
		return Lease{}, ErrLeaseSyntax
	}
	var num [3]uint64
	for i := range num {
		v, err := leaseDecimal(part[i+1])
		if err != nil {
			return Lease{}, err
		}
		num[i] = v
	}
	if system.Class() == Reserved {
		return Lease{}, ErrLeaseSystem
	}
	if num[0] > WriterMax {
		return Lease{}, ErrLeaseWriter
	}
	return NewLease(system, uint32(num[0]), num[1], num[2])
}

// String returns the text form that ParseLease reads.
func (l Lease) String() string {
	b := append([]byte(l.System.String()), ':')
	b = appendDecimal(b, uint64(l.Writer))
	b = append(b, ':')
	b = appendDecimal(b, l.NotBefore)
	b = append(b, ':')
	b = appendDecimal(b, l.NotAfter)
	return string(b)
}

// appendDecimal appends n in decimal.
func appendDecimal(b []byte, n uint64) []byte {
	var digits [20]byte
	i := len(digits)
	for {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return append(b, digits[i:]...)
}
