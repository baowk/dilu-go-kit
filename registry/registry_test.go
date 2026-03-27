package registry

import (
	"strings"
	"testing"
	"time"
)

// --------------- Config defaults ---------------

func TestConfig_registryType(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"empty defaults to etcd", Config{}, "etcd"},
		{"explicit etcd", Config{Type: "etcd"}, "etcd"},
		{"explicit consul", Config{Type: "consul"}, "consul"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.registryType(); got != tt.want {
				t.Errorf("registryType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfig_prefix(t *testing.T) {
	c1 := Config{}
	if c1.prefix() != "/mofang/services/" {
		t.Errorf("default prefix = %q", c1.prefix())
	}
	c2 := Config{Prefix: "/custom/"}
	if c2.prefix() != "/custom/" {
		t.Errorf("custom prefix = %q", c2.prefix())
	}
}

func TestConfig_ttl(t *testing.T) {
	c1 := Config{}
	if c1.ttl() != 30 {
		t.Errorf("default ttl = %d", c1.ttl())
	}
	c2 := Config{TTL: 60}
	if c2.ttl() != 60 {
		t.Errorf("custom ttl = %d", c2.ttl())
	}
}

func TestConfig_dialTimeout(t *testing.T) {
	c1 := Config{}
	if c1.dialTimeout() != 5*time.Second {
		t.Errorf("default dialTimeout = %v", c1.dialTimeout())
	}
	c2 := Config{DialTimeout: 10}
	if c2.dialTimeout() != 10*time.Second {
		t.Errorf("custom dialTimeout = %v", c2.dialTimeout())
	}
}

// --------------- Key helpers ---------------

func TestServiceKey(t *testing.T) {
	key := serviceKey("/svc/", "mf-user", "inst-1")
	if key != "/svc/mf-user/inst-1" {
		t.Errorf("serviceKey = %q", key)
	}
}

func TestServicePrefixKey(t *testing.T) {
	key := servicePrefixKey("/svc/", "mf-user")
	if key != "/svc/mf-user/" {
		t.Errorf("servicePrefixKey = %q", key)
	}
}

// --------------- Marshal / Unmarshal ---------------

func TestMarshalUnmarshalService(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc := Service{
		Name:       "mf-user",
		InstanceID: "inst-1",
		Addr:       "10.0.1.5:7801",
		GRPCAddr:   "10.0.1.5:7889",
		Meta:       map[string]string{"version": "v1"},
		RegisterAt: now,
	}

	data, err := marshalService(svc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := unmarshalService([]byte(data))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != svc.Name || got.InstanceID != svc.InstanceID ||
		got.Addr != svc.Addr || got.GRPCAddr != svc.GRPCAddr {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
	if got.Meta["version"] != "v1" {
		t.Errorf("meta mismatch: %v", got.Meta)
	}
}

func TestUnmarshalService_invalid(t *testing.T) {
	_, err := unmarshalService([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --------------- GenerateInstanceID ---------------

func TestGenerateInstanceID(t *testing.T) {
	id := GenerateInstanceID("mf-user")
	if !strings.HasPrefix(id, "mf-user-") {
		t.Errorf("id should start with service name: %q", id)
	}
	// sleep to ensure timestamp differs (uses UnixMilli%100000)
	time.Sleep(2 * time.Millisecond)
	id2 := GenerateInstanceID("mf-user")
	if id == id2 {
		t.Errorf("two generated IDs should differ: %q == %q", id, id2)
	}
}

// --------------- New factory ---------------

func TestNew_unsupportedType(t *testing.T) {
	_, err := New(Config{Type: "zookeeper"})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNew_etcdNoEndpoints(t *testing.T) {
	_, err := New(Config{Type: "etcd"})
	if err == nil {
		t.Fatal("expected error for empty etcd endpoints")
	}
}

func TestNew_consulNoAddress(t *testing.T) {
	_, err := New(Config{Type: "consul"})
	if err == nil {
		t.Fatal("expected error for empty consul address")
	}
}

func TestNew_defaultTypeIsEtcd(t *testing.T) {
	_, err := New(Config{}) // no type, no endpoints → etcd path → should fail on no endpoints
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "etcd") {
		t.Errorf("default type should route to etcd, got error: %v", err)
	}
}
