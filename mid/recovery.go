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

// CORS returns a CORS middleware. Pass no args for allow-all,
// or pass allowed origins for whitelist mode.
func CORS(allowOrigins ...string) gin.HandlerFunc {
	if len(allowOrigins) == 0 {
		return corsAllowAll()
	}
	return corsWhitelist(allowOrigins)
}

// CORSFromConfig creates CORS middleware from CORSConfig.
func CORSFromConfig(cfg CORSCfg) gin.HandlerFunc {
	if !cfg.Enable {
		return func(c *gin.Context) { c.Next() }
	}
	if cfg.Mode == "whitelist" && len(cfg.Whitelist) > 0 {
		return corsWhitelist(cfg.Whitelist)
	}
	return corsAllowAll()
}

// CORSCfg mirrors boot.CORSConfig for mid package use (avoids circular import).
type CORSCfg struct {
	Enable    bool
	Mode      string
	Whitelist []string
}

func corsAllowAll() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
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

func corsWhitelist(allowed []string) gin.HandlerFunc {
	set := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		set[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if set[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Trace-Id")
		c.Header("Access-Control-Expose-Headers", "X-Trace-Id,refresh-access-token,refresh-exp")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
