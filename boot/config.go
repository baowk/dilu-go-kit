// Package boot provides application bootstrapping: config loading, logger,
// database connections, Redis, and graceful lifecycle management.
package boot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/spf13/viper"
)

// Config is the base service configuration. Embed it in your own config
// struct to add service-specific fields.
type Config struct {
	Server      ServerConfig              `mapstructure:"server"`
	Log         LogConfig                 `mapstructure:"log"`
	Database    map[string]DatabaseConfig `mapstructure:"database"`
	Redis       RedisConfig               `mapstructure:"redis"`
	GRPC        GRPCConfig                `mapstructure:"grpc"`
	Registry    RegistryConfig            `mapstructure:"registry"`
	JWT         JWTConfig                 `mapstructure:"jwt"`
	CORS        CORSConfig                `mapstructure:"cors"`
	AccessLimit AccessLimitConfig         `mapstructure:"accessLimit"`
	Notify      NotifyConfig              `mapstructure:"notify"`
}

// LogConfig describes log output targets.
//
//	output: "console" (default), "file", or "both"
type LogConfig struct {
	Output string         `mapstructure:"output"` // "console" (default), "file", "both"
	File   log.FileConfig `mapstructure:"file"`
}

// JWTConfig describes JWT authentication settings.
type JWTConfig struct {
	Secret  string `mapstructure:"secret"`
	Expires int    `mapstructure:"expires"` // minutes
	Refresh int    `mapstructure:"refresh"` // minutes, auto-refresh window
	Issuer  string `mapstructure:"issuer"`
	Subject string `mapstructure:"subject"`
}

// CORSConfig describes CORS settings.
type CORSConfig struct {
	Enable    bool     `mapstructure:"enable"`
	Mode      string   `mapstructure:"mode"`      // "allow-all" or "whitelist"
	Whitelist []string `mapstructure:"whitelist"`  // allowed origins
}

// AccessLimitConfig describes rate limiting.
type AccessLimitConfig struct {
	Enable   bool `mapstructure:"enable"`
	Total    int  `mapstructure:"total"`    // max requests per window (default 300)
	Duration int  `mapstructure:"duration"` // window in seconds (default 5)
}

// NotifyConfig describes the WebSocket notification target.
type NotifyConfig struct {
	WsURL string `mapstructure:"wsUrl"` // mf-ws internal API base URL
}

// RegistryConfig describes the service registry (etcd or consul).
// It also drives optional remote config loading from the same backend.
type RegistryConfig struct {
	Enable       bool     `mapstructure:"enable"`
	Type         string   `mapstructure:"type"`         // "etcd" (default) or "consul"
	Endpoints    []string `mapstructure:"endpoints"`    // etcd endpoints, e.g. ["127.0.0.1:2379"]
	Address      string   `mapstructure:"address"`      // consul address, e.g. "127.0.0.1:8500"
	Token        string   `mapstructure:"token"`        // consul ACL token (optional)
	Prefix       string   `mapstructure:"prefix"`       // service discovery prefix, default "/mofang/services/"
	TTL          int      `mapstructure:"ttl"`          // lease/check TTL in seconds, default 30
	ConfigKey    string   `mapstructure:"configKey"`    // remote config key prefix, e.g. "/config/" → auto appends server.name
	ConfigNode   string   `mapstructure:"configNode"`   // node ID for per-instance override (optional, or env REMOTE_NODE)
	ConfigFormat string   `mapstructure:"configFormat"` // "yaml" (default) or "json"
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
	Username string `mapstructure:"username"` // Redis 6+ ACL username (optional)
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
