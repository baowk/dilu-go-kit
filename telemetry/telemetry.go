// Package telemetry provides optional OpenTelemetry tracing for dilu services.
//
// The package is deliberately opt-in. When no endpoint is configured it uses
// a no-op tracer provider, so existing services keep their current behaviour.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	kitlog "github.com/baowk/dilu-go-kit/log"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

// Config configures optional OTLP tracing. Endpoint may be a complete URL or
// a host:port pair. OTEL_EXPORTER_OTLP_TRACES_ENDPOINT is used when Endpoint
// is empty. Secrets and exporter headers should be supplied through OTEL_*
// environment variables rather than configuration files.
type Config struct {
	Enabled        bool    `mapstructure:"enabled"`
	ServiceName    string  `mapstructure:"serviceName"`
	ServiceVersion string  `mapstructure:"serviceVersion"`
	Environment    string  `mapstructure:"environment"`
	Endpoint       string  `mapstructure:"endpoint"`
	Insecure       bool    `mapstructure:"insecure"`
	SampleRatio    float64 `mapstructure:"sampleRatio"`
	SampleRatioSet bool    `mapstructure:"sampleRatioSet"`
}

// ExporterFactory creates a span exporter for a telemetry configuration.
// Exporters are optional adapters registered by contrib modules.
type ExporterFactory func(context.Context, Config) (sdktrace.SpanExporter, error)

var (
	exporterMu sync.RWMutex
	exporters  = map[string]ExporterFactory{}
)

// RegisterExporter registers or replaces a named exporter implementation.
func RegisterExporter(name string, factory ExporterFactory) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || factory == nil {
		return fmt.Errorf("telemetry: invalid exporter registration")
	}
	exporterMu.Lock()
	exporters[name] = factory
	exporterMu.Unlock()
	return nil
}

func exporterFor(name string) ExporterFactory {
	exporterMu.RLock()
	factory := exporters[strings.ToLower(strings.TrimSpace(name))]
	exporterMu.RUnlock()
	return factory
}

// Provider owns the tracer provider and propagator used by one application.
// It does not replace the process-global provider, avoiding conflicts when a
// host application (for example CrossHub) already owns OpenTelemetry setup.
type Provider struct {
	tp           trace.TracerProvider
	propagator   propagation.TextMapPropagator
	shutdown     func(context.Context) error
	enabled      bool
	shutdownOnce sync.Once
	shutdownErr  error
}

// Init creates a provider. With no endpoint, or when disabled, the returned
// provider is a safe no-op and Shutdown is still valid to call.
func Init(ctx context.Context, cfg Config) (*Provider, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prop := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	)
	if disabledByEnv() {
		return noopProvider(prop), nil
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	}
	if !cfg.Enabled && endpoint == "" {
		return noopProvider(prop), nil
	}
	serviceName := strings.TrimSpace(cfg.ServiceName)
	if serviceName == "" {
		serviceName = strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	}
	if serviceName == "" {
		serviceName = "dilu-service"
	}
	resourceAttrs := []attribute.KeyValue{attribute.String("service.name", serviceName)}
	if v := strings.TrimSpace(cfg.ServiceVersion); v != "" {
		resourceAttrs = append(resourceAttrs, attribute.String("service.version", v))
	}
	if v := strings.TrimSpace(cfg.Environment); v != "" {
		resourceAttrs = append(resourceAttrs, attribute.String("deployment.environment", v))
	}
	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithAttributes(resourceAttrs...))
	if err != nil {
		return nil, err
	}

	if endpoint != "" {
		cfg.Endpoint = endpoint
	}
	factory := exporterFor("otlphttp")
	if factory == nil {
		return nil, fmt.Errorf("telemetry: exporter %q is not registered; import contrib/telemetry/otlphttp", "otlphttp")
	}
	exporter, err := factory(ctx, cfg)
	if err != nil {
		return nil, err
	}

	ratio := cfg.SampleRatio
	samplerName := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")))
	if samplerName == "always_off" {
		ratio = 0
	} else if samplerName == "always_on" {
		ratio = 1
	} else if ratio <= 0 && !cfg.SampleRatioSet {
		if envRatio, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")), 64); err == nil {
			ratio = envRatio
		} else {
			// A conservative default keeps production overhead and exporter
			// volume bounded; development can explicitly set 1.0.
			ratio = 0.1
		}
	}
	samplerOpt := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(normalizeSampleRatio(ratio)))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(samplerOpt),
	)
	return &Provider{
		tp:         tp,
		propagator: prop,
		shutdown:   tp.Shutdown,
		enabled:    true,
	}, nil
}

