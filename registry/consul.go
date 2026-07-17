package registry

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"strconv"
	"sync"
	"time"

	consul "github.com/hashicorp/consul/api"
)

// consulRegistry implements Registry using HashiCorp Consul.
type consulRegistry struct {
	client  *consul.Client
	cfg     Config
	mu      sync.Mutex
	checks  map[string]string             // instanceID → checkID
	cancels map[string]context.CancelFunc // instanceID → TTL refresh cancel
}

// NewConsul creates a new Consul-backed registry.
func NewConsul(cfg Config) (Registry, error) {
	addr := cfg.Address
	if addr == "" && len(cfg.Endpoints) > 0 {
		addr = cfg.Endpoints[0]
	}
	if addr == "" {
		return nil, fmt.Errorf("registry: no consul address configured")
	}

	probeCfg, err := consulProbeConfig(addr, cfg.Token, cfg.dialTimeout())
	if err != nil {
		return nil, fmt.Errorf("registry: consul probe client: %w", err)
	}
	probe, err := consul.NewClient(probeCfg)
	if err != nil {
		return nil, fmt.Errorf("registry: consul client: %w", err)
	}
	_, err = probe.Agent().Self()
	probeCfg.HttpClient.CloseIdleConnections()
	if err != nil {
		return nil, fmt.Errorf("registry: consul connect: %w", err)
	}

	client, err := consul.NewClient(consulConfig(addr, cfg.Token))
	if err != nil {
		return nil, fmt.Errorf("registry: consul client: %w", err)
	}

	return &consulRegistry{
		client:  client,
		cfg:     cfg,
		checks:  make(map[string]string),
		cancels: make(map[string]context.CancelFunc),
	}, nil
}

func consulConfig(addr, token string) *consul.Config {
	cfg := consul.DefaultConfig()
	cfg.Address = addr
	if token != "" {
		cfg.Token = token
	}
	return cfg
}

func consulProbeConfig(addr, token string, timeout time.Duration) (*consul.Config, error) {
	cfg := consulConfig(addr, token)
	httpClient, err := consul.NewHttpClient(cfg.Transport, cfg.TLSConfig)
	if err != nil {
		return nil, err
	}
	httpClient.Timeout = timeout
	cfg.HttpClient = httpClient
	return cfg, nil
}

func (r *consulRegistry) Register(ctx context.Context, svc Service) error {
	if svc.InstanceID == "" {
		svc.InstanceID = GenerateInstanceID(svc.Name)
	}
	if err := validateService(svc); err != nil {
		return err
	}
	svc.RegisterAt = time.Now()

	host, portStr, err := net.SplitHostPort(svc.Addr)
	if err != nil {
		return fmt.Errorf("registry: parse addr %q: %w", svc.Addr, err)
	}
	if host == "" {
		host = localIP()
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("registry: invalid port %q", portStr)
	}

	ttl := r.cfg.ttl()
	checkID := "check-" + svc.InstanceID

	meta := make(map[string]string)
	for k, v := range svc.Meta {
		meta[k] = v
	}
	if svc.GRPCAddr != "" {
		meta["grpc_addr"] = svc.GRPCAddr
	}
	meta["register_at"] = svc.RegisterAt.Format(time.RFC3339)

	reg := &consul.AgentServiceRegistration{
		ID:      svc.InstanceID,
		Name:    svc.Name,
		Address: host,
		Port:    port,
		Meta:    meta,
		Check: &consul.AgentServiceCheck{
			CheckID:                        checkID,
			TTL:                            fmt.Sprintf("%ds", ttl),
			DeregisterCriticalServiceAfter: fmt.Sprintf("%ds", ttl*3),
		},
	}

	if err := r.client.Agent().ServiceRegister(reg); err != nil {
		return fmt.Errorf("registry: consul register: %w", err)
	}

	// Pass initial health check
	if err := r.client.Agent().PassTTL(checkID, "initial"); err != nil {
		_ = r.client.Agent().ServiceDeregister(svc.InstanceID)
		return fmt.Errorf("registry: consul pass ttl: %w", err)
	}

	ttlCtx, ttlCancel := context.WithCancel(context.Background())

	r.mu.Lock()
	oldCancel := r.cancels[svc.InstanceID]
	r.checks[svc.InstanceID] = checkID
	r.cancels[svc.InstanceID] = ttlCancel
	r.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}

	// Background TTL refresh
	go func() {
		ticker := time.NewTicker(consulRefreshInterval(ttl))
		defer ticker.Stop()
		for {
			select {
			case <-ttlCtx.Done():
				return
			case <-ticker.C:
				if err := r.client.Agent().PassTTL(checkID, "alive"); err != nil {
					slog.Warn("registry: consul ttl refresh failed",
						"service", svc.Name, "instance", svc.InstanceID, "error", err)
					continue
				}
			}
		}
	}()

	slog.Info("registry: registered",
		"backend", "consul",
		"service", svc.Name,
		"instance", svc.InstanceID,
		"addr", svc.Addr,
		"grpc", svc.GRPCAddr,
		"ttl", ttl,
	)
	return nil
}

