// Package otlphttp provides the optional OTLP-over-HTTP trace exporter.
package otlphttp

import (
	"context"
	"fmt"
	"os"
	"strings"

	core "github.com/baowk/dilu-go-kit/telemetry"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Register makes the OTLP HTTP exporter available to telemetry.Init.
func Register() error { return core.RegisterExporter("otlphttp", New) }

// New creates an OTLP HTTP exporter from the core telemetry configuration.
func New(ctx context.Context, cfg core.Config) (trace.SpanExporter, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	options := make([]otlptracehttp.Option, 0, 2)
	if endpoint != "" {
		if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
			options = append(options, otlptracehttp.WithEndpointURL(endpoint))
		} else {
			options = append(options, otlptracehttp.WithEndpoint(endpoint))
		}
	}
	envInsecure := strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")), "true")
	if strings.HasPrefix(endpoint, "http://") && !cfg.Insecure && !envInsecure {
		return nil, fmt.Errorf("telemetry: insecure HTTP endpoint requires Insecure=true or OTEL_EXPORTER_OTLP_INSECURE=true")
	}
	if cfg.Insecure || envInsecure {
		options = append(options, otlptracehttp.WithInsecure())
	}
	return otlptracehttp.New(ctx, options...)
}
