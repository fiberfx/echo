// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/httprule"
)

const gzipScheme = "gzip"

// GzipConfig defines the config for Gzip middleware.
type GzipConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper Skipper

	// Gzip compression level.
	// Optional. Default value -1.
	Level int

	// Length threshold before gzip compression is applied.
	// Optional. Default value 0.
	//
	// Most of the time you will not need to change the default. Compressing
	// a short response might increase the transmitted data because of the
	// gzip format overhead. Compressing the response will also consume CPU
	// and time on the server and the client (for decompressing). Depending on
	// your use case such a threshold might be useful.
	//
	// See also:
	// https://webmasters.stackexchange.com/questions/31750/what-is-recommended-minimum-object-size-for-gzip-performance-benefits
	MinLength int
}

type gzipResponseWriter struct {
	http.ResponseWriter
	compressor *gzip.Writer
	buffer     *bytes.Buffer
	minLength  int
	code       int
	wroteBody  bool
	compressed bool
	encoded    bool
}

// Gzip returns a middleware which compresses HTTP response using gzip compression scheme.
func Gzip() echo.MiddlewareFunc {
	return GzipWithConfig(GzipConfig{})
}

// GzipWithConfig returns a middleware which compresses HTTP response using gzip compression scheme.
func GzipWithConfig(config GzipConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

// ToMiddleware converts GzipConfig to middleware or returns an error for invalid configuration
func (config GzipConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}
	if config.Level < gzip.HuffmanOnly || config.Level > gzip.BestCompression {
		return nil, errors.New("invalid gzip level")
	}
	if config.Level == 0 {
		config.Level = gzip.DefaultCompression
	}
	config.MinLength = max(0, config.MinLength)
	compressors := sync.Pool{New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, config.Level) // Level was validated above.
		return w
	}}
	buffers := sync.Pool{New: func() any { return new(bytes.Buffer) }}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if config.Skipper(c) {
				return next(c)
			}
			res := c.Response()
			res.Header().Add(echo.HeaderVary, echo.HeaderAcceptEncoding)
			if !httprule.AcceptsGzip(c.Request().Header.Values(echo.HeaderAcceptEncoding)) {
				return next(c)
			}

			w := &gzipResponseWriter{
				ResponseWriter: res,
				compressor:     compressors.Get().(*gzip.Writer),
				buffer:         buffers.Get().(*bytes.Buffer),
				minLength:      config.MinLength,
			}
			c.SetResponse(w)
			var succeeded bool
			defer func() {
				c.SetResponse(res)
				w.finish(succeeded)
				w.compressor.Reset(io.Discard)
				w.buffer.Reset()
				compressors.Put(w.compressor)
				buffers.Put(w.buffer)
			}()

			err := next(c)
			succeeded = err == nil
			return err
		}
	}, nil
}

func (w *gzipResponseWriter) finish(succeeded bool) {
	if w.compressed {
		_ = w.compressor.Close()
		return
	}
	if w.encoded || (succeeded && w.passThroughIfEncoded()) {
		return
	}
	if !w.wroteBody {
		w.Header().Del(echo.HeaderContentEncoding)
		if !succeeded {
			// An error handler can supply a different body.
			w.Header().Del(echo.HeaderContentLength)
		}
	}
	w.commitHeader()
	if w.wroteBody {
		_, _ = w.buffer.WriteTo(w.ResponseWriter)
	}
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.code != 0 || w.wroteBody || w.compressed || w.encoded {
		return
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	// Defer the final status until the response encoding is known.
	w.code = code
	w.passThroughIfEncoded()
}

func (w *gzipResponseWriter) commitHeader() {
	if w.code != 0 {
		w.ResponseWriter.WriteHeader(w.code)
	}
}

func (w *gzipResponseWriter) passThroughIfEncoded() bool {
	if !w.wroteBody && !w.compressed && !w.encoded && httprule.HasContentCoding(w.Header().Values(echo.HeaderContentEncoding)) {
		w.encoded = true
		w.commitHeader()
	}
	return w.encoded
}

func (w *gzipResponseWriter) startCompression() error {
	w.compressed = true
	w.Header().Set(echo.HeaderContentEncoding, gzipScheme)
	w.Header().Del(echo.HeaderContentLength)
	w.commitHeader()
	w.compressor.Reset(w.ResponseWriter)
	if w.buffer.Len() == 0 {
		return nil
	}
	_, err := w.buffer.WriteTo(w.compressor)
	return err
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.passThroughIfEncoded() {
		return w.ResponseWriter.Write(b)
	}
	if w.Header().Get(echo.HeaderContentType) == "" {
		w.Header().Set(echo.HeaderContentType, http.DetectContentType(b))
	}
	w.wroteBody = true
	if !w.compressed {
		if len(b) < w.minLength-w.buffer.Len() {
			return w.buffer.Write(b)
		}
		if err := w.startCompression(); err != nil {
			return 0, err
		}
	}
	return w.compressor.Write(b)
}

func (w *gzipResponseWriter) Flush() {
	if !w.passThroughIfEncoded() {
		if !w.compressed {
			_ = w.startCompression()
		}
		_ = w.compressor.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *gzipResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}
