package mid

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

func TestTracePrefersTraceHeaderAndAliasesRequestHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Trace())
	router.GET("/trace", func(c *gin.Context) {
		if got := GetTraceID(c); got != "trace-001" {
			t.Fatalf("gin trace_id = %q, want trace-001", got)
		}
		if got := log.GetTraceID(c.Request.Context()); got != "trace-001" {
			t.Fatalf("context trace_id = %q, want trace-001", got)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set(TraceHeader, "trace-001")
	req.Header.Set(RequestHeader, "legacy-001")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if got := rec.Header().Get(TraceHeader); got != "trace-001" {
		t.Fatalf("%s = %q, want trace-001", TraceHeader, got)
	}
	if got := rec.Header().Get(RequestHeader); got != "trace-001" {
		t.Fatalf("%s = %q, want trace-001", RequestHeader, got)
	}
}

func TestTraceUsesRequestHeaderAsCompatibilityAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Trace())
	router.GET("/trace", func(c *gin.Context) {
		if got := GetTraceID(c); got != "legacy-001" {
			t.Fatalf("gin trace_id = %q, want legacy-001", got)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set(RequestHeader, "legacy-001")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if got := rec.Header().Get(TraceHeader); got != "legacy-001" {
		t.Fatalf("%s = %q, want legacy-001", TraceHeader, got)
	}
	if got := rec.Header().Get(RequestHeader); got != "legacy-001" {
		t.Fatalf("%s = %q, want legacy-001", RequestHeader, got)
	}
}

func TestExtractTraceFromMetadataUsesRequestIDAlias(t *testing.T) {
	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(grpcRequestKey, "legacy-001"))

	ctx = extractTraceFromMetadata(ctx)

	if got := log.GetTraceID(ctx); got != "legacy-001" {
		t.Fatalf("context trace_id = %q, want legacy-001", got)
	}
}
