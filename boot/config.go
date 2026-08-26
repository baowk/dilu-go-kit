// Package boot provides application bootstrapping: config loading, logger,
// database connections, Redis, and graceful lifecycle management.
package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/registry"
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
	Secret   string   `mapstructure:"secret"`
	Expires  int      `mapstructure:"expires"` // minutes
	Refresh  int      `mapstructure:"refresh"` // minutes, auto-refresh window
	Issuer   string   `mapstructure:"issuer"`
	Subject  string   `mapstructure:"subject"`
	Audience []string `mapstructure:"audience"`
}

// CORSConfig describes CORS settings.
type CORSConfig struct {
	Enable    bool     `mapstructure:"enable"`
	Mode      string   `mapstructure:"mode"`      // "allow-all" or "whitelist"
	Whitelist []string `mapstructure:"whitelist"` // allowed origins
}

// AccessLimitConfig describes rate limiting.
type AccessLimitConfig struct {
	Enable    bool   `mapstructure:"enable"`
	Total     int    `mapstructure:"total"`    // max requests per window (default 300)
	Duration  int    `mapstructure:"duration"` // window in seconds (default 5)
	Backend   string `mapstructure:"backend"`  // "memory" (default) or "redis"
	KeyPrefix string `mapstructure:"keyPrefix"`
}

// NotifyConfig describes the WebSocket notification target.
type NotifyConfig struct {
	WsURL string `mapstructure:"wsUrl"` // mf-ws internal API base URL
	Token string `mapstructure:"token"`
}

// RegistryConfig describes the service registry (etcd or consul).
// It also drives optional remote config loading from the same backend.
type RegistryConfig struct {
	Enable                  bool               `mapstructure:"enable"`
	Type                    string             `mapstructure:"type"`                    // "etcd" (default) or "consul"
	Endpoints               []string           `mapstructure:"endpoints"`               // etcd endpoints, e.g. ["127.0.0.1:2379"]
	Address                 string             `mapstructure:"address"`                 // consul address, e.g. "127.0.0.1:8500"
	Token                   string             `mapstructure:"token"`                   // consul ACL token (optional)
	Prefix                  string             `mapstructure:"prefix"`                  // service discovery prefix, default "/mofang/services/"
	TTL                     int                `mapstructure:"ttl"`                     // lease/check TTL in seconds, default 30
	DialTimeout             int                `mapstructure:"dialTimeout"`             // seconds, default 5
	CheckType               string             `mapstructure:"checkType"`               // consul check: "http" (boot default) or "ttl"
	CheckPath               string             `mapstructure:"checkPath"`               // consul readiness check path, default "/ready"
	DeregisterCriticalAfter int                `mapstructure:"deregisterCriticalAfter"` // consul critical deregister delay in seconds, default 300
	ConfigKey               string             `mapstructure:"configKey"`               // remote config key prefix, e.g. "/config/" → auto appends server.name
	ConfigNode              string             `mapstructure:"configNode"`              // node ID for per-instance override (optional, or env REMOTE_NODE)
	ConfigFormat            string             `mapstructure:"configFormat"`            // "yaml" (default) or "json"
	TLS                     registry.TLSConfig `mapstructure:"tls"`
}

func (r *RegistryConfig) consulCheckType() string {
	if strings.TrimSpace(r.CheckType) == "" {
		return "http"
	}
	return r.CheckType
}

// ServerConfig describes the HTTP server.
type ServerConfig struct {
	Name              string   `mapstructure:"name"`
	Addr              string   `mapstructure:"addr"`              // listen addr, e.g. ":7801"
	AdvertiseAddr     string   `mapstructure:"advertiseAddr"`     // service discovery addr, e.g. "10.0.1.5:7801"
	Mode              string   `mapstructure:"mode"`              // "debug" or "release"
	ReadHeaderTimeout int      `mapstructure:"readHeaderTimeout"` // seconds, default 5
	ReadTimeout       int      `mapstructure:"readTimeout"`       // seconds, default 15
	WriteTimeout      int      `mapstructure:"writeTimeout"`      // seconds, default 30
	IdleTimeout       int      `mapstructure:"idleTimeout"`       // seconds, default 60
	ShutdownTimeout   int      `mapstructure:"shutdownTimeout"`   // seconds, default 10
	MaxHeaderBytes    int      `mapstructure:"maxHeaderBytes"`    // default 1 MiB
	TrustedProxies    []string `mapstructure:"trustedProxies"`    // explicit reverse-proxy CIDRs
}

