package boot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyEnvOverridesSecrets(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://all")
	t.Setenv("DATABASE_ANALYTICS_DSN", "postgres://analytics")
	t.Setenv("REDIS_USERNAME", "redis-user")
	t.Setenv("REDIS_PASSWORD", "redis-pass")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("REGISTRY_TOKEN", "registry-token")
	t.Setenv("NOTIFY_WS_URL", "http://notify")
	t.Setenv("NOTIFY_TOKEN", "notify-token")
	t.Setenv("SERVER_ADDR", ":9001")
	t.Setenv("SERVER_ADVERTISE_ADDR", "10.0.1.5:9001")
	t.Setenv("GRPC_ADDR", ":9002")
	t.Setenv("GRPC_ADVERTISE_ADDR", "10.0.1.5:9002")
	t.Setenv("REMOTE_NODE", "node-a")

	cfg := &Config{
		Database: map[string]DatabaseConfig{
			"main":      {DSN: "file-main"},
			"analytics": {DSN: "file-analytics"},
		},
	}

	applyEnvOverrides(cfg)

	if cfg.Database["main"].DSN != "postgres://all" {
		t.Fatalf("main dsn = %q", cfg.Database["main"].DSN)
	}
	if cfg.Database["analytics"].DSN != "postgres://analytics" {
		t.Fatalf("analytics dsn = %q", cfg.Database["analytics"].DSN)
	}
	if cfg.Redis.Username != "redis-user" || cfg.Redis.Password != "redis-pass" {
		t.Fatalf("redis env override failed: %+v", cfg.Redis)
	}
	if cfg.JWT.Secret != "jwt-secret" {
		t.Fatalf("jwt secret = %q", cfg.JWT.Secret)
	}
	if cfg.Registry.Token != "registry-token" {
		t.Fatalf("registry token = %q", cfg.Registry.Token)
	}
	if cfg.Notify.WsURL != "http://notify" {
		t.Fatalf("notify ws url = %q", cfg.Notify.WsURL)
	}
	if cfg.Notify.Token != "notify-token" {
		t.Fatalf("notify token = %q", cfg.Notify.Token)
	}
	if cfg.Server.Addr != ":9001" || cfg.Server.AdvertiseAddr != "10.0.1.5:9001" {
		t.Fatalf("server env override failed: %+v", cfg.Server)
	}
	if cfg.GRPC.Addr != ":9002" || cfg.GRPC.AdvertiseAddr != "10.0.1.5:9002" {
		t.Fatalf("grpc env override failed: %+v", cfg.GRPC)
	}
	if cfg.Registry.ConfigNode != "node-a" {
		t.Fatalf("config node = %q", cfg.Registry.ConfigNode)
	}
}

func TestReleaseConfigRejectsInlineSecrets(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Mode: "release"},
		JWT:    JWTConfig{Secret: "inline"},
	}
	if err := cfg.validateInlineSecrets(); err == nil {
		t.Fatal("expected inline secret to be rejected")
	}
}

func TestLoadBaseConfigPreservesExplicitFalseBooleans(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := "server:\n  name: test\n  addr: ':8080'\ndatabase:\n  main:\n    dsn: postgres://test\n    pingOnOpen: false\n    prepareStmt: false\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadBaseConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	db := cfg.Database["main"]
	if db.PingOnOpen == nil || *db.PingOnOpen || db.PrepareStmt == nil || *db.PrepareStmt {
		t.Fatalf("explicit false values were lost: %+v", db)
	}
}

func TestLoadConfigAppliesEnvToEmbeddedConfig(t *testing.T) {
	t.Setenv("DATABASE_MAIN_DSN", "postgres://from-env")
	t.Setenv("JWT_SECRET", "from-env")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  name: test\n  addr: ':8080'\ndatabase:\n  main:\n    dsn: file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	type serviceConfig struct {
		Config  `mapstructure:",squash"`
		Feature bool `mapstructure:"feature"`
	}
	var cfg serviceConfig
	if err := LoadConfig(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Database["main"].DSN != "postgres://from-env" || cfg.JWT.Secret != "from-env" {
		t.Fatalf("environment overrides not applied: %+v", cfg.Config)
	}
}

func TestEnvName(t *testing.T) {
	if got := envName("analytics-db"); got != "ANALYTICS_DB" {
		t.Fatalf("envName = %q", got)
	}
}
