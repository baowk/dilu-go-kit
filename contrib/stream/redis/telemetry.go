package stream

import (
	"context"
	"github.com/baowk/dilu-go-kit/telemetry"
	"go.opentelemetry.io/otel/propagation"
	"strings"
)

type telemetryKey struct{}

// WithTelemetry associates an OpenTelemetry provider with stream operations
// performed using ctx. Existing APIs remain unchanged when omitted.
func WithTelemetry(ctx context.Context, provider *telemetry.Provider) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, telemetryKey{}, provider)
}

func providerFromContext(ctx context.Context) *telemetry.Provider {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(telemetryKey{}).(*telemetry.Provider)
	return p
}

// InjectMessageContext copies W3C trace context into message fields. It never
// mutates the caller's map and is safe to use before publishing a message.
func InjectMessageContext(ctx context.Context, values map[string]any) map[string]any {
	out := make(map[string]any, len(values)+2)
	for k, v := range values {
		out[k] = v
	}
	if p := providerFromContext(ctx); p != nil {
		carrier := propagation.MapCarrier{}
		p.Propagator().Inject(ctx, carrier)
		for k, v := range carrier {
			out[k] = v
		}
	}
	return out
}

// ContextForMessage restores W3C trace context from a message's traceparent
// and tracestate fields. It returns the original context when absent/invalid.
func ContextForMessage(ctx context.Context, msg Message) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	p := providerFromContext(ctx)
	if p == nil {
		return ctx
	}
	carrier := propagation.MapCarrier{}
	for _, key := range []string{"traceparent", "tracestate"} {
		if value, ok := msg.Values[key].(string); ok && strings.TrimSpace(value) != "" {
			carrier[key] = value
		}
	}
	return p.Propagator().Extract(ctx, carrier)
}
