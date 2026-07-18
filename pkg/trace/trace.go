package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type ctxKey struct{}

// Generate returns a fresh W3C traceparent header value.
func Generate() string {
	traceID := make([]byte, 16)
	spanID := make([]byte, 8)
	_, _ = rand.Read(traceID)
	_, _ = rand.Read(spanID)
	return "00-" + hex.EncodeToString(traceID) + "-" + hex.EncodeToString(spanID) + "-01"
}

// StoreInContext returns a new context with the traceparent value stored.
func StoreInContext(ctx context.Context, traceparent string) context.Context {
	return context.WithValue(ctx, ctxKey{}, traceparent)
}

// FromContext returns the traceparent stored by StoreInContext, or "".
func FromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}
