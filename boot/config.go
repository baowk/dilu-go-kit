// Package boot provides application bootstrapping: config loading, logger,
// database connections, Redis, and graceful lifecycle management.
package boot

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"github.com/baowk/dilu-go-kit/diagnostics"
	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/registry"
	"github.com/baowk/dilu-go-kit/telemetry"
	"github.com/spf13/viper"
)

// Config is the base service configuration. Embed it in your own config
// struct to add service-specific fields.
type Config struct {
	Server      ServerConfig              `mapstructure:"server"`
	Security    SecurityConfig            `mapstructure:"security"`
	Log         LogConfig                 `mapstructure:"log"`
	Database    map[string]DatabaseConfig `mapstructure:"database"`
	Redis       RedisConfig               `mapstructure:"redis"`
	GRPC        GRPCConfig                `mapstructure:"grpc"`
	Registry    RegistryConfig            `mapstructure:"registry"`
	JWT         JWTConfig                 `mapstructure:"jwt"`
	CORS        CORSConfig                `mapstructure:"cors"`
	AccessLimit AccessLimitConfig         `mapstructure:"accessLimit"`
	Notify      NotifyConfig              `mapstructure:"notify"`
	Telemetry   telemetry.Config          `mapstructure:"telemetry"`
	Diagnostics diagnostics.Config        `mapstructure:"diagnostics"`
}

// SecurityConfig controls transport security policy. TLS remains optional by
// default so local development can use loopback plaintext; setting RequireTLS
// makes boot reject plaintext Redis, registry, gRPC, Notify, and dedicated
// diagnostics transports.
type SecurityConfig struct {
	RequireTLS bool `mapstructure:"requireTLS"`
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
	Secret         string   `mapstructure:"secret"`
	Expires        int      `mapstructure:"expires"` // minutes
	Refresh        int      `mapstructure:"refresh"` // minutes, auto-refresh window
	Issuer         string   `mapstructure:"issuer"`
	Subject        string   `mapstructure:"subject"`
	Audience       []string `mapstructure:"audience"`
	HeaderUID      string   `mapstructure:"headerUid"`
	TrustHeaderUID bool     `mapstructure:"trustHeaderUid"`
	// TrustedHeaderCIDRs limits TrustHeaderUID to requests originating from
	// these gateway networks. It is required in release/production mode when
	// header trust is enabled.
	TrustedHeaderCIDRs string `mapstructure:"trustedHeaderCidrs"`
	// AllowQueryToken is an explicit compatibility switch for WebSocket clients
	// that cannot set Authorization headers. Keep disabled whenever possible.
	AllowQueryToken bool `mapstructure:"allowQueryToken"`
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
	WsURL           string `mapstructure:"wsUrl"` // mf-ws internal API base URL
	Token           string `mapstructure:"token"`
	Timeout         int    `mapstructure:"timeout"`         // request timeout in seconds, default 3
	MaxPayloadBytes int    `mapstructure:"maxPayloadBytes"` // default 1 MiB, maximum 16 MiB
}

// RegistryConfig describes the service registry (etcd or consul).
// It also drives optional remote config loading from the same backend.
type RegistryConfig struct {
	Enable                  bool               `mapstructure:"enable"`
	Type                    string             `mapstructure:"type"`                    // "etcd" (default), "consul", or registered backend
	Endpoints               []string           `mapstructure:"endpoints"`               // etcd endpoints, e.g. ["127.0.0.1:2379"]
	Address                 string             `mapstructure:"address"`                 // consul address, e.g. "127.0.0.1:8500"
	Token                   string             `mapstructure:"token"`                   // consul ACL token (optional)
	Username                string             `mapstructure:"username"`                // etcd auth username (optional)
	Password                string             `mapstructure:"password"`                // etcd auth password (optional)
	Prefix                  string             `mapstructure:"prefix"`                  // service discovery prefix, default "/mofang/services/"
	TTL                     int                `mapstructure:"ttl"`                     // lease/check TTL in seconds, default 30
	DialTimeout             int                `mapstructure:"dialTimeout"`             // seconds, default 5
	CheckType               string             `mapstructure:"checkType"`               // consul check: "http" (boot default) or "ttl"
	CheckPath               string             `mapstructure:"checkPath"`               // consul readiness check path, default "/ready"
	CheckTLS                bool               `mapstructure:"checkTLS"`                // HTTPS for the service readiness check, independent from registry TLS
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
	return strings.ToLower(strings.TrimSpace(r.CheckType))
}

