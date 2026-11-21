// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package reqid holds a request's name as a value, an incoming traceparent as a link beside it, and the COR id of the
// flow the request belongs to, in the branded-id contract of echo_data native.
//
// The package is pure: it imports only context, for NewContext and FromContext, and sync/atomic, for Minter, and reads
// neither a clock nor a random source. Whoever names a request gives New the time and the random number, and whoever
// mints a COR gives NewMinter the clock.
package reqid

const hexDigits = "0123456789abcdef"

// ID is a request's name: 6 bytes of Unix milliseconds, big-endian, then 4 random bytes (the layout of fv's key).
// It is comparable and time-ordered. The zero ID means "not named".
type ID struct{ b [10]byte }

// New builds an ID from a time and a random number; it reads neither a clock nor a random source.
func New(unixMilli int64, random uint32) ID {
	// The low 48 bits of unixMilli are kept: they hold every millisecond from 1970 to the year 10889.
	// A time outside that range does not come back from UnixMilli.
	var id ID
	id.b[0] = byte(unixMilli >> 40)
	id.b[1] = byte(unixMilli >> 32)
	id.b[2] = byte(unixMilli >> 24)
	id.b[3] = byte(unixMilli >> 16)
	id.b[4] = byte(unixMilli >> 8)
	id.b[5] = byte(unixMilli)
	id.b[6] = byte(random >> 24)
	id.b[7] = byte(random >> 16)
	id.b[8] = byte(random >> 8)
	id.b[9] = byte(random)
	return id
}

// Parse reads the 20 lowercase hex digits String writes.
func Parse(s string) (ID, bool) {
	var id ID
	if !unhex(id.b[:], s) {
		return ID{}, false
	}
	return id, true
}

// IsZero reports whether id is the zero ID.
func (id ID) IsZero() bool {
	return id == ID{}
}

// UnixMilli returns the low 48 bits of the time New was given: the time itself for every millisecond from 1970
// to the year 10889.
func (id ID) UnixMilli() int64 {
	return int64(id.b[0])<<40 | int64(id.b[1])<<32 | int64(id.b[2])<<24 | int64(id.b[3])<<16 | int64(id.b[4])<<8 | int64(id.b[5])
}

// String returns 20 lowercase hex digits.
func (id ID) String() string {
	var buf [20]byte
	return string(appendHex(buf[:0], id.b[:]))
}

// AppendText appends String's text without allocating; the error is always nil.
func (id ID) AppendText(b []byte) ([]byte, error) {
	return appendHex(b, id.b[:]), nil
}

// MarshalText returns String's text, so that encoding/json and log/slog's JSON handler write the name and not {};
// the error is always nil.
func (id ID) MarshalText() ([]byte, error) {
	return appendHex(make([]byte, 0, 2*len(id.b)), id.b[:]), nil
}

// appendHex appends src to dst as lowercase hex digits.
func appendHex(dst, src []byte) []byte {
	for _, v := range src {
		dst = append(dst, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return dst
}

// unhex fills dst from s, which must be exactly two lowercase hex digits per byte of dst.
func unhex(dst []byte, s string) bool {
	if len(s) != 2*len(dst) {
		return false
	}
	for i := range dst {
		hi, okHi := nibble(s[2*i])
		lo, okLo := nibble(s[2*i+1])
		if !okHi || !okLo {
			return false
		}
		dst[i] = hi<<4 | lo
	}
	return true
}

// nibble returns the value of one lowercase hex digit.
func nibble(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}
