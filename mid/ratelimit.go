package mid

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimiter is a simple in-memory rate limiter. It limits each client IP in
// the current process only; use a shared backend for global multi-instance
// limits.
type RateLimiter struct {
	max       int
	window    time.Duration
	mu        sync.Mutex
	clients   map[string]*rateEntry
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

type rateEntry struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter creates a closeable in-memory rate limiter.
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	if max <= 0 {
		max = 1
	}
	if window <= 0 {
		window = time.Second
	}
	rl := &RateLimiter{
		max:     max,
		window:  window,
		clients: make(map[string]*rateEntry),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// RateLimit returns a simple in-memory rate limiter middleware.
// max requests per window per client IP. For explicit lifecycle management,
// use NewRateLimiter and call Close during shutdown.
func RateLimit(max int, window time.Duration) gin.HandlerFunc {
	return NewRateLimiter(max, window).Middleware()
}

// Middleware returns the Gin middleware for the limiter.
func (l *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		l.mu.Lock()
		e, ok := l.clients[ip]
		now := time.Now()
		if !ok || now.After(e.resetAt) {
			e = &rateEntry{count: 0, resetAt: now.Add(l.window)}
			l.clients[ip] = e
		}
		e.count++
		over := e.count > l.max
		l.mu.Unlock()

		if over {
			resp.FailStatus(c, 429, 42901, "请求过于频繁")
			c.Abort()
			return
		}
		c.Next()
	}
}

// Close stops the background cleanup goroutine.
func (l *RateLimiter) Close() {
	if l == nil {
		return
	}
	l.closeOnce.Do(func() {
		close(l.stop)
		<-l.done
	})
}

func (l *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(l.window)
	defer func() {
		ticker.Stop()
		close(l.done)
	}()

	for {
		select {
		case <-ticker.C:
			l.cleanupExpired()
		case <-l.stop:
			return
		}
	}
}

func (l *RateLimiter) cleanupExpired() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, e := range l.clients {
		if now.After(e.resetAt) {
			delete(l.clients, k)
		}
	}
}

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
	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = func(c *gin.Context) string { return c.ClientIP() }
	}
	return &RedisRateLimiter{
		rdb:       cfg.Client,
		max:       max,
		window:    window,
		keyPrefix: keyPrefix,
		keyFunc:   keyFunc,
	}
}

// RedisRateLimit returns a Redis-backed rate limiter middleware.
func RedisRateLimit(cfg RedisRateLimitConfig) gin.HandlerFunc {
	return NewRedisRateLimiter(cfg).Middleware()
}

// Middleware returns the Gin middleware for the Redis limiter.
func (l *RedisRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if l == nil || l.rdb == nil {
			c.Next()
			return
		}
		keyPart := l.keyFunc(c)
		if keyPart == "" {
			keyPart = c.ClientIP()
		}
		key := fmt.Sprintf("%s:%s", l.keyPrefix, keyPart)
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