// ServerConfig describes the HTTP server.
type ServerConfig struct {
	Name              string   `mapstructure:"name"`
	Version           string   `mapstructure:"version"`           // service release used by registry filtering
	Addr              string   `mapstructure:"addr"`              // listen addr, e.g. ":7801"
	AdvertiseAddr     string   `mapstructure:"advertiseAddr"`     // service discovery addr, e.g. "10.0.1.5:7801"
	Mode              string   `mapstructure:"mode"`              // "debug" or "release"
	ReadHeaderTimeout int      `mapstructure:"readHeaderTimeout"` // seconds, default 5
	ReadTimeout       int      `mapstructure:"readTimeout"`       // seconds, default 15
	WriteTimeout      int      `mapstructure:"writeTimeout"`      // seconds, default 30
	IdleTimeout       int      `mapstructure:"idleTimeout"`       // seconds, default 60
	ShutdownTimeout   int      `mapstructure:"shutdownTimeout"`   // seconds, default 10
	MaxHeaderBytes    int      `mapstructure:"maxHeaderBytes"`    // default 1 MiB
	MaxBodyBytes      int      `mapstructure:"maxBodyBytes"`      // default 10 MiB
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
	Enable            bool   `mapstructure:"enable"`
	CAFile            string `mapstructure:"caFile"`
	CertFile          string `mapstructure:"certFile"`
	KeyFile           string `mapstructure:"keyFile"`
	ServerName        string `mapstructure:"serverName"`
	RequireClientCert bool   `mapstructure:"requireClientCert"` // gRPC server mTLS
}

