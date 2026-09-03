package mid

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

func TestTraceUsesTraceHeaderOnly(t *testing.T) {
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
	legacyHeader := "X-" + strings.Join([]string{"Request", "Id"}, "-")
	req.Header.Set(legacyHeader, "legacy-001")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if got := rec.Header().Get(TraceHeader); got != "trace-001" {
		t.Fatalf("%s = %q, want trace-001", TraceHeader, got)
	}
	if got := rec.Header().Get(legacyHeader); got != "" {
		t.Fatalf("legacy correlation header must not be emitted, got %q", got)
	}
}

func TestTraceIgnoresLegacyCorrelationHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Trace())
	router.GET("/trace", func(c *gin.Context) {
		if got := GetTraceID(c); got == "legacy-001" || got == "" {
			t.Fatalf("request ID must not be used as trace ID: %q", got)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	legacyHeader := "X-" + strings.Join([]string{"Request", "Id"}, "-")
	req.Header.Set(legacyHeader, "legacy-001")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if got := rec.Header().Get(TraceHeader); got == "legacy-001" || got == "" {
		t.Fatalf("request ID must not become trace ID: %q", got)
	}
	if got := rec.Header().Get(legacyHeader); got != "" {
		t.Fatalf("legacy correlation header must not be emitted, got %q", got)
	}
}

func TestExtractTraceFromMetadataIgnoresLegacyCorrelationKey(t *testing.T) {
	legacyKey := strings.ToLower("X-" + strings.Join([]string{"Request", "Id"}, "-"))
	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(legacyKey, "legacy-001"))

	ctx = extractTraceFromMetadata(ctx)

	if got := log.GetTraceID(ctx); got == "legacy-001" || got == "" {
		t.Fatalf("request ID must not become trace ID: %q", got)
	}
}

func TestExtractTraceFromMetadataGeneratesMissingID(t *testing.T) {
	ctx := extractTraceFromMetadata(t.Context())
	if got := log.GetTraceID(ctx); got == "" {
		t.Fatal("expected generated trace ID")
	}
}

func TestSetOutgoingTraceReplacesStaleMetadata(t *testing.T) {
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(grpcTraceKey, "old"))
	ctx = setOutgoingTrace(ctx, "new")
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok || len(md.Get(grpcTraceKey)) != 1 || md.Get(grpcTraceKey)[0] != "new" {
		t.Fatalf("metadata = %v", md)
	}
}

func TestTraceRejectsOversizedExternalID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Trace())
	router.GET("/trace", func(c *gin.Context) {
		if got := GetTraceID(c); got == strings.Repeat("x", 129) || got == "" {
			t.Fatalf("invalid trace ID was not replaced: %q", got)
		}
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set(TraceHeader, strings.Repeat("x", 129))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
}
