// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package httprule

import "strings"

// MatchOrigin decides whether origin is one of the allowed origins, and returns the allowed spelling it matched.
func MatchOrigin(allowed []string, origin string) (string, bool) {
	for _, allowedOrigin := range allowed {
		if strings.EqualFold(allowedOrigin, origin) {
			return allowedOrigin, true
		}
	}
	return "", false
}
