// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package httprule

import "strings"

const gzipScheme = "gzip"

// HasContentCoding decides whether a response is already encoded: whether its Content-Encoding values list any coding
// other than identity.
func HasContentCoding(headers []string) bool {
	for _, header := range headers {
		for coding := range strings.SplitSeq(header, ",") {
			coding = strings.TrimSpace(coding)
			if coding != "" && !strings.EqualFold(coding, "identity") {
				return true
			}
		}
	}
	return false
}

// AcceptsGzip decides whether the client accepts a gzip response, from its Accept-Encoding values: gzip or x-gzip
// with a positive weight, or a wildcard that is not refused.
func AcceptsGzip(headers []string) bool {
	var gzipAllowed, wildcardAllowed, wildcardRefused bool
	for _, header := range headers {
		for entry := range strings.SplitSeq(header, ",") {
			coding, weight, hasWeight := strings.Cut(entry, ";")
			coding = strings.TrimSpace(coding)
			switch {
			case strings.EqualFold(coding, gzipScheme), strings.EqualFold(coding, "x-gzip"):
				// RFC 9110, section 8.4.1.3: x-gzip is equivalent to gzip.
				// An explicit refusal or invalid weight takes precedence over
				// wildcards and over any duplicate entry allowing gzip.
				if hasWeight && !positiveQualityValue(weight) {
					return false
				}
				gzipAllowed = true
			case coding == "*":
				if hasWeight && !positiveQualityValue(weight) {
					wildcardRefused = true
				} else {
					wildcardAllowed = true
				}
			}
		}
	}
	return gzipAllowed || (wildcardAllowed && !wildcardRefused)
}

// positiveQualityValue matches the qvalue grammar in RFC 9110, section 12.4.2.
func positiveQualityValue(weight string) bool {
	name, value, ok := strings.Cut(strings.TrimSpace(weight), "=")
	if !ok || !strings.EqualFold(name, "q") {
		return false
	}
	whole, fraction, _ := strings.Cut(value, ".")
	if (whole != "0" && whole != "1") || len(fraction) > 3 {
		return false
	}
	positive := whole == "1"
	for i := range len(fraction) {
		digit := fraction[i]
		if digit < '0' || digit > '9' || (whole == "1" && digit != '0') {
			return false
		}
		positive = positive || digit != '0'
	}
	return positive
}
