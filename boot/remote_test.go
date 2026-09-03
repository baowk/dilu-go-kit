package boot

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baowk/dilu-go-kit/registry"
	"github.com/spf13/viper"
)

type testKVStore struct{ value []byte }

func (s testKVStore) Get(context.Context, string) ([]byte, error) {
	return append([]byte(nil), s.value...), nil
}
func (s testKVStore) WatchKey(ctx context.Context, _ string, onChange func([]byte)) error {
	onChange(s.value)
	<-ctx.Done()
	return ctx.Err()
}

// ── key resolution ──

func TestConfigKeyPrefix_default(t *testing.T) {
	r := RegistryConfig{}
	if r.configKeyPrefix() != "/config/" {
		t.Errorf("default = %q", r.configKeyPrefix())
	}
}

func TestConfigKeyPrefix_custom(t *testing.T) {
	r := RegistryConfig{ConfigKey: "/myapp/conf/"}
	if r.configKeyPrefix() != "/myapp/conf/" {
		t.Errorf("custom = %q", r.configKeyPrefix())
	}
}

func TestResolveConfigKey(t *testing.T) {
	r := RegistryConfig{ConfigKey: "/config/"}
	got := r.resolveConfigKey("mf-user")
	if got != "/config/mf-user" {
		t.Errorf("resolveConfigKey = %q", got)
	}
}

func TestResolveConfigKey_defaultPrefix(t *testing.T) {
	r := RegistryConfig{}
	got := r.resolveConfigKey("mf-order")
	if got != "/config/mf-order" {
		t.Errorf("resolveConfigKey = %q", got)
	}
}

