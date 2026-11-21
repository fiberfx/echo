// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"fmt"

	"github.com/fiberfx/mx-echo/v6/bind"
)

// TimeLayout specifies the format for parsing time values in request parameters.
type TimeLayout = bind.TimeLayout

// TimeOpts configures how time.Time values are parsed and converted.
type TimeOpts = bind.TimeOpts

// TimeLayout constants select Unix timestamps in seconds, milliseconds or nanoseconds.
const (
	TimeLayoutUnixTime      = bind.TimeLayoutUnixTime
	TimeLayoutUnixTimeMilli = bind.TimeLayoutUnixTimeMilli
	TimeLayoutUnixTimeNano  = bind.TimeLayoutUnixTimeNano
)

// PathParam extracts and parses a path parameter from the context by name.
// It returns the typed value and an error if binding fails. Returns ErrNonExistentKey if parameter not found.
//
// Empty String Handling:
//
//	If the parameter exists but has an empty value, the zero value of type T is returned
//	with no error. For example, a path parameter with value "" returns (0, nil) for int types.
//	This differs from standard library behavior where parsing empty strings returns errors.
//	To treat empty values as errors, validate the result separately or check the raw value.
//
// See ParseValue for supported types and options
func PathParam[T any](c *Context, paramName string, opts ...any) (T, error) {
	for _, pv := range c.PathValues() {
		if pv.Name == paramName {
			v, err := ParseValue[T](pv.Value, opts...)
			if err != nil {
				return v, NewBindingError(paramName, []string{pv.Value}, "path value", err)
			}
			return v, nil
		}
	}
	var zero T
	return zero, ErrNonExistentKey
}

// PathParamOr extracts and parses a path parameter from the context by name.
// Returns defaultValue if the parameter is not found or has an empty value.
// Returns an error only if parsing fails (e.g., "abc" for int type).
//
// Example:
//
//	id, err := echo.PathParamOr[int](c, "id", 0)
//	// If "id" is missing: returns (0, nil)
//	// If "id" is "123": returns (123, nil)
//	// If "id" is "abc": returns (0, BindingError)
//
// See ParseValue for supported types and options
func PathParamOr[T any](c *Context, paramName string, defaultValue T, opts ...any) (T, error) {
	for _, pv := range c.PathValues() {
		if pv.Name == paramName {
			v, err := ParseValueOr[T](pv.Value, defaultValue, opts...)
			if err != nil {
				return v, NewBindingError(paramName, []string{pv.Value}, "path value", err)
			}
			return v, nil
		}
	}
	return defaultValue, nil
}

// QueryParam extracts and parses a single query parameter from the request by key.
// It returns the typed value and an error if binding fails. Returns ErrNonExistentKey if parameter not found.
//
// Empty String Handling:
//
//	If the parameter exists but has an empty value (?key=), the zero value of type T is returned
//	with no error. For example, "?count=" returns (0, nil) for int types.
//	This differs from standard library behavior where parsing empty strings returns errors.
//	To treat empty values as errors, validate the result separately or check the raw value.
//
// Behavior Summary:
//   - Missing key (?other=value): returns (zero, ErrNonExistentKey)
//   - Empty value (?key=): returns (zero, nil)
//   - Invalid value (?key=abc for int): returns (zero, BindingError)
//
// See ParseValue for supported types and options
func QueryParam[T any](c *Context, key string, opts ...any) (T, error) {
	values, ok := c.QueryParams()[key]
	if !ok {
		var zero T
		return zero, ErrNonExistentKey
	}
	if len(values) == 0 {
		var zero T
		return zero, nil
	}
	value := values[0]
	v, err := ParseValue[T](value, opts...)
	if err != nil {
		return v, NewBindingError(key, []string{value}, "query param", err)
	}
	return v, nil
}

// QueryParamOr extracts and parses a single query parameter from the request by key.
// Returns defaultValue if the parameter is not found or has an empty value.
// Returns an error only if parsing fails (e.g., "abc" for int type).
//
// Example:
//
//	page, err := echo.QueryParamOr[int](c, "page", 1)
//	// If "page" is missing: returns (1, nil)
//	// If "page" is "5": returns (5, nil)
//	// If "page" is "abc": returns (1, BindingError)
//
// See ParseValue for supported types and options
func QueryParamOr[T any](c *Context, key string, defaultValue T, opts ...any) (T, error) {
	values, ok := c.QueryParams()[key]
	if !ok {
		return defaultValue, nil
	}
	if len(values) == 0 {
		return defaultValue, nil
	}
	value := values[0]
	v, err := ParseValueOr[T](value, defaultValue, opts...)
	if err != nil {
		return v, NewBindingError(key, []string{value}, "query param", err)
	}
	return v, nil
}

// QueryParams extracts and parses all values for a query parameter key as a slice.
// It returns the typed slice and an error if binding any value fails. Returns ErrNonExistentKey if parameter not found.
//
// See ParseValues for supported types and options
func QueryParams[T any](c *Context, key string, opts ...any) ([]T, error) {
	values, ok := c.QueryParams()[key]
	if !ok {
		return nil, ErrNonExistentKey
	}

	result, err := ParseValues[T](values, opts...)
	if err != nil {
		return nil, NewBindingError(key, values, "query params", err)
	}
	return result, nil
}

