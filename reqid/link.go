// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package reqid

// The bytes of a traceparent of version 00: "vv-" 32 digits of trace "-" 16 digits of span "-" 2 digits of flags.
const (
	traceStart      = 3
	traceEnd        = 35
	spanStart       = 36
	spanEnd         = 52
	flagsStart      = 53
	traceparentSize = 55
)

// Link is an incoming W3C traceparent, kept beside a request's name and never adopted as its parent.
// The zero Link means "none".
type Link struct {
	trace [16]byte
	span  [8]byte
	flags byte
}

// ParseTraceparent reads a traceparent header value: version-trace-span-flags in lowercase hex, version not ff,
// trace and span not all zero, 55 bytes for version 00, and for a later version 55 bytes or more with a '-' at 55.
func ParseTraceparent(s string) (Link, bool) {
	if len(s) < traceparentSize || s[traceStart-1] != '-' || s[traceEnd] != '-' || s[spanEnd] != '-' {
		return Link{}, false
	}
	var version [1]byte
	if !unhex(version[:], s[:traceStart-1]) || version[0] == 0xff {
		return Link{}, false
	}
	if len(s) > traceparentSize && (version[0] == 0 || s[traceparentSize] != '-') {
		return Link{}, false
	}
	var l Link
	var flags [1]byte
	if !unhex(l.trace[:], s[traceStart:traceEnd]) || !unhex(l.span[:], s[spanStart:spanEnd]) || !unhex(flags[:], s[flagsStart:traceparentSize]) {
		return Link{}, false
	}
	if l.trace == [16]byte{} || l.span == [8]byte{} {
		return Link{}, false
	}
	l.flags = flags[0]
	return l, true
}

// IsZero reports whether l is the zero Link.
func (l Link) IsZero() bool {
	return l == Link{}
}

// String returns "00-<trace>-<span>-<flags>".
func (l Link) String() string {
	var buf [traceparentSize]byte
	b, _ := l.AppendText(buf[:0])
	return string(b)
}

// AppendText appends String's text; the error is always nil.
func (l Link) AppendText(b []byte) ([]byte, error) {
	// The version written is 00 whatever version was read: the fields kept are the ones version 00 defines.
	b = append(b, '0', '0', '-')
	b = appendHex(b, l.trace[:])
	b = append(b, '-')
	b = appendHex(b, l.span[:])
	b = append(b, '-', hexDigits[l.flags>>4], hexDigits[l.flags&0x0f])
	return b, nil
}
