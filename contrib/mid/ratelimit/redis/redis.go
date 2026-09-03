// Package redisrate provides distributed Gin rate limiting backed by Redis.
package redisrate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
	redis "github.com/redis/go-redis/v9"
)

// RedisRateLimiter is a fixed-window distributed rate limiter backed by Redis.
type RedisRateLimiter struct {
	rdb       redis.Cmdable
	max       int
	window    time.Duration
	keyPrefix string
	keyFunc   func(*gin.Context) string
}

var redisRateLimitScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return count
`)

// RedisRateLimitConfig configures a Redis-backed rate limiter.
type RedisRateLimitConfig struct {
	Client    redis.Cmdable
	Max       int
	Window    time.Duration
	KeyPrefix string
	KeyFunc   func(*gin.Context) string
}

// NewRedisRateLimiter creates a Redis-backed rate limiter.
func NewRedisRateLimiter(cfg RedisRateLimitConfig) *RedisRateLimiter {
	max := cfg.Max
	if max <= 0 {
		max = 1
	}
	window := cfg.Window
	if window <= 0 {
		window = time.Second
	}
	keyPrefix := cfg.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = "ratelimit"
	}
	keyPrefix = boundedKey(keyPrefix)
	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = func(c *gin.Context) string { return c.ClientIP() }
	}
	return &RedisRateLimiter{rdb: cfg.Client, max: max, window: window, keyPrefix: keyPrefix, keyFunc: keyFunc}
}

// RedisRateLimit returns a Redis-backed rate limiter middleware.
func RedisRateLimit(cfg RedisRateLimitConfig) gin.HandlerFunc {
	return NewRedisRateLimiter(cfg).Middleware()
}

// Middleware returns the Gin middleware for the Redis limiter.
func (l *RedisRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if l == nil || l.rdb == nil {
			resp.FailStatus(c, 503, 50002, "限流服务不可用")
			c.Abort()
			return
		}
		keyPart := l.keyFunc(c)
		if keyPart == "" {
			keyPart = c.ClientIP()
		}
		key := fmt.Sprintf("%s:%s", l.keyPrefix, boundedKey(keyPart))
		allowed, err := l.allow(c.Request.Context(), key)
		if err != nil {
			resp.FailStatus(c, 503, 50002, "限流服务不可用")
			c.Abort()
			return
		}
		if !allowed {
			resp.FailStatus(c, 429, 42901, "请求过于频繁")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (l *RedisRateLimiter) allow(ctx context.Context, key string) (bool, error) {
	ttlMillis := l.window.Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	n, err := redisRateLimitScript.Run(ctx, l.rdb, []string{key}, ttlMillis).Int64()
	if err != nil {
		return false, err
	}
	return n <= int64(l.max), nil
}

func boundedKey(value string) string {
	if len(value) <= 256 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}
