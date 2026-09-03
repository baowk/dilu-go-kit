package mid

import (
	"github.com/baowk/dilu-go-kit/log"
	kitmetadata "github.com/baowk/dilu-go-kit/metadata"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

const (
	TraceHeader = "X-Trace-Id"
)

// Trace returns a Gin middleware that extracts or generates a trace ID,
// stores it in the context, and sets the response header.
func Trace() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := kitmetadata.ExtractHTTP(c.Request.Context(), c.Request.Header)
		values := kitmetadata.From(ctx)
		traceID := values.TraceID
		if traceID == "" {
			traceID = log.GetTraceID(ctx)
		}
		if traceID == "" {
			if spanCtx := trace.SpanContextFromContext(c.Request.Context()); spanCtx.IsValid() {
				traceID = spanCtx.TraceID().String()
			}
		}
		if traceID == "" {
			traceID = c.GetHeader(TraceHeader)
		}
		traceID = validTraceID(traceID)
		if traceID == "" {
			traceID = uuid.NewString()
		}

		// Store in gin context (for handlers)
		c.Set("trace_id", traceID)

		// Store in request context (for log.InfoContext)
		ctx = kitmetadata.With(ctx, kitmetadata.Values{TraceID: traceID})
		ctx = log.WithTraceID(ctx, traceID)
		c.Request = c.Request.WithContext(ctx)

		// Trace ID is the single request correlation identifier.
		c.Header(TraceHeader, traceID)

		c.Next()
	}
}

func validTraceID(traceID string) string {
	if traceID == "" || len(traceID) > 128 {
		return ""
	}
	for i := 0; i < len(traceID); i++ {
		c := traceID[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return ""
	}
	return traceID
}

// GetTraceID extracts the trace ID from a Gin context.
func GetTraceID(c *gin.Context) string {
	if v, ok := c.Get("trace_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
