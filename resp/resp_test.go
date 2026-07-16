package resp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFailStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		FailStatus(c, http.StatusUnauthorized, CodeUnauthorized, "未登录")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Body.String(); got != `{"code":40101,"msg":"未登录"}` {
		t.Fatalf("body = %q", got)
	}
}

func TestErrorDoesNotExposeInternalMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { Error(c, errors.New("password=secret")) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("response leaked internal error: %s", w.Body.String())
	}
}
