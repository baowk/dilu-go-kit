package telemetry

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

func TestInitDefaultsToNoop(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	p, err := Init(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Enabled() {
		t.Fatal("default provider should be disabled")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGinMiddlewarePreservesContextAndResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p, err := Init(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(p.GinMiddleware())
	r.GET("/ping", func(c *gin.Context) {
		if got := trace.SpanContextFromContext(c.Request.Context()); got.IsValid() {
			t.Fatal("noop provider should not create a recording span")
		}
		c.Status(204)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/ping", nil))
	if w.Code != 204 {
		t.Fatalf("status = %d", w.Code)
	}
}
