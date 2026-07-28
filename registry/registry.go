// Package registry provides service registration and discovery via etcd or consul.
//
// Usage (etcd):
//
//	r, _ := registry.New(registry.Config{Type: "etcd", Endpoints: []string{"127.0.0.1:2379"}})
//	r.Register(ctx, registry.Service{Name: "mf-user", Addr: ":7801"})
//	defer r.Deregister(ctx, "mf-user", instanceID)
//
// Usage (consul):
//
//	r, _ := registry.New(registry.Config{Type: "consul", Address: "127.0.0.1:8500"})
//	r.Register(ctx, registry.Service{Name: "mf-user", Addr: ":7801"})
//	defer r.Deregister(ctx, "mf-user", instanceID)
//
// Usage (gateway side):
//
//	r, _ := registry.New(cfg)
//	services := r.Discover(ctx, "mf-user")
//	ch := r.Watch(ctx, "mf-user") // live updates
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service describes a registered service instance.
type Service struct {
	Name       string            `json:"name"`                // e.g. "mf-user"
	InstanceID string            `json:"instance_id"`         // unique per instance
	Addr       string            `json:"addr"`                // HTTP address, e.g. "10.0.1.5:7801"
	GRPCAddr   string            `json:"grpc_addr,omitempty"` // gRPC address, e.g. "10.0.1.5:7889"
	Meta       map[string]string `json:"meta,omitempty"`      // version, weight, etc.
	RegisterAt time.Time         `json:"register_at"`
}

// Event describes a service change event from Watch.
type Event struct {
	Type    EventType // PUT or DELETE
	Service Service
}

// UpstreamSnapshot is a service instance snapshot for gateway routing.
// Stale means Services is the last known good snapshot because the registry
// currently reports no healthy instances.
type UpstreamSnapshot struct {
	Services []Service
	Stale    bool
}

// EventType is the type of a watch event.
type EventType int

const (
	EventPut    EventType = iota // service registered or updated
	EventDelete                  // service deregistered or lease expired
)

// Registry is the interface for service registration and discovery.
type Registry interface {
	// Register registers a service instance with a TTL lease.
	// It starts a background goroutine to keep the lease alive.
	Register(ctx context.Context, svc Service) error

	// Deregister removes a service instance.
	Deregister(ctx context.Context, name, instanceID string) error

	// Discover returns all healthy instances of a service.
	Discover(ctx context.Context, name string) ([]Service, error)

	// Watch returns a channel of service change events.
	// The channel is closed when ctx is cancelled.
	Watch(ctx context.Context, name string) (<-chan Event, error)

	// Close releases resources.
	Close() error
}

// WatchUpstreams watches a service and preserves the last known good upstreams
// when the registry temporarily reports zero healthy instances.
func WatchUpstreams(ctx context.Context, r Registry, name string) (<-chan UpstreamSnapshot, error) {
	currentServices, err := r.Discover(ctx, name)
	if err != nil {
		return nil, err
	}
	events, err := r.Watch(ctx, name)
	if err != nil {
		return nil, err
	}

	ch := make(chan UpstreamSnapshot, 8)
	go func() {
		defer close(ch)

		current := servicesByInstanceID(currentServices)
		lastGood := cloneServiceMap(current)
		sendUpstreamSnapshot(ctx, ch, name, current, lastGood)

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				switch event.Type {
				case EventPut:
					current[event.Service.InstanceID] = event.Service
				case EventDelete:
					delete(current, event.Service.InstanceID)
				}
				if len(current) > 0 {
					lastGood = cloneServiceMap(current)
				}
				if !sendUpstreamSnapshot(ctx, ch, name, current, lastGood) {
					return
				}
			}
		}
	}()
	return ch, nil
}

// Config for the registry.
type Config struct {
	Type                    string   `mapstructure:"type"`                    // "etcd" (default) or "consul"
	Endpoints               []string `mapstructure:"endpoints"`               // etcd endpoints, e.g. ["127.0.0.1:2379"]
	Address                 string   `mapstructure:"address"`                 // consul address, e.g. "127.0.0.1:8500"
	Token                   string   `mapstructure:"token"`                   // consul ACL token (optional)
	Prefix                  string   `mapstructure:"prefix"`                  // key prefix, default "/mofang/services/"
	TTL                     int      `mapstructure:"ttl"`                     // lease/check TTL in seconds, default 30
	DialTimeout             int      `mapstructure:"dialTimeout"`             // dial timeout in seconds, default 5
	CheckPath               string   `mapstructure:"checkPath"`               // consul HTTP readiness path, default "/ready"
	DeregisterCriticalAfter int      `mapstructure:"deregisterCriticalAfter"` // consul critical deregister delay in seconds, default 300
}

func (c *Config) registryType() string {
	if c.Type != "" {
		return c.Type
	}
	return "etcd"
}

