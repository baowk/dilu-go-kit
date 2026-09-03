package gorm

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/baowk/dilu-go-kit/metrics"
	core "github.com/baowk/dilu-go-kit/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// GORMPlugin adds lightweight spans around GORM CRUD operations. It records
// operation and table metadata but not SQL text, which may contain secrets.
type GORMPlugin struct {
	Provider       *core.Provider
	lastPoolSample atomic.Int64
}

// NewGORMPlugin creates a GORM tracing plugin.
func NewGORMPlugin(provider *core.Provider) *GORMPlugin { return &GORMPlugin{Provider: provider} }

func (p *GORMPlugin) Name() string { return "dilu:otel" }

func (p *GORMPlugin) Initialize(db *gorm.DB) error {
	if err := db.Callback().Query().Before("gorm:query").Register("dilu:otel:start:query", p.start("query")); err != nil {
		return err
	}
	if err := db.Callback().Query().After("gorm:query").Register("dilu:otel:end:query", p.end); err != nil {
		return err
	}
	if err := db.Callback().Row().Before("gorm:row").Register("dilu:otel:start:row", p.start("row")); err != nil {
		return err
	}
	if err := db.Callback().Row().After("gorm:row").Register("dilu:otel:end:row", p.end); err != nil {
		return err
	}
	if err := db.Callback().Create().Before("gorm:create").Register("dilu:otel:start:create", p.start("create")); err != nil {
		return err
	}
	if err := db.Callback().Create().After("gorm:create").Register("dilu:otel:end:create", p.end); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").Register("dilu:otel:start:update", p.start("update")); err != nil {
		return err
	}
	if err := db.Callback().Update().After("gorm:update").Register("dilu:otel:end:update", p.end); err != nil {
		return err
	}
	if err := db.Callback().Delete().Before("gorm:delete").Register("dilu:otel:start:delete", p.start("delete")); err != nil {
		return err
	}
	if err := db.Callback().Delete().After("gorm:delete").Register("dilu:otel:end:delete", p.end); err != nil {
		return err
	}
	if err := db.Callback().Raw().Before("gorm:raw").Register("dilu:otel:start:raw", p.start("raw")); err != nil {
		return err
	}
	return db.Callback().Raw().After("gorm:raw").Register("dilu:otel:end:raw", p.end)
}

type spanContextKey struct{}

func (p *GORMPlugin) start(operation string) func(*gorm.DB) {
	return func(db *gorm.DB) {
		if p == nil || p.Provider == nil {
			return
		}
		ctx, span := p.Provider.Tracer("github.com/baowk/dilu-go-kit/telemetry/gorm").Start(db.Statement.Context, "db "+operation, trace.WithSpanKind(trace.SpanKindClient))
		span.SetAttributes(attribute.String("db.system", "postgresql"), attribute.String("db.operation.name", strings.ToUpper(operation)))
		if table := db.Statement.Table; table != "" {
			span.SetAttributes(attribute.String("db.collection.name", table))
		}
		db.Statement.Context = context.WithValue(ctx, spanContextKey{}, span)
	}
}

func (p *GORMPlugin) end(db *gorm.DB) {
	if db == nil || db.Statement == nil {
		return
	}
	if span, ok := db.Statement.Context.Value(spanContextKey{}).(trace.Span); ok {
		if db.Error != nil {
			span.RecordError(db.Error)
			span.SetStatus(codes.Error, "database error")
		}
		// Pool statistics are useful but sql.DB.Stats can contend at high QPS;
		// sample at most once per second per plugin instead of on every query.
		now := time.Now().UnixNano()
		last := p.lastPoolSample.Load()
		if now-last >= int64(time.Second) && p.lastPoolSample.CompareAndSwap(last, now) {
			if sqlDB, err := db.DB(); err == nil {
				name := "default"
				if value, ok := db.Get("dilu:db_name"); ok {
					if s, ok := value.(string); ok && s != "" {
						name = s
					}
				}
				metrics.ObserveDBPool(name, sqlDB.Stats())
			}
		}
		span.End()
	}
}
