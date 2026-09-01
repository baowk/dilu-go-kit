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

func TestHandlerFromEnvDefaultsToLoopback(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "")
	t.Setenv("METRICS_ALLOWED_CIDRS", "")
	h := HandlerFromEnv()

	localReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	localReq.RemoteAddr = "127.0.0.1:1234"
	localResp := httptest.NewRecorder()
	h(localContext(localResp, localReq))
	if localResp.Code != http.StatusOK {
		t.Fatalf("loopback metrics status = %d", localResp.Code)
	}

	remoteReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	remoteReq.RemoteAddr = "192.0.2.10:1234"
	remoteResp := httptest.NewRecorder()
	h(localContext(remoteResp, remoteReq))
	if remoteResp.Code != http.StatusForbidden {
		t.Fatalf("remote metrics status = %d, want %d", remoteResp.Code, http.StatusForbidden)
	}
}

func TestHandlerFromEnvAcceptsToken(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "metrics-secret")
	t.Setenv("METRICS_ALLOWED_CIDRS", "")
	h := HandlerFromEnv()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	resp := httptest.NewRecorder()
	h(localContext(resp, req))
	if resp.Code != http.StatusForbidden {
		t.Fatalf("missing token status = %d", resp.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("Authorization", "Bearer metrics-secret")
	resp = httptest.NewRecorder()
	h(localContext(resp, req))
	if resp.Code != http.StatusOK {
		t.Fatalf("valid token status = %d", resp.Code)
	}
}

func TestHandlerFromEnvAcceptsAllowedCIDR(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "")
	t.Setenv("METRICS_ALLOWED_CIDRS", "192.0.2.0/24")
	h := HandlerFromEnv()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	resp := httptest.NewRecorder()
	h(localContext(resp, req))
	if resp.Code != http.StatusOK {
		t.Fatalf("allowed CIDR status = %d", resp.Code)
	}
}

func localContext(resp http.ResponseWriter, req *http.Request) *gin.Context {
	c, _ := gin.CreateTestContext(resp)
	c.Request = req
	return c
}
