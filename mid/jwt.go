package mid

import (
	"errors"
	"strconv"
	"strings"

	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig configures the JWT middleware.
type JWTConfig struct {
	Secret string // HMAC signing key

	// HeaderUID is an optional header name for pre-verified user ID
	// (e.g. from an API gateway). If set and present, JWT parsing is skipped.
	// TrustHeaderUID must also be true to enable this path.
	HeaderUID string // default: "" (disabled)

	// TrustHeaderUID explicitly enables HeaderUID trust mode. Only turn this on
	// behind a trusted gateway that strips user-supplied identity headers.
	TrustHeaderUID bool
}

// JWT returns a Gin middleware that verifies Bearer tokens.
// On success it sets "uid" (int64) in the Gin context.
func JWT(cfg JWTConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Pre-verified by gateway. This is opt-in because identity headers are
		// trivially spoofable on services that can be reached directly.
		if cfg.HeaderUID != "" && cfg.TrustHeaderUID {
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
		if cfg.Secret == "" {
			resp.Fail(c, 40103, "Token 无效")
			c.Abort()
			return
		}
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
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
	return 0
}

// GetNickname extracts the nickname from the Gin context.
func GetNickname(c *gin.Context) string {
	if v, ok := c.Get("nickname"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// GetRoleID extracts the role ID from the Gin context.
func GetRoleID(c *gin.Context) int {
	if v := c.GetInt("role_id"); v != 0 {
		return v
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
	return ""
}
