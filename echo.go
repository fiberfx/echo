// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

/*
Package echo implements MX Echo, a minimalist Go HTTP framework.

Example:

	package main

	import (
		"log/slog"
		"net/http"

		"github.com/fiberfx/mx-echo/v6"
		"github.com/fiberfx/mx-echo/v6/middleware"
	)

	// Handler
	func hello(c *echo.Context) error {
		return c.String(http.StatusOK, "Hello, World!")
	}

	func main() {
		// Echo instance
		e := echo.New()

		// Middleware
		e.Use(middleware.RequestLogger())
		e.Use(middleware.Recover())

		// Routes
		e.GET("/", hello)

		// Start server
		if err := e.Start(":8080"); err != nil {
			slog.Error("failed to start server", "error", err)
		}
	}

Learn more at https://github.com/fiberfx/mx-echo
*/
package echo

import (
	stdContext "context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/fiberfx/mx-echo/v6/internal/httprule"
	"github.com/fiberfx/mx-echo/v6/observe"
	"github.com/fiberfx/mx-echo/v6/outcome"
)

// Echo is the top-level framework instance.
//
// Goroutine safety: Do not mutate Echo instance fields after server has started. Accessing these
// fields from handlers/middlewares and changing field values at the same time leads to data-races.
// Same rule applies to adding new routes after server has been started - Adding a route is not Goroutine safe action.
type Echo struct {
	serveHTTPFunc func(http.ResponseWriter, *http.Request)

	Binder Binder

	// Filesystem is the file system used for serving static files. Defaults to the current working directory (os.Getwd()).
	//
	// Note: fs.FS.Open() already assumes that file names are relative to FS root path and considers name with prefix `/` as invalid
	// so if you have `fs := os.DirFS("/tmp")` and you try to `fs.Open("/tmp/file.txt")` it will fail, but "file.txt"
	// would succeed. `echo.NewDefaultFS("/tmp")` overwrites this behavior and allows you to use Open with a matching
	// absolute path prefix.
	Filesystem fs.FS

	Renderer         Renderer
	Validator        Validator
	JSONSerializer   JSONSerializer
	IPExtractor      IPExtractor
	SchemeExtractor  SchemeExtractor
	OnAddRoute       func(route Route) error
	HTTPErrorHandler HTTPErrorHandler
	Logger           *slog.Logger

	contextPool sync.Pool

	router Router

	// premiddleware are middlewares that are called before routing is done
	premiddleware []MiddlewareFunc

	// middleware are middlewares that are called after routing is done and before handler is called
	middleware []MiddlewareFunc

	// chain is the global middleware chain (e.middleware) compiled once and reused for every request.
	// It terminates in a dispatcher that invokes the route handler stored on the Context during routing.
	// Rebuilt by Use(). See buildRouterChains.
	chain HandlerFunc
	// preChain is the pre-middleware chain (e.premiddleware) compiled once. It performs routing and then
	// invokes chain. Rebuilt by Pre()/Use(). Only used when premiddleware is registered.
	preChain HandlerFunc

	contextPathParamAllocSize atomic.Int32

	// frozen is set by Freeze. Only the registration paths read it.
	frozen atomic.Bool

	// formParseMaxMemory is passed to Context for multipart form parsing (See http.Request.ParseMultipartForm)
	formParseMaxMemory int64

	// noGroupAutoRegisterRoutes is a flag that indicates whether echo.Group should NOT register 404 routes automatically
	// when there are middlewares registered with the group.
	noGroupAutoRegisterRoutes bool

	enablePathUnescapingStaticFiles bool

	// observer is Config.Observer when it has a Sink, else nil. serveHTTP checks it once per request.
	observer *observe.Observer
}

// JSONSerializer is the interface that encodes and decodes JSON to and from interfaces.
type JSONSerializer interface {
	Serialize(c *Context, target any, indent string) error
	Deserialize(c *Context, target any) error
}

// HTTPErrorHandler is a centralized HTTP error handler.
type HTTPErrorHandler func(c *Context, err error)

// HandlerFunc defines a function to serve HTTP requests.
type HandlerFunc func(c *Context) error

// MiddlewareFunc defines a function to process middleware.
type MiddlewareFunc func(next HandlerFunc) HandlerFunc

// MiddlewareConfigurator defines interface for creating middleware handlers with possibility to return configuration errors instead of panicking.
type MiddlewareConfigurator interface {
	ToMiddleware() (MiddlewareFunc, error)
}

// Validator is the interface that wraps the Validate function.
type Validator interface {
	Validate(i any) error
}

