// SPDX-License-Identifier: MIT

package mcprule_test

import (
	"slices"
	"testing"

	"github.com/fiberfx/mx-echo/v6/internal/mcprule"
)

var served = mcprule.Served(nil)

func accepted(v mcprule.Verdict) bool {
	return v.Status == 0 && v.Code == 0 && v.Message == "" && v.Supported == nil && v.Requested == ""
}

func TestServed(t *testing.T) {
	if want := []string{"2026-07-28", "2025-11-25", "2025-06-18"}; !slices.Equal(served, want) {
		t.Fatalf("Served(nil) = %q, want %q", served, want)
	}
	if got := mcprule.Served([]string{"2025-06-18", "2026-07-28"}); !slices.Equal(got, []string{"2026-07-28", "2025-06-18"}) {
		t.Errorf("Served narrowed to two = %q, want both in the known order", got)
	}
	got := mcprule.Served(nil)
	got[0] = "1900-01-01"
	if mcprule.Modern[0] != "2026-07-28" {
		t.Error("changing what Served returned changed Modern")
	}
}

func TestServedNeverWidens(t *testing.T) {
	if got := mcprule.Served([]string{"2099-01-01"}); len(got) != 0 {
		t.Errorf("Served of an unknown version = %q, want none", got)
	}
	if got := mcprule.Served([]string{"2099-01-01", "2025-11-25", "2024-11-05"}); !slices.Equal(got, []string{"2025-11-25"}) {
		t.Errorf("Served with unknown entries = %q, want only 2025-11-25", got)
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		meta    string
		hasMeta bool
		method  string
		header  string
		era     mcprule.Era
	}{
		{"modern", "2026-07-28", true, "tools/list", "2026-07-28", mcprule.EraModern},
		{"modern initialize is modern", "2026-07-28", true, "initialize", "2026-07-28", mcprule.EraModern},
		{"legacy initialize", "", false, "initialize", "", mcprule.EraLegacyInit},
		{"legacy initialize with a header", "", false, "initialize", "2025-11-25", mcprule.EraLegacyInit},
		{"legacy 2025-11-25", "", false, "tools/list", "2025-11-25", mcprule.EraLegacy},
		{"legacy 2025-06-18", "", false, "tools/call", "2025-06-18", mcprule.EraLegacy},
	}
	for _, c := range cases {
		era, v := mcprule.Decide(c.meta, c.hasMeta, c.method, c.header, served)
		if era != c.era || !accepted(v) {
			t.Errorf("%s: Decide = %d, %+v; want era %d and the zero verdict", c.name, era, v, c.era)
		}
	}
	// One legacy version served is enough for a legacy initialize.
	if era, v := mcprule.Decide("", false, "initialize", "", []string{"2025-06-18"}); era != mcprule.EraLegacyInit || !accepted(v) {
		t.Errorf("legacy initialize with 2025-06-18 served: Decide = %d, %+v; want era %d", era, v, mcprule.EraLegacyInit)
	}
}

