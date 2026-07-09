package mid

import (
	"net/url"
	"strings"
	"time"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/gin-gonic/gin"
)

// Logger returns a Gin middleware that logs every request with
// method, path, status, latency, client IP, and trace_id.
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := logPath(c.Request.URL)

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

func logPath(u *url.URL) string {
	if u.RawQuery == "" {
		return u.Path
	}

	q := u.Query()
	for key := range q {
		if isSensitiveQueryKey(key) {
			q.Set(key, "[REDACTED]")
		}
	}
	if encoded := q.Encode(); encoded != "" {
		return u.Path + "?" + encoded
	}
	return u.Path
}

func isSensitiveQueryKey(key string) bool {
	switch strings.ToLower(key) {
	case "token", "access_token", "refresh_token", "id_token", "authorization", "auth", "jwt":
		return true
	default:
		return false
	}
}
