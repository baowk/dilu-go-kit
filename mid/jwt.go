package mid

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/baowk/dilu-go-kit/resp"
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

		// Extract optional claims
		if rid, ok := claims["rid"].(float64); ok {
			c.Set("role_id", int(rid))
		}
		if nick, ok := claims["nick"].(string); ok {
			c.Set("nickname", nick)
		}
		if mob, ok := claims["mob"].(string); ok {
			c.Set("phone", mob)
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
	if s := c.GetHeader("a_uid"); s != "" {
		id, _ := strconv.ParseInt(s, 10, 64)
		return id
	}
	return 0
}

// GetNickname extracts the nickname from the Gin context.
func GetNickname(c *gin.Context) string {
	if v, ok := c.Get("nickname"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return c.GetHeader("a_nickname")
}

// GetRoleID extracts the role ID from the Gin context.
func GetRoleID(c *gin.Context) int {
	if v := c.GetInt("role_id"); v != 0 {
		return v
	}
	if s := c.GetHeader("a_rid"); s != "" {
		id, _ := strconv.Atoi(s)
		return id
	}
	return 0
}

// GetPhone extracts the phone from the Gin context.
func GetPhone(c *gin.Context) string {
	if v, ok := c.Get("phone"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return c.GetHeader("a_mobile")
}
