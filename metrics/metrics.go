// Package metrics provides Prometheus metrics collection for HTTP and gRPC services.
//
// Usage:
//
//	metrics.Init("mf-user")
//	r.Use(metrics.GinMiddleware())
//	r.GET("/metrics", metrics.Handler())
package metrics

import (
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	httpRequestsInFlight prometheus.Gauge
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

	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration, httpRequestsInFlight)
}

// GinMiddleware returns a Gin middleware that records request metrics.
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" || c.Request.URL.Path == "/health" || c.Request.URL.Path == "/api/health" {
			c.Next()
			return
		}

		httpRequestsInFlight.Inc()
		start := time.Now()

		c.Next()

		httpRequestsInFlight.Dec()
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		// Use route pattern instead of actual path to avoid cardinality explosion
		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}

		httpRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
	}
}

// Handler returns a Gin handler that serves the Prometheus metrics endpoint.
//
//	r.GET("/metrics", metrics.Handler())
func Handler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}