func TestDecideViolation(t *testing.T) {
	cases := []struct {
		name      string
		meta      string
		hasMeta   bool
		method    string
		header    string
		served    []string
		code      int
		message   string
		supported []string
		requested string
	}{
		{"header differs from _meta", "2026-07-28", true, "tools/list", "2025-11-25", served, mcprule.HeaderMismatch, "Header mismatch: MCP-Protocol-Version header value '2025-11-25' does not match body value '2026-07-28'", nil, ""},
		{"header missing beside _meta", "2026-07-28", true, "tools/list", "", served, mcprule.HeaderMismatch, "MCP-Protocol-Version header missing", nil, ""},
		{"_meta version unknown", "1900-01-01", true, "tools/list", "1900-01-01", served, mcprule.UnsupportedProtocolVersion, "Unsupported protocol version '1900-01-01'", served, "1900-01-01"},
		{"_meta version legacy", "2025-11-25", true, "tools/list", "2025-11-25", served, mcprule.UnsupportedProtocolVersion, "Unsupported protocol version '2025-11-25'", served, "2025-11-25"},
		{"_meta version narrowed out", "2026-07-28", true, "tools/list", "2026-07-28", []string{"2025-11-25"}, mcprule.UnsupportedProtocolVersion, "Unsupported protocol version '2026-07-28'", []string{"2025-11-25"}, "2026-07-28"},
		{"modern header without _meta", "", false, "tools/list", "2026-07-28", served, -32602, "Invalid params: required _meta field missing: io.modelcontextprotocol/protocolVersion", nil, ""},
		{"modern header with an empty _meta version", "", true, "tools/list", "2026-07-28", served, -32602, "Invalid params: required _meta field missing: io.modelcontextprotocol/protocolVersion", nil, ""},
		{"unknown header without _meta", "", false, "tools/list", "2024-11-05", served, mcprule.UnsupportedProtocolVersion, "Unsupported protocol version '2024-11-05'", served, "2024-11-05"},
		{"modern header narrowed out", "", false, "tools/list", "2026-07-28", []string{"2025-11-25"}, mcprule.UnsupportedProtocolVersion, "Unsupported protocol version '2026-07-28'", []string{"2025-11-25"}, "2026-07-28"},
		{"legacy header narrowed out", "", false, "tools/list", "2025-06-18", []string{"2025-11-25"}, mcprule.UnsupportedProtocolVersion, "Unsupported protocol version '2025-06-18'", []string{"2025-11-25"}, "2025-06-18"},
		{"unknown version in served is not served", "", false, "tools/list", "2099-01-01", []string{"2099-01-01"}, mcprule.UnsupportedProtocolVersion, "Unsupported protocol version '2099-01-01'", []string{"2099-01-01"}, "2099-01-01"},
		{"no _meta and no header", "", false, "tools/list", "", served, mcprule.HeaderMismatch, "MCP-Protocol-Version header missing", nil, ""},
		{"legacy initialize with only a modern version served", "", false, "initialize", "", []string{"2026-07-28"}, mcprule.UnsupportedProtocolVersion, "Unsupported protocol version ''", []string{"2026-07-28"}, ""},
		{"legacy initialize with a narrow that serves none", "", false, "initialize", "2025-06-18", mcprule.Served([]string{"2025-6-18"}), mcprule.UnsupportedProtocolVersion, "Unsupported protocol version ''", []string{}, ""},
	}
	for _, c := range cases {
		era, v := mcprule.Decide(c.meta, c.hasMeta, c.method, c.header, c.served)
		if era != mcprule.EraRefused || v.Status != 400 || v.Code != c.code || v.Message != c.message {
			t.Errorf("%s: Decide = %d, %d, %d, %q; want era %d, 400, %d, %q", c.name, era, v.Status, v.Code, v.Message,
				mcprule.EraRefused, c.code, c.message)
		}
		if !slices.Equal(v.Supported, c.supported) || v.Requested != c.requested {
			t.Errorf("%s: Decide data = %q, %q; want %q, %q", c.name, v.Supported, v.Requested, c.supported, c.requested)
		}
	}
}

func TestDecideSupportedIsACopy(t *testing.T) {
	list := []string{"2026-07-28", "2025-11-25"}
	_, v := mcprule.Decide("1900-01-01", true, "tools/list", "1900-01-01", list)
	v.Supported[0] = "changed"
	if list[0] != "2026-07-28" {
		t.Error("changing the verdict's Supported changed the caller's served list")
	}
}

func TestEraZeroRefuses(t *testing.T) {
	var era mcprule.Era
	if era != mcprule.EraRefused {
		t.Errorf("the zero Era is %d, want EraRefused (%d)", era, mcprule.EraRefused)
	}
}

