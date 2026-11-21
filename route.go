// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"github.com/fiberfx/mx-echo/v6/route/pattern"
)

// Route contains information to adding/registering new route with the router.
// Method+Path pair uniquely identifies the Route. It is mandatory to provide Method+Path fields.
type Route struct {
	Method string
	Path   string
	Name   string

	// HandlerFunc is a function that handles HTTP requests. This could be left nil when the Router implementation allows
	// fallback to default/global handlers in certain situations.
	Handler     HandlerFunc
	Middlewares []MiddlewareFunc

	// allowOverwrite permits this route to replace an existing route with the same method+path,
	// overriding the router's AllowOverwritingRoute config for this specific registration.
	allowOverwrite bool
}

// ToRouteInfo converts Route to RouteInfo
func (r Route) ToRouteInfo(params []string) RouteInfo {
	name := r.Name
	if name == "" {
		name = r.Method + ":" + r.Path
	}

	return RouteInfo{
		Method:     r.Method,
		Path:       r.Path,
		Parameters: append([]string(nil), params...),
		Name:       name,
	}
}

// WithPrefix recreates Route with added group prefix and group middlewares it is grouped to.
func (r Route) WithPrefix(pathPrefix string, middlewares []MiddlewareFunc) Route {
	r.Path = pathPrefix + r.Path

	if len(middlewares) > 0 {
		r.Middlewares = slices.Concat(middlewares, r.Middlewares)
	}
	return r
}

// RouteInfo contains information about registered Route.
type RouteInfo struct {
	Name       string
	Method     string
	Path       string
	Parameters []string

	// NOTE: handler and middlewares are not exposed because handler could be already wrapping middlewares. Therefore,
	// it is not always 100% known if handler function already wraps middlewares or not. In Echo handler could be one
	// function or several functions wrapping each other.
}

// Clone creates copy of RouteInfo
func (r RouteInfo) Clone() RouteInfo {
	return RouteInfo{
		Name:       r.Name,
		Method:     r.Method,
		Path:       r.Path,
		Parameters: append([]string(nil), r.Parameters...),
	}
}

// Reverse reverses route to URL string by replacing path parameters with given params values.
func (r RouteInfo) Reverse(pathValues ...any) string {
	uri := new(bytes.Buffer)
	nextValue := 0
	pattern.Walk(r.Path, func(part pattern.Part) {
		switch part.Kind {
		case pattern.Static:
			uri.WriteString(part.Value)
		case pattern.Param:
			if nextValue < len(pathValues) {
				fmt.Fprint(uri, pathValues[nextValue])
				nextValue++
			} else {
				// placeholder for a missing value. An escaped colon in a param name is written without its backslash.
				uri.WriteByte(paramLabel)
				uri.WriteString(strings.ReplaceAll(part.Value, `\:`, ":"))
			}
		case pattern.Any:
			if nextValue < len(pathValues) {
				fmt.Fprint(uri, pathValues[nextValue])
				nextValue++
			} else {
				uri.WriteString(strings.ReplaceAll(part.Value, `\:`, ":"))
			}
		}
	})
	return uri.String()
}

// HandlerName returns string name for given function.
func HandlerName(h HandlerFunc) string {
	t := reflect.ValueOf(h).Type()
	if t.Kind() == reflect.Func {
		return runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
	}
	return t.String()
}

// Clone creates copy of Routes
func (r Routes) Clone() Routes {
	result := make(Routes, len(r))
	for i, route := range r {
		result[i] = route.Clone()
	}
	return result
}

// Reverse reverses route to URL string by replacing path parameters with given params values.
func (r Routes) Reverse(routeName string, pathValues ...any) (string, error) {
	for _, rr := range r {
		if rr.Name == routeName {
			return rr.Reverse(pathValues...), nil
		}
	}
	return "", errors.New("route not found")
}

// FindByMethodPath searched for matching route info by method and path
func (r Routes) FindByMethodPath(method string, path string) (RouteInfo, error) {
	if r == nil {
		return RouteInfo{}, errors.New("route not found by method and path")
	}

	for _, rr := range r {
		if rr.Method == method && rr.Path == path {
			return rr, nil
		}
	}
	return RouteInfo{}, errors.New("route not found by method and path")
}

// FilterByMethod searched for matching route info by method
func (r Routes) FilterByMethod(method string) (Routes, error) {
	if r == nil {
		return nil, errors.New("route not found by method")
	}

	result := make(Routes, 0)
	for _, rr := range r {
		if rr.Method == method {
			result = append(result, rr)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("route not found by method")
	}
	return result, nil
}

// FilterByPath searched for matching route info by path
func (r Routes) FilterByPath(path string) (Routes, error) {
	if r == nil {
		return nil, errors.New("route not found by path")
	}

	result := make(Routes, 0)
	for _, rr := range r {
		if rr.Path == path {
			result = append(result, rr)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("route not found by path")
	}
	return result, nil
}

// FilterByName searched for matching route info by name
func (r Routes) FilterByName(name string) (Routes, error) {
	if r == nil {
		return nil, errors.New("route not found by name")
	}

	result := make(Routes, 0)
	for _, rr := range r {
		if rr.Name == name {
			result = append(result, rr)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("route not found by name")
	}
	return result, nil
}
