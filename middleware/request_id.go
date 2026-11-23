// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"github.com/fiberfx/mx-echo/v7"
)

// RequestIDConfig defines the config for RequestID middleware.
type RequestIDConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper Skipper

	// Generator defines a function to generate an ID.
	// Optional. Default value random.String(32).
	Generator func() string

	// RequestIDHandler defines a function which is executed for a request id.
	RequestIDHandler func(c *echo.Context, requestID string)

	// TargetHeader defines what header to look for to populate the id.
	// Optional. Default value is `X-Request-Id`
	TargetHeader string
}

// RequestID returns a middleware that reads RequestIDConfig.TargetHeader (`X-Request-Id`) header value or when
// the header value is empty, generates that value and sets request ID to response
// as RequestIDConfig.TargetHeader (`X-Request-Id`) value.
//
// When echo.Config.Observer names requests, the request's name (echo.Context.RequestID) is the request ID: the
// header value of the request is ignored and the Generator is not called.
func RequestID() echo.MiddlewareFunc {
	return RequestIDWithConfig(RequestIDConfig{})
}

// RequestIDWithConfig returns a middleware with given valid config or panics on invalid configuration.
// The middleware reads RequestIDConfig.TargetHeader (`X-Request-Id`) header value or when the header value is empty,
// generates that value and sets request ID to response as RequestIDConfig.TargetHeader (`X-Request-Id`) value.
func RequestIDWithConfig(config RequestIDConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

// ToMiddleware converts RequestIDConfig to middleware or returns an error for invalid configuration
func (config RequestIDConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}
	if config.Generator == nil {
		config.Generator = createRandomStringGenerator(32)
	}
	if config.TargetHeader == "" {
		config.TargetHeader = echo.HeaderXRequestID
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if config.Skipper(c) {
				return next(c)
			}

			req := c.Request()
			res := c.Response()
			rid := req.Header.Get(config.TargetHeader)
			if id := c.RequestID(); !id.IsZero() {
				rid = id.String() // the request is named (echo.Config.Observer): one id, never the client's
			} else if rid == "" {
				rid = config.Generator()
			}
			res.Header().Set(config.TargetHeader, rid)
			if config.RequestIDHandler != nil {
				config.RequestIDHandler(c, rid)
			}

			return next(c)
		}
	}, nil
}