func TestResolveConfigNodeKey_noNode(t *testing.T) {
	r := RegistryConfig{ConfigKey: "/config/"}
	os.Unsetenv("REMOTE_NODE")
	got := r.resolveConfigNodeKey("mf-user")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestResolveConfigNodeKey_fromField(t *testing.T) {
	r := RegistryConfig{ConfigKey: "/config/", ConfigNode: "node-1"}
	got := r.resolveConfigNodeKey("mf-user")
	if got != "/config/mf-user/node-1" {
		t.Errorf("resolveConfigNodeKey = %q", got)
	}
}

func TestResolveConfigNodeKey_fromEnv(t *testing.T) {
	r := RegistryConfig{ConfigKey: "/config/"}
	os.Setenv("REMOTE_NODE", "pod-abc")
	defer os.Unsetenv("REMOTE_NODE")
	got := r.resolveConfigNodeKey("mf-user")
	if got != "/config/mf-user/pod-abc" {
		t.Errorf("resolveConfigNodeKey = %q", got)
	}
}

func TestConfigNode_fieldOverridesEnv(t *testing.T) {
	os.Setenv("REMOTE_NODE", "from-env")
	defer os.Unsetenv("REMOTE_NODE")
	r := RegistryConfig{ConfigNode: "from-field"}
	if r.configNode() != "from-field" {
		t.Errorf("configNode = %q, want from-field", r.configNode())
	}
}

// ── configFormat / registryType defaults ──

func TestConfigFormat_default(t *testing.T) {
	r := RegistryConfig{}
	if r.configFormat() != "yaml" {
		t.Errorf("default format = %q", r.configFormat())
	}
}

func TestConfigFormat_json(t *testing.T) {
	r := RegistryConfig{ConfigFormat: "json"}
	if r.configFormat() != "json" {
		t.Errorf("json format = %q", r.configFormat())
	}
}

func TestRegistryType_default(t *testing.T) {
	r := RegistryConfig{}
	if r.registryType() != "etcd" {
		t.Errorf("default type = %q", r.registryType())
	}
}

func TestRegistryType_consul(t *testing.T) {
	r := RegistryConfig{Type: "consul"}
	if r.registryType() != "consul" {
		t.Errorf("consul type = %q", r.registryType())
	}
}

func TestRemoteConfigInjectedKVStore(t *testing.T) {
	store := testKVStore{value: []byte("server:\n  name: injected\n  addr: ':8080'\n")}
	var cfg Config
	reg := RegistryConfig{Type: "custom", ConfigKey: "/config/"}
	if err := LoadRemoteConfigFromStore(store, reg, "svc", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Name != "injected" {
		t.Fatalf("config = %+v", cfg)
	}
	if _, ok := any(store).(registry.KVStore); !ok {
		t.Fatal("test store should satisfy registry.KVStore")
	}
}

// ── unmarshalBytes ──

func TestUnmarshalBytes_yaml(t *testing.T) {
	data := []byte("server:\n  name: test-svc\n  addr: \":8080\"\n  mode: debug\n")
	var cfg Config
	if err := unmarshalBytes(data, "yaml", &cfg); err != nil {
		t.Fatalf("unmarshal yaml: %v", err)
	}
	if cfg.Server.Name != "test-svc" {
		t.Errorf("name = %q", cfg.Server.Name)
	}
	if cfg.Server.Addr != ":8080" {
		t.Errorf("addr = %q", cfg.Server.Addr)
	}
}

func TestUnmarshalBytes_json(t *testing.T) {
	data := []byte(`{"server":{"name":"json-svc","addr":":9090","mode":"release"}}`)
	var cfg Config
	if err := unmarshalBytes(data, "json", &cfg); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if cfg.Server.Name != "json-svc" {
		t.Errorf("name = %q", cfg.Server.Name)
	}
}

func TestUnmarshalBytes_invalidYaml(t *testing.T) {
	data := []byte(":\x00bad")
	var cfg Config
	if err := unmarshalBytes(data, "yaml", &cfg); err == nil {
		t.Error("expected error for invalid yaml")
	}
}

func TestUnmarshalBytesRejectsSensitiveValues(t *testing.T) {
	var cfg Config
	err := unmarshalBytes([]byte("jwt:\n  secret: leaked\n"), "yaml", &cfg)
	if err == nil || !strings.Contains(err.Error(), "jwt.secret") {
		t.Fatalf("expected sensitive remote config error, got %v", err)
	}
}

// ── mergeLayer (deep merge correctness) ──

func TestMergeLayer_overridesOnlyPresent(t *testing.T) {
	// Base: server.name=base, server.addr=:8080
	base := viper.New()
	base.SetConfigType("yaml")
	base.ReadConfig(strings.NewReader("server:\n  name: base\n  addr: \":8080\"\nredis:\n  addr: \"localhost:6379\"\n"))

	// Layer: only override server.name, leave server.addr and redis untouched
	layer := []byte("server:\n  name: override\n")
	if err := mergeLayer(base, layer, "yaml"); err != nil {
		t.Fatalf("mergeLayer: %v", err)
	}

	if base.GetString("server.name") != "override" {
		t.Errorf("server.name = %q, want override", base.GetString("server.name"))
	}
	if base.GetString("server.addr") != ":8080" {
		t.Errorf("server.addr should be preserved, got %q", base.GetString("server.addr"))
	}
	if base.GetString("redis.addr") != "localhost:6379" {
		t.Errorf("redis.addr should be preserved, got %q", base.GetString("redis.addr"))
	}
}

func TestMergeLayer_twoLayers(t *testing.T) {
	// Simulate: local → service → node
	base := viper.New()
	base.SetConfigType("yaml")
	base.ReadConfig(strings.NewReader("server:\n  name: local\n  addr: \":8080\"\n  mode: debug\n"))

	// Service layer: override name
	svc := []byte("server:\n  name: svc-override\n")
	if err := mergeLayer(base, svc, "yaml"); err != nil {
		t.Fatalf("service layer: %v", err)
	}

	// Node layer: override addr
	node := []byte("server:\n  addr: \":9999\"\n")
	if err := mergeLayer(base, node, "yaml"); err != nil {
		t.Fatalf("node layer: %v", err)
	}

	if base.GetString("server.name") != "svc-override" {
		t.Errorf("name = %q", base.GetString("server.name"))
	}
	if base.GetString("server.addr") != ":9999" {
		t.Errorf("addr = %q", base.GetString("server.addr"))
	}
	if base.GetString("server.mode") != "debug" {
		t.Errorf("mode should be preserved, got %q", base.GetString("server.mode"))
	}
}

func TestMergeLayerRejectsSensitiveValues(t *testing.T) {
	base := viper.New()
	base.SetConfigType("yaml")
	base.ReadConfig(strings.NewReader("server:\n  name: base\n"))
	err := mergeLayer(base, []byte("jwt:\n  secret: leaked\n"), "yaml")
	if err == nil || !strings.Contains(err.Error(), "jwt.secret") {
		t.Fatalf("expected sensitive remote config error, got %v", err)
	}
}

func TestRemoteSensitivePathCoversInfrastructureCredentials(t *testing.T) {
	for _, input := range []string{
		"registry:\n  password: leaked\n",
		"diagnostics:\n  pprof:\n    authToken: leaked\n",
	} {
		var cfg Config
		if err := unmarshalBytes([]byte(input), "yaml", &cfg); err == nil {
			t.Fatalf("expected sensitive remote config rejection for %q", input)
		}
	}
}

func TestMergeConfigLayersRebuildsFromLocalBase(t *testing.T) {
	base := &Config{
		Server: ServerConfig{Name: "local", Addr: ":8080", Mode: "debug"},
		Redis:  RedisConfig{Addr: "local-redis:6379"},
	}

	first := cloneConfig(base)
	if err := mergeConfigLayers(first, "yaml",
		[]byte("server:\n  name: remote\nredis:\n  addr: remote-redis:6379\n"),
		[]byte("server:\n  addr: \":9090\"\n"),
	); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if first.Redis.Addr != "remote-redis:6379" {
		t.Fatalf("first merge redis = %q", first.Redis.Addr)
	}

	second := cloneConfig(base)
	if err := mergeConfigLayers(second, "yaml",
		[]byte("server:\n  name: remote2\n"),
		[]byte("server:\n  addr: \":9091\"\n"),
	); err != nil {
		t.Fatalf("second merge: %v", err)
	}

	if second.Server.Name != "remote2" {
		t.Fatalf("server.name = %q", second.Server.Name)
	}
	if second.Server.Addr != ":9091" {
		t.Fatalf("server.addr = %q", second.Server.Addr)
	}
	if second.Redis.Addr != "local-redis:6379" {
		t.Fatalf("redis addr should fall back to local base, got %q", second.Redis.Addr)
	}
}

func TestMergeConfigLayersLocalOnlyFallback(t *testing.T) {
	base := &Config{
		Server: ServerConfig{Name: "local", Addr: ":8080"},
		Redis:  RedisConfig{Addr: "local-redis:6379"},
	}
	if err := mergeConfigLayers(base, "yaml", nil, nil); err != nil {
		t.Fatalf("merge local only: %v", err)
	}
	if base.Server.Name != "local" || base.Redis.Addr != "local-redis:6379" {
		t.Fatalf("base changed unexpectedly: %+v", base)
	}
}

func TestRegistryConfigRemoteDialTimeoutIsBounded(t *testing.T) {
	if got := (RegistryConfig{}).dialTimeout(); got != 5*time.Second {
		t.Fatalf("default remote dial timeout = %s", got)
	}
	if got := (RegistryConfig{DialTimeout: 10}).dialTimeout(); got != 10*time.Second {
		t.Fatalf("configured remote dial timeout = %s", got)
	}
	if got := (RegistryConfig{DialTimeout: 9999}).dialTimeout(); got != 5*time.Minute {
		t.Fatalf("bounded remote dial timeout = %s", got)
	}
}