// DatabaseConfig describes a single database connection.
type DatabaseConfig struct {
	DSN           string `mapstructure:"dsn"`
	MaxIdle       int    `mapstructure:"maxIdle"`       // max idle connections (default 10)
	MaxOpen       int    `mapstructure:"maxOpen"`       // max open connections (default 50)
	MaxLifetime   int    `mapstructure:"maxLifetime"`   // max connection lifetime in seconds (default 3600)
	MaxIdleTime   int    `mapstructure:"maxIdleTime"`   // max idle time in seconds (default 300)
	SlowThreshold int    `mapstructure:"slowThreshold"` // slow query threshold in ms (default 200)
	PingOnOpen    *bool  `mapstructure:"pingOnOpen"`    // nil defaults to true
	PrepareStmt   *bool  `mapstructure:"prepareStmt"`   // nil defaults to true
}

// RedisConfig describes a Redis connection. Leave Addr empty to disable.
type RedisConfig struct {
	Addr     string    `mapstructure:"addr"`
	Username string    `mapstructure:"username"` // Redis 6+ ACL username (optional)
	Password string    `mapstructure:"password"`
	DB       int       `mapstructure:"db"`
	TLS      TLSConfig `mapstructure:"tls"`
}

// TLSConfig configures TLS transport settings for infrastructure clients.
// CAFile is optional when the server certificate chains to the system roots.
type TLSConfig struct {
	Enable     bool   `mapstructure:"enable"`
	CAFile     string `mapstructure:"caFile"`
	CertFile   string `mapstructure:"certFile"`
	KeyFile    string `mapstructure:"keyFile"`
	ServerName string `mapstructure:"serverName"`
}

// GRPCConfig describes an optional gRPC listener.
type GRPCConfig struct {
	Enable          bool   `mapstructure:"enable"`
	Addr            string `mapstructure:"addr"`            // listen addr, e.g. ":7889"
	AdvertiseAddr   string `mapstructure:"advertiseAddr"`   // service discovery addr, e.g. "10.0.1.5:7889"
	ShutdownTimeout int    `mapstructure:"shutdownTimeout"` // seconds, default 10
	MaxRecvMsgSize  int    `mapstructure:"maxRecvMsgSize"`  // bytes, default 4 MiB
	MaxSendMsgSize  int    `mapstructure:"maxSendMsgSize"`  // bytes, default 4 MiB
}

// LoadConfig reads a YAML config file into cfg.
// Environment variables override matching base Config fields, including when
// Config is embedded in a service-specific struct.
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
	if base := embeddedBaseConfig(cfg); base != nil {
		if err := base.validateInlineSecrets(); err != nil {
			return err
		}
		applyEnvOverrides(base)
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
	if err := cfg.validateInlineSecrets(); err != nil {
		return nil, err
	}
	applyEnvOverrides(&cfg)
	return &cfg, nil
}

func (c *Config) validateInlineSecrets() error {
	if c.Server.Mode != "release" && c.Server.Mode != "production" {
		return nil
	}
	for name, db := range c.Database {
		if db.DSN != "" {
			return fmt.Errorf("database.%s.dsn must use an environment variable in release mode", name)
		}
	}
	if c.Redis.Password != "" || c.JWT.Secret != "" || c.Registry.Token != "" || c.Notify.Token != "" {
		return fmt.Errorf("passwords, JWT secrets, and tokens must use environment variables in release mode")
	}
	return nil
}