// GRPCConfig describes an optional gRPC listener.
type GRPCConfig struct {
	Enable          bool      `mapstructure:"enable"`
	Addr            string    `mapstructure:"addr"`            // listen addr, e.g. ":7889"
	AdvertiseAddr   string    `mapstructure:"advertiseAddr"`   // service discovery addr, e.g. "10.0.1.5:7889"
	ShutdownTimeout int       `mapstructure:"shutdownTimeout"` // seconds, default 10
	MaxRecvMsgSize  int       `mapstructure:"maxRecvMsgSize"`  // bytes, default 4 MiB
	MaxSendMsgSize  int       `mapstructure:"maxSendMsgSize"`  // bytes, default 4 MiB
	RequestTimeout  int       `mapstructure:"requestTimeout"`  // seconds, zero disables default deadline
	MaxConcurrent   int       `mapstructure:"maxConcurrent"`   // max in-flight RPCs, zero disables limit
	TLS             TLSConfig `mapstructure:"tls"`
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
	if c.Redis.Password != "" || c.JWT.Secret != "" || c.Registry.Token != "" || c.Registry.Password != "" || c.Notify.Token != "" || c.Diagnostics.Pprof.AuthToken != "" {
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
	if username := os.Getenv("REGISTRY_USERNAME"); username != "" {
		cfg.Registry.Username = username
	}
	if password := os.Getenv("REGISTRY_PASSWORD"); password != "" {
		cfg.Registry.Password = password
	}
	if wsURL := os.Getenv("NOTIFY_WS_URL"); wsURL != "" {
		cfg.Notify.WsURL = wsURL
	}
	if token := os.Getenv("NOTIFY_TOKEN"); token != "" {
		cfg.Notify.Token = token
	}
	if token := os.Getenv("PPROF_TOKEN"); token != "" {
		cfg.Diagnostics.Pprof.AuthToken = token
	}
	if token := os.Getenv("METRICS_TOKEN"); token != "" {
		// Metrics handlers are usually registered by the service. Keep the
		// environment available to applications without placing secrets in YAML.
		// It is intentionally not stored in Config to avoid accidental logging.
	}
	if value := strings.TrimSpace(os.Getenv("REQUIRE_TLS")); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Security.RequireTLS = parsed
		}
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
	if len(c.Server.Name) > 128 || strings.Contains(c.Server.Name, "/") || strings.ContainsAny(c.Server.Name, "\r\n\x00") {
		return fmt.Errorf("server.name is invalid")
	}
	if len(c.Server.Version) > 128 || strings.ContainsAny(c.Server.Version, "\r\n\x00") {
		return fmt.Errorf("server.version is invalid")
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
	if c.Registry.TTL < 0 || c.Registry.DialTimeout < 0 || c.Registry.DeregisterCriticalAfter < 0 {
		return fmt.Errorf("registry timeout values cannot be negative")
	}
	if c.Registry.TTL > 24*60*60 || c.Registry.DialTimeout > 5*60 || c.Registry.DeregisterCriticalAfter > 30*24*60*60 {
		return fmt.Errorf("registry timeout values exceed safe bounds")
	}
	if len(c.Registry.Prefix) > 256 || strings.ContainsAny(c.Registry.Prefix, "\r\n\x00") || strings.Contains(c.Registry.Prefix, "..") {
		return fmt.Errorf("registry.prefix is invalid")
	}
	if len(c.Registry.ConfigKey) > 256 || strings.ContainsAny(c.Registry.ConfigKey, "\r\n\x00") || strings.Contains(c.Registry.ConfigKey, "..") {
		return fmt.Errorf("registry.configKey is invalid")
	}
	if c.Registry.ConfigNode != "" && (len(c.Registry.ConfigNode) > 128 || strings.ContainsAny(c.Registry.ConfigNode, "/\r\n\x00")) {
		return fmt.Errorf("registry.configNode is invalid")
	}
	if strings.TrimSpace(c.Notify.WsURL) != "" {
		u, err := url.Parse(strings.TrimSpace(c.Notify.WsURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("notify.wsUrl is invalid")
		}
	}
	if c.Notify.Timeout < 0 || c.Notify.Timeout > 5*60 {
		return fmt.Errorf("notify.timeout must be between 0 and 300 seconds")
	}
	if c.Notify.MaxPayloadBytes < 0 || c.Notify.MaxPayloadBytes > 16<<20 {
		return fmt.Errorf("notify.maxPayloadBytes must be between 0 and 16777216")
	}
	if math.IsNaN(c.Telemetry.SampleRatio) || math.IsInf(c.Telemetry.SampleRatio, 0) || c.Telemetry.SampleRatio < 0 || c.Telemetry.SampleRatio > 1 {
		return fmt.Errorf("telemetry.sampleRatio must be between 0 and 1")
	}
	if c.Diagnostics.Pprof.Enabled && (mode == "release" || mode == "production") {
		p := c.Diagnostics.Pprof
		if p.AuthToken == "" && strings.TrimSpace(p.AllowedCIDRs) == "" {
			addr := strings.TrimSpace(p.Addr)
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			ip := net.ParseIP(host)
			if host == "" || (!strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback())) {
				return fmt.Errorf("diagnostics.pprof requires authToken or allowedCidrs when exposed beyond loopback")
			}
		}
		if strings.TrimSpace(p.AllowedCIDRs) != "" {
			for _, cidr := range strings.Split(p.AllowedCIDRs, ",") {
				if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
					return fmt.Errorf("diagnostics.pprof.allowedCidrs contains invalid CIDR")
				}
			}
		}
	}
	if c.Diagnostics.Pprof.TLS.Enable {
		if !c.Diagnostics.Pprof.Enabled {
			return fmt.Errorf("diagnostics.pprof.tls requires diagnostics.pprof.enabled")
		}
		if strings.TrimSpace(c.Diagnostics.Pprof.Addr) == "" {
			return fmt.Errorf("diagnostics.pprof.tls requires a dedicated addr")
		}
		if c.Diagnostics.Pprof.TLS.CertFile == "" || c.Diagnostics.Pprof.TLS.KeyFile == "" {
			return fmt.Errorf("diagnostics.pprof.tls certFile and keyFile are required")
		}
		if c.Diagnostics.Pprof.TLS.RequireClientCert && c.Diagnostics.Pprof.TLS.CAFile == "" {
			return fmt.Errorf("diagnostics.pprof.tls caFile is required when requireClientCert is enabled")
		}
	}
	if c.Server.MaxBodyBytes < 0 || c.Server.MaxBodyBytes > 256<<20 {
		return fmt.Errorf("server.maxBodyBytes must be between 0 and 268435456")
	}
	if c.Server.MaxHeaderBytes < 0 || c.Server.MaxHeaderBytes > 16<<20 {
		return fmt.Errorf("server.maxHeaderBytes must be between 0 and 16777216")
	}
	if c.Server.ReadHeaderTimeout < 0 || c.Server.ReadTimeout < 0 || c.Server.WriteTimeout < 0 || c.Server.IdleTimeout < 0 || c.Server.ShutdownTimeout < 0 {
		return fmt.Errorf("server timeout values cannot be negative")
	}
	if c.GRPC.MaxRecvMsgSize < 0 || c.GRPC.MaxSendMsgSize < 0 || c.GRPC.MaxRecvMsgSize > 64<<20 || c.GRPC.MaxSendMsgSize > 64<<20 {
		return fmt.Errorf("grpc message sizes must be between 0 and 67108864")
	}
	if c.GRPC.RequestTimeout < 0 || c.GRPC.RequestTimeout > 5*60 {
		return fmt.Errorf("grpc.requestTimeout must be between 0 and 300 seconds")
	}
	if c.GRPC.MaxConcurrent < 0 || c.GRPC.MaxConcurrent > 1_000_000 {
		return fmt.Errorf("grpc.maxConcurrent must be between 0 and 1000000")
	}
	if err := c.validateTLSRequirements(); err != nil {
		return err
	}
	if c.AccessLimit.Total < 0 || c.AccessLimit.Duration < 0 || c.AccessLimit.Total > 10_000_000 || c.AccessLimit.Duration > 24*60*60 {
		return fmt.Errorf("accessLimit values exceed safe bounds")
	}
	for name, db := range c.Database {
		if db.MaxIdle < 0 || db.MaxOpen < 0 || db.MaxLifetime < 0 || db.MaxIdleTime < 0 || db.SlowThreshold < 0 {
			return fmt.Errorf("database.%s pool values cannot be negative", name)
		}
		if db.MaxOpen > 10_000 || db.MaxIdle > 10_000 || db.MaxLifetime > 7*24*60*60 || db.MaxIdleTime > 7*24*60*60 || db.SlowThreshold > 24*60*60*1000 {
			return fmt.Errorf("database.%s pool values exceed safe bounds", name)
		}
	}
	if mode == "release" || mode == "production" {
		if c.CORS.Enable && strings.EqualFold(strings.TrimSpace(c.CORS.Mode), "allow-all") {
			return fmt.Errorf("cors allow-all is not permitted in release mode; use whitelist")
		}
		if strings.TrimSpace(c.JWT.TrustedHeaderCIDRs) == "" && c.JWT.HeaderUID != "" && c.JWT.TrustHeaderUID {
			return fmt.Errorf("jwt.trustedHeaderCidrs is required when header identity trust is enabled in release mode")
		}
		if c.JWT.TrustHeaderUID && c.JWT.TrustedHeaderCIDRs != "" {
			for _, cidr := range strings.Split(c.JWT.TrustedHeaderCIDRs, ",") {
				if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
					return fmt.Errorf("jwt.trustedHeaderCidrs contains invalid CIDR")
				}
			}
		}
		if c.JWT.Secret != "" && len([]byte(c.JWT.Secret)) < 32 {
			return fmt.Errorf("jwt.secret must be at least 32 bytes in release mode")
		}
		if c.JWT.Secret != "" && (c.JWT.Issuer == "" || len(c.JWT.Audience) == 0) {
			return fmt.Errorf("jwt issuer and audience are required in release mode")
		}
	}
	return nil
}