// MIME types
const (
	// MIMEApplicationJSON JavaScript Object Notation (JSON) https://www.rfc-editor.org/rfc/rfc8259
	MIMEApplicationJSON = "application/json"
	// Deprecated: Please use MIMEApplicationJSON instead. JSON should be encoded using UTF-8 by default.
	// No "charset" parameter is defined for this registration.
	// Adding one really has no effect on compliant recipients.
	// See RFC 8259, section 8.1. https://datatracker.ietf.org/doc/html/rfc8259#section-8.1n"
	MIMEApplicationJSONCharsetUTF8       = MIMEApplicationJSON + "; " + charsetUTF8
	MIMEApplicationJavaScript            = "application/javascript"
	MIMEApplicationJavaScriptCharsetUTF8 = MIMEApplicationJavaScript + "; " + charsetUTF8
	MIMEApplicationXML                   = "application/xml"
	MIMEApplicationXMLCharsetUTF8        = MIMEApplicationXML + "; " + charsetUTF8
	MIMETextXML                          = "text/xml"
	MIMETextXMLCharsetUTF8               = MIMETextXML + "; " + charsetUTF8
	MIMEApplicationForm                  = "application/x-www-form-urlencoded"
	MIMEApplicationProtobuf              = "application/protobuf"
	MIMEApplicationMsgpack               = "application/msgpack"
	// MIMEApplicationProblemJSON is the content type for RFC 9457 Problem Details responses.
	// https://www.rfc-editor.org/rfc/rfc9457
	MIMEApplicationProblemJSON = "application/problem+json"
	MIMETextHTML               = "text/html"
	MIMETextHTMLCharsetUTF8    = MIMETextHTML + "; " + charsetUTF8
	MIMETextPlain              = "text/plain"
	MIMETextPlainCharsetUTF8   = MIMETextPlain + "; " + charsetUTF8
	MIMEMultipartForm          = "multipart/form-data"
	MIMEOctetStream            = "application/octet-stream"
)

const (
	charsetUTF8 = "charset=UTF-8"
	// PROPFIND Method can be used on collection and property resources.
	PROPFIND = "PROPFIND"
	// REPORT Method can be used to get information about a resource, see rfc 3253
	REPORT = "REPORT"
	// QUERY Method is a safe, idempotent request method carrying request content (a query) in its body, see rfc 10008.
	// It is not (yet) part of the `net/http` standard library, so Echo defines it here.
	QUERY = "QUERY"
	// RouteNotFound is a special method type for routes handling "route not found" (404) cases
	RouteNotFound = "echo_route_not_found"
	// RouteAny is a special method type that matches any HTTP method in request. Any has lower
	// priority that other methods that have been registered with Router to that path.
	RouteAny = "echo_route_any"
)

// Headers
const (
	HeaderAccept         = "Accept"
	HeaderAcceptEncoding = "Accept-Encoding"
	// HeaderAllow is the name of the "Allow" header field used to list the set of methods
	// advertised as supported by the target resource. Returning an Allow header is mandatory
	// for status 405 (method not found) and useful for the OPTIONS method in responses.
	// See RFC 7231: https://datatracker.ietf.org/doc/html/rfc7231#section-7.4.1
	HeaderAllow               = "Allow"
	HeaderAuthorization       = "Authorization"
	HeaderContentDisposition  = "Content-Disposition"
	HeaderContentEncoding     = "Content-Encoding"
	HeaderContentLength       = "Content-Length"
	HeaderContentType         = "Content-Type"
	HeaderCookie              = "Cookie"
	HeaderSetCookie           = "Set-Cookie"
	HeaderIfModifiedSince     = "If-Modified-Since"
	HeaderLastModified        = "Last-Modified"
	HeaderLocation            = "Location"
	HeaderRetryAfter          = "Retry-After"
	HeaderUpgrade             = "Upgrade"
	HeaderVary                = "Vary"
	HeaderWWWAuthenticate     = "WWW-Authenticate"
	HeaderXForwardedFor       = "X-Forwarded-For"
	HeaderXForwardedProto     = "X-Forwarded-Proto"
	HeaderXForwardedProtocol  = "X-Forwarded-Protocol"
	HeaderXForwardedSsl       = "X-Forwarded-Ssl"
	HeaderXUrlScheme          = "X-Url-Scheme"
	HeaderXHTTPMethodOverride = "X-HTTP-Method-Override"
	HeaderXRealIP             = "X-Real-Ip"
	HeaderXRequestID          = "X-Request-Id"
	HeaderXCorrelationID      = "X-Correlation-Id"
	HeaderXRequestedWith      = "X-Requested-With"
	HeaderServer              = "Server"

	// HeaderOrigin request header indicates the origin (scheme, hostname, and port) that caused the request.
	// See: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Origin
	HeaderOrigin       = "Origin"
	HeaderCacheControl = "Cache-Control"
	HeaderConnection   = "Connection"

	// Access control
	HeaderAccessControlRequestMethod    = "Access-Control-Request-Method"
	HeaderAccessControlRequestHeaders   = "Access-Control-Request-Headers"
	HeaderAccessControlAllowOrigin      = "Access-Control-Allow-Origin"
	HeaderAccessControlAllowMethods     = "Access-Control-Allow-Methods"
	HeaderAccessControlAllowHeaders     = "Access-Control-Allow-Headers"
	HeaderAccessControlAllowCredentials = "Access-Control-Allow-Credentials"
	HeaderAccessControlExposeHeaders    = "Access-Control-Expose-Headers"
	HeaderAccessControlMaxAge           = "Access-Control-Max-Age"

	// Security
	HeaderStrictTransportSecurity         = "Strict-Transport-Security"
	HeaderXContentTypeOptions             = "X-Content-Type-Options"
	HeaderXXSSProtection                  = "X-XSS-Protection"
	HeaderXFrameOptions                   = "X-Frame-Options"
	HeaderContentSecurityPolicy           = "Content-Security-Policy"
	HeaderContentSecurityPolicyReportOnly = "Content-Security-Policy-Report-Only"
	HeaderXCSRFToken                      = "X-CSRF-Token" // #nosec G101
	HeaderReferrerPolicy                  = "Referrer-Policy"

	// HeaderSecFetchSite fetch metadata request header indicates the relationship between a request initiator's
	// origin and the origin of the requested resource.
	// See: https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Sec-Fetch-Site
	HeaderSecFetchSite = "Sec-Fetch-Site"
)

