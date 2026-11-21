// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package jsonrpc frames one JSON-RPC 2.0 message for MCP: it decodes a body into a request, a notification or a
// response, refuses what MCP refuses, and encodes results, errors and notifications.
//
// It reads no request and writes no response: the caller applies its size limit before Decode and writes the bytes
// the encoders return. Member names are case-sensitive ("Method" is not "method"), and when an object repeats a
// member the last one wins, as encoding/json decides.
package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// The standard JSON-RPC 2.0 error codes.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

const version = "2.0"

// Kind is what a decoded message is.
type Kind int

const (
	// Request is a message with a method and an id: it expects a response.
	Request Kind = iota + 1
	// Notification is a message with a method and no id: it expects none.
	Notification
	// Response is a message with a result or an error member. A server is sent none; the caller refuses it.
	Response
)

// String names the kind.
func (k Kind) String() string {
	switch k {
	case Request:
		return "request"
	case Notification:
		return "notification"
	case Response:
		return "response"
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// ID is a request id as the client wrote it: the raw JSON token of a string or of an integer, kept byte for byte, so
// that 1 and "1" stay distinct and a response echoes the id exactly. The zero ID is an absent id. IDs are comparable.
type ID struct{ raw string }

// IsZero reports whether the id is absent.
func (id ID) IsZero() bool { return id.raw == "" }

// String returns the raw token, for example 7 or "a", or "" for an absent id.
func (id ID) String() string { return id.raw }

// Message is one decoded JSON-RPC message. A request has a Method and an ID; a notification a Method and the zero
// ID; a response only its ID, when it was a string or an integer. Params is the raw params object, or nil when
// absent; it does not alias the decoded bytes.
type Message struct {
	Kind   Kind
	ID     ID
	Method string
	Params json.RawMessage
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error returns the code and the message.
func (e *Error) Error() string {
	return "jsonrpc: " + strconv.Itoa(e.Code) + " " + e.Message
}

// Decode decodes one message, the whole of b. It returns ParseError when b is not one JSON value; InvalidRequest when
// b is a batch (an array, empty or not), is not an object, its jsonrpc member is not "2.0", its method is absent or
// not a string while it has neither result nor error, or its id is null, fractional, outside int64, an object or an
// array; and InvalidParams when params is present and not an object. On an InvalidParams error the Message still
// carries the kind, id and method, so that the caller can answer the request; on every other error it is zero, and
// the caller answers with an absent id. A message with a result or an error member decodes as a Response without
// further checks. Decode does not panic on any input.
func Decode(b []byte) (Message, *Error) {
	if !json.Valid(b) {
		return Message{}, &Error{Code: ParseError, Message: "parse error"}
	}
	switch bytes.TrimLeft(b, " \t\r\n")[0] {
	case '[':
		return Message{}, invalidRequest("batches are not supported")
	case '{':
	default:
		return Message{}, invalidRequest("a message must be a JSON object")
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(b, &members); err != nil {
		return Message{}, &Error{Code: ParseError, Message: "parse error"}
	}
	var v string
	if raw, ok := members["jsonrpc"]; !ok || json.Unmarshal(raw, &v) != nil || v != version {
		return Message{}, invalidRequest(`jsonrpc must be "2.0"`)
	}
	rawID, hasID := members["id"]
	_, hasResult := members["result"]
	_, hasError := members["error"]
	if hasResult || hasError {
		id, _ := readID(rawID)
		return Message{Kind: Response, ID: id}, nil
	}
	m := Message{Kind: Notification}
	if raw, ok := members["method"]; !ok || !startsWith(raw, '"') || json.Unmarshal(raw, &m.Method) != nil {
		return Message{}, invalidRequest("method must be a string")
	}
	if hasID {
		id, ok := readID(rawID)
		if !ok {
			return Message{}, invalidRequest("id must be a string or an integer")
		}
		m.Kind, m.ID = Request, id
	}
	if raw, ok := members["params"]; ok {
		if !startsWith(raw, '{') {
			return m, &Error{Code: InvalidParams, Message: "params must be an object"}
		}
		m.Params = raw
	}
	return m, nil
}

func invalidRequest(message string) *Error {
	return &Error{Code: InvalidRequest, Message: message}
}

func startsWith(raw json.RawMessage, c byte) bool {
	return len(raw) > 0 && raw[0] == c
}

// readID accepts a JSON string or a JSON number whose value is an integer within int64, and keeps its token.
func readID(raw json.RawMessage) (ID, bool) {
	switch {
	case startsWith(raw, '"'):
		return ID{raw: string(raw)}, true
	case len(raw) > 0 && (raw[0] == '-' || '0' <= raw[0] && raw[0] <= '9') && integer(string(raw)):
		return ID{raw: string(raw)}, true
	}
	return ID{}, false
}

// integer reports whether tok, a valid JSON number token, has an integer value within int64: 7, -0, 1.0, 1e3 and
// 2.5e1 do; 1.5, 1e-1 and 9223372036854775808 do not.
func integer(tok string) bool {
	sign := ""
	if strings.HasPrefix(tok, "-") {
		sign, tok = "-", tok[1:]
	}
	mantissa, exponent := tok, "0"
	if i := strings.IndexAny(tok, "eE"); i >= 0 {
		mantissa, exponent = tok[:i], tok[i+1:]
	}
	whole, frac, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+frac, "0")
	if digits == "" {
		return true // zero, whatever its exponent
	}
	exp, err := strconv.ParseInt(exponent, 10, 32)
	if err != nil {
		return false // nonzero digits under an exponent this large are far outside int64 or far below 1
	}
	// The value is digits × 10^shift, and digits starts with a nonzero digit.
	shift := exp - int64(len(frac))
	switch {
	case shift < 0:
		if -shift >= int64(len(digits)) {
			return false
		}
		cut := len(digits) - int(-shift)
		if strings.Trim(digits[cut:], "0") != "" {
			return false
		}
		digits = digits[:cut]
	case shift > 0:
		if int64(len(digits))+shift > 19 {
			return false
		}
		digits += strings.Repeat("0", int(shift))
	}
	_, err = strconv.ParseInt(sign+digits, 10, 64)
	return err == nil
}

// EncodeResult encodes the response to the request with the given id, {"jsonrpc":"2.0","id":ID,"result":RESULT},
// with the id written as the request wrote it. The zero ID is an error: a result answers a request.
func EncodeResult(id ID, result any) ([]byte, error) {
	if id.IsZero() {
		return nil, errors.New("jsonrpc: a result needs the request's id")
	}
	r, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 0, len(r)+len(id.raw)+32)
	b = append(b, `{"jsonrpc":"2.0","id":`...)
	b = append(b, id.raw...)
	b = append(b, `,"result":`...)
	b = append(b, r...)
	return append(b, '}'), nil
}

// EncodeError encodes an error response, {"jsonrpc":"2.0","id":ID,"error":{...}}. The zero ID writes "id":null, the
// JSON-RPC 2.0 answer when the request's id could not be read.
func EncodeError(id ID, e *Error) ([]byte, error) {
	obj, err := marshalError(e)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 0, len(obj)+len(id.raw)+32)
	b = append(b, `{"jsonrpc":"2.0","id":`...)
	if id.IsZero() {
		b = append(b, "null"...)
	} else {
		b = append(b, id.raw...)
	}
	b = append(b, `,"error":`...)
	b = append(b, obj...)
	return append(b, '}'), nil
}

// EncodeErrorNoID encodes an error response without an id member, {"jsonrpc":"2.0","error":{...}}: MCP's answer
// to a request refused before it was read, such as a 403 or a 405 body.
func EncodeErrorNoID(e *Error) ([]byte, error) {
	obj, err := marshalError(e)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 0, len(obj)+32)
	b = append(b, `{"jsonrpc":"2.0","error":`...)
	b = append(b, obj...)
	return append(b, '}'), nil
}

func marshalError(e *Error) ([]byte, error) {
	if e == nil {
		return nil, errors.New("jsonrpc: no error to encode")
	}
	return json.Marshal(e)
}

// EncodeNotification encodes a notification, {"jsonrpc":"2.0","method":METHOD,"params":PARAMS}. Params that are nil
// or encode as null are omitted; params that encode as anything but an object are an error.
func EncodeNotification(method string, params any) ([]byte, error) {
	m, err := json.Marshal(method)
	if err != nil {
		return nil, err
	}
	var p []byte
	if params != nil {
		if p, err = json.Marshal(params); err != nil {
			return nil, err
		}
		if string(p) == "null" {
			p = nil
		} else if !startsWith(p, '{') {
			return nil, errors.New("jsonrpc: params must encode as a JSON object")
		}
	}
	b := make([]byte, 0, len(m)+len(p)+40)
	b = append(b, `{"jsonrpc":"2.0","method":`...)
	b = append(b, m...)
	if p != nil {
		b = append(b, `,"params":`...)
		b = append(b, p...)
	}
	return append(b, '}'), nil
}
