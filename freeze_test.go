// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freezeHolds adds a late route to e and requests it. It returns an error when the late route is served. Handlers
// are declared inside functions: a package-level func literal here would shift the init.funcN that TestHandlerName
// expects.
func freezeHolds(e *Echo) error {
	late := func(c *Context) error { return c.String(http.StatusOK, "late") }
	_, addErr := e.AddRoute(Route{Method: http.MethodGet, Path: "/late", Handler: late})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/late", nil))
	if rec.Code == http.StatusOK {
		return fmt.Errorf("a route added after Freeze is served (AddRoute returned %v)", addErr)
	}
	if !errors.Is(addErr, ErrRoutesFrozen) {
		return fmt.Errorf("AddRoute after Freeze returned %v, want ErrRoutesFrozen", addErr)
	}
	return nil
}

func TestFreeze(t *testing.T) {
	e := New()
	early := func(c *Context) error { return c.String(http.StatusOK, "early") }
	mw := func(next HandlerFunc) HandlerFunc { return next }
	e.GET("/early", early)
	g := e.Group("/g")

	require.NoError(t, e.Freeze())

	require.NoError(t, freezeHolds(e))
	assert.PanicsWithValue(t, ErrRoutesFrozen, func() { e.Add(http.MethodGet, "/added", early) })
	assert.PanicsWithValue(t, ErrRoutesFrozen, func() { e.GET("/get", early) })
	assert.PanicsWithValue(t, ErrRoutesFrozen, func() { e.Pre(mw) })
	assert.PanicsWithValue(t, ErrRoutesFrozen, func() { e.Use(mw) })
	assert.PanicsWithValue(t, ErrRoutesFrozen, func() { g.Use(mw) })
	assert.PanicsWithValue(t, ErrRoutesFrozen, func() { g.GET("/x", early) })

	// Routing answers as before.
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/early", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "early", rec.Body.String())
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFreezeConcurrentRouter(t *testing.T) {
	e := NewWithConfig(Config{Router: NewConcurrentRouter(NewRouter(RouterConfig{}))})
	ok := func(c *Context) error { return c.String(http.StatusOK, "ok") }

	require.Error(t, e.Freeze())

	// Nothing is frozen: a later Add works and is served.
	assert.NotPanics(t, func() { e.GET("/after", ok) })
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/after", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestFreezeViolation(t *testing.T) {
	// The checker given an Echo that serves a late route returns an error: here Freeze was refused, so nothing holds.
	e := NewWithConfig(Config{Router: NewConcurrentRouter(NewRouter(RouterConfig{}))})
	require.Error(t, e.Freeze())
	assert.Error(t, freezeHolds(e))

	// And an Echo that was never frozen.
	assert.Error(t, freezeHolds(New()))
}
