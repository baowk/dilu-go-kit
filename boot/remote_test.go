package boot

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

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

// ── fetchRemoteByKey error paths ──

func TestFetchRemoteByKey_unsupportedType(t *testing.T) {
	reg := RegistryConfig{Type: "zookeeper"}
	_, err := fetchRemoteByKey(reg, "/any")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFetchRemoteByKey_etcdNoEndpoints(t *testing.T) {
	reg := RegistryConfig{Type: "etcd"}
	_, err := fetchRemoteByKey(reg, "/any")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no etcd endpoints") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFetchRemoteByKey_consulNoAddress(t *testing.T) {
	reg := RegistryConfig{Type: "consul"}
	_, err := fetchRemoteByKey(reg, "/any")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no consul address") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── WatchRemoteConfig error path ──

func TestWatchRemoteConfig_unsupportedType(t *testing.T) {
	reg := RegistryConfig{Type: "zookeeper", ConfigKey: "/config/"}
	err := WatchRemoteConfig(nil, reg, "svc", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── consul client fallback to Endpoints[0] ──

func TestRemoteConsulClient_fallbackToEndpoints(t *testing.T) {
	reg := RegistryConfig{Type: "consul", Endpoints: []string{"127.0.0.1:8500"}}
	cli, err := remoteConsulClient(reg)
	if err != nil {
		t.Fatalf("consul client: %v", err)
	}
	if cli == nil {
		t.Fatal("expected non-nil client")
	}
}
