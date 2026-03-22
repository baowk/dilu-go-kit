package mid

import (
	"github.com/baowk/dilu-go-kit/log"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const TraceHeader = "X-Trace-Id"

// Trace returns a Gin middleware that extracts or generates a trace ID,
// stores it in the context, and sets the response header.
func Trace() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(TraceHeader)
		if traceID == "" {
			traceID = uuid.NewString()
		}

		// Store in gin context (for handlers)
		c.Set("trace_id", traceID)

		// Store in request context (for log.InfoContext)
		ctx := log.WithTraceID(c.Request.Context(), traceID)
		c.Request = c.Request.WithContext(ctx)

		// Response header
		c.Header(TraceHeader, traceID)

		c.Next()
	}
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