// Config is configuration for NewWithConfig function
type Config struct {
	// Logger is the slog logger instance used for application-wide structured logging.
	// If not set, a default TextHandler writing to stdout is created.
	Logger *slog.Logger

	// HTTPErrorHandler is the centralized error handler that processes errors returned
	// by handlers and middleware, converting them to appropriate HTTP responses.
	// If not set, DefaultHTTPErrorHandler(false) is used.
	HTTPErrorHandler HTTPErrorHandler

	// Router is the HTTP request router responsible for matching URLs to handlers
	// using a radix tree-based algorithm.
	// If not set, NewRouter(RouterConfig{}) is used.
	Router Router

	// OnAddRoute is an optional callback hook executed when routes are registered.
	// Useful for route validation, logging, or custom route processing.
	// If not set, no callback is executed.
	OnAddRoute func(route Route) error

	// Filesystem is the fs.FS implementation used for serving static files.
	// Supports os.DirFS, embed.FS, and custom implementations.
	// If not set, defaults to current working directory.
	Filesystem fs.FS

	// Binder handles automatic data binding from HTTP requests to Go structs.
	// Supports JSON, XML, form data, query parameters, and path parameters.
	// If not set, DefaultBinder is used.
	Binder Binder

	// Validator provides optional struct validation after data binding.
	// Commonly used with third-party validation libraries.
	// If not set, Context.Validate() returns ErrValidatorNotRegistered.
	Validator Validator

	// Renderer provides template rendering for generating HTML responses.
	// Requires integration with a template engine like html/template.
	// If not set, Context.Render() returns ErrRendererNotRegistered.
	Renderer Renderer

	// JSONSerializer handles JSON encoding and decoding for HTTP requests/responses.
	// Can be replaced with faster alternatives like jsoniter or sonic.
	// If not set, DefaultJSONSerializer using encoding/json is used.
	JSONSerializer JSONSerializer

	// IPExtractor defines the strategy for extracting the real client IP address
	// from requests, particularly important when behind proxies or load balancers.
	// Used for rate limiting, access control, and logging.
	// If not set, the address of the direct peer (http.Request.RemoteAddr) is used.
	IPExtractor IPExtractor

	// SchemeExtractor defines the strategy for determining the request scheme (`http` or `https`),
	// particularly important when behind proxies or load balancers that terminate TLS.
	// Used by Context.Scheme and by the HTTPS redirect, Secure and Proxy middlewares.
	// If not set, forwarding headers such as `X-Forwarded-Proto` are trusted only from loopback, link-local and
	// private network addresses and unix sockets. See ExtractSchemeFromHeaders.
	SchemeExtractor SchemeExtractor

	// FormParseMaxMemory is default value for memory limit that is used
	// when parsing multipart forms (See (*http.Request).ParseMultipartForm)
	FormParseMaxMemory int64

	// EnablePathUnescapingStaticFiles enables path parameter (param: *) unescaping for static file methods.
	// Default false (safe): encoded slashes (%2f) in the wildcard param are NOT decoded,
	// preventing ACL bypass where /admin%2fprivate.txt bypasses a /admin/* route guard by
	// not matching that route but having its wildcard param decoded to admin/private.txt.
	// Set to true only when serving files whose names contain URL-encoded characters
	// (e.g. "hello world.txt" via /hello%20world.txt) and you are not relying on
	// route-based ACL guards to restrict access.
	// If you are enabling this option, make sure you understand the security implications.
	// See: https://github.com/labstack/echo/security/advisories/GHSA-vfp3-v2gw-7wfq
	//
	// Enabling RouterConfig.UseEscapedPathForMatching makes this field irrelevant and can lead to security issues when
	// using different Routes to exclude some of the files from being served.
	// e.g. if you serve files from directory as such and use different route to exclude some of the files from being served.
	// 0. given folder structure:
	//   public/
	//   public/index.html
	//   public/admin/private.txt
	// 1. share `public/` folder contents from the server root with `e.Static("/", "public")`
	// 2. naively assume that everything under /admin folder is now forbidden
	//       e.GET("/admin/*", func(c *Context) error { return echo.ErrForbidden })
	// Then request to `/assets/../admin%2fprivate.txt` will be served as router does not match it to guarded route.
	//
	// Applies to methods: Echo.Static, Echo.StaticFS, Group.Static, Group.StaticFS.
	EnablePathUnescapingStaticFiles bool

	// NoGroupAutoRegister404Routes bool is a flag that indicates whether echo.Group should NOT register 404 routes automatically
	// when there are middlewares registered with the group.
	// Note: if you decide not to register 404 routes automatically, make sure to check if all your middlewares are executed
	// as expected. For example - CORS middleware.
	NoGroupAutoRegister404Routes bool

	// Observer names every request before the first middleware and receives one observation after the error
	// handler. The zero value, and an Observer with no Sink, leave the request path as v6.1.0.
	//
	// With an Observer set, a route registered through Echo or a Group with an empty Name gets the name
	// Method + ":" + Path, which is the name Routes already reports for it, and the observation reports a matched
	// route by that name. A route added through Router() directly is reported by the name it has, "none" when it
	// has none. A Mint that returns the zero ID leaves that request unnamed: Context.RequestID is the zero ID
	// and the observation writes "none".
	Observer observe.Observer
}

