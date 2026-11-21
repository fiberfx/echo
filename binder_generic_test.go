// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"cmp"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPathParam(t *testing.T) {
	var testCases = []struct {
		name       string
		givenKey   string
		givenValue string
		expect     bool
		expectErr  string
	}{
		{
			name:       "ok",
			givenValue: "true",
			expect:     true,
		},
		{
			name:       "nok, non existent key",
			givenKey:   "missing",
			givenValue: "true",
			expect:     false,
			expectErr:  ErrNonExistentKey.Error(),
		},
		{
			name:       "nok, invalid value",
			givenValue: "can_parse_me",
			expect:     false,
			expectErr:  `code=400, message=path value, err=failed to parse value, err: strconv.ParseBool: parsing "can_parse_me": invalid syntax, field=key`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewContext(nil, nil)
			c.SetPathValues(PathValues{{
				Name:  cmp.Or(tc.givenKey, "key"),
				Value: tc.givenValue,
			}})

			v, err := PathParam[bool](c, "key")
			if tc.expectErr != "" {
				assert.EqualError(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestPathParam_UnsupportedType(t *testing.T) {
	c := NewContext(nil, nil)
	c.SetPathValues(PathValues{{Name: "key", Value: "true"}})

	v, err := PathParam[[]bool](c, "key")

	expectErr := "code=400, message=path value, err=failed to parse value, err: unsupported value type: *[]bool, field=key"
	assert.EqualError(t, err, expectErr)
	assert.Equal(t, []bool(nil), v)
}

func TestQueryParam(t *testing.T) {
	var testCases = []struct {
		name      string
		givenURL  string
		expect    bool
		expectErr string
	}{
		{
			name:     "ok",
			givenURL: "/?key=true",
			expect:   true,
		},
		{
			name:      "nok, non existent key",
			givenURL:  "/?different=true",
			expect:    false,
			expectErr: ErrNonExistentKey.Error(),
		},
		{
			name:      "nok, invalid value",
			givenURL:  "/?key=invalidbool",
			expect:    false,
			expectErr: `code=400, message=query param, err=failed to parse value, err: strconv.ParseBool: parsing "invalidbool": invalid syntax, field=key`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.givenURL, nil)
			c := NewContext(req, nil)

			v, err := QueryParam[bool](c, "key")
			if tc.expectErr != "" {
				assert.EqualError(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestQueryParam_UnsupportedType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/?key=bool", nil)
	c := NewContext(req, nil)

	v, err := QueryParam[[]bool](c, "key")

	expectErr := "code=400, message=query param, err=failed to parse value, err: unsupported value type: *[]bool, field=key"
	assert.EqualError(t, err, expectErr)
	assert.Equal(t, []bool(nil), v)
}

func TestQueryParams(t *testing.T) {
	var testCases = []struct {
		name      string
		givenURL  string
		expect    []bool
		expectErr string
	}{
		{
			name:     "ok",
			givenURL: "/?key=true&key=false",
			expect:   []bool{true, false},
		},
		{
			name:      "nok, non existent key",
			givenURL:  "/?different=true",
			expect:    []bool(nil),
			expectErr: ErrNonExistentKey.Error(),
		},
		{
			name:      "nok, invalid value",
			givenURL:  "/?key=true&key=invalidbool",
			expect:    []bool(nil),
			expectErr: `code=400, message=query params, err=failed to parse value, err: strconv.ParseBool: parsing "invalidbool": invalid syntax, field=key`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.givenURL, nil)
			c := NewContext(req, nil)

			v, err := QueryParams[bool](c, "key")
			if tc.expectErr != "" {
				assert.EqualError(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestQueryParams_UnsupportedType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/?key=bool", nil)
	c := NewContext(req, nil)

	v, err := QueryParams[[]bool](c, "key")

	expectErr := "code=400, message=query params, err=failed to parse value, err: unsupported value type: *[]bool, field=key"
	assert.EqualError(t, err, expectErr)
	assert.Equal(t, [][]bool(nil), v)
}

func TestFormValue(t *testing.T) {
	var testCases = []struct {
		name      string
		givenURL  string
		expect    bool
		expectErr string
	}{
		{
			name:     "ok",
			givenURL: "/?key=true",
			expect:   true,
		},
		{
			name:      "nok, non existent key",
			givenURL:  "/?different=true",
			expect:    false,
			expectErr: ErrNonExistentKey.Error(),
		},
		{
			name:      "nok, invalid value",
			givenURL:  "/?key=invalidbool",
			expect:    false,
			expectErr: `code=400, message=form value, err=failed to parse value, err: strconv.ParseBool: parsing "invalidbool": invalid syntax, field=key`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.givenURL, nil)
			c := NewContext(req, nil)

			v, err := FormValue[bool](c, "key")
			if tc.expectErr != "" {
				assert.EqualError(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestFormValue_UnsupportedType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/?key=bool", nil)
	c := NewContext(req, nil)

	v, err := FormValue[[]bool](c, "key")

	expectErr := "code=400, message=form value, err=failed to parse value, err: unsupported value type: *[]bool, field=key"
	assert.EqualError(t, err, expectErr)
	assert.Equal(t, []bool(nil), v)
}

func TestFormValues(t *testing.T) {
	var testCases = []struct {
		name      string
		givenURL  string
		expect    []bool
		expectErr string
	}{
		{
			name:     "ok",
			givenURL: "/?key=true&key=false",
			expect:   []bool{true, false},
		},
		{
			name:      "nok, non existent key",
			givenURL:  "/?different=true",
			expect:    []bool(nil),
			expectErr: ErrNonExistentKey.Error(),
		},
		{
			name:      "nok, invalid value",
			givenURL:  "/?key=true&key=invalidbool",
			expect:    []bool(nil),
			expectErr: `code=400, message=form values, err=failed to parse value, err: strconv.ParseBool: parsing "invalidbool": invalid syntax, field=key`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.givenURL, nil)
			c := NewContext(req, nil)

			v, err := FormValues[bool](c, "key")
			if tc.expectErr != "" {
				assert.EqualError(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestFormValues_UnsupportedType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/?key=bool", nil)
	c := NewContext(req, nil)

	v, err := FormValues[[]bool](c, "key")

	expectErr := "code=400, message=form values, err=failed to parse value, err: unsupported value type: *[]bool, field=key"
	assert.EqualError(t, err, expectErr)
	assert.Equal(t, [][]bool(nil), v)
}

func TestParseValue_BindUnmarshaler(t *testing.T) {
	exampleTime, _ := time.Parse(time.RFC3339, "2020-12-23T09:45:31+02:00")

	var testCases = []struct {
		name      string
		when      string
		expect    Timestamp
		expectErr string
	}{
		{
			name:   "ok",
			when:   "2020-12-23T09:45:31+02:00",
			expect: Timestamp(exampleTime),
		},
		{
			name:      "nok, invalid value",
			when:      "2020-12-23T09:45:3102:00",
			expect:    Timestamp{},
			expectErr: `failed to parse value, err: parsing time "2020-12-23T09:45:3102:00" as "2006-01-02T15:04:05Z07:00": cannot parse "02:00" as "Z07:00"`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ParseValue[Timestamp](tc.when)
			if tc.expectErr != "" {
				assert.EqualError(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestPathParamOr(t *testing.T) {
	var testCases = []struct {
		name         string
		givenKey     string
		givenValue   string
		defaultValue int
		expect       int
		expectErr    string
	}{
		{
			name:         "ok, param exists",
			givenKey:     "id",
			givenValue:   "123",
			defaultValue: 999,
			expect:       123,
		},
		{
			name:         "ok, param missing - returns default",
			givenKey:     "other",
			givenValue:   "123",
			defaultValue: 999,
			expect:       999,
		},
		{
			name:         "ok, param exists but empty - returns default",
			givenKey:     "id",
			givenValue:   "",
			defaultValue: 999,
			expect:       999,
		},
		{
			name:         "nok, invalid value",
			givenKey:     "id",
			givenValue:   "invalid",
			defaultValue: 999,
			expectErr:    "code=400, message=path value, err=failed to parse value",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewContext(nil, nil)
			c.SetPathValues(PathValues{{Name: tc.givenKey, Value: tc.givenValue}})

			v, err := PathParamOr[int](c, "id", tc.defaultValue)
			if tc.expectErr != "" {
				assert.ErrorContains(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestQueryParamOr(t *testing.T) {
	var testCases = []struct {
		name         string
		givenURL     string
		defaultValue int
		expect       int
		expectErr    string
	}{
		{
			name:         "ok, param exists",
			givenURL:     "/?key=42",
			defaultValue: 999,
			expect:       42,
		},
		{
			name:         "ok, param missing - returns default",
			givenURL:     "/?other=42",
			defaultValue: 999,
			expect:       999,
		},
		{
			name:         "ok, param exists but empty - returns default",
			givenURL:     "/?key=",
			defaultValue: 999,
			expect:       999,
		},
		{
			name:         "nok, invalid value",
			givenURL:     "/?key=invalid",
			defaultValue: 999,
			expectErr:    "code=400, message=query param",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.givenURL, nil)
			c := NewContext(req, nil)

			v, err := QueryParamOr[int](c, "key", tc.defaultValue)
			if tc.expectErr != "" {
				assert.ErrorContains(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestQueryParamsOr(t *testing.T) {
	var testCases = []struct {
		name         string
		givenURL     string
		defaultValue []int
		expect       []int
		expectErr    string
	}{
		{
			name:         "ok, params exist",
			givenURL:     "/?key=1&key=2&key=3",
			defaultValue: []int{999},
			expect:       []int{1, 2, 3},
		},
		{
			name:         "ok, params missing - returns default",
			givenURL:     "/?other=1",
			defaultValue: []int{7, 8, 9},
			expect:       []int{7, 8, 9},
		},
		{
			name:         "nok, invalid value",
			givenURL:     "/?key=1&key=invalid",
			defaultValue: []int{999},
			expectErr:    "code=400, message=query params",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.givenURL, nil)
			c := NewContext(req, nil)

			v, err := QueryParamsOr[int](c, "key", tc.defaultValue)
			if tc.expectErr != "" {
				assert.ErrorContains(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestFormValueOr(t *testing.T) {
	var testCases = []struct {
		name         string
		givenURL     string
		defaultValue string
		expect       string
		expectErr    string
	}{
		{
			name:         "ok, value exists",
			givenURL:     "/?name=john",
			defaultValue: "default",
			expect:       "john",
		},
		{
			name:         "ok, value missing - returns default",
			givenURL:     "/?other=john",
			defaultValue: "default",
			expect:       "default",
		},
		{
			name:         "ok, value exists but empty - returns default",
			givenURL:     "/?name=",
			defaultValue: "default",
			expect:       "default",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.givenURL, nil)
			c := NewContext(req, nil)

			v, err := FormValueOr[string](c, "name", tc.defaultValue)
			if tc.expectErr != "" {
				assert.ErrorContains(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}

func TestFormValuesOr(t *testing.T) {
	var testCases = []struct {
		name         string
		givenURL     string
		defaultValue []string
		expect       []string
		expectErr    string
	}{
		{
			name:         "ok, values exist",
			givenURL:     "/?tags=go&tags=rust&tags=python",
			defaultValue: []string{"default"},
			expect:       []string{"go", "rust", "python"},
		},
		{
			name:         "ok, values missing - returns default",
			givenURL:     "/?other=value",
			defaultValue: []string{"a", "b"},
			expect:       []string{"a", "b"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.givenURL, nil)
			c := NewContext(req, nil)

			v, err := FormValuesOr[string](c, "tags", tc.defaultValue)
			if tc.expectErr != "" {
				assert.ErrorContains(t, err, tc.expectErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expect, v)
		})
	}
}
