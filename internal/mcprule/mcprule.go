// SPDX-License-Identifier: MIT

// Package mcprule holds the decisions of MCP's Streamable HTTP transport (revision 2026-07-28) as functions of strings:
// which protocol era a POST belongs to, whether its MCP-Protocol-Version, Mcp-Method and Mcp-Name headers agree with
// its body, how an Mcp-Name value is decoded, and whether its Origin and Host are allowed.
//
// The functions read no request, write no response and import no os, net, time, fmt or encoding/json, so the
// transport that applies them and a guard around another MCP server ask the same questions and get one answer. They
// start no process and read no environment.
package mcprule

import (
	"encoding/base64"
	"slices"
	"strings"
	"unicode/utf8"
)

// The error codes MCP defines in the sub-range -32020 to -32099 that the specification reserves for itself. No other
// code from that sub-range is emitted.
const (
	HeaderMismatch                  = -32020
	MissingRequiredClientCapability = -32021
	UnsupportedProtocolVersion      = -32022
)

// Modern lists the revisions that carry their protocol version and client capabilities in each request's _meta.
// Legacy lists the earlier revisions served statelessly: initialize is answered, and no session is ever minted.
var (
	Modern = []string{"2026-07-28"}
	Legacy = []string{"2025-11-25", "2025-06-18"}
)

// Era is the protocol era Decide assigns to one POST. The zero Era refuses, so an Era nobody set is never served.
type Era int

// The eras: EraRefused refuses the POST, EraModern serves it from its own _meta, EraLegacyInit answers a legacy
// initialize, and EraLegacy serves a legacy request without a session.
const (
	EraRefused Era = iota
	EraModern
	EraLegacyInit
	EraLegacy
)

// Verdict is a refusal: the HTTP status, the JSON-RPC error code and its message, and for UnsupportedProtocolVersion
// the served versions and the requested one (the error's data). The zero Verdict, Status 0, means accepted.
type Verdict struct {
	Status    int
	Code      int
	Message   string
	Supported []string
	Requested string
}

const (
	versionHeader  = "MCP-Protocol-Version"
	methodHeader   = "Mcp-Method"
	nameHeader     = "Mcp-Name"
	versionMetaKey = "io.modelcontextprotocol/protocolVersion"
	sentinelPrefix = "=?base64?"
	sentinelSuffix = "?="
	invalidParams  = -32602
)

// Served narrows the known versions, Modern then Legacy, to those listed in narrow, and never widens them: an entry
// of narrow that is not a known version is dropped. An empty narrow serves every known version.
func Served(narrow []string) []string {
	known := slices.Concat(Modern, Legacy)
	if len(narrow) == 0 {
		return known
	}
	served := make([]string, 0, len(known))
	for _, version := range known {
		if slices.Contains(narrow, version) {
			served = append(served, version)
		}
	}
	return served
}

// Decide assigns a POST its era from the protocol version in its _meta (metaVersion, when hasMeta), its method and
// its MCP-Protocol-Version header, against the served versions. The first rule that matches wins:
//
//  1. A _meta version is modern: the header must equal it byte for byte, else HeaderMismatch; it must be served and
//     modern, else UnsupportedProtocolVersion.
//  2. The method initialize is legacy initialize.
//  3. A header naming a served legacy version is legacy.
//  4. A header naming a served modern version, without a _meta version, is -32602: the _meta field is required.
//  5. Any other header is UnsupportedProtocolVersion; no header is HeaderMismatch.
//
// A refused POST gets EraRefused and a Verdict with Status 400; an accepted one gets its era and the zero Verdict.
func Decide(metaVersion string, hasMeta bool, method, headerVersion string, served []string) (Era, Verdict) {
	if hasMeta && metaVersion != "" {
		if headerVersion == "" {
			return EraRefused, missing(versionHeader)
		}
		if headerVersion != metaVersion {
			return EraRefused, mismatch(versionHeader, headerVersion, metaVersion)
		}
		if !slices.Contains(served, metaVersion) || !slices.Contains(Modern, metaVersion) {
			return EraRefused, unsupported(metaVersion, served)
		}
		return EraModern, Verdict{}
	}
	if method == "initialize" {
		return EraLegacyInit, Verdict{}
	}
	if slices.Contains(served, headerVersion) && slices.Contains(Legacy, headerVersion) {
		return EraLegacy, Verdict{}
	}
	if slices.Contains(served, headerVersion) && slices.Contains(Modern, headerVersion) {
		msg := "Invalid params: required _meta field missing: " + versionMetaKey
		return EraRefused, refuse(invalidParams, msg)
	}
	if headerVersion != "" {
		return EraRefused, unsupported(headerVersion, served)
	}
	return EraRefused, missing(versionHeader)
}

