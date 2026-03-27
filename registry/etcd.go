package registry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// etcdRegistry implements Registry using etcd v3.
type etcdRegistry struct {
	client *clientv3.Client
	cfg    Config
	leases map[string]clientv3.LeaseID // instanceID → leaseID
}

// NewEtcd creates a new etcd-backed registry.
func NewEtcd(cfg Config) (Registry, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("registry: no etcd endpoints configured")
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.dialTimeout(),
	})
	if err != nil {
		return nil, fmt.Errorf("registry: etcd connect: %w", err)
	}

	// Verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), cfg.dialTimeout())
	defer cancel()
	if _, err := client.Status(ctx, cfg.Endpoints[0]); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("registry: etcd status: %w", err)
	}

	return &etcdRegistry{
		client: client,
		cfg:    cfg,
		leases: make(map[string]clientv3.LeaseID),
	}, nil
}

func (r *etcdRegistry) Register(ctx context.Context, svc Service) error {
	if svc.InstanceID == "" {
		svc.InstanceID = GenerateInstanceID(svc.Name)
	}
	svc.RegisterAt = time.Now()

	// Create lease
	ttl := r.cfg.ttl()
	lease, err := r.client.Grant(ctx, ttl)
	if err != nil {
		return fmt.Errorf("registry: grant lease: %w", err)
	}

	// Put key with lease
	key := serviceKey(r.cfg.prefix(), svc.Name, svc.InstanceID)
	val, err := marshalService(svc)
	if err != nil {
		return fmt.Errorf("registry: marshal: %w", err)
	}

	_, err = r.client.Put(ctx, key, val, clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("registry: put: %w", err)
	}

	r.leases[svc.InstanceID] = lease.ID

	// Keep alive in background
	ch, err := r.client.KeepAlive(context.Background(), lease.ID)
	if err != nil {
		return fmt.Errorf("registry: keepalive: %w", err)
	}

	go func() {
		for ka := range ch {
			if ka == nil {
				slog.Warn("registry: keepalive channel closed", "service", svc.Name, "instance", svc.InstanceID)
				return
			}
		}
	}()

	slog.Info("registry: registered",
		"service", svc.Name,
		"instance", svc.InstanceID,
		"addr", svc.Addr,
		"grpc", svc.GRPCAddr,
		"ttl", ttl,
	)
	return nil
}

func (r *etcdRegistry) Deregister(ctx context.Context, name, instanceID string) error {
	key := serviceKey(r.cfg.prefix(), name, instanceID)
	_, err := r.client.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("registry: delete: %w", err)
	}

	// Revoke lease
	if leaseID, ok := r.leases[instanceID]; ok {
		_, _ = r.client.Revoke(ctx, leaseID)
		delete(r.leases, instanceID)
	}

	slog.Info("registry: deregistered", "service", name, "instance", instanceID)
	return nil
}

func (r *etcdRegistry) Discover(ctx context.Context, name string) ([]Service, error) {
	prefix := servicePrefixKey(r.cfg.prefix(), name)
	resp, err := r.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("registry: get: %w", err)
	}

	services := make([]Service, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		svc, err := unmarshalService(kv.Value)
		if err != nil {
			slog.Warn("registry: invalid service data", "key", string(kv.Key), "error", err)
			continue
		}
		services = append(services, svc)
	}
	return services, nil
}

func (r *etcdRegistry) Watch(ctx context.Context, name string) (<-chan Event, error) {
	prefix := servicePrefixKey(r.cfg.prefix(), name)
	ch := make(chan Event, 32)

	// First, send current state
	current, err := r.Discover(ctx, name)
	if err != nil {
		return nil, err
	}
	go func() {
		for _, svc := range current {
			ch <- Event{Type: EventPut, Service: svc}
		}

		// Then watch for changes
		wch := r.client.Watch(ctx, prefix, clientv3.WithPrefix())
		for wresp := range wch {
			for _, ev := range wresp.Events {
				var event Event
				switch ev.Type {
				case clientv3.EventTypePut:
					svc, err := unmarshalService(ev.Kv.Value)
					if err != nil {
						continue
					}
					event = Event{Type: EventPut, Service: svc}
				case clientv3.EventTypeDelete:
					// On delete we only have the key, try to extract name
					event = Event{Type: EventDelete, Service: Service{
						Name: name,
					}}
				}
				select {
				case ch <- event:
				case <-ctx.Done():
					close(ch)
					return
				}
			}
		}
		close(ch)
	}()

	return ch, nil
}

func (r *etcdRegistry) Close() error {
	// Revoke all leases
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for id, leaseID := range r.leases {
		_, _ = r.client.Revoke(ctx, leaseID)
		delete(r.leases, id)
	}
	return r.client.Close()
}