// NewWithConfig creates an instance of Echo with given configuration.
func NewWithConfig(config Config) *Echo {
	e := New()
	if config.Logger != nil {
		e.Logger = config.Logger
	}
	if config.HTTPErrorHandler != nil {
		e.HTTPErrorHandler = config.HTTPErrorHandler
	}
	if config.Router != nil {
		e.router = config.Router
	}
	if config.OnAddRoute != nil {
		e.OnAddRoute = config.OnAddRoute
	}
	if config.Filesystem != nil {
		e.Filesystem = config.Filesystem
	}
	if config.Binder != nil {
		e.Binder = config.Binder
	}
	if config.Validator != nil {
		e.Validator = config.Validator
	}
	if config.Renderer != nil {
		e.Renderer = config.Renderer
	}
	if config.JSONSerializer != nil {
		e.JSONSerializer = config.JSONSerializer
	}
	if config.IPExtractor != nil {
		e.IPExtractor = config.IPExtractor
	}
	if config.SchemeExtractor != nil {
		e.SchemeExtractor = config.SchemeExtractor
	}
	if config.FormParseMaxMemory > 0 {
		e.formParseMaxMemory = config.FormParseMaxMemory
	}
	e.enablePathUnescapingStaticFiles = config.EnablePathUnescapingStaticFiles

	e.noGroupAutoRegisterRoutes = config.NoGroupAutoRegister404Routes
	e.observer = newObserver(config.Observer)

	return e
}

// New creates an instance of Echo.
func New() *Echo {
	dir, _ := os.Getwd()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	e := &Echo{
		Logger:             logger,
		Filesystem:         NewDefaultFS(dir),
		Binder:             &DefaultBinder{},
		JSONSerializer:     &DefaultJSONSerializer{},
		formParseMaxMemory: defaultMemory,
	}

	e.serveHTTPFunc = e.serveHTTP
	e.router = NewRouter(RouterConfig{
		AllowOverwritingRoute: true,
	})
	e.HTTPErrorHandler = DefaultHTTPErrorHandler(false)
	e.contextPool.New = func() any {
		return newContext(nil, nil, e)
	}
	e.buildRouterChains()
	return e
}

// buildRouterChains compiles the global and pre-middleware chains once so that ServeHTTP does not have to
// re-wrap middleware closures on every request. It must be called whenever e.middleware or e.premiddleware
// changes (i.e. from Use/Pre). This is safe because middleware must not be mutated after the server starts.
func (e *Echo) buildRouterChains() {
	// dispatch is the terminal of the global chain: it invokes the handler resolved during routing.
	dispatch := func(c *Context) error {
		return c.handler(c)
	}
	e.chain = applyMiddleware(dispatch, e.middleware...)

	// route performs routing (storing the matched handler on the Context) and then runs the global chain.
	route := func(c *Context) error {
		c.handler = e.router.Route(c)
		return e.chain(c)
	}
	e.preChain = applyMiddleware(route, e.premiddleware...)
}

// NewContext returns a new Context instance.
//
// Note: both request and response can be left to nil as Echo.ServeHTTP will call c.Reset(req,resp) anyway
// these arguments are useful when creating context for tests and cases like that.
func (e *Echo) NewContext(r *http.Request, w http.ResponseWriter) *Context {
	return newContext(r, w, e)
}

// Router returns the default router.
func (e *Echo) Router() Router {
	return e.router
}

// Freeze refuses every later Add, AddRoute, Group route, Pre and Use on e. It returns an error and freezes nothing
// when the router was made by NewConcurrentRouter. Routes added through e.Router() are not held.
func (e *Echo) Freeze() error {
	if _, ok := e.router.(*concurrentRouter); ok {
		return errors.New("echo: a router made by NewConcurrentRouter cannot be frozen")
	}
	e.frozen.Store(true)
	return nil
}

// clearRepresentationHeaders drops metadata for the body an error response replaces.
func clearRepresentationHeaders(h http.Header) {
	h.Del(HeaderContentEncoding)
	h.Del(HeaderContentLength)
	h.Del(HeaderContentDisposition)
}

// DefaultHTTPErrorHandler creates new default HTTP error handler implementation. It sends a JSON response
// with status code. `exposeError` parameter decides if returned message will contain also error message or not
//
// Note: DefaultHTTPErrorHandler does not log errors. Use middleware for it if errors need to be logged (separately)
// Note: In case errors happens in middleware call-chain that is returning from handler (which did not return an error).
// When handler has already sent response (ala c.JSON()) and there is error in middleware that is returning from
// handler. Then the error that global error handler received will be ignored because we have already "committed" the
// response and status code header has been sent to the client.
func DefaultHTTPErrorHandler(exposeError bool) HTTPErrorHandler {
	return func(c *Context, err error) {
		d := decideError(c, err)
		if !d.Write {
			return
		}
		writeError(c, d.Status, defaultErrorBody(err, d.Status, exposeError), "echo default error handler failed to send error to client")
	}
}

// decideError asks outcome.Decide for the status half of the error rule: the status err states, 500 when it
// states none, and nothing to write once the response is committed. A committed response is answered without
// looking at err.
func decideError(c *Context, err error) outcome.Decision {
	if r, _ := UnwrapResponse(c.response); r != nil && r.Committed {
		return outcome.Decide(0, true)
	}
	return outcome.Decide(StatusCode(err), false)
}

