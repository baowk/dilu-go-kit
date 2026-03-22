package mid

import (
	"time"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/gin-gonic/gin"
)

// Logger returns a Gin middleware that logs every request with
// method, path, status, latency, client IP, and trace_id.
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		if c.Request.URL.RawQuery != "" {
			path += "?" + c.Request.URL.RawQuery
		}

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		log.InfoContext(c.Request.Context(), "request",
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"latency", latency.String(),
			"ip", c.ClientIP(),
			"size", c.Writer.Size(),
		)
	}
}
