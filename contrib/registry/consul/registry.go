package consul

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	core "github.com/baowk/dilu-go-kit/registry"
	consul "github.com/hashicorp/consul/api"
)

// consulRegistry implements Registry using HashiCorp Consul.
type consulRegistry struct {
	client  *consul.Client
	cfg     Config
	mu      sync.Mutex
	wg      sync.WaitGroup
	checks  map[string]string             // instanceID → checkID
	cancels map[string]context.CancelFunc // instanceID → TTL refresh cancel
}

var _ core.Registry = (*consulRegistry)(nil)
var _ core.KVStore = (*consulRegistry)(nil)

// NewConsul creates a new Consul-backed registry.
func NewConsul(cfg Config) (Registry, error) {
	addr := cfg.Address
	if addr == "" && len(cfg.Endpoints) > 0 {
		addr = cfg.Endpoints[0]
	}
	if addr == "" {
		return nil, fmt.Errorf("registry: no consul address configured")
	}
	if _, err := cfg.EffectiveConsulCheckType(); err != nil {
		return nil, err
	}

	probeCfg, err := consulProbeConfigWithTLS(addr, cfg.Token, cfg.TLS, cfg.EffectiveDialTimeout())
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

	client, err := consul.NewClient(consulConfig(addr, cfg.Token, cfg.TLS))
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

// Get reads a raw Consul KV value.
func (r *consulRegistry) Get(ctx context.Context, key string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pair, _, err := r.client.KV().Get(key, (&consul.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("registry: consul get %q: %w", key, err)
	}
	if pair == nil {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), pair.Value...), nil
}

// WatchKey watches a raw Consul KV value until ctx is cancelled.
func (r *consulRegistry) WatchKey(ctx context.Context, key string, onChange func([]byte)) error {
	if onChange == nil {
		return errors.New("registry: watch callback is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var lastIndex uint64
	for ctx.Err() == nil {
		pair, meta, err := r.client.KV().Get(key, (&consul.QueryOptions{WaitIndex: lastIndex, WaitTime: 55 * time.Second}).WithContext(ctx))
		if err != nil {
			if !waitForContext(ctx, 2*time.Second) {
				return ctx.Err()
			}
			continue
		}
		if meta != nil && meta.LastIndex != lastIndex {
			lastIndex = meta.LastIndex
			if pair == nil {
				onChange(nil)
			} else {
				onChange(append([]byte(nil), pair.Value...))
			}
		}
	}
	return ctx.Err()
}

func consulConfig(addr, token string, tlsOptions ...TLSConfig) *consul.Config {
	cfg := consul.DefaultConfig()
	cfg.Address = addr
	if token != "" {
		cfg.Token = token
	}
	if len(tlsOptions) > 0 && tlsOptions[0].Enable {
		tlsCfg := tlsOptions[0]
		tlsAddress := addr
		if tlsCfg.ServerName != "" {
			tlsAddress = tlsCfg.ServerName
		}
		cfg.Scheme = "https"
		cfg.TLSConfig = consul.TLSConfig{
			Address:  tlsAddress,
			CAFile:   tlsCfg.CAFile,
			CertFile: tlsCfg.CertFile,
			KeyFile:  tlsCfg.KeyFile,
		}
	}
	if httpClient, err := consul.NewHttpClient(cfg.Transport, cfg.TLSConfig); err == nil {
		// Watch uses Consul blocking queries (up to 30s). Keep a transport
		// deadline longer than the long-poll interval; individual startup and
		// API calls still receive caller contexts with bounded deadlines.
		httpClient.Timeout = 60 * time.Second
		cfg.HttpClient = httpClient
	}
	return cfg
}

func consulProbeConfig(addr, token string, timeout time.Duration) (*consul.Config, error) {
	return consulProbeConfigWithTLS(addr, token, TLSConfig{}, timeout)
}

func consulProbeConfigWithTLS(addr, token string, tlsOptions TLSConfig, timeout time.Duration) (*consul.Config, error) {
	cfg := consulConfig(addr, token, tlsOptions)
	httpClient, err := consul.NewHttpClient(cfg.Transport, cfg.TLSConfig)
	if err != nil {
		return nil, err
	}
	httpClient.Timeout = timeout
	cfg.HttpClient = httpClient
	return cfg, nil
}

func (r *consulRegistry) Register(ctx context.Context, svc Service) error {
	if ctx == nil {
		ctx = context.Background()
	}
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
	checkAddr := net.JoinHostPort(host, portStr)
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("registry: invalid port %q", portStr)
	}

	ttl := r.cfg.EffectiveTTL()
	checkID := "check-" + svc.InstanceID
	check, usesTTL, err := consulServiceCheck(r.cfg, checkID, checkAddr)
	if err != nil {
		return err
	}

	meta := make(map[string]string)
	for k, v := range svc.Meta {
		meta[k] = v
	}
	// Consul has no first-class version field; persist it as metadata so
	// version filters work consistently across etcd and Consul backends.
	if svc.Version != "" {
		meta["version"] = svc.Version
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
		Check:   check,
	}

	if err := r.client.Agent().ServiceRegisterOpts(reg, consul.ServiceRegisterOpts{}.WithContext(ctx)); err != nil {
		return fmt.Errorf("registry: consul register: %w", err)
	}

	var ttlCancel context.CancelFunc
	var ttlCtx context.Context
	if usesTTL {
		if err := r.client.Agent().UpdateTTLOpts(checkID, "initial", consul.HealthPassing, (&consul.QueryOptions{}).WithContext(ctx)); err != nil {
			_ = r.client.Agent().ServiceDeregisterOpts(svc.InstanceID, (&consul.QueryOptions{}).WithContext(ctx))
			return fmt.Errorf("registry: consul pass ttl: %w", err)
		}
		ttlCtx, ttlCancel = context.WithCancel(context.Background())
	}

	r.mu.Lock()
	oldCancel := r.cancels[svc.InstanceID]
	r.checks[svc.InstanceID] = checkID
	if ttlCancel != nil {
		r.cancels[svc.InstanceID] = ttlCancel
	} else {
		delete(r.cancels, svc.InstanceID)
	}
	r.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
	if ttlCancel != nil {
		r.wg.Add(1)
		go func() { defer r.wg.Done(); r.refreshTTL(ttlCtx, svc, checkID, ttl) }()
	}
	checkType := "http"
	if usesTTL {
		checkType = "ttl"
	}

	slog.Info("registry: registered",
		"backend", "consul",
		"service", svc.Name,
		"instance", svc.InstanceID,
		"addr", svc.Addr,
		"grpc", svc.GRPCAddr,
		"ttl", ttl,
		"check_type", checkType,
	)
	return nil
}

func consulServiceCheck(cfg Config, checkID, checkAddr string) (*consul.AgentServiceCheck, bool, error) {
	checkType, err := cfg.EffectiveConsulCheckType()
	if err != nil {
		return nil, false, err
	}
	check := &consul.AgentServiceCheck{
		CheckID:                        checkID,
		DeregisterCriticalServiceAfter: durationSeconds(cfg.EffectiveDeregisterCriticalAfter()),
	}
	if checkType == "http" {
		ttl := cfg.EffectiveTTL()
		// The service health endpoint is a separate transport from the Consul
		// API. Do not infer its scheme from registry TLS; most services expose
		// plain HTTP readiness even when Consul itself uses HTTPS.
		check.HTTP = consulCheckURL(checkAddr, cfg.EffectiveCheckPath(), cfg.CheckTLS)
		check.Interval = fmt.Sprintf("%ds", ttl)
		check.Timeout = fmt.Sprintf("%ds", minInt64(5, ttl))
		return check, false, nil
	}
	check.TTL = fmt.Sprintf("%ds", cfg.EffectiveTTL())
	return check, true, nil
}

func (r *consulRegistry) refreshTTL(ctx context.Context, svc Service, checkID string, ttl int64) {
	ticker := time.NewTicker(consulRefreshInterval(ttl))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.client.Agent().UpdateTTLOpts(checkID, "alive", consul.HealthPassing, (&consul.QueryOptions{}).WithContext(ctx)); err != nil {
				slog.Warn("registry: consul ttl refresh failed",
					"service", svc.Name, "instance", svc.InstanceID, "error", err)
			}
		}
	}
}

