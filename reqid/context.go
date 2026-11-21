// SPDX-License-Identifier: MIT

package reqid

import "context"

// contextKey is the key an ID is kept under. Its type is unexported, so no other package can read the value or
// put another in its place.
type contextKey struct{}

// NewContext returns ctx carrying id.
func NewContext(ctx context.Context, id ID) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext returns the ID ctx carries, or the zero ID.
func FromContext(ctx context.Context) ID {
	id, _ := ctx.Value(contextKey{}).(ID)
	return id
}
