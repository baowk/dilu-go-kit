package telemetry

import (
	"context"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// InSpan runs fn inside a span and records any returned error.
func (p *Provider) InSpan(ctx context.Context, name string, kind trace.SpanKind, attrs []attribute.KeyValue, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return nil
	}
	if p == nil {
		return fn(ctx)
	}
	ctx, span := p.Tracer("github.com/baowk/dilu-go-kit/telemetry").Start(ctx, name, trace.WithSpanKind(kind))
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	err := fn(ctx)
	if err != nil {
		span.RecordError(err)
	}
	span.End()
	return err
}