// validateTLSRequirements validates transport security without opening any
// network connections. It is also called before remote configuration is
// fetched so a locally configured requireTLS policy cannot be bypassed by a
// plaintext registry connection during startup.
func (c *Config) validateTLSRequirements() error {
	if c.GRPC.TLS.Enable {
		if c.GRPC.TLS.CertFile == "" || c.GRPC.TLS.KeyFile == "" {
			return fmt.Errorf("grpc.tls certFile and keyFile are required when enabled")
		}
		if c.GRPC.TLS.RequireClientCert && c.GRPC.TLS.CAFile == "" {
			return fmt.Errorf("grpc.tls caFile is required when requireClientCert is enabled")
		}
	}
	if !c.Security.RequireTLS {
		return nil
	}
	if c.Redis.Addr != "" && !c.Redis.TLS.Enable {
		return fmt.Errorf("security.requireTLS requires redis.tls.enable")
	}
	// Registry is also used for remote configuration when configKey is set,
	// even if service registration itself is disabled.
	registryTransport := len(c.Registry.Endpoints) > 0 || strings.TrimSpace(c.Registry.Address) != ""
	if registryTransport && (c.Registry.Enable || strings.TrimSpace(c.Registry.ConfigKey) != "") && !c.Registry.TLS.Enable {
		return fmt.Errorf("security.requireTLS requires registry.tls.enable")
	}
	if c.GRPC.Enable && !c.GRPC.TLS.Enable {
		return fmt.Errorf("security.requireTLS requires grpc.tls.enable")
	}
	if c.Diagnostics.Pprof.Enabled && !c.Diagnostics.Pprof.TLS.Enable {
		return fmt.Errorf("security.requireTLS requires diagnostics.pprof.tls.enable")
	}
	if strings.TrimSpace(c.Notify.WsURL) != "" {
		u, err := url.Parse(strings.TrimSpace(c.Notify.WsURL))
		if err != nil || u.Scheme != "https" {
			return fmt.Errorf("security.requireTLS requires notify.wsUrl to use https")
		}
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
