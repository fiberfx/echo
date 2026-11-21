// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/httprule"
)

// AddTrailingSlashConfig is the middleware config for adding trailing slash to the request.
type AddTrailingSlashConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper Skipper

	// Status code to be used when redirecting the request.
	// Optional, but when provided the request is redirected using this code.
	// Valid status codes: [300...308]
	RedirectCode int
}

// AddTrailingSlash returns a root level (before router) middleware which adds a
// trailing slash to the request `URL#Path`.
//
// Usage `Echo#Pre(AddTrailingSlash())`
func AddTrailingSlash() echo.MiddlewareFunc {
	return AddTrailingSlashWithConfig(AddTrailingSlashConfig{})
}

// AddTrailingSlashWithConfig returns an AddTrailingSlash middleware with config or panics on invalid configuration.
func AddTrailingSlashWithConfig(config AddTrailingSlashConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

// ToMiddleware converts AddTrailingSlashConfig to middleware or returns an error for invalid configuration
func (config AddTrailingSlashConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}
	if config.RedirectCode != 0 && (config.RedirectCode < http.StatusMultipleChoices || config.RedirectCode > http.StatusPermanentRedirect) {
		// this is same check as `echo.context.Redirect()` does, but we can check this before even serving the request.
		return nil, errors.New("invalid redirect code for add trailing slash middleware")
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if config.Skipper(c) {
				return next(c)
			}

			req := c.Request()
			url := req.URL
			path := url.Path
			rawPath := url.EscapedPath()
			qs := c.QueryString()
			if !strings.HasSuffix(rawPath, "/") {
				path += "/"
				rawPath += "/"
				uri := rawPath
				if qs != "" {
					uri += "?" + qs
				}

				// Redirect
				if config.RedirectCode != 0 {
					return c.Redirect(config.RedirectCode, httprule.SanitizeURI(uri))
				}

				// Forward
				req.RequestURI = uri
				url.Path = path
				if url.RawPath != "" {
					url.RawPath = rawPath
				}
			}
			return next(c)
		}
	}, nil
}

// RemoveTrailingSlashConfig is the middleware config for removing trailing slash from the request.
type RemoveTrailingSlashConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper Skipper

	// Status code to be used when redirecting the request.
	// Optional, but when provided the request is redirected using this code.
	RedirectCode int
}

// RemoveTrailingSlash returns a root level (before router) middleware which removes
// a trailing slash from the request URI.
//
// Usage `Echo#Pre(RemoveTrailingSlash())`
func RemoveTrailingSlash() echo.MiddlewareFunc {
	return RemoveTrailingSlashWithConfig(RemoveTrailingSlashConfig{})
}

// RemoveTrailingSlashWithConfig returns a RemoveTrailingSlash middleware with config or panics on invalid configuration.
func RemoveTrailingSlashWithConfig(config RemoveTrailingSlashConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

// ToMiddleware converts RemoveTrailingSlashConfig to middleware or returns an error for invalid configuration
func (config RemoveTrailingSlashConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}
	if config.RedirectCode != 0 && (config.RedirectCode < http.StatusMultipleChoices || config.RedirectCode > http.StatusPermanentRedirect) {
		// this is same check as `echo.context.Redirect()` does, but we can check this before even serving the request.
		return nil, errors.New("invalid redirect code for remove trailing slash middleware")
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if config.Skipper(c) {
				return next(c)
			}

			req := c.Request()
			url := req.URL
			path := url.Path
			rawPath := url.EscapedPath()
			qs := c.QueryString()
			if len(rawPath) > 1 && strings.HasSuffix(rawPath, "/") {
				path = path[:len(path)-1]
				rawPath = rawPath[:len(rawPath)-1]
				uri := rawPath
				if qs != "" {
					uri += "?" + qs
				}

				// Redirect
				if config.RedirectCode != 0 {
					return c.Redirect(config.RedirectCode, httprule.SanitizeURI(uri))
				}

				// Forward
				req.RequestURI = uri
				url.Path = path
				if url.RawPath != "" {
					url.RawPath = rawPath
				}
			}
			return next(c)
		}
	}, nil
}
