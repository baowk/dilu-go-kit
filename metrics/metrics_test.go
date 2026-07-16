package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
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

func TestInitAfterMiddlewareConstructionUpdatesServiceLabel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	middleware := GinMiddleware()
	Init("late-init-service")

	r := gin.New()
	r.Use(middleware)
	r.GET("/late", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/late", nil))

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "http_requests_total" {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "service" && label.GetValue() == "late-init-service" {
					return
				}
			}
		}
	}
	t.Fatal("http_requests_total did not use the service name set after middleware construction")
}
