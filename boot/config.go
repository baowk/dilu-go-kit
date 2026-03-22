// Package boot provides application bootstrapping: config loading, logger,
// database connections, Redis, and graceful lifecycle management.
package boot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config is the base service configuration. Embed it in your own config
// struct to add service-specific fields.
type Config struct {
	Server   ServerConfig              `mapstructure:"server"`
	Database map[string]DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig               `mapstructure:"redis"`
	GRPC     GRPCConfig                `mapstructure:"grpc"`
}

// ServerConfig describes the HTTP server.
type ServerConfig struct {
	Name string `mapstructure:"name"`
	Addr string `mapstructure:"addr"` // e.g. ":7801"
	Mode string `mapstructure:"mode"` // "debug" or "release"
}

// DatabaseConfig describes a single database connection.
type DatabaseConfig struct {
	DSN            string `mapstructure:"dsn"`
	MaxIdle        int    `mapstructure:"maxIdle"`        // max idle connections (default 10)
	MaxOpen        int    `mapstructure:"maxOpen"`        // max open connections (default 50)
	MaxLifetime    int    `mapstructure:"maxLifetime"`    // max connection lifetime in seconds (default 3600)
	MaxIdleTime    int    `mapstructure:"maxIdleTime"`    // max idle time in seconds (default 300)
	SlowThreshold  int    `mapstructure:"slowThreshold"`  // slow query threshold in ms (default 200)
	PingOnOpen     bool   `mapstructure:"pingOnOpen"`     // ping DB on open to verify connectivity (default true)
}

// RedisConfig describes a Redis connection. Leave Addr empty to disable.
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// GRPCConfig describes an optional gRPC listener.
type GRPCConfig struct {
	Enable bool   `mapstructure:"enable"`
	Addr   string `mapstructure:"addr"` // e.g. ":7889"
}

// LoadConfig reads a YAML config file into cfg.
// Environment variables DATABASE_DSN and REDIS_ADDR override file values.
func LoadConfig(path string, cfg any) error {
	v := viper.New()
	v.SetConfigFile(path)
	ext := filepath.Ext(path)
	if ext == ".yaml" || ext == ".yml" {
		v.SetConfigType("yaml")
	}
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := v.Unmarshal(cfg); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}
	return nil
}

// LoadBaseConfig is a convenience wrapper that loads into a *Config.
func LoadBaseConfig(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	ext := filepath.Ext(path)
	if ext == ".yaml" || ext == ".yml" {
		v.SetConfigType("yaml")
	}
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	// Env overrides
	if dsn := os.Getenv("DATABASE_DSN"); dsn != "" {
		for k := range cfg.Database {
			db := cfg.Database[k]
			db.DSN = dsn
			cfg.Database[k] = db
		}
	}
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		cfg.Redis.Addr = addr
	}
	return &cfg, nil
}
