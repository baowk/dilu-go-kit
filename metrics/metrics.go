// Package metrics provides Prometheus metrics collection for HTTP and gRPC services.
//
// Usage:
//
//	metrics.Init("mf-user")
//	r.Use(metrics.GinMiddleware())
//	r.GET("/metrics", metrics.Handler())
package metrics

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var (
	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	httpRequestsInFlight *prometheus.GaugeVec
	grpcRequestsTotal    *prometheus.CounterVec
	grpcRequestDuration  *prometheus.HistogramVec
	collectorsOnce       sync.Once
	currentServiceName   atomic.Pointer[string]
)

// Init sets the service label used by subsequent observations and ensures the
// collectors are registered. It is safe to call again after a config reload.
func Init(serviceName string) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		serviceName = "unknown"
	}
	currentServiceName.Store(&serviceName)
	ensureCollectors()
}

func initMetrics() {
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"service", "method", "path", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"service", "method", "path"},
	)

	httpRequestsInFlight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Number of HTTP requests currently being processed",
		},
		[]string{"service"},
	)
	grpcRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "grpc_server_requests_total", Help: "Total number of gRPC server requests"},
		[]string{"service", "method", "code"},
	)
	grpcRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "grpc_server_request_duration_seconds", Help: "gRPC server request duration in seconds"},
		[]string{"service", "method"},
	)

	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration, httpRequestsInFlight, grpcRequestsTotal, grpcRequestDuration)
}

func ensureCollectors() {
	collectorsOnce.Do(initMetrics)
}

func serviceName() string {
	if name := currentServiceName.Load(); name != nil {
		return *name
	}
	return "unknown"
}

// GinMiddleware returns a Gin middleware that records request metrics.
func GinMiddleware() gin.HandlerFunc {
	ensureCollectors()
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" || c.Request.URL.Path == "/health" || c.Request.URL.Path == "/ready" || c.Request.URL.Path == "/api/health" {
			c.Next()
			return
		}

		service := serviceName()
		inFlight := httpRequestsInFlight.WithLabelValues(service)
		inFlight.Inc()
		start := time.Now()
		defer func() {
			inFlight.Dec()
			duration := time.Since(start).Seconds()
			statusCode := strconv.Itoa(c.Writer.Status())
			path := c.FullPath()
			if path == "" {
				path = "unknown"
			}
			httpRequestsTotal.WithLabelValues(service, c.Request.Method, path, statusCode).Inc()
			httpRequestDuration.WithLabelValues(service, c.Request.Method, path).Observe(duration)
		}()
		c.Next()
	}
}

// Handler returns a Gin handler that serves the Prometheus metrics endpoint.
//
//	r.GET("/metrics", metrics.Handler())
func Handler() gin.HandlerFunc {
	ensureCollectors()
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// GRPCUnaryServerInterceptor records unary gRPC latency and status codes.
func GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	ensureCollectors()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		service := serviceName()
		grpcRequestsTotal.WithLabelValues(service, info.FullMethod, status.Code(err).String()).Inc()
		grpcRequestDuration.WithLabelValues(service, info.FullMethod).Observe(time.Since(start).Seconds())
		return resp, err
	}
}

// GRPCStreamServerInterceptor records streaming RPC lifetime and status codes.
func GRPCStreamServerInterceptor() grpc.StreamServerInterceptor {
	ensureCollectors()
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, stream)
		service := serviceName()
		grpcRequestsTotal.WithLabelValues(service, info.FullMethod, status.Code(err).String()).Inc()
		grpcRequestDuration.WithLabelValues(service, info.FullMethod).Observe(time.Since(start).Seconds())
		return err
	}
}
