// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fiberfx/mx-echo/v6"
)

// goldenTrustRange is the range of the proxies the golden cases trust explicitly.
var goldenTrustRange = func() *net.IPNet {
	_, n, err := net.ParseCIDR("203.0.113.0/24")
	if err != nil {
		panic(err)
	}
	return n
}()

// TestTrustServeGolden pins what a handler sees as RealIP and Scheme through ServeHTTP for each IP and scheme
// extractor. The expected bodies were taken at v6.5.0 and do not change when the trust bodies move.
func TestTrustServeGolden(t *testing.T) {
	tests := []struct {
		name       string
		ip         echo.IPExtractor
		scheme     echo.SchemeExtractor
		remoteAddr string
		header     http.Header
		want       string
	}{
		{
			name:       "xff trusted range stops at a private hop when private is not trusted",
			ip:         echo.ExtractIPFromXFFHeader(echo.TrustIPRange(goldenTrustRange), echo.TrustPrivateNet(false)),
			remoteAddr: "203.0.113.5:1234",
			header:     http.Header{echo.HeaderXForwardedFor: {"198.51.100.7, 10.0.0.2"}},
			want:       "10.0.0.2 http",
		},
		{
			name:       "xff trusted range walks to the client",
			ip:         echo.ExtractIPFromXFFHeader(echo.TrustIPRange(goldenTrustRange), echo.TrustPrivateNet(false)),
			remoteAddr: "203.0.113.5:1234",
			header:     http.Header{echo.HeaderXForwardedFor: {"198.51.100.7, 203.0.113.9"}},
			want:       "198.51.100.7 http",
		},
		{
			name:       "xff from an untrusted hop is ignored",
			ip:         echo.ExtractIPFromXFFHeader(),
			remoteAddr: "198.51.100.9:80",
			header:     http.Header{echo.HeaderXForwardedFor: {"1.2.3.4"}},
			want:       "198.51.100.9 http",
		},
		{
			name:       "x-real-ip from a trusted peer",
			ip:         echo.ExtractIPFromRealIPHeader(),
			remoteAddr: "10.0.0.1:5555",
			header:     http.Header{echo.HeaderXRealIP: {"[2001:db8::1]"}},
			want:       "2001:db8::1 http",
		},
		{
			name:       "x-real-ip from an untrusted peer is ignored",
			ip:         echo.ExtractIPFromRealIPHeader(),
			remoteAddr: "198.51.100.9:80",
			header:     http.Header{echo.HeaderXRealIP: {"1.2.3.4"}},
			want:       "198.51.100.9 http",
		},
		{
			name:       "legacy extractor takes the first xff address",
			ip:         echo.LegacyIPExtractor(),
			remoteAddr: "198.51.100.9:80",
			header:     http.Header{echo.HeaderXForwardedFor: {"1.2.3.4, 10.0.0.2"}},
			want:       "1.2.3.4 http",
		},
		{
			name:       "legacy extractors trust any peer",
			ip:         echo.LegacyIPExtractor(),
			scheme:     echo.LegacySchemeExtractor(),
			remoteAddr: "198.51.100.9:80",
			header:     http.Header{echo.HeaderXRealIP: {"1.2.3.4"}, echo.HeaderXForwardedProto: {"https"}},
			want:       "1.2.3.4 https",
		},
		{
			name:       "direct extractors ignore every header",
			ip:         echo.ExtractIPDirect(),
			scheme:     echo.ExtractSchemeDirect(),
			remoteAddr: "10.0.0.1:5555",
			header:     http.Header{echo.HeaderXForwardedFor: {"1.2.3.4"}, echo.HeaderXForwardedProto: {"https"}},
			want:       "10.0.0.1 http",
		},
		{
			name:       "x-forwarded-proto from a trusted peer",
			remoteAddr: "10.0.0.1:5555",
			header:     http.Header{echo.HeaderXForwardedProto: {"https"}},
			want:       "10.0.0.1 https",
		},
		{
			name:       "x-forwarded-proto from an untrusted peer is ignored",
			remoteAddr: "198.51.100.9:80",
			header:     http.Header{echo.HeaderXForwardedProto: {"https"}},
			want:       "198.51.100.9 http",
		},
		{
			name:       "x-forwarded-proto takes the nearest value from a trusted range",
			scheme:     echo.ExtractSchemeFromHeaders(echo.TrustIPRange(goldenTrustRange)),
			remoteAddr: "203.0.113.5:443",
			header:     http.Header{echo.HeaderXForwardedProto: {"http, https"}},
			want:       "203.0.113.5 https",
		},
		{
			name:       "x-forwarded-ssl from loopback",
			remoteAddr: "127.0.0.1:1",
			header:     http.Header{echo.HeaderXForwardedSsl: {"on"}},
			want:       "127.0.0.1 https",
		},
		{
			name:       "link-local peer with an ipv6 zone",
			remoteAddr: "[fe80::1%eth0]:443",
			header:     http.Header{echo.HeaderXForwardedProto: {"wss"}},
			want:       "fe80::1 wss",
		},
		{
			name:       "unix socket peer is trusted like loopback",
			remoteAddr: "@",
			header:     http.Header{echo.HeaderXForwardedProto: {"https"}},
			want:       " https",
		},
		{
			name:       "unix socket peer is not trusted without loopback",
			scheme:     echo.ExtractSchemeFromHeaders(echo.TrustLoopback(false)),
			remoteAddr: "/run/app.sock",
			header:     http.Header{echo.HeaderXForwardedProto: {"https"}},
			want:       " http",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			e.IPExtractor = tt.ip
			e.SchemeExtractor = tt.scheme
			e.GET("/who", func(c *echo.Context) error {
				return c.String(http.StatusOK, c.RealIP()+" "+c.Scheme())
			})
			req := httptest.NewRequest(http.MethodGet, "/who", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.header {
				req.Header[k] = v
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Body.String(); got != tt.want {
				t.Errorf("body %q, want %q", got, tt.want)
			}
		})
	}
}
