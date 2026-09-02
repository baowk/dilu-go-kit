package registry

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	consul "github.com/hashicorp/consul/api"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
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
	c3 := Config{Prefix: "/custom"}
	if c3.prefix() != "/custom/" {
		t.Errorf("normalized prefix = %q", c3.prefix())
	}
}

func TestConsulConfigAppliesExplicitToken(t *testing.T) {
	cfg := consulConfig("127.0.0.1:8500", "explicit-token")
	if cfg.Address != "127.0.0.1:8500" || cfg.Token != "explicit-token" {
		t.Fatalf("consul config = address %q token %q", cfg.Address, cfg.Token)
	}
	if cfg.HttpClient == nil {
		t.Fatal("consul HTTP client is nil")
	}
	if cfg.HttpClient.Timeout < 60*time.Second {
		t.Fatalf("consul HTTP timeout = %v, want at least 60s for blocking queries", cfg.HttpClient.Timeout)
	}
}

func TestConsulConfigEnablesTLS(t *testing.T) {
	cfg := consulConfig("127.0.0.1:8500", "", TLSConfig{Enable: true, ServerName: "consul.internal"})
	if cfg.Scheme != "https" || cfg.TLSConfig.Address != "consul.internal" {
		t.Fatalf("TLS config = scheme=%q address=%q", cfg.Scheme, cfg.TLSConfig.Address)
	}
}

