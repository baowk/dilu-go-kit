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
	max        int
	window     time.Duration
	shards     [rateLimiterShardCount]rateLimiterShard
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	maxClients int
}

const rateLimiterShardCount = 64

type rateLimiterShard struct {
	mu      sync.Mutex
	clients map[string]*rateEntry
}

type rateEntry struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter creates a closeable in-memory rate limiter.
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	return newRateLimiter(max, window, true)
}

func newRateLimiter(max int, window time.Duration, cleanup bool) *RateLimiter {
	if max <= 0 {
		max = 1
	}
	if window <= 0 {
		window = time.Second
	}
	rl := &RateLimiter{
		max:        max,
		window:     window,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		maxClients: 100_000,
	}
	for i := range rl.shards {
		rl.shards[i].clients = make(map[string]*rateEntry)
	}
	if cleanup {
		go rl.cleanupLoop()
	} else {
		close(rl.done)
	}
	return rl
}

// RateLimit returns a simple in-memory rate limiter middleware.
// max requests per window per client IP. For explicit lifecycle management,
// use NewRateLimiter and call Close during shutdown.
func RateLimit(max int, window time.Duration) gin.HandlerFunc {
	// The convenience API cannot expose a lifecycle hook. Use bounded
	// opportunistic eviction instead of leaking a cleanup goroutine.
	return newRateLimiter(max, window, false).Middleware()
}

// Middleware returns the Gin middleware for the limiter.
func (l *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		shard := &l.shards[rateLimiterShardIndex(ip)]
		shard.mu.Lock()
		e, ok := shard.clients[ip]
		now := time.Now()
		if !ok || now.After(e.resetAt) {
			if !ok && l.maxClients > 0 && len(shard.clients) >= l.maxClients/rateLimiterShardCount+1 {
				for key, entry := range shard.clients {
					if now.After(entry.resetAt) {
						delete(shard.clients, key)
					}
				}
				if len(shard.clients) >= l.maxClients/rateLimiterShardCount+1 {
					shard.mu.Unlock()
					resp.FailStatus(c, 429, 42902, "客户端数量过多")
					c.Abort()
					return
				}
			}
			e = &rateEntry{count: 0, resetAt: now.Add(l.window)}
			shard.clients[ip] = e
		}
		e.count++
		over := e.count > l.max
		shard.mu.Unlock()

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
	interval := l.window
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
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
	now := time.Now()
	for i := range l.shards {
		shard := &l.shards[i]
		shard.mu.Lock()
		for k, e := range shard.clients {
			if now.After(e.resetAt) {
				delete(shard.clients, k)
			}
		}
		shard.mu.Unlock()
	}
}

func rateLimiterShardIndex(key string) uint32 {
	const offset32 = uint32(2166136261)
	const prime32 = uint32(16777619)
	hash := offset32
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= prime32
	}
	return hash & (rateLimiterShardCount - 1)
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
	keyPrefix = boundedRateLimitKey(keyPrefix)
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
			// A directly constructed Redis limiter with no backend must not
			// silently disable protection. RateLimitFromConfig handles the
			// explicit compatibility fallback to memory; this lower-level API
			// fails closed so misconfiguration cannot become an open endpoint.
			resp.FailStatus(c, 503, 50002, "限流服务不可用")
			c.Abort()
			return
		}
		keyPart := l.keyFunc(c)
		if keyPart == "" {
			keyPart = c.ClientIP()
		}
		keyPart = boundedRateLimitKey(keyPart)
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
