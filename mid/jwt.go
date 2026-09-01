package mid

import (
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig configures the JWT middleware.
type JWTConfig struct {
	Secret             string // HMAC signing key
	Issuer             string
	Subject            string
	Audience           []string
	TrustedHeaderCIDRs []string
	// AllowQueryToken enables the legacy WebSocket ?token= flow. Authorization
	// headers are preferred because URLs are routinely logged.
	AllowQueryToken bool

	// HeaderUID is an optional header name for pre-verified user ID
	// (e.g. from an API gateway). If set and present, JWT parsing is skipped.
	// TrustHeaderUID must also be true to enable this path.
	HeaderUID string // default: "" (disabled)

	// HeaderTenantID/HeaderShopIDs/HeaderScopes are optional identity context
	// headers from a trusted gateway. They are only parsed when TrustHeaderUID
	// is true and HeaderUID is valid.
	HeaderTenantID    string // e.g. "x-tenant-id"
	HeaderWorkspaceID string // e.g. "x-workspace-id"
	HeaderShopIDs     string // comma-separated shop IDs, e.g. "1,2,3"
	HeaderScopes      string // comma-separated scopes, e.g. "order.read,order.write"

	// TrustHeaderUID explicitly enables HeaderUID trust mode. Only turn this on
	// behind a trusted gateway that strips user-supplied identity headers.
	TrustHeaderUID bool
}

// JWT returns a Gin middleware that verifies Bearer tokens.
// On success it sets "uid" (int64) in the Gin context.
func JWT(cfg JWTConfig) gin.HandlerFunc {
	trustedNetworks := parseTrustedCIDRs(cfg.TrustedHeaderCIDRs)
	return func(c *gin.Context) {
		// Pre-verified by gateway. This is opt-in because identity headers are
		// trivially spoofable on services that can be reached directly.
		if cfg.HeaderUID != "" && cfg.TrustHeaderUID && trustedHeaderSourceAllowed(c, trustedNetworks) {
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
		if auth == "" && cfg.AllowQueryToken && strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			auth = "Bearer " + c.Query("token")
		}
		parts := strings.Fields(auth)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			resp.FailStatus(c, http.StatusUnauthorized, resp.CodeUnauthorized, "未登录")
			c.Abort()
			return
		}

		tokenStr := parts[1]
		if cfg.Secret == "" {
			resp.FailStatus(c, http.StatusUnauthorized, resp.CodeTokenInvalid, "Token 无效")
			c.Abort()
			return
		}
		parseOpts := []jwt.ParserOption{
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithExpirationRequired(),
			jwt.WithJSONNumber(),
		}
		if cfg.Issuer != "" {
			parseOpts = append(parseOpts, jwt.WithIssuer(cfg.Issuer))
		}
		if cfg.Subject != "" {
			parseOpts = append(parseOpts, jwt.WithSubject(cfg.Subject))
		}
		if len(cfg.Audience) > 0 {
			parseOpts = append(parseOpts, jwt.WithAudience(cfg.Audience...))
		}
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(cfg.Secret), nil
		}, parseOpts...)
		if err != nil || !token.Valid {
			resp.FailStatus(c, http.StatusUnauthorized, resp.CodeTokenInvalid, "Token 无效")
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			resp.FailStatus(c, http.StatusUnauthorized, resp.CodeTokenInvalid, "Token 无效")
			c.Abort()
			return
		}

		if uid := claimInt64(claims, "uid"); uid > 0 {
			c.Set("uid", uid)
		} else {
			resp.FailStatus(c, http.StatusUnauthorized, resp.CodeTokenInvalid, "Token 无效")
			c.Abort()
			return
		}

		// Extract optional claims
		if rid := claimInt64(claims, "rid"); rid > 0 && uint64(rid) <= uint64(^uint(0)>>1) {
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

func parseTrustedCIDRs(values []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err == nil {
			out = append(out, network)
		}
	}
	return out
}

func trustedHeaderSourceAllowed(c *gin.Context, networks []*net.IPNet) bool {
	if len(networks) == 0 {
		// Development and existing callers may omit the allowlist. boot.Config
		// rejects that configuration in release/production mode.
		return true
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		host = c.Request.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func setTrustedHeaderContext(c *gin.Context, cfg JWTConfig) {
	if cfg.HeaderTenantID != "" {
		if tenantID, _ := strconv.ParseInt(c.GetHeader(cfg.HeaderTenantID), 10, 64); tenantID > 0 {
			c.Set("tenant_id", tenantID)
		}
	}
	if cfg.HeaderWorkspaceID != "" {
		if workspaceID, _ := strconv.ParseInt(c.GetHeader(cfg.HeaderWorkspaceID), 10, 64); workspaceID > 0 {
			c.Set("workspace_id", workspaceID)
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
	if workspaceID := claimInt64(claims, "workspace_id", "wid"); workspaceID > 0 {
		c.Set("workspace_id", workspaceID)
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
			if v > 0 && v <= math.MaxInt64 && math.Trunc(v) == v {
				return int64(v)
			}
		case json.Number:
			if n, err := v.Int64(); err == nil && n > 0 {
				return n
			}
		case int64:
			if v > 0 {
				return v
			}
		case int:
			if v > 0 {
				return int64(v)
			}
		case string:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// GetWorkspaceID extracts the authenticated workspace ID from the Gin context.
func GetWorkspaceID(c *gin.Context) int64 {
	if v, ok := c.Get("workspace_id"); ok {
		if id, ok := v.(int64); ok {
			return id
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
					if n > 0 && n <= math.MaxInt64 && math.Trunc(n) == n {
						out = append(out, int64(n))
					}
				case json.Number:
					if parsed, err := n.Int64(); err == nil && parsed > 0 {
						out = append(out, parsed)
					}
				case string:
					if parsed, _ := strconv.ParseInt(n, 10, 64); parsed > 0 {
						out = append(out, parsed)
					}
				}
			}
			return out
		case []int64:
			out := make([]int64, 0, len(v))
			for _, n := range v {
				if n > 0 {
					out = append(out, n)
				}
			}
			return out
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
