package mid

import (
	"time"

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
	Enable   bool
	Total    int // max requests per window (default 300)
	Duration int // window in seconds (default 5)
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
		total := cfg.AccessLimit.Total
		if total <= 0 {
			total = 300
		}
		dur := cfg.AccessLimit.Duration
		if dur <= 0 {
			dur = 5
		}
		r.Use(RateLimit(total, time.Duration(dur)*time.Second))
	}
}