// defaultErrorBody returns the value DefaultHTTPErrorHandler encodes for err when it answers with code.
// It writes nothing.
func defaultErrorBody(err error, code int, exposeError bool) any {
	var sc HTTPStatusCoder
	_ = errors.As(err, &sc)
	switch m := sc.(type) {
	case json.Marshaler: // this type knows how to format itself to JSON
		return m
	case *HTTPError:
		sText := m.Message
		if sText == "" {
			sText = http.StatusText(code)
		}
		msg := map[string]any{"message": sText}
		if exposeError {
			if wrappedErr := m.Unwrap(); wrappedErr != nil {
				msg["error"] = wrappedErr.Error()
			}
		}
		return msg
	default:
		msg := map[string]any{"message": http.StatusText(code)}
		if exposeError {
			msg["error"] = err.Error()
		}
		return msg
	}
}

// writeError is the one effect of the two default error handlers. It drops the headers of the representation the
// error replaces, then sends code with body, or with no body for HEAD. A send that fails is logged with failure.
func writeError(c *Context, code int, body any, failure string) {
	clearRepresentationHeaders(c.Response().Header())
	var cErr error
	if c.Request().Method == http.MethodHead { // Issue #608
		cErr = c.NoContent(code)
	} else {
		cErr = c.JSON(code, body)
	}
	if cErr != nil {
		c.Logger().Error(failure, "error", cErr) // truly rare case. ala client already disconnected
	}
}

// Pre adds middleware to the chain which is run before router tries to find matching route.
// Meaning middleware is executed even for 404 (not found) cases.
func (e *Echo) Pre(middleware ...MiddlewareFunc) {
	if e.frozen.Load() {
		panic(ErrRoutesFrozen)
	}
	e.premiddleware = append(e.premiddleware, middleware...)
	e.buildRouterChains()
}

// Use adds middleware to the chain which is run after router has found matching route and before route/request handler method is executed.
func (e *Echo) Use(middleware ...MiddlewareFunc) {
	if e.frozen.Load() {
		panic(ErrRoutesFrozen)
	}
	e.middleware = append(e.middleware, middleware...)
	e.buildRouterChains()
}

// CONNECT registers a new CONNECT route for a path with matching handler in the
// router with optional route-level middleware. Panics on error.
func (e *Echo) CONNECT(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodConnect, path, h, m...)
}

// DELETE registers a new DELETE route for a path with matching handler in the router
// with optional route-level middleware. Panics on error.
func (e *Echo) DELETE(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodDelete, path, h, m...)
}

// GET registers a new GET route for a path with matching handler in the router
// with optional route-level middleware. Panics on error.
func (e *Echo) GET(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodGet, path, h, m...)
}

// HEAD registers a new HEAD route for a path with matching handler in the
// router with optional route-level middleware. Panics on error.
func (e *Echo) HEAD(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodHead, path, h, m...)
}

// OPTIONS registers a new OPTIONS route for a path with matching handler in the
// router with optional route-level middleware. Panics on error.
func (e *Echo) OPTIONS(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodOptions, path, h, m...)
}

// PATCH registers a new PATCH route for a path with matching handler in the
// router with optional route-level middleware. Panics on error.
func (e *Echo) PATCH(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodPatch, path, h, m...)
}

// POST registers a new POST route for a path with matching handler in the
// router with optional route-level middleware. Panics on error.
func (e *Echo) POST(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodPost, path, h, m...)
}

// PUT registers a new PUT route for a path with matching handler in the
// router with optional route-level middleware. Panics on error.
func (e *Echo) PUT(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodPut, path, h, m...)
}

// QUERY registers a new QUERY route for a path with matching handler in the
// router with optional route-level middleware. Panics on error.
func (e *Echo) QUERY(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(QUERY, path, h, m...)
}

// TRACE registers a new TRACE route for a path with matching handler in the
// router with optional route-level middleware. Panics on error.
func (e *Echo) TRACE(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(http.MethodTrace, path, h, m...)
}

// RouteNotFound registers a special-case route which is executed when no other route is found (i.e. HTTP 404 cases)
// for current request URL.
// Path supports static and named/any parameters just like other http method is defined. Generally path is ended with
// wildcard/match-any character (`/*`, `/download/*` etc).
//
// Example: `e.RouteNotFound("/*", func(c *echo.Context) error { return c.NoContent(http.StatusNotFound) })`
func (e *Echo) RouteNotFound(path string, h HandlerFunc, m ...MiddlewareFunc) RouteInfo {
	return e.Add(RouteNotFound, path, h, m...)
}

// Any registers a new route for a path with matching handler in the router with optional
// route-level middleware. Panics on error. The route matches any HTTP method in the request,
// including methods not known to Echo, by registering a single RouteAny route.
//
// A method-specific route registered for the same path takes precedence over the Any route.
func (e *Echo) Any(path string, handler HandlerFunc, middleware ...MiddlewareFunc) RouteInfo {
	return e.Add(RouteAny, path, handler, middleware...)
}

