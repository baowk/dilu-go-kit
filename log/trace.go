package log

import "context"

type ctxKey struct{}

// TraceIDKey is the context key for trace ID.
var TraceIDKey = ctxKey{}

// WithTraceID returns a new context carrying the trace ID.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, TraceIDKey, traceID)
}

// GetTraceID extracts the trace ID from context. Returns "" if not set.
func GetTraceID(ctx context.Context) string {
	if v, ok := ctx.Value(TraceIDKey).(string); ok {
		return v
	}
	return ""
}
