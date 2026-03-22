package boot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// OpenDB creates a GORM database connection with production-ready pool settings.
func OpenDB(cfg DatabaseConfig, mode string) (*gorm.DB, error) {
	// Slow query threshold
	slowMs := cfg.SlowThreshold
	if slowMs <= 0 {
		slowMs = 200
	}

	logLevel := gormlogger.Warn
	if mode == "debug" {
		logLevel = gormlogger.Info
	}

	gormCfg := &gorm.Config{
		Logger: gormlogger.New(
			slogWriter{},
			gormlogger.Config{
				SlowThreshold:             time.Duration(slowMs) * time.Millisecond,
				LogLevel:                  logLevel,
				IgnoreRecordNotFoundError: true,
				Colorful:                  mode == "debug",
			},
		),
		SkipDefaultTransaction: true,
		// PrepareStmt caches prepared statements for ~10-15% throughput improvement
		PrepareStmt: true,
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN), gormCfg)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}

	// ── Connection pool settings ──

	maxIdle := cfg.MaxIdle
	if maxIdle <= 0 {
		maxIdle = 10
	}
	maxOpen := cfg.MaxOpen
	if maxOpen <= 0 {
		maxOpen = 50
	}
	maxLife := cfg.MaxLifetime
	if maxLife <= 0 {
		maxLife = 3600 // 1 hour
	}
	maxIdleTime := cfg.MaxIdleTime
	if maxIdleTime <= 0 {
		maxIdleTime = 300 // 5 minutes — reclaim idle connections, prevent PG "too many connections"
	}

	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetConnMaxLifetime(time.Duration(maxLife) * time.Second)
	sqlDB.SetConnMaxIdleTime(time.Duration(maxIdleTime) * time.Second)

	// ── Health check on startup ──

	pingOnOpen := cfg.PingOnOpen || cfg.SlowThreshold == 0 // default true
	if pingOnOpen {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			return nil, fmt.Errorf("db ping: %w", err)
		}
	}

	return db, nil
}

// OpenAllDBs opens all databases defined in config.
func OpenAllDBs(cfgs map[string]DatabaseConfig, mode string) (map[string]*gorm.DB, error) {
	dbs := make(map[string]*gorm.DB, len(cfgs))
	for name, cfg := range cfgs {
		db, err := OpenDB(cfg, mode)
		if err != nil {
			return nil, fmt.Errorf("db[%s]: %w", name, err)
		}
		dbs[name] = db
	}
	return dbs, nil
}

// slogWriter adapts slog to gormlogger.Writer interface.
type slogWriter struct{}

func (slogWriter) Printf(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}