// Match registers a new route for multiple HTTP methods and path with matching
// handler in the router with optional route-level middleware. Panics on error.
func (e *Echo) Match(methods []string, path string, handler HandlerFunc, middleware ...MiddlewareFunc) Routes {
	errs := make([]error, 0)
	ris := make(Routes, 0)
	for _, m := range methods {
		ri, err := e.AddRoute(Route{
			Method:      m,
			Path:        path,
			Handler:     handler,
			Middlewares: middleware,
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ris = append(ris, ri)
	}
	if len(errs) > 0 {
		panic(errs) // this is how `v4` handles errors. `v5` has methods to have panic-free usage
	}
	return ris
}

// Static registers a new route with path prefix to serve static files from the provided root directory.
//
// A mount without a trailing slash also serves its directory redirect.
// The returned route matches files below the mount.
func (e *Echo) Static(pathPrefix, fsRoot string, middleware ...MiddlewareFunc) RouteInfo {
	return e.StaticFS(pathPrefix, MustSubFS(e.Filesystem, fsRoot), middleware...)
}

// StaticFS registers a new route with path prefix to serve static files from the provided file system.
//
// When dealing with `embed.FS` use `fs := echo.MustSubFS(fs, "rootDirectory") to create sub fs which uses necessary
// prefix for directory path. This is necessary as `//go:embed assets/images` embeds files with paths
// including `assets/images` as their prefix.
func (e *Echo) StaticFS(pathPrefix string, filesystem fs.FS, middleware ...MiddlewareFunc) RouteInfo {
	return e.mountStatic("", pathPrefix, filesystem, e.GET, middleware)
}

// mountStatic registers the same mount for an Echo instance or a route group.
// Keep an application's explicit handler for the directory itself.
func (e *Echo) mountStatic(groupPrefix, pathPrefix string, filesystem fs.FS, get func(string, HandlerFunc, ...MiddlewareFunc) RouteInfo, middleware []MiddlewareFunc) RouteInfo {
	serve := StaticDirectoryHandler(filesystem, !e.enablePathUnescapingStaticFiles)
	fullPath := normalizePathSlash(groupPrefix + pathPrefix)
	if !strings.HasSuffix(fullPath, "/") {
		alreadyRegistered := slices.ContainsFunc(e.router.Routes(), func(r RouteInfo) bool {
			return (r.Method == http.MethodGet || r.Method == RouteAny) && normalizePathSlash(r.Path) == fullPath
		})
		if !alreadyRegistered {
			get(pathPrefix, serve, middleware...)
		}
		pathPrefix += "/"
	}
	return get(pathPrefix+"*", serve, middleware...)
}

// StaticDirectoryHandler creates handler function to serve files from provided file system
// When disablePathUnescaping is set then file name from path is not unescaped and is served as is.
//
// Note: when disablePathUnescaping=false, the handler decodes the wildcard param before serving.
// If route guards (e.g. e.GET("/admin/*", forbidden)) are used to restrict parts of the
// filesystem, an encoded separator (%2F) or encoded dot-dot (%2E%2E) in the URL can resolve to
// a path that the router never matched against the guard route. Enabling
// RouterConfig.UseEscapedPathForMatching does NOT fix this — it changes which path the router
// uses for matching but still lets path.Clean resolve ".." segments into a guarded directory.
// Do not rely on route guards alone to restrict a filesystem served by this handler.
// See https://github.com/labstack/echo/security/advisories/GHSA-vfp3-v2gw-7wfq
func StaticDirectoryHandler(fileSystem fs.FS, disablePathUnescaping bool) HandlerFunc {
	return func(c *Context) error {
		p := c.Param("*")
		// The router matches the path as it was sent, but the file name is resolved with path.Clean(). A path with a
		// ".", ".." or empty segment could therefore reach a file under a route that the router never matched, for
		// example `/assets/../admin/private.txt` bypassing a guarded `/admin/*` route (GHSA-3pmx-cf9f-34xr).
		if hasUncleanPath(c.Request()) || httprule.HasUncleanWildcardPath(p, c.Path()) {
			return ErrNotFound
		}
		if !disablePathUnescaping { // when router is already unescaping we do not want to do is twice
			tmpPath, err := url.PathUnescape(p)
			if err != nil {
				return fmt.Errorf("failed to unescape path variable: %w", err)
			}
			p = tmpPath
			if httprule.HasUncleanWildcardPath(p, c.Path()) { // unescaping can create dot or empty segments
				return ErrNotFound
			}
		}

		// fs.FS.Open() already assumes that file names are relative to FS root path and considers name with prefix `/`
		// as invalid
		// Use path.Clean (not filepath.Clean): fs.FS paths are always forward-slash, so a backslash must stay a literal
		// character rather than being interpreted as a separator on Windows (which would resolve a file across a boundary
		// the router never matched on, the same Windows backslash traversal class as GHSA-pgvm-wxw2-hrv9).
		name := path.Clean(strings.TrimPrefix(p, "/"))
		fi, err := fs.Stat(fileSystem, name)
		if err != nil {
			return ErrNotFound
		}

		// If the request is for a directory and does not end with "/" redirect to path which ends with "/"
		p = c.Request().URL.Path
		if fi.IsDir() && len(p) > 0 && p[len(p)-1] != '/' {
			return c.Redirect(http.StatusMovedPermanently, httprule.SanitizeURI(p+"/"))
		}
		return fsFile(c, name, fileSystem)
	}
}

// FileFS registers a new route with path to serve file from the provided file system.
//
// Avoid using the leading `/` slash as most of the Go standard library fs.FS implementations require relative paths for
// file operations.
func (e *Echo) FileFS(path, file string, filesystem fs.FS, m ...MiddlewareFunc) RouteInfo {
	return e.GET(path, StaticFileHandler(file, filesystem), m...)
}

// StaticFileHandler creates handler function to serve file from provided file system.
//
// Avoid using the leading `/` slash as most of the Go standard library fs.FS implementations require relative paths for
// file operations.
func StaticFileHandler(file string, filesystem fs.FS) HandlerFunc {
	return func(c *Context) error {
		return fsFile(c, file, filesystem)
	}
}

// File registers a new route with path to serve a static file with optional route-level middleware. Panics on error.
//
// Avoid using the leading `/` slash as most of the Go standard library fs.FS implementations require relative paths for
// file operations.
func (e *Echo) File(path, file string, middleware ...MiddlewareFunc) RouteInfo {
	handler := func(c *Context) error {
		return c.File(file)
	}
	return e.Add(http.MethodGet, path, handler, middleware...)
}

// AddRoute registers a new Route with default host Router
func (e *Echo) AddRoute(route Route) (RouteInfo, error) {
	return e.add(route)
}

func (e *Echo) add(route Route) (RouteInfo, error) {
	if e.frozen.Load() {
		return RouteInfo{}, ErrRoutesFrozen
	}
	if e.OnAddRoute != nil {
		if err := e.OnAddRoute(route); err != nil {
			return RouteInfo{}, err
		}
	}
	if e.observer != nil && route.Name == "" {
		// The observation reports a matched route by its name. This is the name Route.ToRouteInfo gives an
		// unnamed route; set here, the router keeps it where the request path can read it without building it.
		route.Name = route.Method + ":" + route.Path
	}

	ri, err := e.router.Add(route)
	if err != nil {
		return RouteInfo{}, err
	}

	paramsCount := int32(len(ri.Parameters)) // #nosec G115
	if paramsCount > e.contextPathParamAllocSize.Load() {
		e.contextPathParamAllocSize.Store(paramsCount)
	}
	return ri, nil
}

// Add registers a new route for an HTTP method and path with matching handler
// in the router with optional route-level middleware.
func (e *Echo) Add(method, path string, handler HandlerFunc, middleware ...MiddlewareFunc) RouteInfo {
	ri, err := e.add(
		Route{
			Method:      method,
			Path:        path,
			Handler:     handler,
			Middlewares: middleware,
			Name:        "",
		},
	)
	if err != nil {
		panic(err) // this is how `v4` handles errors. `v5` has methods to have panic-free usage
	}
	return ri
}

// Group creates a new router group with prefix and optional group-level middleware.
// The prefix is concatenated with each route path without inserting a slash.
// For example:
//
//	g := e.Group("/v1")
//	g.GET("/posts", handler) // Registers /v1/posts.
//	g.GET("posts", handler)  // Registers /v1posts.
func (e *Echo) Group(prefix string, m ...MiddlewareFunc) (g *Group) {
	g = &Group{
		prefix:               prefix,
		echo:                 e,
		noAutoRegisterRoutes: e.noGroupAutoRegisterRoutes,
	}
	g.Use(m...)
	return
}

// PreMiddlewares returns registered pre middlewares. These are middleware to the chain
// which are run before router tries to find matching route.
// Use this method to build your own ServeHTTP method.
//
// NOTE: returned slice is not a copy. Do not mutate.
func (e *Echo) PreMiddlewares() []MiddlewareFunc {
	return e.premiddleware
}

// Middlewares returns registered route level middlewares. Does not contain any group level
// middlewares. Use this method to build your own ServeHTTP method.
//
// NOTE: returned slice is not a copy. Do not mutate.
func (e *Echo) Middlewares() []MiddlewareFunc {
	return e.middleware
}

// AcquireContext returns an empty `Context` instance from the pool.
// You must return the context by calling `ReleaseContext()`.
func (e *Echo) AcquireContext() *Context {
	return e.contextPool.Get().(*Context)
}

// ReleaseContext returns the `Context` instance back to the pool.
// You must call it after `AcquireContext()`.
func (e *Echo) ReleaseContext(c *Context) {
	e.contextPool.Put(c)
}

// ServeHTTP implements `http.Handler` interface, which serves HTTP requests.
func (e *Echo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.serveHTTPFunc(w, r)
}

// serveHTTP implements `http.Handler` interface, which serves HTTP requests.
func (e *Echo) serveHTTP(w http.ResponseWriter, r *http.Request) {
	c := e.contextPool.Get().(*Context)
	defer e.contextPool.Put(c)

	c.Reset(r, w)

	if e.observer != nil {
		e.serveObserved(c, r)
		return
	}

	// The global (e.chain) and pre-middleware (e.preChain) chains are compiled once in buildRouterChains and
	// reused here, so no middleware closures are allocated per request.
	var err error
	if e.premiddleware == nil {
		c.handler = e.router.Route(c)
		err = e.chain(c)
	} else {
		err = e.preChain(c)
	}

	if err != nil {
		e.HTTPErrorHandler(c, err)
	}
}

// Start stars HTTP server on given address with Echo as a handler serving requests. The server can be shutdown by
// sending os.Interrupt signal with `ctrl+c`. Method returns only errors that are not http.ErrServerClosed.
//
// Note: this method is created for use in examples/demos and is deliberately simple without providing configuration
// options.
//
// In need of customization use:
//
//	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer cancel()
//	sc := echo.StartConfig{Address: ":8080"}
//	if err := sc.Start(ctx, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
//		slog.Error(err.Error())
//	}
//
// // or standard library `http.Server`
//
//	s := http.Server{Addr: ":8080", Handler: e}
//	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
//		slog.Error(err.Error())
//	}
func (e *Echo) Start(address string) error {
	sc := StartConfig{Address: address}
	ctx, cancel := signal.NotifyContext(stdContext.Background(), os.Interrupt, syscall.SIGTERM) // start shutdown process on ctrl+c
	defer cancel()
	return sc.Start(ctx, e)
}

// WrapHandler wraps `http.Handler` into `echo.HandlerFunc`.
func WrapHandler(h http.Handler) HandlerFunc {
	return func(c *Context) error {
		req := c.Request()
		req.Pattern = c.Path()
		for _, p := range c.PathValues() {
			req.SetPathValue(p.Name, p.Value)
		}

		h.ServeHTTP(c.Response(), req)
		return nil
	}
}

// WrapMiddleware wraps `func(http.Handler) http.Handler` into `echo.MiddlewareFunc`
func WrapMiddleware(m func(http.Handler) http.Handler) MiddlewareFunc {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) (err error) {
			req := c.Request()
			req.Pattern = c.Path()
			for _, p := range c.PathValues() {
				req.SetPathValue(p.Name, p.Value)
			}

			m(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c.SetRequest(r)
				c.SetResponse(NewResponse(w, c.echo.Logger))
				err = next(c)
			})).ServeHTTP(c.Response(), req)
			return
		}
	}
}

