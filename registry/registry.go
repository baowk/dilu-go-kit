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
	"net"
	"os"
	"time"
)

// Service describes a registered service instance.
type Service struct {
	Name       string            `json:"name"`        // e.g. "mf-user"
	InstanceID string            `json:"instance_id"`  // unique per instance
	Addr       string            `json:"addr"`         // HTTP address, e.g. "10.0.1.5:7801"
	GRPCAddr   string            `json:"grpc_addr,omitempty"` // gRPC address, e.g. "10.0.1.5:7889"
	Meta       map[string]string `json:"meta,omitempty"`      // version, weight, etc.
	RegisterAt time.Time         `json:"register_at"`
}

// Event describes a service change event from Watch.
type Event struct {
	Type    EventType // PUT or DELETE
	Service Service
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

// Config for the registry.
type Config struct {
	Type        string   `mapstructure:"type"`        // "etcd" (default) or "consul"
	Endpoints   []string `mapstructure:"endpoints"`   // etcd endpoints, e.g. ["127.0.0.1:2379"]
	Address     string   `mapstructure:"address"`     // consul address, e.g. "127.0.0.1:8500"
	Token       string   `mapstructure:"token"`       // consul ACL token (optional)
	Prefix      string   `mapstructure:"prefix"`      // key prefix, default "/mofang/services/"
	TTL         int      `mapstructure:"ttl"`          // lease/check TTL in seconds, default 30
	DialTimeout int      `mapstructure:"dialTimeout"`  // dial timeout in seconds, default 5
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
		return c.Prefix
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
	return fmt.Sprintf("%s-%s-%d-%d", name, host, os.Getpid(), time.Now().UnixMilli()%100000)
}
