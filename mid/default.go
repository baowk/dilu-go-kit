package mid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	kitlog "github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
)

// DefaultConfig holds settings for the Default middleware chain.
// Map this from your boot.Config in main.go.
type DefaultConfig struct {
	CORS        CORSCfg
	AccessLimit AccessLimitCfg
}

// AccessLimitCfg for rate limiting.
type AccessLimitCfg struct {
	Enable    bool
	Total     int    // max requests per window (default 300)
	Duration  int    // window in seconds (default 5)
	Backend   string // memory (default); Redis implementation is in contrib
	KeyPrefix string
}

// Default registers all standard middleware on the Gin engine.
// Order: Trace → Recovery → ErrorHandler → Logger → CORS → RateLimit
//
// Usage:
//
//	mid.Default(a.Gin, mid.DefaultConfig{
//	    CORS: mid.CORSCfg{Enable: true, Mode: "allow-all"},
//	    AccessLimit: mid.AccessLimitCfg{Enable: true, Total: 300, Duration: 5},
//	})
func Default(r *gin.Engine, cfg DefaultConfig) {
	r.Use(Trace())
	r.Use(Recovery())
	r.Use(ErrorHandler())
	r.Use(Logger())

	// CORS — only add if explicitly enabled
	if cfg.CORS.Enable {
		r.Use(CORSFromConfig(cfg.CORS))
	}

	// Rate limit
	if cfg.AccessLimit.Enable {
		r.Use(RateLimitFromConfig(cfg.AccessLimit))
	}
}

// RateLimitFromConfig returns the core in-memory limiter. Redis configuration
// falls back to memory; use contrib/mid/ratelimit/redis for distributed limits.
func RateLimitFromConfig(cfg AccessLimitCfg) gin.HandlerFunc {
	total := cfg.Total
	if total <= 0 {
		total = 300
	}
	dur := cfg.Duration
	if dur <= 0 {
		dur = 5
	}
	window := time.Duration(dur) * time.Second
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "" || backend == "memory" {
		return RateLimit(total, window)
	}
	if backend == "redis" {
		kitlog.Warn("redis rate limiter is provided by contrib; falling back to bounded in-memory limiter")
		return RateLimit(total, window)
	}
	kitlog.Error("unknown rate limiter backend; refusing to silently fall back", "backend", cfg.Backend)
	return func(c *gin.Context) {
		resp.FailStatus(c, 503, 50002, "限流服务配置错误")
		c.Abort()
	}
}

func boundedRateLimitKey(value string) string {
	if len(value) <= 256 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