func applyEnvOverrides(cfg *Config) {
	if cfg == nil {
		return
	}
	if addr := os.Getenv("SERVER_ADDR"); addr != "" {
		cfg.Server.Addr = addr
	}
	if addr := os.Getenv("SERVER_ADVERTISE_ADDR"); addr != "" {
		cfg.Server.AdvertiseAddr = addr
	}
	if addr := os.Getenv("GRPC_ADDR"); addr != "" {
		cfg.GRPC.Addr = addr
	}
	if addr := os.Getenv("GRPC_ADVERTISE_ADDR"); addr != "" {
		cfg.GRPC.AdvertiseAddr = addr
	}
	if node := os.Getenv("REMOTE_NODE"); node != "" {
		cfg.Registry.ConfigNode = node
	}
	if dsn := os.Getenv("DATABASE_DSN"); dsn != "" {
		for k := range cfg.Database {
			db := cfg.Database[k]
			db.DSN = dsn
			cfg.Database[k] = db
		}
	}
	for name := range cfg.Database {
		envKey := "DATABASE_" + envName(name) + "_DSN"
		if dsn := os.Getenv(envKey); dsn != "" {
			db := cfg.Database[name]
			db.DSN = dsn
			cfg.Database[name] = db
		}
	}
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		cfg.Redis.Addr = addr
	}
	if username := os.Getenv("REDIS_USERNAME"); username != "" {
		cfg.Redis.Username = username
	}
	if password := os.Getenv("REDIS_PASSWORD"); password != "" {
		cfg.Redis.Password = password
	}
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		cfg.JWT.Secret = secret
	}
	if token := os.Getenv("REGISTRY_TOKEN"); token != "" {
		cfg.Registry.Token = token
	}
	if wsURL := os.Getenv("NOTIFY_WS_URL"); wsURL != "" {
		cfg.Notify.WsURL = wsURL
	}
	if token := os.Getenv("NOTIFY_TOKEN"); token != "" {
		cfg.Notify.Token = token
	}
}

func embeddedBaseConfig(cfg any) *Config {
	if cfg == nil {
		return nil
	}
	if base, ok := cfg.(*Config); ok {
		return base
	}
	rv := reflect.ValueOf(cfg)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return nil
	}
	configType := reflect.TypeOf(Config{})
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Field(i)
		if field.Type() == configType && field.CanAddr() && field.Addr().CanInterface() {
			return field.Addr().Interface().(*Config)
		}
		if field.Type() == reflect.PointerTo(configType) && !field.IsNil() && field.CanInterface() {
			return field.Interface().(*Config)
		}
	}
	return nil
}

// Validate checks startup-critical configuration before resources are opened.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	mode := strings.TrimSpace(c.Server.Mode)
	if mode != c.Server.Mode || (mode != "" && mode != "debug" && mode != "release" && mode != "production") {
		return fmt.Errorf("server.mode must be debug, release, or production")
	}
	if strings.TrimSpace(c.Server.Name) == "" {
		return fmt.Errorf("server.name is required")
	}
	if strings.TrimSpace(c.Server.Addr) == "" {
		return fmt.Errorf("server.addr is required")
	}
	for name, db := range c.Database {
		if strings.TrimSpace(db.DSN) == "" {
			return fmt.Errorf("database.%s.dsn is required", name)
		}
	}
	if c.GRPC.Enable && strings.TrimSpace(c.GRPC.Addr) == "" {
		return fmt.Errorf("grpc.addr is required when grpc is enabled")
	}
	if c.Registry.Enable && len(c.Registry.Endpoints) == 0 && c.Registry.Address == "" {
		return fmt.Errorf("registry endpoint is required when registry is enabled")
	}
	return nil
}

func envName(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	cloned.Server.TrustedProxies = cloneStrings(cfg.Server.TrustedProxies)
	cloned.JWT.Audience = cloneStrings(cfg.JWT.Audience)
	cloned.CORS.Whitelist = cloneStrings(cfg.CORS.Whitelist)
	cloned.Registry.Endpoints = cloneStrings(cfg.Registry.Endpoints)
	if cfg.Database != nil {
		cloned.Database = make(map[string]DatabaseConfig, len(cfg.Database))
		for name, db := range cfg.Database {
			db.PingOnOpen = cloneBool(db.PingOnOpen)
			db.PrepareStmt = cloneBool(db.PrepareStmt)
			cloned.Database[name] = db
		}
	}
	return &cloned
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append(make([]string, 0, len(values)), values...)
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
