package mid

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mofang-ai/mofang-go-kit/resp"
)

// RateLimit returns a simple in-memory rate limiter middleware.
// max requests per window per client IP.
func RateLimit(max int, window time.Duration) gin.HandlerFunc {
	type entry struct {
		count    int
		resetAt  time.Time
	}
	var mu sync.Mutex
	clients := make(map[string]*entry)

	// Background cleanup every window period
	go func() {
		for range time.Tick(window) {
			mu.Lock()
			now := time.Now()
			for k, e := range clients {
				if now.After(e.resetAt) {
					delete(clients, k)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		ip := c.ClientIP()
		mu.Lock()
		e, ok := clients[ip]
		now := time.Now()
		if !ok || now.After(e.resetAt) {
			e = &entry{count: 0, resetAt: now.Add(window)}
			clients[ip] = e
		}
		e.count++
		over := e.count > max
		mu.Unlock()

		if over {
			resp.Fail(c, 42901, "请求过于频繁")
			c.Abort()
			return
		}
		c.Next()
	}
}
