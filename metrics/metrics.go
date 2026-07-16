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
	"sync"
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
	httpRequestsInFlight prometheus.Gauge
	grpcRequestsTotal    *prometheus.CounterVec
	grpcRequestDuration  *prometheus.HistogramVec
	initOnce             sync.Once
)

// Init registers Prometheus metrics with the given service name as a label.
// Safe to call multiple times; only the first call takes effect.
func Init(serviceName string) {
	initOnce.Do(func() { initMetrics(serviceName) })
}

func initMetrics(serviceName string) {
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "http_requests_total",
			Help:        "Total number of HTTP requests",
			ConstLabels: prometheus.Labels{"service": serviceName},
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:        "http_request_duration_seconds",
			Help:        "HTTP request duration in seconds",
			ConstLabels: prometheus.Labels{"service": serviceName},
			Buckets:     []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path"},
	)

	httpRequestsInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "http_requests_in_flight",
			Help:        "Number of HTTP requests currently being processed",
			ConstLabels: prometheus.Labels{"service": serviceName},
		},
	)
	grpcRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "grpc_server_requests_total", Help: "Total number of gRPC server requests", ConstLabels: prometheus.Labels{"service": serviceName}},
		[]string{"method", "code"},
	)
	grpcRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "grpc_server_request_duration_seconds", Help: "gRPC server request duration in seconds", ConstLabels: prometheus.Labels{"service": serviceName}},
		[]string{"method"},
	)

	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration, httpRequestsInFlight, grpcRequestsTotal, grpcRequestDuration)
}

func ensureInit() { Init("unknown") }

// GinMiddleware returns a Gin middleware that records request metrics.
func GinMiddleware() gin.HandlerFunc {
	ensureInit()
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" || c.Request.URL.Path == "/health" || c.Request.URL.Path == "/api/health" {
			c.Next()
			return
		}

		httpRequestsInFlight.Inc()
		start := time.Now()
		defer func() {
			httpRequestsInFlight.Dec()
			duration := time.Since(start).Seconds()
			statusCode := strconv.Itoa(c.Writer.Status())
			path := c.FullPath()
			if path == "" {
				path = "unknown"
			}
			httpRequestsTotal.WithLabelValues(c.Request.Method, path, statusCode).Inc()
			httpRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
		}()
		c.Next()
	}
}

// Handler returns a Gin handler that serves the Prometheus metrics endpoint.
//
//	r.GET("/metrics", metrics.Handler())
func Handler() gin.HandlerFunc {
	ensureInit()
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// GRPCUnaryServerInterceptor records unary gRPC latency and status codes.
func GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	ensureInit()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		grpcRequestsTotal.WithLabelValues(info.FullMethod, status.Code(err).String()).Inc()
		grpcRequestDuration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		return resp, err
	}
}

// GRPCStreamServerInterceptor records streaming RPC lifetime and status codes.
func GRPCStreamServerInterceptor() grpc.StreamServerInterceptor {
	ensureInit()
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, stream)
		grpcRequestsTotal.WithLabelValues(info.FullMethod, status.Code(err).String()).Inc()
		grpcRequestDuration.WithLabelValues(info.FullMethod).Observe(time.Since(start).Seconds())
		return err
	}
}
