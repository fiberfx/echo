// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/fiberfx/echo/v6"
)

// Skipper defines a function to skip middleware. Returning true skips processing the middleware.
type Skipper func(c *echo.Context) bool

// BeforeFunc defines a function which is executed just before the middleware.
type BeforeFunc func(c *echo.Context)

func captureTokens(pattern *regexp.Regexp, input string) *strings.Replacer {
	match := pattern.FindStringSubmatch(input)
	if match == nil {
		return nil
	}
	values := match[1:]
	replace := make([]string, 2*len(values))
	for i, v := range values {
		j := 2 * i
		replace[j] = "$" + strconv.Itoa(i+1)
		replace[j+1] = v
	}
	return strings.NewReplacer(replace...)
}

func rewriteRulesRegex(rewrite map[string]string) map[*regexp.Regexp]string {
	// Initialize
	rulesRegex := map[*regexp.Regexp]string{}
	for k, v := range rewrite {
		k = regexp.QuoteMeta(k)
		k = strings.ReplaceAll(k, `\*`, "(.*?)")
		if strings.HasPrefix(k, `\^`) {
			k = strings.ReplaceAll(k, `\^`, "^")
		}
		k = k + "$"
		rulesRegex[regexp.MustCompile(k)] = v
	}
	return rulesRegex
}

// rewriteURL applies the first matching rule. A routed target must be a literal path on the same authority.
func rewriteURL(rewriteRegex map[*regexp.Regexp]string, req *http.Request, routed bool) error {
	if len(rewriteRegex) == 0 {
		return nil
	}

	// Depending how HTTP request is sent RequestURI could contain Scheme://Host/path or be just /path.
	// We only want to use path part for rewriting and therefore trim prefix if it exists
	rawURI := req.RequestURI
	if rawURI != "" && rawURI[0] != '/' {
		prefix := ""
		if req.URL.Scheme != "" {
			prefix = req.URL.Scheme + "://"
		}
		if req.URL.Host != "" {
			prefix += req.URL.Host // host or host:port
		}
		if prefix != "" {
			rawURI = strings.TrimPrefix(rawURI, prefix)
		}
	}

	for k, v := range rewriteRegex {
		if replacer := captureTokens(k, rawURI); replacer != nil {
			target := replacer.Replace(v)
			if routed {
				return setRewriteTarget(req, target)
			}
			url, err := req.URL.Parse(target)
			if err != nil {
				return err
			}
			req.URL = url

			return nil // rewrite only once
		}
	}
	return nil
}

// setRewriteTarget updates only the path and query of a request whose route has already been selected.
func setRewriteTarget(req *http.Request, target string) error {
	target, _, _ = strings.Cut(target, "#")
	rawPath, query, _ := strings.Cut(target, "?")
	path, err := url.PathUnescape(rawPath)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || hasDotSegment(path) {
		return echo.ErrBadRequest
	}

	u := *req.URL
	u.Path = path
	u.RawPath = ""
	if u.EscapedPath() != rawPath {
		u.RawPath = rawPath
	}
	u.RawQuery = query
	u.ForceQuery = false
	u.Fragment, u.RawFragment = "", ""
	req.URL = &u
	return nil
}

func hasDotSegment(path string) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

// isRouted includes router misses, which carry a named fallback route.
func isRouted(c *echo.Context) bool {
	route := c.RouteInfo()
	return route.Method != "" || route.Name != ""
}

// DefaultSkipper returns false which processes the middleware.
func DefaultSkipper(c *echo.Context) bool {
	return false
}

func toMiddlewareOrPanic(config echo.MiddlewareConfigurator) echo.MiddlewareFunc {
	mw, err := config.ToMiddleware()
	if err != nil {
		panic(err)
	}
	return mw
}
