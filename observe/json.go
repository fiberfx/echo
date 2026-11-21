// SPDX-License-Identifier: MIT

package observe

import (
	"strconv"
	"unicode/utf8"
)

const hexDigits = "0123456789abcdef"

// AppendJSON appends the observation as one JSON object with no newline. An empty string field is written "none".
//
// The keys come in this order: id, link, attempt, build, method, admission, pattern, handler, kind, status,
// committed, panicked. The zero ID and the zero Link are written "none" as well.
func (o Observation) AppendJSON(dst []byte) []byte {
	dst = append(dst, `{"id":`...)
	if o.ID.IsZero() {
		dst = appendString(dst, None)
	} else {
		dst = append(dst, '"')
		dst, _ = o.ID.AppendText(dst)
		dst = append(dst, '"')
	}
	dst = append(dst, `,"link":`...)
	if o.Link.IsZero() {
		dst = appendString(dst, None)
	} else {
		dst = append(dst, '"')
		dst, _ = o.Link.AppendText(dst)
		dst = append(dst, '"')
	}
	dst = append(dst, `,"attempt":`...)
	dst = appendString(dst, o.Attempt)
	dst = append(dst, `,"build":`...)
	dst = appendString(dst, o.Build)
	dst = append(dst, `,"method":`...)
	dst = appendString(dst, o.Method)
	dst = append(dst, `,"admission":`...)
	dst = appendString(dst, o.Admission.String())
	dst = append(dst, `,"pattern":`...)
	dst = appendString(dst, o.Pattern)
	dst = append(dst, `,"handler":`...)
	dst = appendString(dst, o.Handler)
	dst = append(dst, `,"kind":`...)
	dst = appendString(dst, o.Kind.String())
	dst = append(dst, `,"status":`...)
	dst = strconv.AppendInt(dst, int64(o.Status), 10)
	dst = append(dst, `,"committed":`...)
	dst = strconv.AppendBool(dst, o.Committed)
	dst = append(dst, `,"panicked":`...)
	dst = strconv.AppendBool(dst, o.Panicked)
	return append(dst, '}')
}

// appendString appends s as a JSON string, and None for an empty s. The method is the client's text, so this
// is a boundary: a quote and a backslash get a backslash, a byte below 0x20 becomes \u00XX, and a byte that is
// not valid UTF-8 becomes U+FFFD.
func appendString(dst []byte, s string) []byte {
	if s == "" {
		s = None
	}
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			} else {
				dst = append(dst, '\\', c)
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, s[start:i]...)
			dst = utf8.AppendRune(dst, utf8.RuneError)
			i++
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}
