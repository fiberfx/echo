// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// This file pins the bind engine's error texts and what a client sees of query, form and multipart binding,
// taken at v6.5.0 (2db3c81) before the engine moved to internal/bindengine. A move changes none of them.

// checkGolden is the assertion: an error unless err's text is want, byte for byte.
func checkGolden(err error, want string) error {
	if err == nil {
		return fmt.Errorf("no error, want %q", want)
	}
	if got := err.Error(); got != want {
		return fmt.Errorf("got %q, want %q", got, want)
	}
	return nil
}

func TestBindGoldenErrorTexts(t *testing.T) {
	type Embedded struct {
		A string `form:"a"`
	}
	data := map[string][]string{"id": {"x"}}
	tests := []struct {
		name string
		bind func() error
		want string
	}{
		{
			name: "map value",
			bind: func() error {
				var dest map[string]int
				return bindData(&dest, data, "param", nil)
			},
			want: "binding element must be a map with string, []string or any values",
		},
		{
			name: "not a struct",
			bind: func() error {
				var dest int
				return bindData(&dest, data, "form", nil)
			},
			want: "binding element must be a struct",
		},
		{
			name: "tagged anonymous struct",
			bind: func() error {
				var dest struct {
					Embedded `form:"embedded"`
				}
				return bindData(&dest, data, "form", nil)
			},
			want: "query/param/form tags are not allowed with anonymous struct field",
		},
		{
			name: "field value",
			bind: func() error {
				var dest struct {
					ID int `form:"id"`
				}
				return bindData(&dest, data, "form", nil)
			},
			want: `id: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			name: "unknown kind",
			bind: func() error {
				var dest complex64
				return setWithProperType(reflect.Complex64, "1", reflect.ValueOf(&dest).Elem())
			},
			want: "unknown type",
		},
		{
			name: "file header struct",
			bind: func() error {
				var dest struct {
					File multipart.FileHeader `form:"file"`
				}
				files := map[string][]*multipart.FileHeader{"file": {{Filename: "a.txt"}}}
				return bindData(&dest, nil, "form", files)
			},
			want: "binding to multipart.FileHeader struct is not supported, use pointer to struct",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkGolden(tt.bind(), tt.want); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestBindGoldenViolation(t *testing.T) {
	want := "binding element must be a struct"
	if checkGolden(errors.New(want[:len(want)-1]+"T"), want) == nil {
		t.Error("a text changed by one byte passed the check")
	}
	if checkGolden(nil, want) == nil {
		t.Error("no error passed the check")
	}
}

// goldenMultipart returns the content type and body of a multipart form with the field name=jo and the file
// "file" named a.txt holding "hello".
func goldenMultipart(t *testing.T) (string, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.SetBoundary("golden"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("name", "jo"); err != nil {
		t.Fatal(err)
	}
	fw, err := w.CreateFormFile("file", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.FormDataContentType(), body.String()
}

func TestBindGoldenServeHTTP(t *testing.T) {
	e := New()
	e.HTTPErrorHandler = DefaultHTTPErrorHandler(true)
	e.GET("/q", func(c *Context) error {
		var v struct {
			ID   int      `query:"id" json:"id"`
			Tags []string `query:"tags" json:"tags"`
		}
		if err := c.Bind(&v); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, v)
	})
	e.POST("/f", func(c *Context) error {
		var v struct {
			Name string `form:"name" json:"name"`
			Age  int    `form:"age" json:"age"`
		}
		if err := c.Bind(&v); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, v)
	})
	e.POST("/m", func(c *Context) error {
		var v struct {
			Name string                `form:"name"`
			File *multipart.FileHeader `form:"file"`
		}
		if err := c.Bind(&v); err != nil {
			return err
		}
		if v.File == nil {
			return c.String(http.StatusInternalServerError, "no file bound")
		}
		return c.JSON(http.StatusOK, struct {
			Name string `json:"name"`
			File string `json:"file"`
			Size int64  `json:"size"`
		}{v.Name, v.File.Filename, v.File.Size})
	})
	multipartType, multipartBody := goldenMultipart(t)

	tests := []struct {
		name        string
		method      string
		target      string
		contentType string
		body        string
		wantCode    int
		wantBody    string
	}{
		{
			name:     "query",
			method:   http.MethodGet,
			target:   "/q?id=7&tags=a&tags=b",
			wantCode: http.StatusOK,
			wantBody: `{"id":7,"tags":["a","b"]}` + "\n",
		},
		{
			name:        "form",
			method:      http.MethodPost,
			target:      "/f",
			contentType: MIMEApplicationForm,
			body:        "name=jo&age=3",
			wantCode:    http.StatusOK,
			wantBody:    `{"name":"jo","age":3}` + "\n",
		},
		{
			name:        "multipart",
			method:      http.MethodPost,
			target:      "/m",
			contentType: multipartType,
			body:        multipartBody,
			wantCode:    http.StatusOK,
			wantBody:    `{"name":"jo","file":"a.txt","size":5}` + "\n",
		},
		{
			name:     "query refused",
			method:   http.MethodGet,
			target:   "/q?id=x",
			wantCode: http.StatusBadRequest,
			wantBody: `{"error":"id: strconv.ParseInt: parsing \"x\": invalid syntax","message":"Bad Request"}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set(HeaderContentType, tt.contentType)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode || rec.Body.String() != tt.wantBody {
				t.Errorf("got %d %q, want %d %q", rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
			}
		})
	}
}
