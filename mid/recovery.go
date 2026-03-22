// Package mid provides common Gin middleware: panic recovery, CORS,
// JWT authentication, and rate limiting.
package mid

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/mofang-ai/mofang-go-kit/resp"
	"github.com/rs/zerolog/log"
)

// Recovery returns a middleware that recovers from panics and returns a 500.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Str("stack", string(debug.Stack())).
					Str("path", c.Request.URL.Path).
					Msg("panic recovered")
				resp.Fail(c, 50000, "服务内部错误")
				c.Abort()
			}
		}()
		c.Next()
	}
}

// CORS returns a permissive CORS middleware.
// For production, restrict AllowOrigin to your domain.
func CORS(allowOrigins ...string) gin.HandlerFunc {
	origin := "*"
	if len(allowOrigins) > 0 {
		origin = allowOrigins[0]
	}
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
