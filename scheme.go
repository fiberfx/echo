// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"net/http"

	"github.com/fiberfx/mx-echo/v7/internal/trust"
)

// SchemeExtractor is a function to determine the scheme (for example `http` or `https`) of http.Request.
// Set appropriate one to Echo#SchemeExtractor.
//
// When Echo#SchemeExtractor is not set, Context.Scheme uses ExtractSchemeFromHeaders with the default trust options.
type SchemeExtractor func(*http.Request) string

// ExtractSchemeDirect returns a SchemeExtractor that uses only the actual connection: `https` for TLS connections
// and `http` otherwise. Forwarding headers are ignored.
// Use this if your server faces the internet directly (i.e.: uses no proxy).
func ExtractSchemeDirect() SchemeExtractor {
	return extractSchemeDirect
}

func extractSchemeDirect(req *http.Request) string {
	return trust.SchemeDirect(req)
}

// ExtractSchemeFromHeaders returns a SchemeExtractor that uses the `X-Forwarded-Proto`, `X-Forwarded-Protocol`,
// `X-Forwarded-Ssl` and `X-Url-Scheme` headers only when the request comes directly from a trusted address
// (http.Request.RemoteAddr). For requests from other addresses the headers are ignored and the scheme of the actual
// connection is used.
//
// By default, loopback, link-local and private network addresses and unix socket peers are trusted. Use TrustOption
// to change this, for example TrustIPRange to trust a proxy that connects from a public address (such as a CDN or a
// cloud load balancer). TrustLoopback(false) also stops trusting unix socket peers.
//
// When `X-Forwarded-Proto` is present, the other headers are ignored, and the last value is used. The trusted proxy
// must therefore set (overwrite) `X-Forwarded-Proto` rather than pass through the value sent by the client.
//
// This is the default strategy when Echo#SchemeExtractor is not set.
func ExtractSchemeFromHeaders(options ...TrustOption) SchemeExtractor {
	checker := newIPChecker(options)
	return func(req *http.Request) string {
		return extractScheme(req, checker)
	}
}

// LegacySchemeExtractor returns a SchemeExtractor that uses the forwarding headers from any client. This was the
// behavior of Context.Scheme before the address of the client was checked.
//
// It is not safe against spoofing unless every request passes through a proxy that sets or removes the
// `X-Forwarded-Proto`, `X-Forwarded-Protocol`, `X-Forwarded-Ssl` and `X-Url-Scheme` headers.
// Use ExtractSchemeFromHeaders instead.
func LegacySchemeExtractor() SchemeExtractor {
	return legacySchemeExtractor
}

func legacySchemeExtractor(req *http.Request) string {
	return trust.LegacyScheme(req)
}

// defaultSchemeChecker is used by Context.Scheme when Echo#SchemeExtractor is not set.
var defaultSchemeChecker = newIPChecker(nil)

func extractScheme(req *http.Request, checker *ipChecker) string {
	return trust.Scheme(req, &checker.checker)
}