func TestCheckHeaders(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		body      string
		hdrMethod string
		hdrName   string
	}{
		{"tools/call", "tools/call", "get_weather", "tools/call", "get_weather"},
		{"resources/read", "resources/read", "file:///project/a.txt", "resources/read", "file:///project/a.txt"},
		{"prompts/get", "prompts/get", "code_review", "prompts/get", "code_review"},
		{"name in base64", "tools/call", "Hello, 世界", "tools/call", "=?base64?SGVsbG8sIOS4lueVjA==?="},
		{"padded name in base64", "tools/call", " padded ", "tools/call", "=?base64?IHBhZGRlZCA=?="},
		{"sentinel-shaped name in base64", "tools/call", "=?base64?literal?=", "tools/call", "=?base64?PT9iYXNlNjQ/bGl0ZXJhbD89?="},
		{"no name needed", "tools/list", "", "tools/list", ""},
		{"a name not needed is not read", "tools/list", "", "tools/list", " padded "},
	}
	for _, c := range cases {
		if v := mcprule.CheckHeaders(c.method, c.body, c.hdrMethod, c.hdrName, mcprule.NameRequired(c.method)); !accepted(v) {
			t.Errorf("%s: CheckHeaders = %+v, want the zero verdict", c.name, v)
		}
	}
}

func TestCheckHeadersViolation(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		body      string
		hdrMethod string
		hdrName   string
		message   string
	}{
		{"Mcp-Method missing", "tools/list", "", "", "", "Mcp-Method header missing"},
		{"Mcp-Method differs", "tools/list", "", "tools/call", "", "Header mismatch: Mcp-Method header value 'tools/call' does not match body value 'tools/list'"},
		{"Mcp-Method differs in case", "tools/list", "", "Tools/List", "", "Header mismatch: Mcp-Method header value 'Tools/List' does not match body value 'tools/list'"},
		{"Mcp-Name missing on tools/call", "tools/call", "get_weather", "tools/call", "", "Mcp-Name header missing"},
		{"Mcp-Name missing on resources/read", "resources/read", "file:///a.txt", "resources/read", "", "Mcp-Name header missing"},
		{"Mcp-Name missing on prompts/get", "prompts/get", "code_review", "prompts/get", "", "Mcp-Name header missing"},
		{"Mcp-Name differs", "tools/call", "bar", "tools/call", "foo", "Header mismatch: Mcp-Name header value 'foo' does not match body value 'bar'"},
		{"uppercase sentinel is a literal", "tools/call", "foo", "tools/call", "=?BASE64?Zm9v?=", "Header mismatch: Mcp-Name header value '=?BASE64?Zm9v?=' does not match body value 'foo'"},
		{"base64 decodes to another name", "tools/call", "bar", "tools/call", "=?base64?Zm9v?=", "Header mismatch: Mcp-Name header value '=?base64?Zm9v?=' does not match body value 'bar'"},
		{"padded plain name", "tools/call", " padded ", "tools/call", " padded ", "Invalid characters in Mcp-Name header value"},
		{"non-ASCII plain name", "tools/call", "café", "tools/call", "café", "Invalid characters in Mcp-Name header value"},
		{"bad base64", "tools/call", "foo", "tools/call", "=?base64?Zm9v!?=", "Invalid characters in Mcp-Name header value"},
	}
	for _, c := range cases {
		v := mcprule.CheckHeaders(c.method, c.body, c.hdrMethod, c.hdrName, mcprule.NameRequired(c.method))
		if v.Status != 400 || v.Code != mcprule.HeaderMismatch || v.Message != c.message {
			t.Errorf("%s: CheckHeaders = %d, %d, %q; want 400, %d, %q", c.name, v.Status, v.Code, v.Message,
				mcprule.HeaderMismatch, c.message)
		}
	}
}

