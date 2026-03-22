package mid

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mofang-ai/mofang-go-kit/resp"
)

// JWTConfig configures the JWT middleware.
type JWTConfig struct {
	Secret string // HMAC signing key

	// HeaderUID is an optional header name for pre-verified user ID
	// (e.g. from an API gateway). If set and present, JWT parsing is skipped.
	HeaderUID string // default: "" (disabled)
}

// JWT returns a Gin middleware that verifies Bearer tokens.
// On success it sets "uid" (int64) in the Gin context.
func JWT(cfg JWTConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Pre-verified by gateway
		if cfg.HeaderUID != "" {
			if uidStr := c.GetHeader(cfg.HeaderUID); uidStr != "" {
				uid, _ := strconv.ParseInt(uidStr, 10, 64)
				if uid > 0 {
					c.Set("uid", uid)
					c.Next()
					return
				}
			}
		}

		// Parse Bearer token
		auth := c.GetHeader("Authorization")
		if auth == "" {
			auth = "Bearer " + c.Query("token") // fallback for WebSocket
		}
		if !strings.HasPrefix(auth, "Bearer ") {
			resp.Fail(c, 40101, "未登录")
			c.Abort()
			return
		}

		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			return []byte(cfg.Secret), nil
		})
		if err != nil || !token.Valid {
			resp.Fail(c, 40103, "Token 无效")
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			resp.Fail(c, 40103, "Token 无效")
			c.Abort()
			return
		}

		if uid, ok := claims["uid"].(float64); ok && uid > 0 {
			c.Set("uid", int64(uid))
		} else {
			resp.Fail(c, 40103, "Token 无效")
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetUID extracts the authenticated user ID from the Gin context.
func GetUID(c *gin.Context) int64 {
	if v, ok := c.Get("uid"); ok {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}
