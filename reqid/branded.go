// SPDX-License-Identifier: MIT

package reqid

import "sync/atomic"

// The branded-id contract of echo_data native (branded_id.h): 3 bytes of A-Z, then the 11 Base62 digits of a snowflake
// below 2^63, laid out ts(41)<<22 | node(10)<<12 | seq(12), with ts in milliseconds since 2024-01-01T00:00:00Z.
// maxPayload is the 11 digits of 2^63 - 1.
const (
	brandedLen     = 14
	brandedNSLen   = 3
	brandedEpochMS = 1704067200000
	nodeBits       = 10
	seqBits        = 12
	tsShift        = nodeBits + seqBits
	nodeMask       = 1<<nodeBits - 1
	seqMask        = 1<<seqBits - 1
	base62Digits   = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	maxPayload     = "AzL8n0Y58m7"
)

// The FNV-32a offset basis and prime, for NodeFrom.
const (
	fnvOffset32 = 0x811c9dc5
	fnvPrime32  = 0x01000193
)

// EncodeBranded returns the 14-byte branded id of a snowflake under a namespace. It reports false when ns is not 3 bytes
// of A-Z or the snowflake is 2^63 or more.
func EncodeBranded(ns string, snowflake uint64) (string, bool) {
	var b [brandedLen]byte
	if !encodeBranded(&b, ns, snowflake) {
		return "", false
	}
	return string(b[:]), true
}

// encodeBranded writes the namespace into b, then the snowflake's Base62 digits, most significant first.
func encodeBranded(b *[brandedLen]byte, ns string, snowflake uint64) bool {
	if snowflake >= 1<<63 || !validNamespace(ns) {
		return false
	}
	copy(b[:], ns)
	for i := brandedLen - 1; i >= brandedNSLen; i-- {
		b[i] = base62Digits[snowflake%62]
		snowflake /= 62
	}
	return true
}

// DecodeBranded reads a branded id. class is "" when s is valid; otherwise it names the first rule s breaks, in this
// order: "length" (not 14 bytes), "namespace" (the first 3 bytes not A-Z), "charset" (a payload byte outside
// 0-9A-Za-z), "range" (the payload above AzL8n0Y58m7, the digits of 2^63 - 1). A refused s gives "" and 0.
func DecodeBranded(s string) (ns string, snowflake uint64, class string) {
	if len(s) != brandedLen {
		return "", 0, "length"
	}
	if !validNamespace(s[:brandedNSLen]) {
		return "", 0, "namespace"
	}
	payload := s[brandedNSLen:]
	for i := 0; i < len(payload); i++ {
		d, ok := base62Digit(payload[i])
		if !ok {
			return "", 0, "charset"
		}
		snowflake = snowflake*62 + d
	}
	// Base62 digits sort as their bytes do, so the range check is a compare of fixed width; a payload that passes it
	// has not wrapped the sum above.
	if payload > maxPayload {
		return "", 0, "range"
	}
	return s[:brandedNSLen], snowflake, ""
}

// BrandedUnixMilli returns the mint time of a snowflake in Unix milliseconds: its ts field plus the contract's epoch.
func BrandedUnixMilli(snowflake uint64) int64 {
	return int64(snowflake>>tsShift) + brandedEpochMS
}

// NodeFrom is the contract's node derivation: FNV-32a over host followed by pid in decimal, kept to its low 10 bits.
func NodeFrom(host string, pid int) uint16 {
	h := uint32(fnvOffset32)
	for i := 0; i < len(host); i++ {
		h ^= uint32(host[i])
		h *= fnvPrime32
	}
	var buf [20]byte
	for _, c := range appendDecimal(buf[:0], pid) {
		h ^= uint32(c)
		h *= fnvPrime32
	}
	return uint16(h & nodeMask)
}

// Minter mints snowflakes as EchoData.Snowflake does: lock-free, strictly increasing, never the same one twice. Two
// Minters of one node can mint the same snowflake in one millisecond; a process keeps one Minter per node.
type Minter struct {
	cell atomic.Int64 // ts<<12 | seq of the last snowflake minted; -1 before the first
	node uint16
	now  func() int64
}

// NewMinter returns a Minter of node, of which the low 10 bits are kept, that takes the time in Unix milliseconds from
// nowUnixMilli. NewMinter reads no clock; Next calls nowUnixMilli once per snowflake.
func NewMinter(node uint16, nowUnixMilli func() int64) *Minter {
	m := &Minter{node: node & nodeMask, now: nowUnixMilli}
	m.cell.Store(-1)
	return m
}

// Next mints a snowflake. It moves the cell to the larger of the clock's ts<<12 and the last value plus one: a burst
// in one millisecond counts seq up, seq 4096 carries into ts, and a clock that steps back leaves the Minter on its own
// logical clock until the clock catches up. It never blocks. The ts field holds 41 bits of milliseconds, so a clock
// past 2093-09-06 is outside the contract.
func (m *Minter) Next() uint64 {
	now := (m.now() - brandedEpochMS) << seqBits
	for {
		last := m.cell.Load()
		next := max(now, last+1)
		if m.cell.CompareAndSwap(last, next) {
			return uint64(next>>seqBits)<<tsShift | uint64(m.node)<<seqBits | uint64(next&seqMask)
		}
	}
}

// Correlation mints a COR from Next. It is never the zero Correlation while the clock is inside the contract.
func (m *Minter) Correlation() Correlation {
	c, _ := NewCorrelation(m.Next())
	return c
}

// validNamespace reports whether ns is 3 bytes of A-Z.
func validNamespace(ns string) bool {
	if len(ns) != brandedNSLen {
		return false
	}
	for i := 0; i < len(ns); i++ {
		if ns[i] < 'A' || ns[i] > 'Z' {
			return false
		}
	}
	return true
}

// base62Digit returns the value of one Base62 digit: 0-9, then A-Z, then a-z.
func base62Digit(c byte) (uint64, bool) {
	switch {
	case '0' <= c && c <= '9':
		return uint64(c - '0'), true
	case 'A' <= c && c <= 'Z':
		return uint64(c-'A') + 10, true
	case 'a' <= c && c <= 'z':
		return uint64(c-'a') + 36, true
	}
	return 0, false
}

// appendDecimal appends n in decimal, as strconv.Itoa writes it.
func appendDecimal(b []byte, n int) []byte {
	u := uint64(n)
	if n < 0 {
		b = append(b, '-')
		u = -u
	}
	var digits [20]byte
	i := len(digits)
	for {
		i--
		digits[i] = byte('0' + u%10)
		u /= 10
		if u == 0 {
			break
		}
	}
	return append(b, digits[i:]...)
}
