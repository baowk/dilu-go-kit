package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// etcdRegistry implements Registry using etcd v3.
type etcdRegistry struct {
	client  *clientv3.Client
	cfg     Config
	mu      sync.Mutex
	leases  map[string]clientv3.LeaseID   // instanceID → leaseID
	cancels map[string]context.CancelFunc // instanceID → keepalive cancel
}

// NewEtcd creates a new etcd-backed registry.
func NewEtcd(cfg Config) (Registry, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("registry: no etcd endpoints configured")
	}

	tlsConfig, err := cfg.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("registry: etcd TLS: %w", err)
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.dialTimeout(),
		TLS:         tlsConfig,
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
		client:  client,
		cfg:     cfg,
		leases:  make(map[string]clientv3.LeaseID),
		cancels: make(map[string]context.CancelFunc),
	}, nil
}

func (r *etcdRegistry) Register(ctx context.Context, svc Service) error {
	if svc.InstanceID == "" {
		svc.InstanceID = GenerateInstanceID(svc.Name)
	}
	if err := validateService(svc); err != nil {
		return err
	}
	svc.RegisterAt = time.Now()
	leaseID, err := r.grantAndPut(ctx, svc)
	if err != nil {
		return err
	}

	kaCtx, kaCancel := context.WithCancel(context.Background())

	r.mu.Lock()
	oldCancel := r.cancels[svc.InstanceID]
	oldLease := r.leases[svc.InstanceID]
	r.leases[svc.InstanceID] = leaseID
	r.cancels[svc.InstanceID] = kaCancel
	r.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
	if oldLease != 0 {
		revokeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = r.client.Revoke(revokeCtx, oldLease)
		cancel()
	}

	go r.keepAliveLoop(kaCtx, svc, leaseID)

	slog.Info("registry: registered",
		"service", svc.Name,
		"instance", svc.InstanceID,
		"addr", svc.Addr,
		"grpc", svc.GRPCAddr,
		"ttl", r.cfg.ttl(),
	)
	return nil
}

func (r *etcdRegistry) grantAndPut(ctx context.Context, svc Service) (clientv3.LeaseID, error) {
	lease, err := r.client.Grant(ctx, r.cfg.ttl())
	if err != nil {
		return 0, fmt.Errorf("registry: grant lease: %w", err)
	}
	val, err := marshalService(svc)
	if err != nil {
		_, _ = r.client.Revoke(ctx, lease.ID)
		return 0, fmt.Errorf("registry: marshal: %w", err)
	}
	key := serviceKey(r.cfg.prefix(), svc.Name, svc.InstanceID)
	if _, err := r.client.Put(ctx, key, val, clientv3.WithLease(lease.ID)); err != nil {
		_, _ = r.client.Revoke(ctx, lease.ID)
		return 0, fmt.Errorf("registry: put: %w", err)
	}
	return lease.ID, nil
}

