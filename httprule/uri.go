// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package httprule

import "strings"

// SanitizeURI decides the form of a same-host redirect target that a browser cannot read as another host.
func SanitizeURI(uri string) string {
	// Browsers remove tab and newline characters from URLs, so `/\t/example.com` is `//example.com` to them and a
	// control character could hide the double slash from the check below. Percent-encode C0 control characters and
	// DEL first; the browser then requests the same path.
	uri = EscapeControlChars(uri)
	// double slash `\\`, `//` or even `\/` is absolute uri for browsers and by redirecting request to that uri
	// we are vulnerable to open redirect attack. so replace all slashes from the beginning with single slash
	if len(uri) > 1 && (uri[0] == '\\' || uri[0] == '/') && (uri[1] == '\\' || uri[1] == '/') {
		uri = "/" + strings.TrimLeft(uri, `/\`)
	}
	return uri
}

// EscapeControlChars decides the text of s as it is written into a Location: C0 control characters and DEL are
// percent-encoded.
func EscapeControlChars(s string) string {
	i := 0
	for i < len(s) && s[i] >= 0x20 && s[i] != 0x7f {
		i++
	}
	if i == len(s) {
		return s
	}
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s) + 8)
	b.WriteString(s[:i])
	for ; i < len(s); i++ {
		if ch := s[i]; ch < 0x20 || ch == 0x7f {
			b.WriteByte('%')
			b.WriteByte(hexDigits[ch>>4])
			b.WriteByte(hexDigits[ch&0x0f])
		} else {
			b.WriteByte(ch)
		}
	}
	return b.String()
}
