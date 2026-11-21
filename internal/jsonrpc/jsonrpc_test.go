// SPDX-License-Identifier: MIT

package jsonrpc_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v6/internal/jsonrpc"
)

// decodeRow is one input to Decode and what it gives: the kind, the raw id token ("" when absent), the method and the
// raw params ("" when absent); or, when code is not 0, the error's code and a part of its message. An InvalidParams
// error still carries the kind, id and method; every other error carries the zero Message.
type decodeRow struct {
	name   string
	in     string
	kind   jsonrpc.Kind
	id     string
	method string
	params string
	code   int
	msg    string
}

var decodeRows = []decodeRow{
	// Good.
	{name: "request, integer id", in: `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`, kind: jsonrpc.Request, id: `7`, method: "tools/list"},
	{name: "request, string id, params", in: `{"jsonrpc":"2.0","id":"a","method":"x","params":{}}`, kind: jsonrpc.Request, id: `"a"`, method: "x", params: `{}`},
	{name: "notification", in: `{"jsonrpc":"2.0","method":"notifications/initialized"}`, kind: jsonrpc.Notification, method: "notifications/initialized"},
	{name: "notification, params", in: `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`, kind: jsonrpc.Notification, method: "notifications/cancelled", params: `{"requestId":7}`},
	{name: "empty string id", in: `{"jsonrpc":"2.0","id":"","method":"x"}`, kind: jsonrpc.Request, id: `""`, method: "x"},
	{name: "string id kept as written", in: `{"jsonrpc":"2.0","id":"A<&>","method":"x"}`, kind: jsonrpc.Request, id: `"A<&>"`, method: "x"},
	{name: "id above 2^53", in: `{"jsonrpc":"2.0","id":9007199254740993,"method":"x"}`, kind: jsonrpc.Request, id: `9007199254740993`, method: "x"},
	{name: "id max int64", in: `{"jsonrpc":"2.0","id":9223372036854775807,"method":"x"}`, kind: jsonrpc.Request, id: `9223372036854775807`, method: "x"},
	{name: "id min int64", in: `{"jsonrpc":"2.0","id":-9223372036854775808,"method":"x"}`, kind: jsonrpc.Request, id: `-9223372036854775808`, method: "x"},
	{name: "id negative zero", in: `{"jsonrpc":"2.0","id":-0,"method":"x"}`, kind: jsonrpc.Request, id: `-0`, method: "x"},
	{name: "id exponent, integer value", in: `{"jsonrpc":"2.0","id":1e3,"method":"x"}`, kind: jsonrpc.Request, id: `1e3`, method: "x"},
	{name: "id fraction and exponent, integer value", in: `{"jsonrpc":"2.0","id":2.5E+1,"method":"x"}`, kind: jsonrpc.Request, id: `2.5E+1`, method: "x"},
	{name: "id zero fraction", in: `{"jsonrpc":"2.0","id":1.0,"method":"x"}`, kind: jsonrpc.Request, id: `1.0`, method: "x"},
	{name: "id negative exponent, integer value", in: `{"jsonrpc":"2.0","id":100e-2,"method":"x"}`, kind: jsonrpc.Request, id: `100e-2`, method: "x"},
	{name: "id zero, huge exponent", in: `{"jsonrpc":"2.0","id":0e99999999999,"method":"x"}`, kind: jsonrpc.Request, id: `0e99999999999`, method: "x"},
	{name: "whitespace around values", in: " {\"jsonrpc\" : \"2.0\", \"id\" : 7 , \"method\":\"x\", \"params\" : { \"a\" : 1 } }\n", kind: jsonrpc.Request, id: `7`, method: "x", params: `{ "a" : 1 }`},
	{name: "duplicate members, last wins", in: `{"jsonrpc":"2.0","id":1,"id":"b","method":"x","method":"y"}`, kind: jsonrpc.Request, id: `"b"`, method: "y"},
	{name: "response with a result", in: `{"jsonrpc":"2.0","id":1,"result":{}}`, kind: jsonrpc.Response, id: `1`},
	{name: "response with an error, null id", in: `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"x"}}`, kind: jsonrpc.Response},

	// Violating: not JSON.
	{name: "not json", in: `{not json`, code: jsonrpc.ParseError, msg: "parse error"},
	{name: "empty body", in: ``, code: jsonrpc.ParseError, msg: "parse error"},
	{name: "two values", in: `{} {}`, code: jsonrpc.ParseError, msg: "parse error"},
	{name: "trailing bytes", in: `{"jsonrpc":"2.0","method":"x"}x`, code: jsonrpc.ParseError, msg: "parse error"},

	// Violating: not a request object.
	{name: "batch", in: `[{"jsonrpc":"2.0","id":1,"method":"x"}]`, code: jsonrpc.InvalidRequest, msg: "batches are not supported"},
	{name: "empty batch", in: ` [] `, code: jsonrpc.InvalidRequest, msg: "batches are not supported"},
	{name: "string", in: `"x"`, code: jsonrpc.InvalidRequest, msg: "object"},
	{name: "number", in: `1`, code: jsonrpc.InvalidRequest, msg: "object"},
	{name: "null", in: `null`, code: jsonrpc.InvalidRequest, msg: "object"},
	{name: "jsonrpc 1.0", in: `{"jsonrpc":"1.0","id":1,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "jsonrpc"},
	{name: "jsonrpc absent", in: `{"id":1,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "jsonrpc"},
	{name: "jsonrpc a number", in: `{"jsonrpc":2.0,"id":1,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "jsonrpc"},
	{name: "jsonrpc null", in: `{"jsonrpc":null,"id":1,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "jsonrpc"},
	{name: "jsonrpc in another case", in: `{"JSONRPC":"2.0","id":1,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "jsonrpc"},
	{name: "method absent", in: `{"jsonrpc":"2.0","id":1}`, code: jsonrpc.InvalidRequest, msg: "method"},
	{name: "method a number", in: `{"jsonrpc":"2.0","id":1,"method":1}`, code: jsonrpc.InvalidRequest, msg: "method"},
	{name: "method null", in: `{"jsonrpc":"2.0","id":1,"method":null}`, code: jsonrpc.InvalidRequest, msg: "method"},
	{name: "method in another case", in: `{"jsonrpc":"2.0","id":1,"Method":"x"}`, code: jsonrpc.InvalidRequest, msg: "method"},

	// Violating: the id.
	{name: "id null", in: `{"jsonrpc":"2.0","id":null,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id fraction", in: `{"jsonrpc":"2.0","id":1.5,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id negative exponent, fraction", in: `{"jsonrpc":"2.0","id":1e-1,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id exponent, fraction", in: `{"jsonrpc":"2.0","id":1.05e1,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id below 1", in: `{"jsonrpc":"2.0","id":0.5,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id above int64", in: `{"jsonrpc":"2.0","id":9223372036854775808,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id below int64", in: `{"jsonrpc":"2.0","id":-9223372036854775809,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id 1e19", in: `{"jsonrpc":"2.0","id":1e19,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id huge exponent", in: `{"jsonrpc":"2.0","id":1e99999999999,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id object", in: `{"jsonrpc":"2.0","id":{},"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id array", in: `{"jsonrpc":"2.0","id":[1],"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id boolean", in: `{"jsonrpc":"2.0","id":true,"method":"x"}`, code: jsonrpc.InvalidRequest, msg: "id"},
	{name: "id checked before params", in: `{"jsonrpc":"2.0","id":null,"method":"x","params":[1]}`, code: jsonrpc.InvalidRequest, msg: "id"},

	// Violating: the params.
	{name: "params an array", in: `{"jsonrpc":"2.0","id":1,"method":"x","params":[1]}`, kind: jsonrpc.Request, id: `1`, method: "x", code: jsonrpc.InvalidParams, msg: "params"},
	{name: "params a string", in: `{"jsonrpc":"2.0","id":"s","method":"x","params":"p"}`, kind: jsonrpc.Request, id: `"s"`, method: "x", code: jsonrpc.InvalidParams, msg: "params"},
	{name: "params null", in: `{"jsonrpc":"2.0","id":2,"method":"x","params":null}`, kind: jsonrpc.Request, id: `2`, method: "x", code: jsonrpc.InvalidParams, msg: "params"},
	{name: "params of a notification", in: `{"jsonrpc":"2.0","method":"x","params":[1]}`, kind: jsonrpc.Notification, method: "x", code: jsonrpc.InvalidParams, msg: "params"},
}

func TestDecode(t *testing.T) {
	for _, row := range decodeRows {
		t.Run(row.name, func(t *testing.T) {
			m, err := jsonrpc.Decode([]byte(row.in))
			if row.code != 0 {
				if err == nil {
					t.Fatalf("Decode(%s) = %+v, no error; want code %d", row.in, m, row.code)
				}
				if err.Code != row.code || !strings.Contains(err.Message, row.msg) {
					t.Fatalf("Decode(%s) error = %d %q; want %d with %q", row.in, err.Code, err.Message, row.code, row.msg)
				}
			} else if err != nil {
				t.Fatalf("Decode(%s) error = %v; want none", row.in, err)
			}
			if m.Kind != row.kind || m.ID.String() != row.id || m.ID.IsZero() != (row.id == "") ||
				m.Method != row.method || string(m.Params) != row.params {
				t.Fatalf("Decode(%s) = {%v %s %q %s}; want {%v %s %q %s}",
					row.in, m.Kind, m.ID, m.Method, m.Params, row.kind, row.id, row.method, row.params)
			}
		})
	}
}

func TestDecodeKeepsIntegerAndStringIDsApart(t *testing.T) {
	n, err := jsonrpc.Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := jsonrpc.Decode([]byte(`{"jsonrpc":"2.0","id":"1","method":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if n.ID == s.ID {
		t.Fatalf("ids 1 and \"1\" are equal: %s, %s", n.ID, s.ID)
	}
}

func TestEncodeResultEchoesTheID(t *testing.T) {
	for _, token := range []string{`7`, `"7"`, `9007199254740993`, `"A<&>"`, `"\u0041"`, `-0`, `1e3`, `""`} {
		m, err := jsonrpc.Decode([]byte(`{"jsonrpc":"2.0","id":` + token + `,"method":"x"}`))
		if err != nil {
			t.Fatalf("Decode with id %s: %v", token, err)
		}
		got, encErr := jsonrpc.EncodeResult(m.ID, map[string]bool{"ok": true})
		want := `{"jsonrpc":"2.0","id":` + token + `,"result":{"ok":true}}`
		if encErr != nil || string(got) != want {
			t.Fatalf("EncodeResult(%s) = %s, %v; want %s", token, got, encErr, want)
		}
	}
}

func TestEncodeResultRefuses(t *testing.T) {
	if b, err := jsonrpc.EncodeResult(jsonrpc.ID{}, struct{}{}); err == nil {
		t.Fatalf("EncodeResult with no id = %s, no error", b)
	}
	m, _ := jsonrpc.Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	if b, err := jsonrpc.EncodeResult(m.ID, make(chan int)); err == nil {
		t.Fatalf("EncodeResult of a channel = %s, no error", b)
	}
}

func TestEncodeError(t *testing.T) {
	one, _ := jsonrpc.Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	for _, row := range []struct {
		name string
		id   jsonrpc.ID
		err  *jsonrpc.Error
		want string
	}{
		{name: "with id", id: one.ID, err: &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "params must be an object"}, want: `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"params must be an object"}}`},
		{name: "unreadable id", err: &jsonrpc.Error{Code: jsonrpc.ParseError, Message: "parse error"}, want: `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`},
		{name: "with data", id: one.ID, err: &jsonrpc.Error{Code: -32022, Message: "unsupported", Data: json.RawMessage(`{"supported":["2026-07-28"]}`)}, want: `{"jsonrpc":"2.0","id":1,"error":{"code":-32022,"message":"unsupported","data":{"supported":["2026-07-28"]}}}`},
	} {
		got, err := jsonrpc.EncodeError(row.id, row.err)
		if err != nil || string(got) != row.want {
			t.Fatalf("%s: EncodeError = %s, %v; want %s", row.name, got, err, row.want)
		}
	}
	got, err := jsonrpc.EncodeErrorNoID(&jsonrpc.Error{Code: jsonrpc.InvalidRequest, Message: "origin not allowed"})
	want := `{"jsonrpc":"2.0","error":{"code":-32600,"message":"origin not allowed"}}`
	if err != nil || string(got) != want {
		t.Fatalf("EncodeErrorNoID = %s, %v; want %s", got, err, want)
	}
}

func TestEncodeErrorRefuses(t *testing.T) {
	bad := &jsonrpc.Error{Code: jsonrpc.InternalError, Message: "x", Data: json.RawMessage(`{`)}
	for _, e := range []*jsonrpc.Error{nil, bad} {
		if b, err := jsonrpc.EncodeError(jsonrpc.ID{}, e); err == nil {
			t.Fatalf("EncodeError(%v) = %s, no error", e, b)
		}
		if b, err := jsonrpc.EncodeErrorNoID(e); err == nil {
			t.Fatalf("EncodeErrorNoID(%v) = %s, no error", e, b)
		}
	}
}

func TestEncodeNotification(t *testing.T) {
	for _, row := range []struct {
		method string
		params any
		want   string
	}{
		{method: "notifications/progress", params: map[string]int{"progress": 1}, want: `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}`},
		{method: "notifications/initialized", want: `{"jsonrpc":"2.0","method":"notifications/initialized"}`},
		{method: "notifications/initialized", params: (*struct{})(nil), want: `{"jsonrpc":"2.0","method":"notifications/initialized"}`},
	} {
		got, err := jsonrpc.EncodeNotification(row.method, row.params)
		if err != nil || string(got) != row.want {
			t.Fatalf("EncodeNotification(%s) = %s, %v; want %s", row.method, got, err, row.want)
		}
		m, decErr := jsonrpc.Decode(got)
		if decErr != nil || m.Kind != jsonrpc.Notification || m.Method != row.method {
			t.Fatalf("Decode(%s) = %+v, %v; want the notification back", got, m, decErr)
		}
	}
	for _, params := range []any{[]int{1}, "p", make(chan int)} {
		if b, err := jsonrpc.EncodeNotification("x", params); err == nil {
			t.Fatalf("EncodeNotification with params %T = %s, no error", params, b)
		}
	}
}

func TestError(t *testing.T) {
	var err error = &jsonrpc.Error{Code: jsonrpc.InvalidRequest, Message: "batches are not supported"}
	if got := err.Error(); !strings.Contains(got, "-32600") || !strings.Contains(got, "batches are not supported") {
		t.Fatalf("Error() = %q", got)
	}
}

// FuzzDecode holds Decode to its promises on any input: no panic, every error has a standard code, params are an
// object or absent, and a decoded request's id is echoed exactly by EncodeResult.
func FuzzDecode(f *testing.F) {
	for _, row := range decodeRows {
		f.Add([]byte(row.in))
	}
	codes := []int{jsonrpc.ParseError, jsonrpc.InvalidRequest, jsonrpc.MethodNotFound, jsonrpc.InvalidParams, jsonrpc.InternalError}
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := jsonrpc.Decode(b)
		if err != nil {
			if !slices.Contains(codes, err.Code) {
				t.Fatalf("Decode(%q) error code %d is not a standard code", b, err.Code)
			}
			if err.Code != jsonrpc.InvalidParams {
				return
			}
		}
		if len(m.Params) > 0 && m.Params[0] != '{' {
			t.Fatalf("Decode(%q) params %s are not an object", b, m.Params)
		}
		if m.Kind == jsonrpc.Request {
			got, encErr := jsonrpc.EncodeResult(m.ID, nil)
			want := `{"jsonrpc":"2.0","id":` + m.ID.String() + `,"result":null}`
			if encErr != nil || string(got) != want || !json.Valid(got) {
				t.Fatalf("EncodeResult(%s) = %s, %v; want %s", m.ID, got, encErr, want)
			}
		}
	})
}