func applyMiddleware(h HandlerFunc, middleware ...MiddlewareFunc) HandlerFunc {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}

// defaultFS emulates os.Open behavior with filesystem opened by `os.DirFs`. Difference between `os.Open` and `fs.Open`
// is that FS does not allow to open path that start with `..` or `/` etc. For example previously you could have `../images`
// in your application but `fs := os.DirFS("./")` would not allow you to use `fs.Open("../images")` and this would break
// all old applications that rely on being able to traverse up from current executable run path.
// NB: private because you really should use fs.FS implementation instances
type defaultFS struct {
	fs     fs.FS
	prefix string
}

// NewDefaultFS returns a new defaultFS instance which allows `fs.FS.Open` to have absolute paths as input if it matches
// then given dir as prefix.
func NewDefaultFS(dir string) fs.FS {
	return &defaultFS{
		prefix: dir,
		fs:     os.DirFS(dir),
	}
}

func (fs defaultFS) Open(name string) (fs.File, error) {
	// fs.FS.Open() already assumes that file names are relative to FS root path and considers name with prefix `/` as invalid
	// For example `f.Name()` returns file names as absolute paths (e.g. `/tmp/data.csv`) so in case user wants to open
	// a file with an absolute path we need to remove prefix and then call fs.FS.Open().
	// not to force users to cut prefix from file name we do it here.
	if filepath.IsAbs(name) {
		if strings.HasPrefix(name, fs.prefix) {
			name = name[len(fs.prefix):]
			if len(name) > 1 && os.IsPathSeparator(name[0]) {
				name = name[1:]
			}
		}
	}
	return fs.fs.Open(name)
}