func TestConsulProbeConfigSetsTimeoutBeforeClientCreation(t *testing.T) {
	cfg, err := consulProbeConfig("127.0.0.1:8500", "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HttpClient == nil || cfg.HttpClient.Timeout != 2*time.Second {
		t.Fatalf("probe HTTP client = %#v", cfg.HttpClient)
	}
	if _, err := consul.NewClient(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HttpClient.Timeout != 2*time.Second {
		t.Fatalf("NewClient replaced probe timeout: %s", cfg.HttpClient.Timeout)
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

func TestConfig_checkPath(t *testing.T) {
	if got := (&Config{}).checkPath(); got != "/ready" {
		t.Fatalf("default check path = %q", got)
	}
	if got := (&Config{CheckPath: "custom-ready"}).checkPath(); got != "/custom-ready" {
		t.Fatalf("normalized check path = %q", got)
	}
}

func TestConfig_consulCheckType(t *testing.T) {
	if got, err := (&Config{}).consulCheckType(); err != nil || got != "ttl" {
		t.Fatalf("default check type = %q, %v", got, err)
	}
	if got, err := (&Config{CheckType: "HTTP"}).consulCheckType(); err != nil || got != "http" {
		t.Fatalf("HTTP check type = %q, %v", got, err)
	}
	if _, err := (&Config{CheckType: "grpc"}).consulCheckType(); err == nil {
		t.Fatal("expected unsupported check type error")
	}
}

func TestConfig_deregisterCriticalAfterDefault(t *testing.T) {
	if got := (&Config{}).deregisterCriticalAfter(); got != 5*time.Minute {
		t.Fatalf("default deregister critical after = %s", got)
	}
	if got := (&Config{DeregisterCriticalAfter: 600}).deregisterCriticalAfter(); got != 10*time.Minute {
		t.Fatalf("custom deregister critical after = %s", got)
	}
}

func TestConsulCheckURLUsesReadyPath(t *testing.T) {
	if got := consulCheckURL("10.0.1.5:7801", "/ready"); got != "http://10.0.1.5:7801/ready" {
		t.Fatalf("check url = %q", got)
	}
}

func TestConsulServiceCheckDefaultsToTTL(t *testing.T) {
	check, usesTTL, err := consulServiceCheck(Config{}, "check-inst-1", "10.0.1.5:7801")
	if err != nil {
		t.Fatal(err)
	}
	if !usesTTL || check.TTL != "30s" || check.HTTP != "" {
		t.Fatalf("TTL check = %+v, usesTTL=%v", check, usesTTL)
	}
	if check.DeregisterCriticalServiceAfter != "300s" {
		t.Fatalf("deregister delay = %q", check.DeregisterCriticalServiceAfter)
	}
}

func TestConsulServiceCheckSupportsHTTPReadiness(t *testing.T) {
	check, usesTTL, err := consulServiceCheck(Config{CheckType: "http"}, "check-inst-1", "10.0.1.5:7801")
	if err != nil {
		t.Fatal(err)
	}
	if usesTTL || check.TTL != "" || check.HTTP != "http://10.0.1.5:7801/ready" {
		t.Fatalf("HTTP check = %+v, usesTTL=%v", check, usesTTL)
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
		Version:    "v1.2.3",
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

	if got.Name != svc.Name || got.Version != svc.Version || got.InstanceID != svc.InstanceID ||
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

func TestValidateServiceRejectsInvalidPort(t *testing.T) {
	for _, addr := range []string{"10.0.0.1:abc", "10.0.0.1:0", "10.0.0.1:65536"} {
		t.Run(addr, func(t *testing.T) {
			err := validateService(Service{Name: "svc", InstanceID: "inst", Addr: addr})
			if err == nil {
				t.Fatalf("expected invalid address %q to be rejected", addr)
			}
		})
	}
}

func TestValidateServiceRejectsInvalidGRPCAddress(t *testing.T) {
	if err := validateService(Service{Name: "svc", InstanceID: "inst", Addr: "10.0.0.1:8080", GRPCAddr: "10.0.0.1:bad"}); err == nil {
		t.Fatal("expected invalid gRPC address to be rejected")
	}
}

func TestConsulHTTPHealthCheckDoesNotFollowRegistryTLS(t *testing.T) {
	check, usesTTL, err := consulServiceCheck(Config{CheckType: "http", TLS: TLSConfig{Enable: true}}, "check-inst-1", "10.0.1.5:7801")
	if err != nil {
		t.Fatal(err)
	}
	if usesTTL || check.HTTP != "http://10.0.1.5:7801/ready" {
		t.Fatalf("health check unexpectedly followed registry TLS: %+v", check)
	}
	check, _, err = consulServiceCheck(Config{CheckType: "http", CheckTLS: true}, "check-inst-1", "10.0.1.5:7801")
	if err != nil {
		t.Fatal(err)
	}
	if check.HTTP != "https://10.0.1.5:7801/ready" {
		t.Fatalf("explicit check TLS not applied: %q", check.HTTP)
	}
}

func TestServiceFilters(t *testing.T) {
	services := []Service{
		{InstanceID: "v1", Version: "v1", Meta: map[string]string{"region": "cn", "canary": "false"}},
		{InstanceID: "v2", Version: "v2", Meta: map[string]string{"region": "cn", "canary": "true"}},
		{InstanceID: "v2-us", Version: "v2", Meta: map[string]string{"region": "us", "canary": "true"}},
	}
	filter := AndFilters(VersionFilter("v2"), MetadataFilter(map[string]string{"region": "cn", "canary": "true"}))
	filtered := filterServices(services, filter)
	if len(filtered) != 1 || filtered[0].InstanceID != "v2" {
		t.Fatalf("filtered services = %+v", filtered)
	}
	// Legacy registrations used meta.version before Version was first-class.
	if !VersionFilter("v1")(Service{Meta: map[string]string{"version": "v1"}}) {
		t.Fatal("legacy meta version should match")
	}
}

func TestWatchUpstreamsAppliesFilter(t *testing.T) {
	reg := &fakeRegistry{
		discovered: []Service{
			{InstanceID: "v1", Version: "v1", Addr: "10.0.1.1:7801"},
			{InstanceID: "v2", Version: "v2", Addr: "10.0.1.2:7801"},
		},
		events: make(chan Event),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots, err := WatchUpstreamsWithOptions(ctx, reg, "orders", WatchUpstreamsOptions{
		Filter:           VersionFilter("v2"),
		ResyncInterval:   time.Hour,
		StaleGracePeriod: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := receiveSnapshot(t, snapshots)
	if initial.Stale || len(initial.Services) != 1 || initial.Services[0].Version != "v2" {
		t.Fatalf("filtered initial snapshot = %+v", initial)
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

func TestEtcdDeleteEventIncludesInstanceID(t *testing.T) {
	event, ok := etcdEvent("/svc/mf-user/", "mf-user", &clientv3.Event{
		Type: clientv3.EventTypeDelete,
		Kv:   &mvccpb.KeyValue{Key: []byte("/svc/mf-user/inst-1")},
	})
	if !ok || event.Service.InstanceID != "inst-1" {
		t.Fatalf("event = %+v ok=%v", event, ok)
	}
}

func TestEtcdEventRejectsPayloadKeyMismatch(t *testing.T) {
	event, ok := etcdEvent("/svc/mf-user/", "mf-user", &clientv3.Event{
		Type: clientv3.EventTypePut,
		Kv: &mvccpb.KeyValue{
			Key:   []byte("/svc/mf-user/inst-1"),
			Value: []byte(`{"name":"mf-user","instance_id":"inst-2","addr":"10.0.1.5:7801"}`),
		},
	})
	if ok || event.Service.InstanceID != "inst-2" {
		t.Fatalf("mismatched event = %+v ok=%v, want rejected", event, ok)
	}
}

func TestWatchUpstreamsReconcilesAndExpiresLastKnownGood(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan Event, 4)
	reg := &fakeRegistry{
		discovered: []Service{{Name: "mf-user", InstanceID: "inst-1", Addr: "10.0.1.5:7801"}},
		events:     events,
	}
	snapshots, err := WatchUpstreamsWithOptions(ctx, reg, "mf-user", WatchUpstreamsOptions{
		StaleGracePeriod: 30 * time.Millisecond,
		ResyncInterval:   time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	initial := <-snapshots
	if initial.Stale || len(initial.Services) != 1 || initial.Services[0].InstanceID != "inst-1" {
		t.Fatalf("initial snapshot = %+v", initial)
	}

	reg.setDiscovered(nil)
	events <- Event{Type: EventDelete, Service: Service{Name: "mf-user", InstanceID: "inst-1"}}
	stale := receiveSnapshot(t, snapshots)
	if !stale.Stale || stale.StaleSince.IsZero() || len(stale.Services) != 1 || stale.Services[0].InstanceID != "inst-1" {
		t.Fatalf("stale snapshot = %+v", stale)
	}
	expired := receiveSnapshot(t, snapshots)
	if !expired.Stale || len(expired.Services) != 0 || !expired.StaleSince.Equal(stale.StaleSince) {
		t.Fatalf("expired snapshot = %+v", expired)
	}
}

func TestWatchUpstreamsEstablishesWatchBeforeInitialDiscover(t *testing.T) {
	reg := &fakeRegistry{
		discovered: []Service{{Name: "mf-user", InstanceID: "deleted", Addr: "10.0.1.5:7801"}},
		events:     make(chan Event),
	}
	reg.onWatch = func() { reg.setDiscovered(nil) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots, err := WatchUpstreamsWithOptions(ctx, reg, "mf-user", WatchUpstreamsOptions{ResyncInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	initial := receiveSnapshot(t, snapshots)
	if !initial.Stale || len(initial.Services) != 0 {
		t.Fatalf("initial snapshot retained pre-watch service: %+v", initial)
	}
}

func TestWatchUpstreamsPeriodicResyncRepairsMissedEvent(t *testing.T) {
	reg := &fakeRegistry{
		discovered: []Service{{Name: "mf-user", InstanceID: "inst-1", Addr: "10.0.1.5:7801"}},
		events:     make(chan Event),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots, err := WatchUpstreamsWithOptions(ctx, reg, "mf-user", WatchUpstreamsOptions{
		StaleGracePeriod: time.Second,
		ResyncInterval:   20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = receiveSnapshot(t, snapshots)
	reg.setDiscovered(nil)
	stale := receiveSnapshot(t, snapshots)
	if !stale.Stale || len(stale.Services) != 1 {
		t.Fatalf("resynced snapshot = %+v", stale)
	}
}

func TestWatchUpstreamsReconcilesAgainBeforeStaleExpiry(t *testing.T) {
	events := make(chan Event, 1)
	reg := &fakeRegistry{
		discovered: []Service{{Name: "mf-user", InstanceID: "inst-1", Addr: "10.0.1.5:7801"}},
		events:     events,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots, err := WatchUpstreamsWithOptions(ctx, reg, "mf-user", WatchUpstreamsOptions{
		StaleGracePeriod: 30 * time.Millisecond,
		ResyncInterval:   time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = receiveSnapshot(t, snapshots)
	reg.setDiscovered(nil)
	events <- Event{Type: EventDelete, Service: Service{InstanceID: "inst-1"}}
	_ = receiveSnapshot(t, snapshots)

	reg.setDiscovered([]Service{{Name: "mf-user", InstanceID: "inst-2", Addr: "10.0.1.6:7801"}})
	recovered := receiveSnapshot(t, snapshots)
	if recovered.Stale || len(recovered.Services) != 1 || recovered.Services[0].InstanceID != "inst-2" {
		t.Fatalf("expiry reconciliation snapshot = %+v", recovered)
	}
}

type fakeRegistry struct {
	mu         sync.Mutex
	discovered []Service
	events     chan Event
	onWatch    func()
}

func (r *fakeRegistry) Register(context.Context, Service) error { return nil }

func (r *fakeRegistry) Deregister(context.Context, string, string) error { return nil }

func (r *fakeRegistry) Discover(context.Context, string) ([]Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneServices(r.discovered), nil
}

func (r *fakeRegistry) Watch(context.Context, string) (<-chan Event, error) {
	if r.onWatch != nil {
		r.onWatch()
	}
	return r.events, nil
}

func (r *fakeRegistry) Close() error { return nil }

func (r *fakeRegistry) setDiscovered(services []Service) {
	r.mu.Lock()
	r.discovered = cloneServices(services)
	r.mu.Unlock()
}

func receiveSnapshot(t *testing.T, snapshots <-chan UpstreamSnapshot) UpstreamSnapshot {
	t.Helper()
	select {
	case snapshot, ok := <-snapshots:
		if !ok {
			t.Fatal("snapshot channel closed")
		}
		return snapshot
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream snapshot")
		return UpstreamSnapshot{}
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

// --------------- localIP ---------------

func TestLocalIP_returnsValidIP(t *testing.T) {
	ip := localIP()
	if net.ParseIP(ip) == nil {
		t.Errorf("localIP() = %q, not a valid IP", ip)
	}
}

func TestLocalIP_nonEmpty(t *testing.T) {
	ip := localIP()
	if ip == "" {
		t.Error("localIP() should never return empty string")
	}
}