// CheckHeaders decides whether a modern request's Mcp-Method header (hdrMethod) equals its body's method, and, when
// nameRequired, whether its Mcp-Name header (hdrName), decoded by DecodeHeaderValue, equals the body's name or URI. A
// missing, different or undecodable header is HeaderMismatch; values compare case-sensitively.
func CheckHeaders(method, name, hdrMethod, hdrName string, nameRequired bool) Verdict {
	if hdrMethod == "" {
		return missing(methodHeader)
	}
	if hdrMethod != method {
		return mismatch(methodHeader, hdrMethod, method)
	}
	if !nameRequired {
		return Verdict{}
	}
	if hdrName == "" {
		return missing(nameHeader)
	}
	decoded, ok := DecodeHeaderValue(hdrName)
	if !ok {
		msg := "Invalid characters in " + nameHeader + " header value"
		return refuse(HeaderMismatch, msg)
	}
	if decoded != name {
		return mismatch(nameHeader, hdrName, name)
	}
	return Verdict{}
}

// DecodeHeaderValue decodes an Mcp-Name or Mcp-Param header value. A value wrapped in the sentinel =?base64?...?=
// (lowercase markers, compared case-sensitively) is the standard Base64 of UTF-8 text. Any other value is taken as
// written and must hold only visible ASCII, space and tab, with no leading or trailing space or tab. It reports false
// for a value that is neither.
func DecodeHeaderValue(v string) (string, bool) {
	if len(v) >= len(sentinelPrefix)+len(sentinelSuffix) && strings.HasPrefix(v, sentinelPrefix) &&
		strings.HasSuffix(v, sentinelSuffix) {
		encoded := v[len(sentinelPrefix) : len(v)-len(sentinelSuffix)]
		if strings.ContainsAny(encoded, "\r\n") {
			return "", false
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || !utf8.Valid(decoded) {
			return "", false
		}
		return string(decoded), true
	}
	if v != strings.Trim(v, " \t") {
		return "", false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c != '\t' && (c < 0x20 || c > 0x7e) {
			return "", false
		}
	}
	return v, true
}

// NameRequired reports whether a modern request with this method must carry an Mcp-Name header.
func NameRequired(method string) bool {
	switch method {
	case "tools/call", "resources/read", "prompts/get":
		return true
	}
	return false
}

// OriginAllowed decides whether a request's Origin header is allowed: an absent origin is, and a present one only when
// allowed lists it exactly. No entry is a wildcard.
func OriginAllowed(origin string, allowed []string) bool {
	return origin == "" || slices.Contains(allowed, origin)
}

// HostAllowed decides whether a request's Host header is allowed. When the local address the request arrived on is a
// loopback address, the host without its port and IPv6 brackets must be localhost, 127.0.0.1, ::1 or one of extra,
// compared case-insensitively; this refuses a DNS-rebound name. Any host is allowed on a non-loopback address.
func HostAllowed(localIsLoopback bool, host string, extra []string) bool {
	if !localIsLoopback {
		return true
	}
	name := hostName(host)
	if name == "" {
		return false
	}
	if name == "127.0.0.1" || name == "::1" || strings.EqualFold(name, "localhost") {
		return true
	}
	for _, allowed := range extra {
		if strings.EqualFold(name, allowed) {
			return true
		}
	}
	return false
}

// hostName strips the port from a Host header value and the brackets from an IPv6 literal. It returns "" for a
// bracketed value that is malformed.
func hostName(host string) string {
	if rest, ok := strings.CutPrefix(host, "["); ok {
		name, after, found := strings.Cut(rest, "]")
		if !found || (after != "" && after[0] != ':') {
			return ""
		}
		return name
	}
	if strings.Count(host, ":") == 1 {
		name, _, _ := strings.Cut(host, ":")
		return name
	}
	return host
}

func refuse(code int, message string) Verdict {
	return Verdict{Status: 400, Code: code, Message: message}
}

func missing(header string) Verdict {
	msg := header + " header missing"
	return refuse(HeaderMismatch, msg)
}

func mismatch(header, got, want string) Verdict {
	msg := "Header mismatch: " + header + " header value '" + got + "' does not match body value '" + want + "'"
	return refuse(HeaderMismatch, msg)
}

func unsupported(requested string, served []string) Verdict {
	msg := "Unsupported protocol version '" + requested + "'"
	v := refuse(UnsupportedProtocolVersion, msg)
	v.Supported = slices.Clone(served)
	v.Requested = requested
	return v
}