func noopProvider(prop propagation.TextMapPropagator) *Provider {
	return &Provider{
		tp:         otel.GetTracerProvider(),
		propagator: prop,
		shutdown:   func(context.Context) error { return nil },
	}
}

func disabledByEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_TRACES_EXPORTER")), "none")
}

func normalizeSampleRatio(r float64) float64 {
	if r < 0 || r > 1 {
		return 0.1
	}
	return r
}

// Enabled reports whether this provider owns an active OTLP exporter.
func (p *Provider) Enabled() bool { return p != nil && p.enabled }

// Tracer returns a tracer from this provider.
func (p *Provider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	if p == nil || p.tp == nil {
		return otel.GetTracerProvider().Tracer(name, options...)
	}
	return p.tp.Tracer(name, options...)
}

// Propagator returns the W3C propagator used by this provider.
func (p *Provider) Propagator() propagation.TextMapPropagator {
	if p == nil || p.propagator == nil {
		return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	}
	return p.propagator
}

// Shutdown flushes pending spans. It is safe to call on a no-op provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.shutdown == nil {
		return nil
	}
	p.shutdownOnce.Do(func() { p.shutdownErr = p.shutdown(ctx) })
	return p.shutdownErr
}

// GinMiddleware creates a Gin tracing middleware for this provider.
func (p *Provider) GinMiddleware() gin.HandlerFunc {
	tracer := p.Tracer("github.com/baowk/dilu-go-kit/telemetry")
	prop := p.Propagator()
	return func(c *gin.Context) {
		ctx := prop.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		// Use a low-cardinality name until Gin has resolved the route. Raw paths
		// often contain IDs or attacker-controlled values and must not become
		// span-name dimensions.
		name := "HTTP " + c.Request.Method
		ctx, span := tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
		if spanCtx := span.SpanContext(); spanCtx.IsValid() {
			ctx = kitlog.WithTraceID(ctx, spanCtx.TraceID().String())
			c.Set("trace_id", spanCtx.TraceID().String())
		}
		c.Request = c.Request.WithContext(ctx)
		defer span.End()
		c.Next()
		span.SetAttributes(attribute.Int("http.response.status_code", c.Writer.Status()))
		if c.Writer.Status() >= 500 {
			span.SetStatus(codes.Error, "http server error")
			for _, requestErr := range c.Errors {
				span.RecordError(requestErr.Err)
			}
		}
		if route := c.FullPath(); route != "" {
			span.SetName(c.Request.Method + " " + route)
			span.SetAttributes(attribute.String("http.route", route))
		}
	}
}

// GRPCServerOption instruments a gRPC server with OpenTelemetry.
func (p *Provider) GRPCServerOption() grpc.ServerOption {
	return grpc.StatsHandler(otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(p.tracerProvider()),
		otelgrpc.WithPropagators(p.Propagator()),
	))
}

// GRPCDialOption instruments a gRPC client connection with OpenTelemetry.
func (p *Provider) GRPCDialOption() grpc.DialOption {
	return grpc.WithStatsHandler(otelgrpc.NewClientHandler(
		otelgrpc.WithTracerProvider(p.tracerProvider()),
		otelgrpc.WithPropagators(p.Propagator()),
	))
}

func (p *Provider) tracerProvider() trace.TracerProvider {
	if p == nil || p.tp == nil {
		return otel.GetTracerProvider()
	}
	return p.tp
}
