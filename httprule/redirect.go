// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package httprule

import "strings"

const www = "www."

func hasWWWPrefix(host string) bool {
	return len(host) >= len(www) && strings.EqualFold(host[:len(www)], www)
}

func stripWWWPrefix(host string) string {
	if hasWWWPrefix(host) {
		return host[len(www):]
	}
	return host
}

// ValidRedirectHost decides whether host may be used as the authority of a Location.
// The stripped host is checked too, as removing a bare www. would leave no hostname.
func ValidRedirectHost(host string) bool {
	host = stripWWWPrefix(host)
	if host == "" || host[0] == ':' || host[0] == '.' {
		return false
	}
	// net/http validates Host syntax. Reject percent escapes too, since a
	// browser could interpret them as authority delimiters in Location.
	return !strings.Contains(host, "%")
}

// RedirectHTTPS decides whether a request that is not https is redirected, and to which https URL.
func RedirectHTTPS(scheme, host, uri string) (ok bool, url string) {
	if scheme != "https" {
		return true, "https://" + host + uri
	}
	return false, ""
}

// RedirectHTTPSWWW decides whether a request is redirected to https with a www host, and to which URL.
func RedirectHTTPSWWW(scheme, host, uri string) (ok bool, url string) {
	// Redirect if not HTTPS OR missing www prefix (needs either fix)
	if scheme != "https" || !hasWWWPrefix(host) {
		host = stripWWWPrefix(host) // Remove www if present to avoid duplication
		return true, "https://www." + host + uri
	}
	return false, ""
}

// RedirectHTTPSNonWWW decides whether a request is redirected to https with no www host, and to which URL.
func RedirectHTTPSNonWWW(scheme, host, uri string) (ok bool, url string) {
	// Redirect if not HTTPS OR has www prefix (needs either fix)
	if scheme != "https" || hasWWWPrefix(host) {
		host = stripWWWPrefix(host)
		return true, "https://" + host + uri
	}
	return false, ""
}

// RedirectWWW decides whether a request whose host lacks www is redirected to the www host, and to which URL.
func RedirectWWW(scheme, host, uri string) (ok bool, url string) {
	if !hasWWWPrefix(host) {
		return true, scheme + "://www." + host + uri
	}
	return false, ""
}

// RedirectNonWWW decides whether a request whose host has www is redirected to the bare host, and to which URL.
func RedirectNonWWW(scheme, host, uri string) (ok bool, url string) {
	if hasWWWPrefix(host) {
		return true, scheme + "://" + host[len(www):] + uri
	}
	return false, ""
}
