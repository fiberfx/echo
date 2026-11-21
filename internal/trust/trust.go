// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package trust holds the bodies of the root's IP and scheme extractors: which direct peers are trusted, and what
// the forwarding headers of a trusted peer say about the client's address and the request's scheme. The root keeps
// every exported name and forwards to this package.
package trust

import (
	"net"
	"net/http"
	"strings"
)

// The forwarding headers, in the canonical form that indexing http.Header directly needs. Each equals the root's
// constant for the same header.
const (
	HeaderXForwardedFor      = "X-Forwarded-For"
	HeaderXRealIP            = "X-Real-Ip"
	HeaderXForwardedProto    = "X-Forwarded-Proto"
	HeaderXForwardedProtocol = "X-Forwarded-Protocol"
	HeaderXForwardedSsl      = "X-Forwarded-Ssl"
	HeaderXUrlScheme         = "X-Url-Scheme"
)

// Checker decides which IP addresses are trusted: the extra Ranges, and loopback, link-local and private network
// addresses when their flags are set.
type Checker struct {
	Ranges     []*net.IPNet
	Loopback   bool
	LinkLocal  bool
	PrivateNet bool
}

// Default returns the checker the trust options start from: loopback, link-local and private network addresses are
// trusted.
func Default() Checker {
	return Checker{Loopback: true, LinkLocal: true, PrivateNet: true}
}

// Trusts reports whether ip is trusted.
func (c *Checker) Trusts(ip net.IP) bool {
	if c.Loopback && ip.IsLoopback() {
		return true
	}
	if c.LinkLocal && ip.IsLinkLocalUnicast() {
		return true
	}
	if c.PrivateNet && ip.IsPrivate() {
		return true
	}
	for _, trustedRange := range c.Ranges {
		if trustedRange.Contains(ip) {
			return true
		}
	}
	return false
}

// DirectIP returns the address of the direct peer (http.Request.RemoteAddr) without its port and IPv6 zone, or ""
// when RemoteAddr holds no IP address.
func DirectIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	host = StripIPv6Zone(host)
	if err != nil && net.ParseIP(host) == nil {
		return ""
	}
	return host
}

// StripIPv6Zone removes the zone of an IPv6 address, for example "fe80::1%eth0" becomes "fe80::1".
func StripIPv6Zone(host string) string {
	if strings.Contains(host, ":") {
		host, _, _ = strings.Cut(host, "%")
	}
	return host
}

// FromRealIP returns the address in the `X-Real-Ip` header when the direct peer is trusted and the header holds an
// IP address, and the direct peer's address otherwise.
func FromRealIP(req *http.Request, c *Checker) string {
	directIP := DirectIP(req)
	peerIP := net.ParseIP(directIP)
	if peerIP == nil || !c.Trusts(peerIP) {
		return directIP
	}

	realIP := req.Header.Get(HeaderXRealIP)
	if realIP != "" {
		realIP = strings.TrimPrefix(realIP, "[")
		realIP = strings.TrimSuffix(realIP, "]")
		if ip := net.ParseIP(realIP); ip != nil {
			return realIP
		}
	}
	return directIP
}

// FromXFF returns the nearest untrusted address of the `X-Forwarded-For` header and the direct peer. If all of them
// are trusted it returns the furthest one (XFF[0]); if one is not an IP address it returns the direct peer's.
func FromXFF(req *http.Request, c *Checker) string {
	directIP := DirectIP(req)
	xffs := req.Header[HeaderXForwardedFor]
	if len(xffs) == 0 {
		return directIP
	}
	ips := append(strings.Split(strings.Join(xffs, ","), ","), directIP)
	for i := len(ips) - 1; i >= 0; i-- {
		ips[i] = strings.TrimSpace(ips[i])
		ips[i] = strings.TrimPrefix(ips[i], "[")
		ips[i] = strings.TrimSuffix(ips[i], "]")
		ip := net.ParseIP(ips[i])
		if ip == nil {
			// Unable to parse IP; cannot trust entire records
			return directIP
		}
		if !c.Trusts(ip) {
			return ip.String()
		}
	}
	// All of the IPs are trusted; return first element because it is furthest from server (best effort strategy).
	return strings.TrimSpace(ips[0])
}