func subFS(currentFs fs.FS, root string) (fs.FS, error) {
	root = filepath.ToSlash(filepath.Clean(root)) // note: fs.FS operates only with slashes. `ToSlash` is necessary for Windows
	if dFS, ok := currentFs.(*defaultFS); ok {
		// we need to make exception for `defaultFS` instances as it interprets root prefix differently from fs.FS.
		// fs.Fs.Open does not like relative paths ("./", "../") and absolute paths at all but prior echo.Filesystem we
		// were able to use paths like `./myfile.log`, `/etc/hosts` and these would work fine with `os.Open` but not with fs.Fs
		if !filepath.IsAbs(root) {
			root = filepath.Join(dFS.prefix, root)
		}
		return &defaultFS{
			prefix: root,
			fs:     os.DirFS(root),
		}, nil
	}
	return fs.Sub(currentFs, root)
}

// MustSubFS creates sub FS from current filesystem or panic on failure.
// Panic happens when `fsRoot` contains invalid path according to `fs.ValidPath` rules.
//
// MustSubFS is helpful when dealing with `embed.FS` because for example `//go:embed assets/images` embeds files with
// paths including `assets/images` as their prefix. In that case use `fs := echo.MustSubFS(fs, "rootDirectory") to
// create sub fs which uses necessary prefix for directory path.
func MustSubFS(currentFs fs.FS, fsRoot string) fs.FS {
	subFs, err := subFS(currentFs, fsRoot)
	if err != nil {
		panic(fmt.Errorf("can not create sub FS, invalid root given, err: %w", err))
	}
	return subFs
}

// hasUncleanPath checks both request path representations for ".", ".." or empty segments.
// Router configuration and parameter unescaping can select either representation. Validating only RawPath would
// miss an empty segment in URL.Path, allowing filesystem cleanup to resolve a path the router never matched.
func hasUncleanPath(req *http.Request) bool {
	if httprule.HasDotOrEmptySegment(req.URL.Path) {
		return true
	}
	return req.URL.RawPath != "" && httprule.HasDotOrEmptySegment(req.URL.RawPath)
}
