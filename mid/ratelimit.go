package mid

import (
	"sync"
	"time"

	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
)

// RateLimiter is a simple in-memory rate limiter. It limits each client IP in
// the current process only; use a shared backend for global multi-instance
// limits.
type RateLimiter struct {
	max     int
	window  time.Duration
	mu      sync.Mutex
	clients map[string]*rateEntry
	stop    chan struct{}
	done    chan struct{}
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
	select {
	case <-l.done:
		return
	default:
	}
	close(l.stop)
	<-l.done
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
