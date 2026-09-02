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
	"crypto/subtle"
	"database/sql"
	"net"
	"net/http"
	"os"
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
	httpRequestsTotal         *prometheus.CounterVec
	httpRequestDuration       *prometheus.HistogramVec
	httpRequestsInFlight      *prometheus.GaugeVec
	grpcRequestsTotal         *prometheus.CounterVec
	grpcRequestDuration       *prometheus.HistogramVec
	dependencyRequestsTotal   *prometheus.CounterVec
	dependencyRequestDuration *prometheus.HistogramVec
	circuitBreakerState       *prometheus.GaugeVec
	dbPoolStats               *prometheus.GaugeVec
	collectorsOnce            sync.Once
	currentServiceName        atomic.Pointer[string]
)

// Init sets the service label used by subsequent observations and ensures the
// collectors are registered. It is safe to call again after a config reload.
func Init(serviceName string) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		serviceName = "unknown"
	}
	serviceName = boundedLabel(serviceName)
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
	dependencyRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "dependency_requests_total", Help: "Total outbound dependency requests"},
		[]string{"service", "dependency", "operation", "status"},
	)
	dependencyRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "dependency_request_duration_seconds", Help: "Outbound dependency request duration in seconds"},
		[]string{"service", "dependency", "operation"},
	)
	circuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "circuit_breaker_state", Help: "Current circuit breaker state (one active state is 1)"},
		[]string{"service", "breaker", "state"},
	)
	dbPoolStats = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "db_pool_stats", Help: "Current database connection pool statistics"},
		[]string{"service", "database", "state"},
	)

	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration, httpRequestsInFlight, grpcRequestsTotal, grpcRequestDuration, dependencyRequestsTotal, dependencyRequestDuration, circuitBreakerState, dbPoolStats)
}

func ensureCollectors() {
	collectorsOnce.Do(initMetrics)
}

// ObserveDependency records an outbound dependency call. Status should be a
// stable class or code (for example "ok", "timeout", or "503").
func ObserveDependency(dependency, operation, status string, duration time.Duration) {
	ensureCollectors()
	if dependency == "" {
		dependency = "unknown"
	}
	if operation == "" {
		operation = "unknown"
	}
	if status == "" {
		status = "error"
	}
	if duration < 0 {
		duration = 0
	}
	dependencyRequestsTotal.WithLabelValues(serviceName(), boundedLabel(dependency), boundedLabel(operation), boundedLabel(status)).Inc()
	dependencyRequestDuration.WithLabelValues(serviceName(), boundedLabel(dependency), boundedLabel(operation)).Observe(duration.Seconds())
}

// ObserveCircuitBreaker publishes the current state of one breaker.
func ObserveCircuitBreaker(name, state string) {
	ensureCollectors()
	if name == "" {
		name = "default"
	}
	for _, candidate := range []string{"closed", "open", "half_open"} {
		value := 0.0
		if candidate == state {
			value = 1
		}
		circuitBreakerState.WithLabelValues(serviceName(), boundedLabel(name), candidate).Set(value)
	}
}

// ObserveDBPool records a database/sql connection pool snapshot.
func ObserveDBPool(name string, stats sql.DBStats) {
	ensureCollectors()
	if name == "" {
		name = "default"
	}
	values := map[string]float64{
		"open": float64(stats.OpenConnections), "in_use": float64(stats.InUse),
		"idle": float64(stats.Idle), "wait_count": float64(stats.WaitCount),
		"wait_duration_seconds": stats.WaitDuration.Seconds(),
	}
	for state, value := range values {
		dbPoolStats.WithLabelValues(serviceName(), boundedLabel(name), state).Set(value)
	}
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
			httpRequestsTotal.WithLabelValues(service, boundedLabel(c.Request.Method), boundedLabel(path), boundedLabel(statusCode)).Inc()
			httpRequestDuration.WithLabelValues(service, boundedLabel(c.Request.Method), boundedLabel(path)).Observe(duration)
		}()
		c.Next()
	}
}

// Handler returns a Gin handler that serves the Prometheus metrics endpoint.
//
//	r.GET("/metrics", metrics.Handler())
func Handler() gin.HandlerFunc {
	return HandlerFromEnv()
}

// HandlerWithAccess serves metrics with optional bearer-token and source-CIDR
// protection. Metrics commonly contain operational details and should not be
// exposed on a public listener without one of these controls.
func HandlerWithAccess(token string, allowedCIDRs []string) gin.HandlerFunc {
	ensureCollectors()
	h := promhttp.Handler()
	allowed, invalid := parseCIDRs(allowedCIDRs)
	return func(c *gin.Context) {
		if !metricsAccessAllowed(c.Request, token, allowed, invalid) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// HandlerFromEnv is a convenience for services that keep metrics on their
// normal listener. It reads METRICS_TOKEN and METRICS_ALLOWED_CIDRS at startup.
func HandlerFromEnv() gin.HandlerFunc {
	token := strings.TrimSpace(os.Getenv("METRICS_TOKEN"))
	cidrs := splitCIDRs(os.Getenv("METRICS_ALLOWED_CIDRS"))
	if token == "" && len(cidrs) == 0 {
		// Safe default for services that accidentally expose /metrics on their
		// public listener. Prometheus deployments should explicitly set either
		// METRICS_TOKEN or METRICS_ALLOWED_CIDRS.
		cidrs = []string{"127.0.0.0/8", "::1/128"}
	}
	return HandlerWithAccess(token, cidrs)
}

func splitCIDRs(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func metricsAccessAllowed(r *http.Request, token string, allowed []*net.IPNet, invalid bool) bool {
	if invalid {
		return false
	}
	if token != "" {
		value := strings.TrimSpace(r.Header.Get("Authorization"))
		const prefix = "Bearer "
		if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
			return false
		}
		got := strings.TrimSpace(value[len(prefix):])
		if len(got) != len(token) || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			return false
		}
	}
	if len(allowed) == 0 {
		// An unconfigured direct handler must never become a public metrics
		// endpoint. HandlerFromEnv supplies loopback by default; callers using
		// HandlerWithAccess must explicitly provide a token or CIDR.
		return token != ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range allowed {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseCIDRs(values []string) ([]*net.IPNet, bool) {
	allowed := make([]*net.IPNet, 0, len(values))
	invalid := false
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			invalid = true
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			invalid = true
			continue
		}
		allowed = append(allowed, network)
	}
	return allowed, invalid
}

// GRPCUnaryServerInterceptor records unary gRPC latency and status codes.
func GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	ensureCollectors()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		service := serviceName()
		grpcRequestsTotal.WithLabelValues(service, boundedLabel(info.FullMethod), status.Code(err).String()).Inc()
		grpcRequestDuration.WithLabelValues(service, boundedLabel(info.FullMethod)).Observe(time.Since(start).Seconds())
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
		grpcRequestsTotal.WithLabelValues(service, boundedLabel(info.FullMethod), status.Code(err).String()).Inc()
		grpcRequestDuration.WithLabelValues(service, boundedLabel(info.FullMethod)).Observe(time.Since(start).Seconds())
		return err
	}
}

func boundedLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
