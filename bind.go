// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"encoding/xml"
	"errors"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"

	"github.com/fiberfx/mx-echo/v7/internal/bindengine"
)

// Binder is the interface that wraps the Bind method.
type Binder interface {
	Bind(c *Context, target any) error
}

// DefaultBinder is the default implementation of the Binder interface.
// For path, query, header, and form binding, time.Time and *time.Time fields (not slices)
// may use a format tag: "date-time" uses the standard RFC3339 decoding, "date-time-local"
// accepts "2006-01-02T15:04:05" with optional fractional seconds, "date" accepts
// "2006-01-02", and other values specify Go time layouts. "date", "date-time-local", and
// layouts without a timezone give UTC times. JSON and XML decoding do not use this tag.
// HTML datetime-local inputs omit seconds by default, so bind them with a Go layout
// such as "2006-01-02T15:04" instead of "date-time-local".
type DefaultBinder struct{}

var errUnsupportedBindMapValue = bindengine.ErrUnsupportedMapValue

// BindUnmarshaler is the interface used to wrap the UnmarshalParam method.
// Types that don't implement this, but do implement encoding.TextUnmarshaler
// will use that interface instead.
type BindUnmarshaler interface {
	// UnmarshalParam decodes and assigns a value from an form or query param.
	UnmarshalParam(param string) error
}

// BindPathValues binds path parameter values to bindable object
func BindPathValues(c *Context, target any) error {
	params := map[string][]string{}
	for _, param := range c.PathValues() {
		params[param.Name] = []string{param.Value}
	}
	if err := bindData(target, params, "param", nil); err != nil {
		return ErrBadRequest.Wrap(err)
	}
	return nil
}

// BindQueryParams binds query params to bindable object
func BindQueryParams(c *Context, target any) error {
	if err := bindData(target, c.QueryParams(), "query", nil); err != nil {
		return ErrBadRequest.Wrap(err)
	}
	return nil
}

// BindBody binds request body contents to bindable object
// NB: then binding forms take note that this implementation uses standard library form parsing
// which parses form data from BOTH URL and BODY if content type is not MIMEMultipartForm
// See non-MIMEMultipartForm: https://golang.org/pkg/net/http/#Request.ParseForm
// See MIMEMultipartForm: https://golang.org/pkg/net/http/#Request.ParseMultipartForm
func BindBody(c *Context, target any) (err error) {
	req := c.Request()
	if req.ContentLength == 0 {
		return
	}

	switch bindBodyMediaType(req) {
	case MIMEApplicationJSON:
		if err = c.Echo().JSONSerializer.Deserialize(c, target); err != nil {
			return wrapBindBodyError(err)
		}
	case MIMEApplicationXML, MIMETextXML:
		if err = xml.NewDecoder(req.Body).Decode(target); err != nil {
			return wrapBindBodyError(err)
		}
	case MIMEApplicationForm:
		params, err := c.FormValues()
		if err != nil {
			return wrapBindBodyError(err)
		}
		if err = bindData(target, params, "form", nil); err != nil {
			return ErrBadRequest.Wrap(err)
		}
	case MIMEMultipartForm:
		params, err := c.MultipartForm()
		if err != nil {
			return wrapBindBodyError(err)
		}
		if err = bindData(target, params.Value, "form", params.File); err != nil {
			return ErrBadRequest.Wrap(err)
		}
	default:
		return &HTTPError{Code: http.StatusUnsupportedMediaType}
	}
	return nil
}

func bindBodyMediaType(req *http.Request) string {
	base, _, _ := strings.Cut(req.Header.Get(HeaderContentType), ";")
	return strings.TrimSpace(base)
}

func wrapBindBodyError(err error) error {
	if StatusCode(err) != 0 {
		return err
	}
	return ErrBadRequest.Wrap(err)
}

// BindHeaders binds HTTP headers to a bindable object
func BindHeaders(c *Context, target any) error {
	if err := bindData(target, c.Request().Header, "header", nil); err != nil {
		return ErrBadRequest.Wrap(err)
	}
	return nil
}

// Bind implements the `Binder#Bind` function.
// Binding is done in following order: 1) path params; 2) query params; 3) request body. Each step COULD override previous
// step bound values. For single source binding use their own methods BindBody, BindQueryParams, BindPathValues.
func (b *DefaultBinder) Bind(c *Context, target any) error {
	if err := bindPathAndQuery(c, target); err != nil {
		req := c.Request()
		if !errors.Is(err, errUnsupportedBindMapValue) || req.ContentLength == 0 {
			return err
		}
		// Form bodies use the same map binding rules. Other bodies have their
		// own decoders, and BindBody handles unsupported media types.
		switch bindBodyMediaType(req) {
		case MIMEApplicationForm, MIMEMultipartForm:
			return err
		}
	}
	return BindBody(c, target)
}

func bindPathAndQuery(c *Context, target any) error {
	if err := BindPathValues(c, target); err != nil {
		return err
	}
	// Only bind query parameters for GET/DELETE/HEAD/QUERY to avoid unexpected behavior with destination struct binding from body.
	// For example a request URL `&id=1&lang=en` with body `{"id":100,"lang":"de"}` would lead to precedence issues.
	// The HTTP method check restores pre-v4.1.11 behavior to avoid these problems (see issue #1670)
	method := c.Request().Method
	if method == http.MethodGet || method == http.MethodDelete || method == http.MethodHead || method == QUERY {
		return BindQueryParams(c, target)
	}
	return nil
}

// bindData will bind data ONLY fields in destination struct that have EXPLICIT tag. The engine is
// bindengine.Bind.
func bindData(destination any, data map[string][]string, tag string, dataFiles map[string][]*multipart.FileHeader) error {
	return bindengine.Bind(destination, data, tag, dataFiles)
}

// setWithProperType sets structField from val as valueKind says. The engine is bindengine.SetWithProperType.
func setWithProperType(valueKind reflect.Kind, val string, structField reflect.Value) error {
	return bindengine.SetWithProperType(valueKind, val, structField)
}
