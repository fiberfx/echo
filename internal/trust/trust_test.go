// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package trust_test

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"

	"github.com/fiberfx/mx-echo/v7/internal/trust"
)

func cidr(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func TestTrusts(t *testing.T) {
	tests := []struct {
		name    string
		checker trust.Checker
		ip      string
		want    bool
	}{
		{name: "default trusts loopback", checker: trust.Default(), ip: "127.0.0.1", want: true},
		{name: "default trusts link-local", checker: trust.Default(), ip: "fe80::1", want: true},
		{name: "default trusts a private IPv4 network", checker: trust.Default(), ip: "10.0.0.1", want: true},
		{name: "default trusts a unique local IPv6 address", checker: trust.Default(), ip: "fc00::1", want: true},
		{name: "default does not trust a public address", checker: trust.Default(), ip: "203.0.113.1", want: false},
		{name: "an empty checker trusts nothing", checker: trust.Checker{}, ip: "127.0.0.1", want: false},
		{name: "loopback alone does not trust a private network", checker: trust.Checker{Loopback: true}, ip: "10.0.0.1", want: false},
		{
			name:    "an extra range is trusted",
			checker: trust.Checker{Ranges: []*net.IPNet{cidr("2001:db8::103/48")}},
			ip:      "2001:0db8:0000:0000:0000:0000:0000:0103",
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.checker.Trusts(net.ParseIP(tt.ip)); got != tt.want {
				t.Errorf("Trusts(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestFromXFF(t *testing.T) {
	tests := []struct {
		name       string
		checker    trust.Checker
		remoteAddr string
		xff        []string
		want       string
	}{
		{
			name:       "no header gives the direct peer",
			checker:    trust.Default(),
			remoteAddr: "203.0.113.1:8080",
			want:       "203.0.113.1",
		},
		{
			name:       "an address that does not parse gives the direct peer",
			checker:    trust.Default(),
			remoteAddr: "127.0.0.1:8080",
			xff:        []string{"xxx.yyy.zzz.ccc, 127.0.0.2"},
			want:       "127.0.0.1",
		},
		{
			name:       "all trusted gives the furthest address",
			checker:    trust.Default(),
			remoteAddr: "127.0.0.1:8080",
			xff:        []string{"127.0.0.3, 127.0.0.2, 127.0.0.1"},
			want:       "127.0.0.3",
		},
		{
			name:       "an untrusted direct peer is the answer",
			checker:    trust.Default(),
			remoteAddr: "203.0.113.1:8080",
			xff:        []string{"203.0.113.199"},
			want:       "203.0.113.1",
		},
		{
			name:       "the nearest untrusted hop behind a trusted range",
			checker:    trust.Checker{Ranges: []*net.IPNet{cidr("203.0.113.199/24")}, Loopback: true, LinkLocal: true, PrivateNet: true},
			remoteAddr: "127.0.0.1:8080",
			xff:        []string{"203.0.1.100, 203.0.100.100, 203.0.113.199, 192.168.1.100"},
			want:       "203.0.100.100",
		},
		{
			name:       "a private hop is untrusted without the private flag",
			checker:    trust.Checker{Loopback: true, LinkLocal: true},
			remoteAddr: "127.0.0.1:1",
			xff:        []string{"198.51.100.7, 10.0.0.2"},
			want:       "10.0.0.2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{RemoteAddr: tt.remoteAddr, Header: http.Header{}}
			if tt.xff != nil {
				req.Header[trust.HeaderXForwardedFor] = tt.xff
			}
			if got := trust.FromXFF(req, &tt.checker); got != tt.want {
				t.Errorf("FromXFF = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScheme(t *testing.T) {
	def := trust.Default()
	tests := []struct {
		name       string
		tls        bool
		remoteAddr string
		header     http.Header
		want       string
	}{
		{name: "tls is https", tls: true, remoteAddr: "203.0.113.1:443", want: "https"},
		{name: "an untrusted peer's header is ignored", remoteAddr: "203.0.113.1:80", header: http.Header{trust.HeaderXForwardedProto: {"https"}}, want: "http"},
		{name: "a trusted peer's header is used", remoteAddr: "10.0.0.1:80", header: http.Header{trust.HeaderXForwardedProto: {"https"}}, want: "https"},
		{name: "the proto is lowercased", remoteAddr: "10.0.0.1:80", header: http.Header{trust.HeaderXForwardedProto: {"HTTPS"}}, want: "https"},
		{name: "the last proto line wins", remoteAddr: "10.0.0.1:80", header: http.Header{trust.HeaderXForwardedProto: {"http", "https"}}, want: "https"},
		{
			name:       "an unknown proto is http and hides the other headers",
			remoteAddr: "10.0.0.1:80",
			header:     http.Header{trust.HeaderXForwardedProto: {"ftp"}, trust.HeaderXForwardedSsl: {"on"}},
			want:       "http",
		},
		{name: "x-forwarded-protocol", remoteAddr: "10.0.0.1:80", header: http.Header{trust.HeaderXForwardedProtocol: {"wss"}}, want: "wss"},
		{name: "x-forwarded-ssl", remoteAddr: "10.0.0.1:80", header: http.Header{trust.HeaderXForwardedSsl: {"on"}}, want: "https"},
		{name: "x-url-scheme", remoteAddr: "10.0.0.1:80", header: http.Header{trust.HeaderXUrlScheme: {"https"}}, want: "https"},
		{name: "a unix socket peer is trusted", remoteAddr: "@", header: http.Header{trust.HeaderXForwardedProto: {"https"}}, want: "https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{RemoteAddr: tt.remoteAddr, Header: tt.header}
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if got := trust.Scheme(req, &def); got != tt.want {
				t.Errorf("Scheme = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrustedPeer(t *testing.T) {
	tests := []struct {
		name       string
		checker    trust.Checker
		remoteAddr string
		want       bool
	}{
		{name: "an empty address is a unix socket", checker: trust.Default(), remoteAddr: "", want: true},
		{name: "an abstract unix socket", checker: trust.Default(), remoteAddr: "@name", want: true},
		{name: "a unix socket path without loopback", checker: trust.Checker{}, remoteAddr: "/tmp/s.sock", want: false},
		{name: "link-local with a zone and a port", checker: trust.Default(), remoteAddr: "[fe80::1%eth0]:443", want: true},
		{name: "link-local with a zone and no port", checker: trust.Default(), remoteAddr: "fe80::1%eth0", want: true},
		{name: "a public address", checker: trust.Default(), remoteAddr: "203.0.113.1:80", want: false},
		{name: "a public address in a trusted range", checker: trust.Checker{Ranges: []*net.IPNet{cidr("203.0.113.0/24")}}, remoteAddr: "203.0.113.1:80", want: true},
		{name: "a host name", checker: trust.Default(), remoteAddr: "localhost:80", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trust.TrustedPeer(tt.remoteAddr, &tt.checker); got != tt.want {
				t.Errorf("TrustedPeer(%q) = %v, want %v", tt.remoteAddr, got, tt.want)
			}
		})
	}
}
