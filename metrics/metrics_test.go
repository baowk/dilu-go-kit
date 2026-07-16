package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGinMiddlewareIsSafeWithoutExplicitInit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
}
