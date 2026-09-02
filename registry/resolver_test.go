package registry

import (
	"context"
	"testing"
	"time"
)

type resolverRegistry struct {
	services []Service
	events   chan Event
}

func (r *resolverRegistry) Register(context.Context, Service) error          { return nil }
func (r *resolverRegistry) Deregister(context.Context, string, string) error { return nil }
func (r *resolverRegistry) Discover(context.Context, string) ([]Service, error) {
	return append([]Service(nil), r.services...), nil
}
func (r *resolverRegistry) Watch(ctx context.Context, service string) (<-chan Event, error) {
	go func() { <-ctx.Done(); close(r.events) }()
	return r.events, nil
}
func (r *resolverRegistry) Close() error { return nil }

func TestResolverRoundRobinAndSnapshot(t *testing.T) {
	rr := &resolverRegistry{services: []Service{{InstanceID: "a", Addr: "127.0.0.1:1"}, {InstanceID: "b", Addr: "127.0.0.1:2"}}, events: make(chan Event, 1)}
	r, err := NewResolver(context.Background(), ResolverConfig{Registry: rr, Service: "orders", ResyncInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	deadline := time.Now().Add(time.Second)
	for len(r.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	a, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if a.InstanceID != "a" || b.InstanceID != "b" {
		t.Fatalf("round robin = %q, %q", a.InstanceID, b.InstanceID)
	}
	if len(r.Snapshot()) != 2 {
		t.Fatalf("snapshot = %#v", r.Snapshot())
	}
}

func TestResolverFiltersVersionAndMetadata(t *testing.T) {
	rr := &resolverRegistry{
		services: []Service{
			{InstanceID: "v1", Version: "v1", Addr: "127.0.0.1:1", Meta: map[string]string{"region": "cn"}},
			{InstanceID: "v2-cn", Version: "v2", Addr: "127.0.0.1:2", Meta: map[string]string{"region": "cn"}},
			{InstanceID: "v2-us", Version: "v2", Addr: "127.0.0.1:3", Meta: map[string]string{"region": "us"}},
		},
		events: make(chan Event, 1),
	}
	r, err := NewResolver(context.Background(), ResolverConfig{
		Registry:       rr,
		Service:        "orders",
		Version:        "v2",
		Metadata:       map[string]string{"region": "cn"},
		ResyncInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	deadline := time.Now().Add(time.Second)
	for len(r.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	got, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != "v2-cn" {
		t.Fatalf("resolved instance = %+v", got)
	}
}