func TestDecodeHeaderValue(t *testing.T) {
	cases := []struct{ in, out string }{
		{"us-west1", "us-west1"},
		{"=?base64?SGVsbG8sIOS4lueVjA==?=", "Hello, 世界"},
		{"=?base64?IHBhZGRlZCA=?=", " padded "},
		{"=?base64?bGluZTEKbGluZTI=?=", "line1\nline2"},
		{"=?base64?PT9iYXNlNjQ/bGl0ZXJhbD89?=", "=?base64?literal?="},
		{"=?base64??=", ""},
		{"=?BASE64?Zm9v?=", "=?BASE64?Zm9v?="},
		{"=?base64?=", "=?base64?="},
		{"inner\ttab and space", "inner\ttab and space"},
		{"", ""},
	}
	for _, c := range cases {
		if got, ok := mcprule.DecodeHeaderValue(c.in); !ok || got != c.out {
			t.Errorf("DecodeHeaderValue(%q) = %q, %t; want %q, true", c.in, got, ok, c.out)
		}
	}
}

func TestDecodeHeaderValueViolation(t *testing.T) {
	for _, in := range []string{
		" padded ",
		"padded ",
		" padded",
		"\tpadded",
		"padded\t",
		"café",
		"line1\nline2",
		"bell\a",
		"del\x7f",
		"=?base64?Zm9v!?=",
		"=?base64?Zm8?=",
		"=?base64?/w==?=",
		"=?base64?Zm9v\n?=",
		"=?base64?Zm9v?= ",
	} {
		if got, ok := mcprule.DecodeHeaderValue(in); ok {
			t.Errorf("DecodeHeaderValue(%q) = %q, true; want false", in, got)
		}
	}
}

func TestNameRequired(t *testing.T) {
	for _, method := range []string{"tools/call", "resources/read", "prompts/get"} {
		if !mcprule.NameRequired(method) {
			t.Errorf("NameRequired(%q) = false, want true", method)
		}
	}
	for _, method := range []string{"tools/list", "initialize", "server/discover", "Tools/Call", ""} {
		if mcprule.NameRequired(method) {
			t.Errorf("NameRequired(%q) = true, want false", method)
		}
	}
}

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		origin  string
		allowed []string
		want    bool
	}{
		{"", nil, true},
		{"https://app.example", []string{"https://app.example"}, true},
		{"https://evil.example", []string{"https://app.example"}, false},
		{"https://app.example", nil, false},
		{"https://app.example", []string{"*"}, false},
		{"HTTPS://APP.EXAMPLE", []string{"https://app.example"}, false},
		{"https://app.example:8443", []string{"https://app.example"}, false},
		{"null", []string{"https://app.example"}, false},
	}
	for _, c := range cases {
		if got := mcprule.OriginAllowed(c.origin, c.allowed); got != c.want {
			t.Errorf("OriginAllowed(%q, %q) = %t, want %t", c.origin, c.allowed, got, c.want)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	cases := []struct {
		loopback bool
		host     string
		extra    []string
		want     bool
	}{
		{true, "127.0.0.1:8081", nil, true},
		{true, "127.0.0.1", nil, true},
		{true, "localhost:8081", nil, true},
		{true, "LocalHost", nil, true},
		{true, "[::1]:8081", nil, true},
		{true, "[::1]", nil, true},
		{true, "mcp.internal:8081", []string{"mcp.internal"}, true},
		{false, "evil.example:8081", nil, true},
		{true, "evil.example:8081", nil, false},
		{true, "evil.example", nil, false},
		{true, "", nil, false},
		{true, "127.0.0.2:8081", nil, false},
		{true, "localhost.evil.example", nil, false},
		{true, "127.0.0.1.evil.example:8081", nil, false},
		{true, "[::1]evil.example", nil, false},
		{true, "[::1", nil, false},
		{true, "other.example:8081", []string{"mcp.internal"}, false},
	}
	for _, c := range cases {
		if got := mcprule.HostAllowed(c.loopback, c.host, c.extra); got != c.want {
			t.Errorf("HostAllowed(%t, %q, %q) = %t, want %t", c.loopback, c.host, c.extra, got, c.want)
		}
	}
}