// QueryParamsOr extracts and parses all values for a query parameter key as a slice.
// Returns defaultValue if the parameter is not found.
// Returns an error only if parsing any value fails.
//
// Example:
//
//	ids, err := echo.QueryParamsOr[int](c, "ids", []int{})
//	// If "ids" is missing: returns ([], nil)
//	// If "ids" is "1&ids=2": returns ([1, 2], nil)
//	// If "ids" contains "abc": returns ([], BindingError)
//
// See ParseValues for supported types and options
func QueryParamsOr[T any](c *Context, key string, defaultValue []T, opts ...any) ([]T, error) {
	values, ok := c.QueryParams()[key]
	if !ok {
		return defaultValue, nil
	}

	result, err := ParseValuesOr[T](values, defaultValue, opts...)
	if err != nil {
		return nil, NewBindingError(key, values, "query params", err)
	}
	return result, nil
}

// FormValue extracts and parses a single form value from the request by key.
// It returns the typed value and an error if binding fails. Returns ErrNonExistentKey if parameter not found.
//
// Empty String Handling:
//
//	If the form field exists but has an empty value, the zero value of type T is returned
//	with no error. For example, an empty form field returns (0, nil) for int types.
//	This differs from standard library behavior where parsing empty strings returns errors.
//	To treat empty values as errors, validate the result separately or check the raw value.
//
// See ParseValue for supported types and options
func FormValue[T any](c *Context, key string, opts ...any) (T, error) {
	formValues, err := c.FormValues()
	if err != nil {
		var zero T
		return zero, fmt.Errorf("failed to parse form value, key: %s, err: %w", key, err)
	}
	values, ok := formValues[key]
	if !ok {
		var zero T
		return zero, ErrNonExistentKey
	}
	if len(values) == 0 {
		var zero T
		return zero, nil
	}
	value := values[0]
	v, err := ParseValue[T](value, opts...)
	if err != nil {
		return v, NewBindingError(key, []string{value}, "form value", err)
	}
	return v, nil
}

// FormValueOr extracts and parses a single form value from the request by key.
// Returns defaultValue if the parameter is not found or has an empty value.
// Returns an error only if parsing fails or form parsing errors occur.
//
// Example:
//
//	limit, err := echo.FormValueOr[int](c, "limit", 100)
//	// If "limit" is missing: returns (100, nil)
//	// If "limit" is "50": returns (50, nil)
//	// If "limit" is "abc": returns (100, BindingError)
//
// See ParseValue for supported types and options
func FormValueOr[T any](c *Context, key string, defaultValue T, opts ...any) (T, error) {
	formValues, err := c.FormValues()
	if err != nil {
		var zero T
		return zero, fmt.Errorf("failed to parse form value, key: %s, err: %w", key, err)
	}
	values, ok := formValues[key]
	if !ok {
		return defaultValue, nil
	}
	if len(values) == 0 {
		return defaultValue, nil
	}
	value := values[0]
	v, err := ParseValueOr[T](value, defaultValue, opts...)
	if err != nil {
		return v, NewBindingError(key, []string{value}, "form value", err)
	}
	return v, nil
}

// FormValues extracts and parses all values for a form values key as a slice.
// It returns the typed slice and an error if binding any value fails. Returns ErrNonExistentKey if parameter not found.
//
// See ParseValues for supported types and options
func FormValues[T any](c *Context, key string, opts ...any) ([]T, error) {
	formValues, err := c.FormValues()
	if err != nil {
		return nil, fmt.Errorf("failed to parse form values, key: %s, err: %w", key, err)
	}
	values, ok := formValues[key]
	if !ok {
		return nil, ErrNonExistentKey
	}
	result, err := ParseValues[T](values, opts...)
	if err != nil {
		return nil, NewBindingError(key, values, "form values", err)
	}
	return result, nil
}

// FormValuesOr extracts and parses all values for a form values key as a slice.
// Returns defaultValue if the parameter is not found.
// Returns an error only if parsing any value fails or form parsing errors occur.
//
// Example:
//
//	tags, err := echo.FormValuesOr[string](c, "tags", []string{})
//	// If "tags" is missing: returns ([], nil)
//	// If form parsing fails: returns (nil, error)
//
// See ParseValues for supported types and options
func FormValuesOr[T any](c *Context, key string, defaultValue []T, opts ...any) ([]T, error) {
	formValues, err := c.FormValues()
	if err != nil {
		return nil, fmt.Errorf("failed to parse form values, key: %s, err: %w", key, err)
	}
	values, ok := formValues[key]
	if !ok {
		return defaultValue, nil
	}
	result, err := ParseValuesOr[T](values, defaultValue, opts...)
	if err != nil {
		return nil, NewBindingError(key, values, "form values", err)
	}
	return result, nil
}

// ParseValues forwards to [bind.ParseValues].
func ParseValues[T any](values []string, opts ...any) ([]T, error) {
	return bind.ParseValues[T](values, opts...)
}

// ParseValuesOr forwards to [bind.ParseValuesOr].
func ParseValuesOr[T any](values []string, defaultValue []T, opts ...any) ([]T, error) {
	return bind.ParseValuesOr[T](values, defaultValue, opts...)
}

// ParseValue forwards to [bind.ParseValue].
func ParseValue[T any](value string, opts ...any) (T, error) {
	return bind.ParseValue[T](value, opts...)
}

// ParseValueOr forwards to [bind.ParseValueOr].
func ParseValueOr[T any](value string, defaultValue T, opts ...any) (T, error) {
	return bind.ParseValueOr[T](value, defaultValue, opts...)
}