func consulRefreshInterval(ttl int64) time.Duration {
	interval := time.Duration(ttl/3) * time.Second
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func consulCheckURL(addr, checkPath string, tlsEnabled ...bool) string {
	scheme := "http"
	if len(tlsEnabled) > 0 && tlsEnabled[0] {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: addr, Path: checkPath}
	return u.String()
}

func durationSeconds(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d/time.Second))
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (r *consulRegistry) Deregister(ctx context.Context, name, instanceID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if err := validateServiceName(name); err != nil {
		return err
	}
	if instanceID == "" || len(instanceID) > 256 || strings.Contains(instanceID, "/") || strings.ContainsAny(instanceID, "\r\n\x00") {
		return fmt.Errorf("registry: invalid instance ID %q", instanceID)
	}
	r.mu.Lock()
	if cancel, ok := r.cancels[instanceID]; ok {
		cancel()
		delete(r.cancels, instanceID)
	}
	delete(r.checks, instanceID)
	r.mu.Unlock()

	if err := r.client.Agent().ServiceDeregisterOpts(instanceID, (&consul.QueryOptions{}).WithContext(ctx)); err != nil {
		return fmt.Errorf("registry: consul deregister: %w", err)
	}

	slog.Info("registry: deregistered", "backend", "consul", "service", name, "instance", instanceID)
	return nil
}

func (r *consulRegistry) Discover(ctx context.Context, name string) ([]Service, error) {
	name = strings.TrimSpace(name)
	if err := validateServiceName(name); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	services, _, err := r.discover(ctx, name, &consul.QueryOptions{})
	if err != nil {
		return nil, err
	}
	return services, nil
}

func (r *consulRegistry) discover(ctx context.Context, name string, query *consul.QueryOptions) ([]Service, uint64, error) {
	entries, meta, err := r.client.Health().Service(name, "", true, query.WithContext(ctx))
	if err != nil {
		return nil, 0, fmt.Errorf("registry: consul discover: %w", err)
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
				if k == "version" {
					svc.Version = v
				}
			}
		}
		if svc.Name != name || validateService(svc) != nil {
			continue
		}
		services = append(services, svc)
	}
	if meta == nil {
		return services, 0, nil
	}
	return services, meta.LastIndex, nil
}

func (r *consulRegistry) Watch(ctx context.Context, name string) (<-chan Event, error) {
	name = strings.TrimSpace(name)
	if err := validateServiceName(name); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan Event, 32)

	// First send current state
	current, lastIndex, err := r.discover(ctx, name, &consul.QueryOptions{})
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
						if k == "version" {
							svc.Version = v
						}
					}
				}
				if svc.Name != name || validateService(svc) != nil {
					continue
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
	r.wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.EffectiveDialTimeout())
	defer cancel()
	for instanceID, checkID := range checks {
		q := (&consul.QueryOptions{}).WithContext(ctx)
		_ = r.client.Agent().ServiceDeregisterOpts(instanceID, q)
		_ = r.client.Agent().CheckDeregisterOpts(checkID, q)
	}
	return nil
}
