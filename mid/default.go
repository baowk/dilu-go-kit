package mid

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
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
	Backend   string // memory (default) or redis
	Redis     redis.Cmdable
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

// RateLimitFromConfig returns a memory or Redis-backed rate limiter based on cfg.
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
	if cfg.Backend == "redis" && cfg.Redis != nil {
		return RedisRateLimit(RedisRateLimitConfig{
			Client:    cfg.Redis,
			Max:       total,
			Window:    window,
			KeyPrefix: cfg.KeyPrefix,
		})
	}
	return RateLimit(total, window)
}
