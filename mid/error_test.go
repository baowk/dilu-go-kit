package mid

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
)

func TestErrorHandlerUsesHTTPStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ErrorHandler())
	r.GET("/x", func(*gin.Context) { PanicApp(resp.CodeForbidden, "无权操作") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCORSInvalidModeFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORSFromConfig(CORSCfg{Enable: true, Mode: "typo"}))
	r.OPTIONS("/x", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCORSWhitelistSetsVaryOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS("https://app.example"))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q", got)
	}
}
