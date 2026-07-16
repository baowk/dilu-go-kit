package mid

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimiterReturns429Status(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewRateLimiter(1, time.Minute)
	defer limiter.Close()

	r := gin.New()
	r.Use(limiter.Middleware())
	r.GET("/x", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first status = %d", w.Code)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d", w.Code)
	}
}

func TestRateLimiterCloseIsIdempotent(t *testing.T) {
	limiter := NewRateLimiter(1, time.Millisecond)
	limiter.Close()
	limiter.Close()
}

func TestRateLimiterCloseIsConcurrentSafe(t *testing.T) {
	limiter := NewRateLimiter(1, time.Millisecond)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limiter.Close()
		}()
	}
	wg.Wait()
}

func TestRateLimiterDistributesClientsAcrossShards(t *testing.T) {
	seen := make(map[uint32]struct{})
	for _, key := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		seen[rateLimiterShardIndex(key)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("all test clients mapped to one shard: %v", seen)
	}
}

func TestRateLimitFromConfigFallsBackToMemoryWithoutRedisClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RateLimitFromConfig(AccessLimitCfg{
		Enable:   true,
		Total:    1,
		Duration: 60,
		Backend:  "redis",
	}))
	r.GET("/x", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first status = %d", w.Code)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d", w.Code)
	}
}
