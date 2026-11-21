// SPDX-License-Identifier: MIT

package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v6"
	"github.com/fiberfx/mx-echo/v6/httprule"
)

// ruleTablePath is the table httprule's own TestRules reads: one table per decision, shared by both tests.
const ruleTablePath = "../httprule/testdata/rules.json"

type ruleTable struct {
	Redirect      []redirectRow      `json:"redirect"`
	RedirectHost  []redirectHostRow  `json:"redirectHost"`
	AcceptsGzip   []acceptsGzipRow   `json:"acceptsGzip"`
	ContentCoding []contentCodingRow `json:"contentCoding"`
	Escape        []textRow          `json:"escape"`
	Sanitize      []textRow          `json:"sanitize"`
	PathSegment   []pathSegmentRow   `json:"pathSegment"`
	WildcardPath  []wildcardPathRow  `json:"wildcardPath"`
	Origin        []originRow        `json:"origin"`
}

type redirectRow struct {
	Rule   string `json:"rule"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	URI    string `json:"uri"`
	OK     bool   `json:"ok"`
	URL    string `json:"url"`
}

type redirectHostRow struct {
	Host  string `json:"host"`
	Valid bool   `json:"valid"`
}

type acceptsGzipRow struct {
	AcceptEncoding []string `json:"acceptEncoding"`
	Accepts        bool     `json:"accepts"`
}

type contentCodingRow struct {
	ContentEncoding []string `json:"contentEncoding"`
	Has             bool     `json:"has"`
}

// textRow is a row of a function from text to text. Direct marks an input that only the function can be given,
// because no request delivers it to the middleware unchanged.
type textRow struct {
	In     string `json:"in"`
	Out    string `json:"out"`
	Direct bool   `json:"direct"`
}

type pathSegmentRow struct {
	Path    string `json:"path"`
	Unclean bool   `json:"unclean"`
}

type wildcardPathRow struct {
	P         string `json:"p"`
	RoutePath string `json:"routePath"`
	Unclean   bool   `json:"unclean"`
}

type originRow struct {
	Allowed []string `json:"allowed"`
	Origin  string   `json:"origin"`
	Match   string   `json:"match"`
	OK      bool     `json:"ok"`
}

func loadRuleTable(filename string) (ruleTable, error) {
	var table ruleTable
	f, err := os.Open(filename)
	if err != nil {
		return table, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&table); err != nil {
		return table, fmt.Errorf("%s: %w", filename, err)
	}
	return table, nil
}

// redirectRule pairs a redirect middleware with the function it is an adapter over.
type redirectRule struct {
	middleware func() echo.MiddlewareFunc
	rule       func(scheme, host, uri string) (bool, string)
}

var redirectRules = map[string]redirectRule{
	"https":         {HTTPSRedirect, httprule.RedirectHTTPS},
	"https-www":     {HTTPSWWWRedirect, httprule.RedirectHTTPSWWW},
	"https-non-www": {HTTPSNonWWWRedirect, httprule.RedirectHTTPSNonWWW},
	"www":           {WWWRedirect, httprule.RedirectWWW},
	"non-www":       {NonWWWRedirect, httprule.RedirectNonWWW},
}

// ruleExchange is what one request through one middleware left behind.
type ruleExchange struct {
	rec    *httptest.ResponseRecorder
	err    error
	called bool // the middleware called the next handler
}

// throughMiddleware sends req through mw with e's Context. prepare may set the route of the Context; next is the
// handler after the middleware, and nil stands for one that writes nothing.
func throughMiddleware(e *echo.Echo, mw echo.MiddlewareFunc, req *http.Request, prepare func(*echo.Context), next echo.HandlerFunc) ruleExchange {
	x := ruleExchange{rec: httptest.NewRecorder()}
	c := e.NewContext(req, x.rec)
	if prepare != nil {
		prepare(c)
	}
	x.err = mw(func(c *echo.Context) error {
		x.called = true
		if next == nil {
			return nil
		}
		return next(c)
	})(c)
	return x
}

// location returns the redirect target of the exchange, and whether the middleware redirected at all.
func (x ruleExchange) location() (string, bool) {
	values := x.rec.Header().Values(echo.HeaderLocation)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func checkRedirectThrough(row redirectRow) error {
	pair, found := redirectRules[row.Rule]
	if !found {
		return fmt.Errorf("no redirect rule is named %q", row.Rule)
	}
	e := echo.New()
	e.SchemeExtractor = func(*http.Request) string { return row.Scheme }
	req := httptest.NewRequest(http.MethodGet, row.URI, nil)
	req.Host = row.Host
	x := throughMiddleware(e, pair.middleware(), req, nil, nil)
	if x.err != nil {
		return fmt.Errorf("%s redirect of %s://%s%s: %w", row.Rule, row.Scheme, row.Host, row.URI, x.err)
	}
	target, redirected := x.location()
	if redirected == x.called {
		return fmt.Errorf("%s redirect of %s://%s%s: redirected is %v and next called is %v", row.Rule, row.Scheme, row.Host, row.URI, redirected, x.called)
	}
	if redirected && x.rec.Code != http.StatusMovedPermanently {
		return fmt.Errorf("%s redirect of %s://%s%s: status %d", row.Rule, row.Scheme, row.Host, row.URI, x.rec.Code)
	}
	if redirected != row.OK || target != row.URL {
		return fmt.Errorf("%s redirect of %s://%s%s through the middleware = %v, %q; the table says %v, %q", row.Rule, row.Scheme, row.Host, row.URI, redirected, target, row.OK, row.URL)
	}
	if ok, url := pair.rule(row.Scheme, row.Host, row.URI); ok != redirected || url != target {
		return fmt.Errorf("%s redirect of %s://%s%s: the middleware gives %v, %q and the function %v, %q", row.Rule, row.Scheme, row.Host, row.URI, redirected, target, ok, url)
	}
	return nil
}

// checkRedirectHostThrough asks the HTTPS redirect, which redirects every http request, about the host: it answers
// a host it will not write into a Location with a bad request.
func checkRedirectHostThrough(row redirectHostRow) error {
	e := echo.New()
	e.SchemeExtractor = func(*http.Request) string { return "http" }
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = row.Host
	x := throughMiddleware(e, HTTPSRedirect(), req, nil, nil)
	target, redirected := x.location()
	valid := x.err == nil && redirected
	switch {
	case x.called:
		return fmt.Errorf("host %q: the redirect called the next handler", row.Host)
	case valid && target != "https://"+row.Host+"/":
		return fmt.Errorf("host %q: redirected to %q", row.Host, target)
	case !valid && (!errors.Is(x.err, echo.ErrBadRequest) || redirected):
		return fmt.Errorf("host %q: error %v, redirected %v", row.Host, x.err, redirected)
	case valid != row.Valid:
		return fmt.Errorf("host %q through the middleware is valid = %v; the table says %v", row.Host, valid, row.Valid)
	case valid != httprule.ValidRedirectHost(row.Host):
		return fmt.Errorf("host %q: the middleware gives %v and the function %v", row.Host, valid, !valid)
	}
	return nil
}

const rulePayload = "a body the gzip middleware may compress"

// gzipped tells a compressed response from one passed through unchanged, and refuses anything else.
func gzipped(x ruleExchange) (bool, error) {
	if x.err != nil {
		return false, x.err
	}
	if !x.called {
		return false, errors.New("the gzip middleware did not call the next handler")
	}
	body := x.rec.Body.Bytes()
	switch {
	case bytes.Equal(body, []byte(rulePayload)):
		return false, nil
	case bytes.HasPrefix(body, []byte{0x1f, 0x8b}) && x.rec.Header().Get(echo.HeaderContentEncoding) == gzipScheme:
		return true, nil
	}
	return false, fmt.Errorf("the body is neither the payload nor a gzip stream: %q", body)
}

func checkAcceptsGzipThrough(row acceptsGzipRow) error {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if len(row.AcceptEncoding) > 0 {
		req.Header[echo.HeaderAcceptEncoding] = row.AcceptEncoding
	}
	x := throughMiddleware(echo.New(), Gzip(), req, nil, func(c *echo.Context) error {
		_, err := c.Response().Write([]byte(rulePayload))
		return err
	})
	compressed, err := gzipped(x)
	if err != nil {
		return fmt.Errorf("Accept-Encoding %q: %w", row.AcceptEncoding, err)
	}
	if compressed != row.Accepts {
		return fmt.Errorf("Accept-Encoding %q through the middleware is compressed = %v; the table says %v", row.AcceptEncoding, compressed, row.Accepts)
	}
	if compressed != httprule.AcceptsGzip(row.AcceptEncoding) {
		return fmt.Errorf("Accept-Encoding %q: the middleware gives %v and the function %v", row.AcceptEncoding, compressed, !compressed)
	}
	return nil
}

// checkContentCodingThrough lets the handler set Content-Encoding before it writes: the middleware passes an
// encoded response through and compresses any other.
func checkContentCodingThrough(row contentCodingRow) error {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAcceptEncoding, gzipScheme)
	x := throughMiddleware(echo.New(), Gzip(), req, nil, func(c *echo.Context) error {
		if len(row.ContentEncoding) > 0 {
			c.Response().Header()[echo.HeaderContentEncoding] = row.ContentEncoding
		}
		_, err := c.Response().Write([]byte(rulePayload))
		return err
	})
	compressed, err := gzipped(x)
	if err != nil {
		return fmt.Errorf("Content-Encoding %q: %w", row.ContentEncoding, err)
	}
	if has := !compressed; has != row.Has {
		return fmt.Errorf("Content-Encoding %q through the middleware is passed through = %v; the table says %v", row.ContentEncoding, has, row.Has)
	}
	if has := !compressed; has != httprule.HasContentCoding(row.ContentEncoding) {
		return fmt.Errorf("Content-Encoding %q: the middleware gives %v and the function %v", row.ContentEncoding, has, !has)
	}
	return nil
}

// checkEscapeThrough gives the HTTPS redirect the row's text as the request URI: the redirect writes it into the
// Location after the host.
func checkEscapeThrough(row textRow) error {
	if row.Direct {
		return errors.New("a direct row cannot be sent through the middleware")
	}
	const origin = "https://example.com"
	e := echo.New()
	e.SchemeExtractor = func(*http.Request) string { return "http" }
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "example.com"
	req.RequestURI = row.In
	x := throughMiddleware(e, HTTPSRedirect(), req, nil, nil)
	target, redirected := x.location()
	if x.err != nil || !redirected {
		return fmt.Errorf("request URI %q: error %v, redirected %v", row.In, x.err, redirected)
	}
	if target != origin+row.Out {
		return fmt.Errorf("request URI %q through the middleware = %q; the table says %q", row.In, target, origin+row.Out)
	}
	if target != origin+httprule.EscapeControlChars(row.In) {
		return fmt.Errorf("request URI %q: the middleware gives %q and the function %q", row.In, target, origin+httprule.EscapeControlChars(row.In))
	}
	return nil
}

// checkSanitizeThrough makes a trailing slash redirect compute the row's text as its target: a path that ends in
// a slash is what AddTrailingSlash builds, any other is what RemoveTrailingSlash builds.
func checkSanitizeThrough(row textRow) error {
	if row.Direct {
		return errors.New("a direct row cannot be sent through the middleware")
	}
	path, query, _ := strings.Cut(row.In, "?")
	mw := RemoveTrailingSlashWithConfig(RemoveTrailingSlashConfig{RedirectCode: http.StatusMovedPermanently})
	requestPath := path + "/"
	if strings.HasSuffix(path, "/") {
		mw = AddTrailingSlashWithConfig(AddTrailingSlashConfig{RedirectCode: http.StatusMovedPermanently})
		requestPath = strings.TrimSuffix(path, "/")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path, req.URL.RawPath, req.URL.RawQuery = requestPath, "", query
	x := throughMiddleware(echo.New(), mw, req, nil, nil)
	target, redirected := x.location()
	if x.err != nil || !redirected || x.called {
		return fmt.Errorf("target %q: error %v, redirected %v, next called %v", row.In, x.err, redirected, x.called)
	}
	if target != row.Out {
		return fmt.Errorf("target %q through the middleware = %q; the table says %q", row.In, target, row.Out)
	}
	if target != httprule.SanitizeURI(row.In) {
		return fmt.Errorf("target %q: the middleware gives %q and the function %q", row.In, target, httprule.SanitizeURI(row.In))
	}
	return nil
}

// openLog is a filesystem with no file that records each name Static asks it to open. Static opens nothing for a
// path it finds unclean, so the log tells the verdict.
type openLog struct {
	names []string
}

func (l *openLog) Open(name string) (fs.File, error) {
	l.names = append(l.names, name)
	return nil, fs.ErrNotExist
}

// staticVerdict sends a request through Static and reports whether Static found its path unclean.
func staticVerdict(requestPath string, prepare func(*echo.Context)) (bool, error) {
	files := &openLog{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path, req.URL.RawPath = requestPath, ""
	x := throughMiddleware(echo.New(), StaticWithConfig(StaticConfig{Filesystem: files}), req, prepare, nil)
	if x.err != nil {
		return false, x.err
	}
	if !x.called {
		return false, errors.New("static did not call the next handler for a file that does not exist")
	}
	return len(files.names) == 0, nil
}

func checkPathSegmentThrough(row pathSegmentRow) error {
	unclean, err := staticVerdict(row.Path, nil)
	if err != nil {
		return fmt.Errorf("path %q: %w", row.Path, err)
	}
	if unclean != row.Unclean {
		return fmt.Errorf("path %q through the middleware is unclean = %v; the table says %v", row.Path, unclean, row.Unclean)
	}
	if unclean != httprule.HasDotOrEmptySegment(row.Path) {
		return fmt.Errorf("path %q: the middleware gives %v and the function %v", row.Path, unclean, !unclean)
	}
	return nil
}

// checkWildcardPathThrough gives Static a clean request path under a wildcard route, so the verdict is the one
// about the wildcard's value alone.
func checkWildcardPathThrough(row wildcardPathRow) error {
	if !strings.HasSuffix(row.RoutePath, "*") {
		return fmt.Errorf("route %q has no wildcard: static does not ask about it", row.RoutePath)
	}
	unclean, err := staticVerdict("/", func(c *echo.Context) {
		c.InitializeRoute(&echo.RouteInfo{Method: http.MethodGet, Path: row.RoutePath}, &echo.PathValues{{Name: "*", Value: row.P}})
	})
	if err != nil {
		return fmt.Errorf("wildcard %q of route %q: %w", row.P, row.RoutePath, err)
	}
	if unclean != row.Unclean {
		return fmt.Errorf("wildcard %q of route %q through the middleware is unclean = %v; the table says %v", row.P, row.RoutePath, unclean, row.Unclean)
	}
	if unclean != httprule.HasUncleanWildcardPath(row.P, row.RoutePath) {
		return fmt.Errorf("wildcard %q of route %q: the middleware gives %v and the function %v", row.P, row.RoutePath, unclean, !unclean)
	}
	return nil
}

// checkOriginThrough asks both adapters of the decision: CORS answers an allowed origin with the spelling it was
// configured with, and CSRF lets a cross-site POST from a trusted origin through.
func checkOriginThrough(row originRow) error {
	want, wantOK := httprule.MatchOrigin(row.Allowed, row.Origin)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderOrigin, row.Origin)
	x := throughMiddleware(echo.New(), CORSWithConfig(CORSConfig{AllowOrigins: row.Allowed}), req, nil, nil)
	if x.err != nil || !x.called {
		return fmt.Errorf("origin %q with CORS: error %v, next called %v", row.Origin, x.err, x.called)
	}
	values := x.rec.Header().Values(echo.HeaderAccessControlAllowOrigin)
	match, ok := "", len(values) > 0
	if ok {
		match = values[0]
	}
	if match != row.Match || ok != row.OK {
		return fmt.Errorf("origin %q through CORS = %q, %v; the table says %q, %v", row.Origin, match, ok, row.Match, row.OK)
	}
	if match != want || ok != wantOK {
		return fmt.Errorf("origin %q: CORS gives %q, %v and the function %q, %v", row.Origin, match, ok, want, wantOK)
	}

	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(echo.HeaderSecFetchSite, "cross-site")
	req.Header.Set(echo.HeaderOrigin, row.Origin)
	x = throughMiddleware(echo.New(), CSRFWithConfig(CSRFConfig{TrustedOrigins: row.Allowed}), req, nil, nil)
	trusted := x.err == nil && x.called
	if !trusted && (x.err == nil || x.called) {
		return fmt.Errorf("origin %q with CSRF: error %v, next called %v", row.Origin, x.err, x.called)
	}
	if trusted != row.OK {
		return fmt.Errorf("origin %q through CSRF is trusted = %v; the table says %v", row.Origin, trusted, row.OK)
	}
	if trusted != wantOK {
		return fmt.Errorf("origin %q: CSRF gives %v and the function %v", row.Origin, trusted, wantOK)
	}
	return nil
}

// deliverable drops the rows only the function can be given.
func deliverable(rows []textRow) []textRow {
	var kept []textRow
	for _, row := range rows {
		if !row.Direct {
			kept = append(kept, row)
		}
	}
	return kept
}

// checkRuleRows holds every row of one table to check, and refuses a table with no row.
func checkRuleRows[R any](t *testing.T, name string, rows []R, check func(R) error) {
	t.Helper()
	if len(rows) == 0 {
		t.Errorf("%s: the table has no row", name)
	}
	for i, row := range rows {
		if err := check(row); err != nil {
			t.Errorf("%s, row %d: %v", name, i, err)
		}
	}
}

// refuseRuleRows gives check every row of one table after falsify has changed what the row expects, and fails for
// each one that still passes.
func refuseRuleRows[R any](t *testing.T, name string, rows []R, falsify func(R) R, check func(R) error) {
	t.Helper()
	if len(rows) == 0 {
		t.Errorf("%s: the table has no row", name)
	}
	for i, row := range rows {
		if check(falsify(row)) == nil {
			t.Errorf("%s, row %d: a falsified row passed the check", name, i)
		}
	}
}

func TestRulesThroughMiddleware(t *testing.T) {
	table, err := loadRuleTable(ruleTablePath)
	if err != nil {
		t.Fatal(err)
	}
	checkRuleRows(t, "redirect", table.Redirect, checkRedirectThrough)
	checkRuleRows(t, "redirectHost", table.RedirectHost, checkRedirectHostThrough)
	checkRuleRows(t, "acceptsGzip", table.AcceptsGzip, checkAcceptsGzipThrough)
	checkRuleRows(t, "contentCoding", table.ContentCoding, checkContentCodingThrough)
	checkRuleRows(t, "escape", deliverable(table.Escape), checkEscapeThrough)
	checkRuleRows(t, "sanitize", deliverable(table.Sanitize), checkSanitizeThrough)
	checkRuleRows(t, "pathSegment", table.PathSegment, checkPathSegmentThrough)
	checkRuleRows(t, "wildcardPath", table.WildcardPath, checkWildcardPathThrough)
	checkRuleRows(t, "origin", table.Origin, checkOriginThrough)
}

func TestRulesThroughMiddlewareViolation(t *testing.T) {
	table, err := loadRuleTable(ruleTablePath)
	if err != nil {
		t.Fatal(err)
	}
	refuseRuleRows(t, "redirect", table.Redirect, func(row redirectRow) redirectRow { row.OK = !row.OK; return row }, checkRedirectThrough)
	refuseRuleRows(t, "redirect target", table.Redirect, func(row redirectRow) redirectRow { row.URL += "x"; return row }, checkRedirectThrough)
	refuseRuleRows(t, "redirectHost", table.RedirectHost, func(row redirectHostRow) redirectHostRow { row.Valid = !row.Valid; return row }, checkRedirectHostThrough)
	refuseRuleRows(t, "acceptsGzip", table.AcceptsGzip, func(row acceptsGzipRow) acceptsGzipRow { row.Accepts = !row.Accepts; return row }, checkAcceptsGzipThrough)
	refuseRuleRows(t, "contentCoding", table.ContentCoding, func(row contentCodingRow) contentCodingRow { row.Has = !row.Has; return row }, checkContentCodingThrough)
	refuseRuleRows(t, "escape", deliverable(table.Escape), func(row textRow) textRow { row.Out += "x"; return row }, checkEscapeThrough)
	refuseRuleRows(t, "sanitize", deliverable(table.Sanitize), func(row textRow) textRow { row.Out += "x"; return row }, checkSanitizeThrough)
	refuseRuleRows(t, "pathSegment", table.PathSegment, func(row pathSegmentRow) pathSegmentRow { row.Unclean = !row.Unclean; return row }, checkPathSegmentThrough)
	refuseRuleRows(t, "wildcardPath", table.WildcardPath, func(row wildcardPathRow) wildcardPathRow { row.Unclean = !row.Unclean; return row }, checkWildcardPathThrough)
	refuseRuleRows(t, "origin", table.Origin, func(row originRow) originRow { row.OK = !row.OK; return row }, checkOriginThrough)

	// A drift is the middleware deciding by another rule than the one the table names: the www redirect answers
	// the rows of the https rule differently, and the check must say so.
	drifted := 0
	for _, row := range table.Redirect {
		if row.Rule != "https" {
			continue
		}
		row.Rule = "www"
		if checkRedirectThrough(row) != nil {
			drifted++
		}
	}
	if drifted == 0 {
		t.Error("no https row told the www redirect from the https redirect")
	}
	if checkRedirectThrough(redirectRow{Rule: "no-such-rule"}) == nil {
		t.Error("a row that names no redirect rule passed the check")
	}
	if checkEscapeThrough(textRow{Direct: true}) == nil || checkSanitizeThrough(textRow{Direct: true}) == nil {
		t.Error("a direct row passed a check through the middleware")
	}
	if checkWildcardPathThrough(wildcardPathRow{P: "file", RoutePath: "/assets"}) == nil {
		t.Error("a route with no wildcard passed the wildcard check")
	}
	if _, err := loadRuleTable("../httprule/testdata/no-such-table.json"); err == nil {
		t.Error("a missing table was loaded")
	}
}
