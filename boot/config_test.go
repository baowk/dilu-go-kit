package boot

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

func TestValidateRejectsUnknownServerMode(t *testing.T) {
	for _, mode := range []string{"prod", "release "} {
		cfg := &Config{Server: ServerConfig{Name: "test", Addr: ":8080", Mode: mode}}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected server mode %q to be rejected", mode)
		}
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

func TestRegistryConfigDefaultsBootConsulCheckToHTTP(t *testing.T) {
	if got := (&RegistryConfig{}).consulCheckType(); got != "http" {
		t.Fatalf("default boot consul check type = %q", got)
	}
	if got := (&RegistryConfig{CheckType: "ttl"}).consulCheckType(); got != "ttl" {
		t.Fatalf("explicit boot consul check type = %q", got)
	}
}

func TestCloneConfigDeepCopiesMutableFields(t *testing.T) {
	trueValue := true
	falseValue := false
	original := &Config{
		Server:   ServerConfig{TrustedProxies: []string{"10.0.0.0/8"}},
		JWT:      JWTConfig{Audience: []string{"api"}},
		CORS:     CORSConfig{Whitelist: []string{"https://example.com"}},
		Registry: RegistryConfig{Endpoints: []string{"127.0.0.1:2379"}},
		Database: map[string]DatabaseConfig{
			"main": {PingOnOpen: &trueValue, PrepareStmt: &falseValue},
		},
	}

	cloned := cloneConfig(original)
	if !reflect.DeepEqual(original, cloned) {
		t.Fatalf("clone value differs\noriginal: %#v\nclone: %#v", original, cloned)
	}
	assertMutableValuesIndependent(t, reflect.ValueOf(original).Elem(), reflect.ValueOf(cloned).Elem(), "Config")
}

func TestCloneConfigMutableFieldGuard(t *testing.T) {
	want := []string{
		"Config.CORS.Whitelist",
		"Config.Database",
		"Config.Database{}.PingOnOpen",
		"Config.Database{}.PrepareStmt",
		"Config.JWT.Audience",
		"Config.Registry.Endpoints",
		"Config.Server.TrustedProxies",
	}
	var got []string
	collectMutableFieldPaths(reflect.TypeOf(Config{}), "Config", make(map[reflect.Type]bool), &got)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("mutable Config fields changed; update cloneConfig and its fixture\nwant: %v\ngot:  %v", want, got)
	}
}

func TestCloneConfigNil(t *testing.T) {
	if cloneConfig(nil) != nil {
		t.Fatal("cloneConfig(nil) must return nil")
	}
}

func collectMutableFieldPaths(typ reflect.Type, path string, visiting map[reflect.Type]bool, paths *[]string) {
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		*paths = append(*paths, path)
		nextPath := path
		if typ.Kind() == reflect.Map {
			nextPath += "{}"
		} else if typ.Kind() == reflect.Slice {
			nextPath += "[]"
		}
		collectMutableFieldPaths(typ.Elem(), nextPath, visiting, paths)
	case reflect.Struct:
		if visiting[typ] {
			return
		}
		visiting[typ] = true
		defer delete(visiting, typ)
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath == "" {
				collectMutableFieldPaths(field.Type, path+"."+field.Name, visiting, paths)
			}
		}
	}
}

func assertMutableValuesIndependent(t *testing.T, original, cloned reflect.Value, path string) {
	t.Helper()
	switch original.Kind() {
	case reflect.Struct:
		for i := 0; i < original.NumField(); i++ {
			field := original.Type().Field(i)
			if field.PkgPath == "" && containsMutableField(field.Type) {
				assertMutableValuesIndependent(t, original.Field(i), cloned.Field(i), path+"."+field.Name)
			}
		}
	case reflect.Pointer:
		assertNonNilAndDistinct(t, original, cloned, path)
		if containsMutableField(original.Type().Elem()) {
			assertMutableValuesIndependent(t, original.Elem(), cloned.Elem(), path)
		}
	case reflect.Slice:
		assertNonNilAndDistinct(t, original, cloned, path)
		if original.Len() == 0 {
			t.Fatalf("%s fixture must be non-empty", path)
		}
		for i := 0; i < original.Len() && containsMutableField(original.Type().Elem()); i++ {
			assertMutableValuesIndependent(t, original.Index(i), cloned.Index(i), path+"[]")
		}
	case reflect.Map:
		assertNonNilAndDistinct(t, original, cloned, path)
		if original.Len() == 0 {
			t.Fatalf("%s fixture must be non-empty", path)
		}
		if containsMutableField(original.Type().Elem()) {
			iter := original.MapRange()
			for iter.Next() {
				cloneValue := cloned.MapIndex(iter.Key())
				if !cloneValue.IsValid() {
					t.Fatalf("%s clone is missing map key %v", path, iter.Key())
				}
				assertMutableValuesIndependent(t, iter.Value(), cloneValue, path+"{}")
			}
		}
	}
}

func assertNonNilAndDistinct(t *testing.T, original, cloned reflect.Value, path string) {
	t.Helper()
	if original.IsNil() {
		t.Fatalf("%s fixture must be non-nil", path)
	}
	if cloned.IsNil() {
		t.Fatalf("%s was not cloned", path)
	}
	if original.Pointer() == cloned.Pointer() {
		t.Fatalf("%s shares mutable storage with its clone", path)
	}
}

func containsMutableField(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return true
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath == "" && containsMutableField(field.Type) {
				return true
			}
		}
	}
	return false
}