func consulRefreshInterval(ttl int64) time.Duration {
	interval := time.Duration(ttl/3) * time.Second
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func (r *consulRegistry) Deregister(ctx context.Context, name, instanceID string) error {
	r.mu.Lock()
	if cancel, ok := r.cancels[instanceID]; ok {
		cancel()
		delete(r.cancels, instanceID)
	}
	delete(r.checks, instanceID)
	r.mu.Unlock()

	if err := r.client.Agent().ServiceDeregister(instanceID); err != nil {
		return fmt.Errorf("registry: consul deregister: %w", err)
	}

	slog.Info("registry: deregistered", "backend", "consul", "service", name, "instance", instanceID)
	return nil
}

func (r *consulRegistry) Discover(ctx context.Context, name string) ([]Service, error) {
	entries, _, err := r.client.Health().Service(name, "", true, (&consul.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("registry: consul discover: %w", err)
	}

	services := make([]Service, 0, len(entries))
	for _, entry := range entries {
		svc := Service{
			Name:       entry.Service.Service,
			InstanceID: entry.Service.ID,
			Addr:       net.JoinHostPort(entry.Service.Address, strconv.Itoa(entry.Service.Port)),
			Meta:       make(map[string]string),
		}
		for k, v := range entry.Service.Meta {
			switch k {
			case "grpc_addr":
				svc.GRPCAddr = v
			case "register_at":
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					svc.RegisterAt = t
				}
			default:
				svc.Meta[k] = v
			}
		}
		services = append(services, svc)
	}
	return services, nil
}

func (r *consulRegistry) Watch(ctx context.Context, name string) (<-chan Event, error) {
	ch := make(chan Event, 32)

	// First send current state
	current, err := r.Discover(ctx, name)
	if err != nil {
		return nil, err
	}

	go func() {
		defer close(ch)
		for _, svc := range current {
			if !sendEvent(ctx, ch, Event{Type: EventPut, Service: svc}) {
				return
			}
		}

		var lastIndex uint64
		known := make(map[string]Service)
		for _, svc := range current {
			known[svc.InstanceID] = svc
		}

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			query := (&consul.QueryOptions{
				WaitIndex: lastIndex,
				WaitTime:  30 * time.Second,
			}).WithContext(ctx)
			entries, meta, err := r.client.Health().Service(name, "", true, query)
			if err != nil {
				slog.Warn("registry: consul watch error", "service", name, "error", err)
				if !waitForContext(ctx, time.Second) {
					return
				}
				continue
			}
			if meta != nil {
				lastIndex = meta.LastIndex
			}

			seen := make(map[string]Service)
			for _, entry := range entries {
				id := entry.Service.ID

				svc := Service{
					Name:       entry.Service.Service,
					InstanceID: id,
					Addr:       net.JoinHostPort(entry.Service.Address, strconv.Itoa(entry.Service.Port)),
					Meta:       make(map[string]string),
				}
				for k, v := range entry.Service.Meta {
					switch k {
					case "grpc_addr":
						svc.GRPCAddr = v
					case "register_at":
						if t, err := time.Parse(time.RFC3339, v); err == nil {
							svc.RegisterAt = t
						}
					default:
						svc.Meta[k] = v
					}
				}
				seen[id] = svc

				previous, exists := known[id]
				if !exists || !reflect.DeepEqual(previous, svc) {
					select {
					case ch <- Event{Type: EventPut, Service: svc}:
					case <-ctx.Done():
						return
					}
				}
			}

			// Detect removed instances
			for id := range known {
				if _, ok := seen[id]; !ok {
					select {
					case ch <- Event{Type: EventDelete, Service: Service{Name: name, InstanceID: id}}:
					case <-ctx.Done():
						return
					}
				}
			}

			known = seen
		}
	}()

	return ch, nil
}

func (r *consulRegistry) Close() error {
	r.mu.Lock()
	for _, cancel := range r.cancels {
		cancel()
	}
	r.cancels = make(map[string]context.CancelFunc)

	checks := r.checks
	r.checks = make(map[string]string)
	r.mu.Unlock()

	for instanceID, checkID := range checks {
		_ = r.client.Agent().ServiceDeregister(instanceID)
		_ = r.client.Agent().CheckDeregister(checkID)
	}
	return nil
}
