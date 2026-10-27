// Package reqctx carries per-request values that layers below the HTTP
// server record, such as the request ID on ledger entries.
package reqctx

import "context"

type requestIDKey struct{}

// WithRequestID returns ctx carrying the request's ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the ID set by WithRequestID, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
