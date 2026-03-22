package boot

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenDB creates a GORM database connection from config.
func OpenDB(cfg DatabaseConfig, mode string) (*gorm.DB, error) {
	logLevel := logger.Warn
	if mode == "debug" {
		logLevel = logger.Info
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger:                 logger.Default.LogMode(logLevel),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}

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
		maxLife = 3600
	}

	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetConnMaxLifetime(time.Duration(maxLife) * time.Second)

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