func (r *etcdRegistry) keepAliveLoop(ctx context.Context, svc Service, leaseID clientv3.LeaseID) {
	for ctx.Err() == nil {
		ch, err := r.client.KeepAlive(ctx, leaseID)
		if err == nil {
			for ka := range ch {
				if ka == nil {
					break
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		slog.Warn("registry: etcd keepalive interrupted; re-registering", "service", svc.Name, "instance", svc.InstanceID, "error", err)
		for ctx.Err() == nil {
			registerCtx, cancel := context.WithTimeout(ctx, r.cfg.dialTimeout())
			newLease, registerErr := r.grantAndPut(registerCtx, svc)
			cancel()
			if registerErr == nil {
				r.mu.Lock()
				currentLease := r.leases[svc.InstanceID]
				if currentLease == leaseID {
					r.leases[svc.InstanceID] = newLease
				}
				r.mu.Unlock()
				if currentLease != leaseID {
					revokeCtx, revokeCancel := context.WithTimeout(context.Background(), 3*time.Second)
					_, _ = r.client.Revoke(revokeCtx, newLease)
					revokeCancel()
					return
				}
				leaseID = newLease
				break
			}
			slog.Warn("registry: etcd re-register failed", "service", svc.Name, "error", registerErr)
			if !waitForContext(ctx, time.Second) {
				return
			}
		}
	}
}

func (r *etcdRegistry) Deregister(ctx context.Context, name, instanceID string) error {
	r.mu.Lock()
	var leaseID clientv3.LeaseID
	if cancel, ok := r.cancels[instanceID]; ok {
		cancel()
		delete(r.cancels, instanceID)
	}
	// Revoke lease
	if storedLease, ok := r.leases[instanceID]; ok {
		leaseID = storedLease
		delete(r.leases, instanceID)
	}
	r.mu.Unlock()

	key := serviceKey(r.cfg.prefix(), name, instanceID)
	_, deleteErr := r.client.Delete(ctx, key)
	var revokeErr error
	if leaseID != 0 {
		if _, err := r.client.Revoke(ctx, leaseID); err != nil {
			revokeErr = fmt.Errorf("registry: revoke lease: %w", err)
		}
	}
	if deleteErr != nil || revokeErr != nil {
		return errors.Join(wrapError("registry: delete", deleteErr), revokeErr)
	}

	slog.Info("registry: deregistered", "service", name, "instance", instanceID)
	return nil
}

func wrapError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
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
	snapshot, err := r.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("registry: etcd watch snapshot: %w", err)
	}
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		known := make(map[string]Service)
		for ctx.Err() == nil {
			current := make(map[string]Service, len(snapshot.Kvs))
			for _, kv := range snapshot.Kvs {
				svc, err := unmarshalService(kv.Value)
				if err != nil {
					continue
				}
				current[svc.InstanceID] = svc
				if !sendEvent(ctx, ch, Event{Type: EventPut, Service: svc}) {
					return
				}
			}
			for id, svc := range known {
				if _, ok := current[id]; !ok {
					if !sendEvent(ctx, ch, Event{Type: EventDelete, Service: svc}) {
						return
					}
				}
			}
			known = current

			wch := r.client.Watch(ctx, prefix, clientv3.WithPrefix(), clientv3.WithRev(snapshot.Header.Revision+1), clientv3.WithPrevKV())
			restart := true
			for wresp := range wch {
				if err := wresp.Err(); err != nil {
					slog.Warn("registry: etcd watch interrupted", "service", name, "error", err)
					restart = true
					break
				}
				for _, ev := range wresp.Events {
					event, ok := etcdEvent(prefix, name, ev)
					if ok {
						if event.Type == EventDelete {
							delete(known, event.Service.InstanceID)
						} else {
							known[event.Service.InstanceID] = event.Service
						}
						if !sendEvent(ctx, ch, event) {
							return
						}
					}
				}
			}
			if ctx.Err() != nil {
				return
			}
			if !restart || !waitForContext(ctx, time.Second) {
				return
			}
			for {
				snapshot, err = r.client.Get(ctx, prefix, clientv3.WithPrefix())
				if err == nil {
					break
				}
				slog.Warn("registry: etcd watch snapshot failed", "service", name, "error", err)
				if !waitForContext(ctx, time.Second) {
					return
				}
			}
		}
	}()

	return ch, nil
}

func etcdEvent(prefix, name string, ev *clientv3.Event) (Event, bool) {
	if ev.Type == clientv3.EventTypePut && ev.Kv != nil {
		svc, err := unmarshalService(ev.Kv.Value)
		return Event{Type: EventPut, Service: svc}, err == nil
	}
	if ev.Type == clientv3.EventTypeDelete {
		if ev.PrevKv != nil {
			if svc, err := unmarshalService(ev.PrevKv.Value); err == nil {
				return Event{Type: EventDelete, Service: svc}, true
			}
		}
		instanceID := ""
		if ev.Kv != nil {
			instanceID = strings.TrimPrefix(string(ev.Kv.Key), prefix)
		}
		return Event{Type: EventDelete, Service: Service{Name: name, InstanceID: instanceID}}, instanceID != ""
	}
	return Event{}, false
}

func sendEvent(ctx context.Context, ch chan<- Event, event Event) bool {
	select {
	case ch <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitForContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *etcdRegistry) Close() error {
	r.mu.Lock()
	leases := make([]clientv3.LeaseID, 0, len(r.leases))
	for _, cancel := range r.cancels {
		cancel()
	}
	for _, leaseID := range r.leases {
		leases = append(leases, leaseID)
	}
	r.cancels = make(map[string]context.CancelFunc)
	r.leases = make(map[string]clientv3.LeaseID)
	r.mu.Unlock()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer ctxCancel()
	for _, leaseID := range leases {
		_, _ = r.client.Revoke(ctx, leaseID)
	}
	return r.client.Close()
}
