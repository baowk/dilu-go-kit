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

	// HeaderTenantID/HeaderShopIDs/HeaderScopes are optional identity context
	// headers from a trusted gateway. They are only parsed when TrustHeaderUID
	// is true and HeaderUID is valid.
	HeaderTenantID string // e.g. "x-tenant-id"
	HeaderShopIDs  string // comma-separated shop IDs, e.g. "1,2,3"
	HeaderScopes   string // comma-separated scopes, e.g. "order.read,order.write"

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
					setTrustedHeaderContext(c, cfg)
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
		setOptionalClaimContext(c, claims)

		c.Next()
	}
}

func setTrustedHeaderContext(c *gin.Context, cfg JWTConfig) {
	if cfg.HeaderTenantID != "" {
		if tenantID, _ := strconv.ParseInt(c.GetHeader(cfg.HeaderTenantID), 10, 64); tenantID > 0 {
			c.Set("tenant_id", tenantID)
		}
	}
	if cfg.HeaderShopIDs != "" {
		if shopIDs := parseInt64CSV(c.GetHeader(cfg.HeaderShopIDs)); len(shopIDs) > 0 {
			c.Set("shop_ids", shopIDs)
		}
	}
	if cfg.HeaderScopes != "" {
		if scopes := parseStringCSV(c.GetHeader(cfg.HeaderScopes)); len(scopes) > 0 {
			c.Set("scopes", scopes)
		}
	}
}

func setOptionalClaimContext(c *gin.Context, claims jwt.MapClaims) {
	if tenantID := claimInt64(claims, "tenant_id", "tid"); tenantID > 0 {
		c.Set("tenant_id", tenantID)
	}
	if shopIDs := claimInt64Slice(claims, "shop_ids", "sids"); len(shopIDs) > 0 {
		c.Set("shop_ids", shopIDs)
	}
	if scopes := claimStringSlice(claims, "scopes", "scope"); len(scopes) > 0 {
		c.Set("scopes", scopes)
	}
}

func claimInt64(claims jwt.MapClaims, keys ...string) int64 {
	for _, key := range keys {
		switch v := claims[key].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		case string:
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
	}
	return 0
}

func claimInt64Slice(claims jwt.MapClaims, keys ...string) []int64 {
	for _, key := range keys {
		switch v := claims[key].(type) {
		case []any:
			out := make([]int64, 0, len(v))
			for _, item := range v {
				switch n := item.(type) {
				case float64:
					if n > 0 {
						out = append(out, int64(n))
					}
				case string:
					if parsed, _ := strconv.ParseInt(n, 10, 64); parsed > 0 {
						out = append(out, parsed)
					}
				}
			}
			return out
		case []int64:
			return v
		case string:
			return parseInt64CSV(v)
		}
	}
	return nil
}

func claimStringSlice(claims jwt.MapClaims, keys ...string) []string {
	for _, key := range keys {
		switch v := claims[key].(type) {
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					out = append(out, s)
				}
			}
			return out
		case []string:
			return v
		case string:
			return parseStringCSV(v)
		}
	}
	return nil
}

func parseInt64CSV(s string) []int64 {
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		n, _ := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func parseStringCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
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

// GetTenantID extracts the tenant ID from the Gin context.
func GetTenantID(c *gin.Context) int64 {
	if v, ok := c.Get("tenant_id"); ok {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}

// GetShopIDs extracts the allowed shop IDs from the Gin context.
func GetShopIDs(c *gin.Context) []int64 {
	if v, ok := c.Get("shop_ids"); ok {
		if ids, ok := v.([]int64); ok {
			return ids
		}
	}
	return nil
}

// GetScopes extracts token scopes from the Gin context.
func GetScopes(c *gin.Context) []string {
	if v, ok := c.Get("scopes"); ok {
		if scopes, ok := v.([]string); ok {
			return scopes
		}
	}
	return nil
}
