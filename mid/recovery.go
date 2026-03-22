// Package mid provides common Gin middleware: panic recovery, CORS,
// JWT authentication, rate limiting, traceId, and gRPC interceptors.
package mid

import (
	"net/http"
	"runtime/debug"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
)

// Recovery returns a middleware that recovers from panics and returns a 500.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.ErrorContext(c.Request.Context(), "panic recovered",
					"panic", r,
					"stack", string(debug.Stack()),
					"path", c.Request.URL.Path,
				)
				resp.Fail(c, 50000, "服务内部错误")
				c.Abort()
			}
		}()
		c.Next()
	}
}

// CORS returns a permissive CORS middleware.
func CORS(allowOrigins ...string) gin.HandlerFunc {
	origin := "*"
	if len(allowOrigins) > 0 {
		origin = allowOrigins[0]
	}
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Trace-Id")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
