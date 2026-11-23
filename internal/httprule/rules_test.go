// SPDX-License-Identifier: MIT

package httprule_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/fiberfx/mx-echo/v7/internal/httprule"
)

// ruleTable is testdata/rules.json: one table per decision. The middleware package reads the same file in
// TestRulesThroughMiddleware, so a row holds the function and the middleware to one answer.
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

var redirectRules = map[string]func(scheme, host, uri string) (bool, string){
	"https":         httprule.RedirectHTTPS,
	"https-www":     httprule.RedirectHTTPSWWW,
	"https-non-www": httprule.RedirectHTTPSNonWWW,
	"www":           httprule.RedirectWWW,
	"non-www":       httprule.RedirectNonWWW,
}

func checkRedirect(row redirectRow) error {
	rule, found := redirectRules[row.Rule]
	if !found {
		return fmt.Errorf("no redirect rule is named %q", row.Rule)
	}
	ok, url := rule(row.Scheme, row.Host, row.URI)
	if ok != row.OK || url != row.URL {
		return fmt.Errorf("%s(%q, %q, %q) = %v, %q; the table says %v, %q", row.Rule, row.Scheme, row.Host, row.URI, ok, url, row.OK, row.URL)
	}
	return nil
}

func checkRedirectHost(row redirectHostRow) error {
	if got := httprule.ValidRedirectHost(row.Host); got != row.Valid {
		return fmt.Errorf("ValidRedirectHost(%q) = %v; the table says %v", row.Host, got, row.Valid)
	}
	return nil
}

func checkAcceptsGzip(row acceptsGzipRow) error {
	if got := httprule.AcceptsGzip(row.AcceptEncoding); got != row.Accepts {
		return fmt.Errorf("AcceptsGzip(%q) = %v; the table says %v", row.AcceptEncoding, got, row.Accepts)
	}
	return nil
}

func checkContentCoding(row contentCodingRow) error {
	if got := httprule.HasContentCoding(row.ContentEncoding); got != row.Has {
		return fmt.Errorf("HasContentCoding(%q) = %v; the table says %v", row.ContentEncoding, got, row.Has)
	}
	return nil
}

func checkEscape(row textRow) error {
	if got := httprule.EscapeControlChars(row.In); got != row.Out {
		return fmt.Errorf("EscapeControlChars(%q) = %q; the table says %q", row.In, got, row.Out)
	}
	return nil
}

func checkSanitize(row textRow) error {
	if got := httprule.SanitizeURI(row.In); got != row.Out {
		return fmt.Errorf("SanitizeURI(%q) = %q; the table says %q", row.In, got, row.Out)
	}
	return nil
}

func checkPathSegment(row pathSegmentRow) error {
	if got := httprule.HasDotOrEmptySegment(row.Path); got != row.Unclean {
		return fmt.Errorf("HasDotOrEmptySegment(%q) = %v; the table says %v", row.Path, got, row.Unclean)
	}
	return nil
}

func checkWildcardPath(row wildcardPathRow) error {
	if got := httprule.HasUncleanWildcardPath(row.P, row.RoutePath); got != row.Unclean {
		return fmt.Errorf("HasUncleanWildcardPath(%q, %q) = %v; the table says %v", row.P, row.RoutePath, got, row.Unclean)
	}
	return nil
}

func checkOrigin(row originRow) error {
	match, ok := httprule.MatchOrigin(row.Allowed, row.Origin)
	if match != row.Match || ok != row.OK {
		return fmt.Errorf("MatchOrigin(%q, %q) = %q, %v; the table says %q, %v", row.Allowed, row.Origin, match, ok, row.Match, row.OK)
	}
	return nil
}

// checkRows holds every row of one table to check, and refuses a table with no row.
func checkRows[R any](t *testing.T, name string, rows []R, check func(R) error) {
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

// refuseRows gives check every row of one table after falsify has changed what the row expects, and fails for
// each one that still passes.
func refuseRows[R any](t *testing.T, name string, rows []R, falsify func(R) R, check func(R) error) {
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

func TestRules(t *testing.T) {
	table, err := loadRuleTable("testdata/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "redirect", table.Redirect, checkRedirect)
	checkRows(t, "redirectHost", table.RedirectHost, checkRedirectHost)
	checkRows(t, "acceptsGzip", table.AcceptsGzip, checkAcceptsGzip)
	checkRows(t, "contentCoding", table.ContentCoding, checkContentCoding)
	checkRows(t, "escape", table.Escape, checkEscape)
	checkRows(t, "sanitize", table.Sanitize, checkSanitize)
	checkRows(t, "pathSegment", table.PathSegment, checkPathSegment)
	checkRows(t, "wildcardPath", table.WildcardPath, checkWildcardPath)
	checkRows(t, "origin", table.Origin, checkOrigin)

	// Every redirect rule has a row that redirects and a row that does not.
	for name := range redirectRules {
		for _, want := range []bool{true, false} {
			if !slices.ContainsFunc(table.Redirect, func(row redirectRow) bool { return row.Rule == name && row.OK == want }) {
				t.Errorf("redirect: no row of rule %q has ok %v", name, want)
			}
		}
	}
}

func TestRulesViolation(t *testing.T) {
	table, err := loadRuleTable("testdata/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	refuseRows(t, "redirect", table.Redirect, func(row redirectRow) redirectRow { row.OK = !row.OK; return row }, checkRedirect)
	refuseRows(t, "redirect target", table.Redirect, func(row redirectRow) redirectRow { row.URL += "x"; return row }, checkRedirect)
	refuseRows(t, "redirectHost", table.RedirectHost, func(row redirectHostRow) redirectHostRow { row.Valid = !row.Valid; return row }, checkRedirectHost)
	refuseRows(t, "acceptsGzip", table.AcceptsGzip, func(row acceptsGzipRow) acceptsGzipRow { row.Accepts = !row.Accepts; return row }, checkAcceptsGzip)
	refuseRows(t, "contentCoding", table.ContentCoding, func(row contentCodingRow) contentCodingRow { row.Has = !row.Has; return row }, checkContentCoding)
	refuseRows(t, "escape", table.Escape, func(row textRow) textRow { row.Out += "x"; return row }, checkEscape)
	refuseRows(t, "sanitize", table.Sanitize, func(row textRow) textRow { row.Out += "x"; return row }, checkSanitize)
	refuseRows(t, "pathSegment", table.PathSegment, func(row pathSegmentRow) pathSegmentRow { row.Unclean = !row.Unclean; return row }, checkPathSegment)
	refuseRows(t, "wildcardPath", table.WildcardPath, func(row wildcardPathRow) wildcardPathRow { row.Unclean = !row.Unclean; return row }, checkWildcardPath)
	refuseRows(t, "origin", table.Origin, func(row originRow) originRow { row.OK = !row.OK; return row }, checkOrigin)

	if checkRedirect(redirectRow{Rule: "no-such-rule"}) == nil {
		t.Error("a row that names no redirect rule passed the check")
	}
	if _, err := loadRuleTable("testdata/no-such-table.json"); err == nil {
		t.Error("a missing table was loaded")
	}
}