// Legacy returns the first address of `X-Forwarded-For`, else `X-Real-Ip`, else the direct peer's, trusting the
// headers of any peer.
func Legacy(req *http.Request) string {
	if ip := req.Header.Get(HeaderXForwardedFor); ip != "" {
		i := strings.IndexAny(ip, ",")
		if i > 0 {
			ip = strings.TrimSpace(ip[:i])
		}
		ip = strings.TrimPrefix(ip, "[")
		ip = strings.TrimSuffix(ip, "]")
		return ip
	}
	if ip := req.Header.Get(HeaderXRealIP); ip != "" {
		ip = strings.TrimPrefix(ip, "[")
		ip = strings.TrimSuffix(ip, "]")
		return ip
	}
	return DirectIP(req)
}

// SchemeDirect returns `https` for a TLS connection and `http` otherwise.
func SchemeDirect(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

// LegacyScheme returns `https` for a TLS connection, and otherwise the scheme of the forwarding headers of any peer.
func LegacyScheme(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	return SchemeFromHeaders(req)
}

// Scheme returns `https` for a TLS connection, the scheme of the forwarding headers when the direct peer is trusted,
// and `http` otherwise.
func Scheme(req *http.Request, c *Checker) string {
	if req.TLS != nil {
		return "https"
	}
	if !TrustedPeer(req.RemoteAddr, c) {
		return "http"
	}
	return SchemeFromHeaders(req)
}

// TrustedPeer reports whether the direct peer of the request is trusted to set forwarding headers.
func TrustedPeer(remoteAddr string, c *Checker) bool {
	// Unix socket peers are on the same host, so they are trusted like loopback addresses. net/http reports them
	// as an empty string, as "@" or "@name" (Linux, unnamed or abstract socket) or as the path of a bound socket.
	if remoteAddr == "" || remoteAddr[0] == '@' || remoteAddr[0] == '/' {
		return c.Loopback
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if i := strings.IndexByte(host, '%'); i != -1 { // IPv6 zone, e.g. "fe80::1%eth0"
		host = host[:i]
	}
	ip := net.ParseIP(host)
	return ip != nil && c.Trusts(ip)
}

// SchemeFromHeaders returns the scheme of the forwarding headers, or `http` when they name none.
func SchemeFromHeaders(req *http.Request) string {
	// When X-Forwarded-Proto is present it is the only header used: a trusted proxy sets it, and the other headers
	// might be client-supplied values that the proxy did not remove. With several values (repeated header lines or a
	// comma-separated list) the last one was added by the nearest proxy.
	if values := req.Header.Values(HeaderXForwardedProto); len(values) > 0 && values[len(values)-1] != "" {
		last := values[len(values)-1]
		if _, nearest, found := strings.CutLast(last, ","); found {
			last = nearest
		}
		if scheme, ok := CanonicalProto(strings.TrimSpace(last)); ok {
			return scheme
		}
		return "http"
	}
	if scheme, ok := CanonicalProto(req.Header.Get(HeaderXForwardedProtocol)); ok {
		return scheme
	}
	if ssl := req.Header.Get(HeaderXForwardedSsl); ssl == "on" {
		return "https"
	}
	if scheme, ok := CanonicalProto(req.Header.Get(HeaderXUrlScheme)); ok {
		return scheme
	}
	return "http"
}

// CanonicalProto returns the lowercase form of proto if it is `http`, `https`, `ws` or `wss` (in any case).
func CanonicalProto(proto string) (string, bool) {
	for _, p := range [...]string{"http", "https", "ws", "wss"} {
		if strings.EqualFold(proto, p) {
			return p, true
		}
	}
	return "", false
}
