// SPDX-License-Identifier: MIT

package reqid_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/fiberfx/mx-echo/v7/reqid"
)

// foreignKey is a key of this test's own: a value kept under it is not a request's name.
type foreignKey struct{}

// checkCarried returns an error unless ctx carries want as reqid.FromContext reads it.
func checkCarried(ctx context.Context, want reqid.ID) error {
	if got := reqid.FromContext(ctx); got != want {
		return fmt.Errorf("FromContext = %s, want %s", got, want)
	}
	return nil
}

func TestContext(t *testing.T) {
	id := reqid.New(1700000000000, 0xdeadbeef)
	other := reqid.New(1700000000001, 7)

	// A bare context carries the zero ID.
	if err := checkCarried(context.Background(), reqid.ID{}); err != nil {
		t.Error(err)
	}
	if !reqid.FromContext(context.Background()).IsZero() {
		t.Error("the ID of a bare context is not zero")
	}

	// The round trip, and through a context derived from it.
	ctx := reqid.NewContext(context.Background(), id)
	if err := checkCarried(ctx, id); err != nil {
		t.Error(err)
	}
	derived, cancel := context.WithCancel(context.WithValue(ctx, foreignKey{}, other))
	defer cancel()
	if err := checkCarried(derived, id); err != nil {
		t.Error(err)
	}

	// The innermost name wins, and the outer context keeps its own.
	inner := reqid.NewContext(ctx, other)
	if err := checkCarried(inner, other); err != nil {
		t.Error(err)
	}
	if err := checkCarried(ctx, id); err != nil {
		t.Error(err)
	}
}

func TestContextViolation(t *testing.T) {
	id := reqid.New(1700000000000, 0xdeadbeef)
	// An ID kept under another key is not the request's name: FromContext must not find it.
	if checkCarried(context.WithValue(context.Background(), foreignKey{}, id), id) == nil {
		t.Fatal("an ID kept under a foreign key was read as the request's name")
	}
	if checkCarried(context.Background(), id) == nil {
		t.Fatal("a bare context was reported to carry an ID")
	}
}
