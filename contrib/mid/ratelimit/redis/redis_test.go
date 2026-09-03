package redisrate

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRedisRateLimiterFailsClosedWithoutBackend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RedisRateLimit(RedisRateLimitConfig{Max: 1, Window: time.Minute}))
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing Redis backend status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
