package telemetry

import (
	"context"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// RedisHook instruments go-redis commands with client spans. Arguments are
// intentionally not recorded to avoid leaking tokens or user data.
type RedisHook struct {
	Provider   *Provider
	TracerName string
}

// NewRedisHook creates a Redis tracing hook.
func NewRedisHook(provider *Provider) *RedisHook {
	return &RedisHook{Provider: provider, TracerName: "github.com/baowk/dilu-go-kit/telemetry/redis"}
}

func (h *RedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *RedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h == nil || h.Provider == nil {
			return next(ctx, cmd)
		}
		tracer := h.Provider.Tracer(h.TracerName)
		name := "redis " + strings.ToUpper(cmd.Name())
		ctx, span := tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient))
		span.SetAttributes(attribute.String("db.system", "redis"), attribute.String("db.operation.name", strings.ToUpper(cmd.Name())))
		err := next(ctx, cmd)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "redis error")
		}
		span.End()
		return err
	}
}

func (h *RedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if h == nil || h.Provider == nil {
			return next(ctx, cmds)
		}
		tracer := h.Provider.Tracer(h.TracerName)
		ctx, span := tracer.Start(ctx, "redis pipeline", trace.WithSpanKind(trace.SpanKindClient))
		span.SetAttributes(attribute.String("db.system", "redis"), attribute.Int("db.redis.pipeline.length", len(cmds)))
		err := next(ctx, cmds)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "redis error")
		}
		span.End()
		return err
	}
}