// New creates a registry based on Config.Type ("etcd" or "consul").
func New(cfg Config) (Registry, error) {
	switch cfg.registryType() {
	case "etcd":
		return NewEtcd(cfg)
	case "consul":
		return NewConsul(cfg)
	default:
		return nil, fmt.Errorf("registry: unsupported type %q (expected etcd or consul)", cfg.Type)
	}
}

func (c *Config) prefix() string {
	if c.Prefix != "" {
		return strings.TrimRight(c.Prefix, "/") + "/"
	}
	return "/mofang/services/"
}

func (c *Config) ttl() int64 {
	if c.TTL > 0 {
		return int64(c.TTL)
	}
	return 30
}

func (c *Config) dialTimeout() time.Duration {
	if c.DialTimeout > 0 {
		return time.Duration(c.DialTimeout) * time.Second
	}
	return 5 * time.Second
}

func (c *Config) checkPath() string {
	if c.CheckPath == "" {
		return "/ready"
	}
	if strings.HasPrefix(c.CheckPath, "/") {
		return c.CheckPath
	}
	return "/" + c.CheckPath
}

func (c *Config) deregisterCriticalAfter() time.Duration {
	if c.DeregisterCriticalAfter > 0 {
		return time.Duration(c.DeregisterCriticalAfter) * time.Second
	}
	return 5 * time.Minute
}

func sendUpstreamSnapshot(ctx context.Context, ch chan<- UpstreamSnapshot, name string, current, lastGood map[string]Service) bool {
	if len(current) > 0 {
		return sendSnapshot(ctx, ch, UpstreamSnapshot{Services: mapServices(current)})
	}
	if len(lastGood) > 0 {
		services := mapServices(lastGood)
		slog.Warn("registry: no healthy upstreams; using last known good",
			"service", name,
			"instances", len(services),
			"stale", true,
		)
		return sendSnapshot(ctx, ch, UpstreamSnapshot{Services: services, Stale: true})
	}
	slog.Warn("registry: no healthy upstreams and no last known good", "service", name, "stale", true)
	return sendSnapshot(ctx, ch, UpstreamSnapshot{Stale: true})
}

func sendSnapshot(ctx context.Context, ch chan<- UpstreamSnapshot, snapshot UpstreamSnapshot) bool {
	select {
	case ch <- snapshot:
		return true
	case <-ctx.Done():
		return false
	}
}

func servicesByInstanceID(services []Service) map[string]Service {
	byID := make(map[string]Service, len(services))
	for _, svc := range services {
		byID[svc.InstanceID] = svc
	}
	return byID
}

func cloneServiceMap(services map[string]Service) map[string]Service {
	cloned := make(map[string]Service, len(services))
	for id, svc := range services {
		cloned[id] = cloneService(svc)
	}
	return cloned
}

func mapServices(services map[string]Service) []Service {
	ids := make([]string, 0, len(services))
	for id := range services {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Service, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneService(services[id]))
	}
	return out
}

func cloneService(svc Service) Service {
	if svc.Meta == nil {
		return svc
	}
	meta := make(map[string]string, len(svc.Meta))
	for k, v := range svc.Meta {
		meta[k] = v
	}
	svc.Meta = meta
	return svc
}

// serviceKey builds the etcd key for a service instance.
func serviceKey(prefix, name, instanceID string) string {
	return fmt.Sprintf("%s%s/%s", prefix, name, instanceID)
}

// servicePrefixKey builds the etcd key prefix for all instances of a service.
func servicePrefixKey(prefix, name string) string {
	return fmt.Sprintf("%s%s/", prefix, name)
}

// marshalService encodes a Service to JSON.
func marshalService(svc Service) (string, error) {
	b, err := json.Marshal(svc)
	return string(b), err
}

// unmarshalService decodes a Service from JSON.
func unmarshalService(data []byte) (Service, error) {
	var svc Service
	err := json.Unmarshal(data, &svc)
	return svc, err
}

// localIP returns a non-loopback IPv4 address of the host, or "127.0.0.1" as fallback.
func localIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return "127.0.0.1"
}

// GenerateInstanceID creates a unique instance ID from hostname + pid + timestamp.
func GenerateInstanceID(name string) string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%s-%s", name, host, uuid.NewString())
}

func validateService(svc Service) error {
	if svc.Name == "" || strings.Contains(svc.Name, "/") {
		return fmt.Errorf("registry: invalid service name %q", svc.Name)
	}
	if svc.InstanceID == "" || strings.Contains(svc.InstanceID, "/") {
		return fmt.Errorf("registry: invalid instance ID %q", svc.InstanceID)
	}
	_, port, err := net.SplitHostPort(svc.Addr)
	if err != nil {
		return fmt.Errorf("registry: parse addr %q: %w", svc.Addr, err)
	}
	if port == "" {
		return fmt.Errorf("registry: address %q has no port", svc.Addr)
	}
	return nil
}
