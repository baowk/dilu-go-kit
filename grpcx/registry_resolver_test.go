package grpcx

import (
	"context"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	kitregistry "github.com/baowk/dilu-go-kit/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

type resolverRegistry struct {
	mu       sync.Mutex
	services []kitregistry.Service
	events   chan kitregistry.Event
}

type blockingResolverRegistry struct{}

func (blockingResolverRegistry) Register(context.Context, kitregistry.Service) error { return nil }
func (blockingResolverRegistry) Deregister(context.Context, string, string) error    { return nil }
func (blockingResolverRegistry) Discover(ctx context.Context, _ string) ([]kitregistry.Service, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockingResolverRegistry) Watch(ctx context.Context, _ string) (<-chan kitregistry.Event, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockingResolverRegistry) Close() error { return nil }

func (r *resolverRegistry) Register(context.Context, kitregistry.Service) error { return nil }
func (r *resolverRegistry) Deregister(context.Context, string, string) error    { return nil }
func (r *resolverRegistry) Discover(context.Context, string) ([]kitregistry.Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]kitregistry.Service(nil), r.services...), nil
}
func (r *resolverRegistry) Watch(context.Context, string) (<-chan kitregistry.Event, error) {
	return r.events, nil
}
func (r *resolverRegistry) Close() error { return nil }

type testResolverClientConn struct {
	mu     sync.Mutex
	states []resolver.State
	errors []error
}

func (c *testResolverClientConn) UpdateState(state resolver.State) error {
	c.mu.Lock()
	c.states = append(c.states, state)
	c.mu.Unlock()
	return nil
}
func (c *testResolverClientConn) ReportError(err error) {
	c.mu.Lock()
	c.errors = append(c.errors, err)
	c.mu.Unlock()
}
func (c *testResolverClientConn) NewAddress([]resolver.Address) {}
func (c *testResolverClientConn) ParseServiceConfig(string) *serviceconfig.ParseResult {
	return &serviceconfig.ParseResult{}
}

func TestRegistryResolverBuilderPublishesGRPCAddresses(t *testing.T) {
	reg := &resolverRegistry{
		services: []kitregistry.Service{{Name: "orders", Addr: "10.0.0.1:8080", GRPCAddr: "10.0.0.1:9090"}},
		events:   make(chan kitregistry.Event),
	}
	cc := &testResolverClientConn{}
	builder := &RegistryResolverBuilder{Registry: reg}
	target := resolver.Target{URL: url.URL{Scheme: "registry", Path: "/orders"}}
	r, err := builder.Build(target, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if builder.Scheme() != "registry" {
		t.Fatalf("scheme = %q", builder.Scheme())
	}
	cc.mu.Lock()
	if len(cc.states) != 1 || len(cc.states[0].Addresses) != 1 || cc.states[0].Addresses[0].Addr != "10.0.0.1:9090" {
		t.Fatalf("initial states = %+v", cc.states)
	}
	cc.mu.Unlock()

	reg.mu.Lock()
	reg.services = []kitregistry.Service{{Name: "orders", Addr: "10.0.0.2:8080"}}
	reg.mu.Unlock()
	reg.events <- kitregistry.Event{Type: kitregistry.EventPut}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cc.mu.Lock()
		updated := len(cc.states) >= 2 && len(cc.states[1].Addresses) == 1 && cc.states[1].Addresses[0].Addr == "10.0.0.2:8080"
		cc.mu.Unlock()
		if updated {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cc.mu.Lock()
	updated := len(cc.states) >= 2 && cc.states[1].Addresses[0].Addr == "10.0.0.2:8080"
	cc.mu.Unlock()
	if !updated {
		t.Fatal("resolver did not publish update")
	}
	r.Close()
}

func TestRegistryResolverBuilderRejectsInvalidTarget(t *testing.T) {
	builder := &RegistryResolverBuilder{Registry: &resolverRegistry{events: make(chan kitregistry.Event)}}
	cc := &testResolverClientConn{}
	_, err := builder.Build(resolver.Target{URL: url.URL{Scheme: "registry", Path: "/bad/service"}}, cc, resolver.BuildOptions{})
	if err == nil {
		t.Fatal("expected invalid service target")
	}
	_, err = (&RegistryResolverBuilder{}).Build(resolver.Target{URL: url.URL{Scheme: "registry", Path: "/orders"}}, cc, resolver.BuildOptions{})
	if err == nil {
		t.Fatal("expected nil registry error")
	}
}

func TestRegistryResolverBuilderInitialDiscoveryTimeout(t *testing.T) {
	builder := &RegistryResolverBuilder{Registry: blockingResolverRegistry{}, InitialDiscoveryTimeout: 20 * time.Millisecond}
	cc := &testResolverClientConn{}
	started := time.Now()
	_, err := builder.Build(resolver.Target{URL: url.URL{Scheme: "registry", Path: "/orders"}}, cc, resolver.BuildOptions{})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("Build err=%v duration=%s", err, time.Since(started))
	}
}

func TestRegistryResolverBuilderFiltersVersionAndMetadata(t *testing.T) {
	reg := &resolverRegistry{
		services: []kitregistry.Service{
			{Name: "orders", InstanceID: "v1", Version: "v1", Addr: "10.0.0.1:8080", GRPCAddr: "10.0.0.1:9090", Meta: map[string]string{"region": "cn"}},
			{Name: "orders", InstanceID: "v2", Version: "v2", Addr: "10.0.0.2:8080", GRPCAddr: "10.0.0.2:9090", Meta: map[string]string{"region": "cn"}},
		},
		events: make(chan kitregistry.Event),
	}
	cc := &testResolverClientConn{}
	builder := &RegistryResolverBuilder{Registry: reg, Version: "v2", Metadata: map[string]string{"region": "cn"}}
	r, err := builder.Build(resolver.Target{URL: url.URL{Scheme: "registry", Path: "/orders"}}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if len(cc.states) != 1 || len(cc.states[0].Addresses) != 1 || cc.states[0].Addresses[0].Addr != "10.0.0.2:9090" {
		t.Fatalf("filtered resolver state = %+v", cc.states)
	}
}

func TestWithRoundRobinPreservesConfig(t *testing.T) {
	got := withRoundRobin(`{"methodConfig":[]}`)
	if !containsRoundRobin(got) {
		t.Fatalf("config = %s", got)
	}
}

func TestDialServiceUsesRegistryResolver(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	go server.Serve(listener)
	defer server.Stop()
	reg := &resolverRegistry{
		services: []kitregistry.Service{{Name: "orders", GRPCAddr: listener.Addr().String()}},
		events:   make(chan kitregistry.Event),
	}
	conn, err := DialService(context.Background(), reg, "orders", DialOption{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
}

func containsRoundRobin(value string) bool {
	return len(value) > 0 && strings.Contains(value, "round_robin")
}
