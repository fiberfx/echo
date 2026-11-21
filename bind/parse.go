// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package bind converts request parameter strings into Go values.
package bind

import (
	"encoding"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// TimeLayout specifies the format for parsing time values in request parameters.
// It can be a standard Go time layout string or one of the special Unix time layouts.
type TimeLayout string

// TimeOpts is options for parsing time.Time values
type TimeOpts struct {
	// Layout specifies the format for parsing time values in request parameters.
	// It can be a standard Go time layout string or one of the special Unix time layouts.
	//
	// Parsing layout defaults to: bind.TimeLayout(time.RFC3339Nano)
	// - To convert to custom layout use `bind.TimeLayout("2006-01-02")`
	// - To convert unix timestamp (integer) to time.Time use `bind.TimeLayoutUnixTime`
	// - To convert unix timestamp in milliseconds to time.Time use `bind.TimeLayoutUnixTimeMilli`
	// - To convert unix timestamp in nanoseconds to time.Time use `bind.TimeLayoutUnixTimeNano`
	Layout TimeLayout

	// ParseInLocation is location used with time.ParseInLocation for layout that do not contain
	// timezone information to set output time in given location.
	// Defaults to time.UTC
	ParseInLocation *time.Location

	// ToInLocation is location to which parsed time is converted to after parsing.
	// The parsed time will be converted using time.In(ToInLocation).
	// Defaults to time.UTC
	ToInLocation *time.Location
}

// TimeLayout constants for parsing Unix timestamps in different precisions.
const (
	TimeLayoutUnixTime      = TimeLayout("UnixTime")      // Unix timestamp in seconds
	TimeLayoutUnixTimeMilli = TimeLayout("UnixTimeMilli") // Unix timestamp in milliseconds
	TimeLayoutUnixTimeNano  = TimeLayout("UnixTimeNano")  // Unix timestamp in nanoseconds
)

// ParseValues parses value to generic type slice. Same types are supported as ParseValue
// function but the result type is slice instead of scalar value.
//
// See ParseValue for supported types and options
func ParseValues[T any](values []string, opts ...any) ([]T, error) {
	var zero []T
	return ParseValuesOr(values, zero, opts...)
}

// ParseValuesOr parses value to generic type slice, when value is empty defaultValue is returned.
// Same types are supported as ParseValue function but the result type is slice instead of scalar value.
//
// See ParseValue for supported types and options
func ParseValuesOr[T any](values []string, defaultValue []T, opts ...any) ([]T, error) {
	if len(values) == 0 {
		return defaultValue, nil
	}
	result := make([]T, 0, len(values))
	for _, v := range values {
		tmp, err := ParseValue[T](v, opts...)
		if err != nil {
			return nil, err
		}
		result = append(result, tmp)
	}
	return result, nil
}

// ParseValue parses value to generic type
//
// Types that are supported:
//   - bool
//   - float32
//   - float64
//   - int
//   - int8
//   - int16
//   - int32
//   - int64
//   - uint
//   - uint8/byte
//   - uint16
//   - uint32
//   - uint64
//   - string
//   - types implementing UnmarshalParam(string) error
//   - encoding.TextUnmarshaler interface
//   - json.Unmarshaler interface
//   - time.Duration
//   - time.Time use bind.TimeOpts or bind.TimeLayout to set time parsing configuration
func ParseValue[T any](value string, opts ...any) (T, error) {
	var zero T
	return ParseValueOr(value, zero, opts...)
}

// ParseValueOr parses value to generic type, when value is empty defaultValue is returned.
//
// Types that are supported:
//   - bool
//   - float32
//   - float64
//   - int
//   - int8
//   - int16
//   - int32
//   - int64
//   - uint
//   - uint8/byte
//   - uint16
//   - uint32
//   - uint64
//   - string
//   - types implementing UnmarshalParam(string) error
//   - encoding.TextUnmarshaler interface
//   - json.Unmarshaler interface
//   - time.Duration
//   - time.Time use bind.TimeOpts or bind.TimeLayout to set time parsing configuration
func ParseValueOr[T any](value string, defaultValue T, opts ...any) (T, error) {
	if len(value) == 0 {
		return defaultValue, nil
	}
	var tmp T
	if err := bindValue(value, &tmp, opts...); err != nil {
		var zero T
		return zero, fmt.Errorf("failed to parse value, err: %w", err)
	}
	return tmp, nil
}

func bindValue(value string, dest any, opts ...any) error {
	// NOTE: if this function is ever made public the dest should be checked for nil
	// values when dealing with interfaces
	if len(opts) > 0 {
		if _, isTime := dest.(*time.Time); !isTime {
			return fmt.Errorf("options are only supported for time.Time, got %T", dest)
		}
	}

	switch d := dest.(type) {
	case *bool:
		n, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		*d = n
	case *float32:
		n, err := strconv.ParseFloat(value, 32)
		if err != nil {
			return err
		}
		*d = float32(n)
	case *float64:
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		*d = n
	case *int:
		n, err := strconv.ParseInt(value, 10, 0)
		if err != nil {
			return err
		}
		*d = int(n)
	case *int8:
		n, err := strconv.ParseInt(value, 10, 8)
		if err != nil {
			return err
		}
		*d = int8(n)
	case *int16:
		n, err := strconv.ParseInt(value, 10, 16)
		if err != nil {
			return err
		}
		*d = int16(n)
	case *int32:
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return err
		}
		*d = int32(n)
	case *int64:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		*d = n
	case *uint:
		n, err := strconv.ParseUint(value, 10, 0)
		if err != nil {
			return err
		}
		*d = uint(n)
	case *uint8:
		n, err := strconv.ParseUint(value, 10, 8)
		if err != nil {
			return err
		}
		*d = uint8(n)
	case *uint16:
		n, err := strconv.ParseUint(value, 10, 16)
		if err != nil {
			return err
		}
		*d = uint16(n)
	case *uint32:
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return err
		}
		*d = uint32(n)
	case *uint64:
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		*d = n
	case *string:
		*d = value
	case *time.Duration:
		t, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = t
	case *time.Time:
		to := TimeOpts{
			Layout:          TimeLayout(time.RFC3339Nano),
			ParseInLocation: time.UTC,
			ToInLocation:    time.UTC,
		}
		for _, o := range opts {
			switch v := o.(type) {
			case TimeOpts:
				if v.Layout != "" {
					to.Layout = v.Layout
				}
				if v.ParseInLocation != nil {
					to.ParseInLocation = v.ParseInLocation
				}
				if v.ToInLocation != nil {
					to.ToInLocation = v.ToInLocation
				}
			case TimeLayout:
				to.Layout = v
			}
		}
		var t time.Time
		var err error
		switch to.Layout {
		case TimeLayoutUnixTime:
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			t = time.Unix(n, 0)
		case TimeLayoutUnixTimeMilli:
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			t = time.UnixMilli(n)
		case TimeLayoutUnixTimeNano:
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			t = time.Unix(0, n)
		default:
			if to.ParseInLocation != nil {
				t, err = time.ParseInLocation(string(to.Layout), value, to.ParseInLocation)
			} else {
				t, err = time.Parse(string(to.Layout), value)
			}
			if err != nil {
				return err
			}
		}
		*d = t.In(to.ToInLocation)
	case interface{ UnmarshalParam(string) error }:
		if err := d.UnmarshalParam(value); err != nil {
			return err
		}
	case encoding.TextUnmarshaler:
		if err := d.UnmarshalText([]byte(value)); err != nil {
			return err
		}
	case json.Unmarshaler:
		if err := d.UnmarshalJSON([]byte(value)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported value type: %T", dest)
	}
	return nil
}
