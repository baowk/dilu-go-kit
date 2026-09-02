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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service describes a registered service instance.
type Service struct {
	Name       string            `json:"name"`                // e.g. "mf-user"
	Version    string            `json:"version,omitempty"`   // optional service release, e.g. "v2.1.0"
	InstanceID string            `json:"instance_id"`         // unique per instance
	Addr       string            `json:"addr"`                // HTTP address, e.g. "10.0.1.5:7801"
	GRPCAddr   string            `json:"grpc_addr,omitempty"` // gRPC address, e.g. "10.0.1.5:7889"
	Meta       map[string]string `json:"meta,omitempty"`      // arbitrary routing and deployment labels
	RegisterAt time.Time         `json:"register_at"`
}

// ServiceFilter determines whether a discovered service instance is eligible
// for a consumer. Filters are optional; a nil filter accepts every instance.
type ServiceFilter func(Service) bool

// VersionFilter matches instances with the given version. An empty version
// disables version filtering, preserving compatibility with unversioned
// services and existing callers.
func VersionFilter(version string) ServiceFilter {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	return func(svc Service) bool {
		got := strings.TrimSpace(svc.Version)
		// Read the legacy metadata key as a compatibility fallback for
		// instances registered before Version became a first-class field.
		if got == "" && svc.Meta != nil {
			got = strings.TrimSpace(svc.Meta["version"])
		}
		return got == version
	}
}

// MetadataFilter matches all provided metadata key/value pairs exactly. An
// empty map disables metadata filtering. The input map is copied so callers
// may safely reuse or mutate their original map after construction.
func MetadataFilter(metadata map[string]string) ServiceFilter {
	if len(metadata) == 0 {
		return nil
	}
	want := make(map[string]string, len(metadata))
	for key, value := range metadata {
		want[key] = value
	}
	return func(svc Service) bool {
		for key, value := range want {
			if svc.Meta == nil || svc.Meta[key] != value {
				return false
			}
		}
		return true
	}
}

// AndFilters combines filters with logical AND. Nil filters are ignored; if
// all filters are nil, AndFilters returns nil.
func AndFilters(filters ...ServiceFilter) ServiceFilter {
	active := make([]ServiceFilter, 0, len(filters))
	for _, filter := range filters {
		if filter != nil {
			active = append(active, filter)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func(svc Service) bool {
		for _, filter := range active {
			if !filter(svc) {
				return false
			}
		}
		return true
	}
}

// Event describes a service change event from Watch.
type Event struct {
	Type    EventType // PUT or DELETE
	Service Service
}

// UpstreamSnapshot is a service instance snapshot for gateway routing.
// Stale means the registry currently reports no healthy instances or could not
// be reconciled. Services may contain a bounded last-known-good fallback.
type UpstreamSnapshot struct {
	Services   []Service
	Stale      bool
	StaleSince time.Time
}

// WatchUpstreamsOptions controls last-known-good fallback and reconciliation.
type WatchUpstreamsOptions struct {
	// StaleGracePeriod is how long last-known-good services remain routable.
	// Zero uses the 30-second default.
	StaleGracePeriod time.Duration
	// ResyncInterval bounds drift for Registry implementations whose Watch may
	// miss a transition. Zero uses the 30-second default.
	ResyncInterval time.Duration
	// Filter optionally limits which discovered instances are routable.
	Filter ServiceFilter
	// InitialDiscoveryTimeout bounds the initial Watch/Discover calls. The
	// returned watch continues using the parent context after initialization.
	// Zero uses five seconds.
	InitialDiscoveryTimeout time.Duration
}

// EventType is the type of a watch event.
type EventType int

const (
	EventPut    EventType = iota // service registered or updated
	EventDelete                  // service deregistered or lease expired
)

// Registry is the interface for service registration and discovery.
type Registry interface {
	// Register registers a service instance. Lease/check lifecycle depends on
	// the selected backend and Consul check type.
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

// WatchUpstreams watches a service with bounded last-known-good fallback.
func WatchUpstreams(ctx context.Context, r Registry, name string) (<-chan UpstreamSnapshot, error) {
	return WatchUpstreamsWithOptions(ctx, r, name, WatchUpstreamsOptions{})
}

// WatchUpstreamsWithOptions watches a service and reconciles every event
// against an authoritative Discover result. Last-known-good services expire
// after StaleGracePeriod instead of remaining routable indefinitely.
func WatchUpstreamsWithOptions(ctx context.Context, r Registry, name string, options WatchUpstreamsOptions) (<-chan UpstreamSnapshot, error) {
	if r == nil {
		return nil, fmt.Errorf("registry: registry is nil")
	}
	name = strings.TrimSpace(name)
	if err := validateServiceName(name); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	watchCtx, cancel := context.WithCancel(ctx)
	initialTimeout := options.InitialDiscoveryTimeout
	if initialTimeout <= 0 {
		initialTimeout = 5 * time.Second
	}
	type watchResult struct {
		events <-chan Event
		err    error
	}
	watchResultCh := make(chan watchResult, 1)
	go func() {
		events, err := r.Watch(watchCtx, name)
		watchResultCh <- watchResult{events: events, err: err}
	}()
	var watch watchResult
	timer := time.NewTimer(initialTimeout)
	select {
	case watch = <-watchResultCh:
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	case <-timer.C:
		cancel()
		return nil, fmt.Errorf("registry: initial watch timeout after %s", initialTimeout)
	}
	if watch.err != nil {
		cancel()
		return nil, watch.err
	}
	type discoverResult struct {
		services []Service
		err      error
	}
	discoverCh := make(chan discoverResult, 1)
	go func() {
		services, err := r.Discover(watchCtx, name)
		discoverCh <- discoverResult{services: services, err: err}
	}()
	timer = time.NewTimer(initialTimeout)
	var discovered discoverResult
	select {
	case discovered = <-discoverCh:
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	case <-timer.C:
		cancel()
		return nil, fmt.Errorf("registry: initial discovery timeout after %s", initialTimeout)
	}
	if discovered.err != nil {
		cancel()
		return nil, discovered.err
	}
	events := watch.events
	currentServices := discovered.services

	staleGracePeriod := options.StaleGracePeriod
	if staleGracePeriod <= 0 {
		staleGracePeriod = 30 * time.Second
	}
	resyncInterval := options.ResyncInterval
	if resyncInterval <= 0 {
		resyncInterval = 30 * time.Second
	}

	ch := make(chan UpstreamSnapshot, 8)
	go func() {
		defer close(ch)
		defer cancel()

		currentServices = filterServices(currentServices, options.Filter)
		state := upstreamState{lastGood: normalizeServices(currentServices)}
		initial, expiresIn := state.snapshot(currentServices, time.Now(), staleGracePeriod)
		if initial.Stale {
			logStaleSnapshot(name, initial)
		}
		if !sendSnapshot(watchCtx, ch, initial) {
			return
		}
		lastSent := initial

		resync := time.NewTicker(resyncInterval)
		defer resync.Stop()
		var staleTimer *time.Timer
		var staleTimerC <-chan time.Time
		resetStaleTimer := func(after time.Duration) {
			if staleTimer != nil {
				if !staleTimer.Stop() {
					select {
					case <-staleTimer.C:
					default:
					}
				}
			}
			staleTimerC = nil
			if after > 0 {
				if staleTimer == nil {
					staleTimer = time.NewTimer(after)
				} else {
					staleTimer.Reset(after)
				}
				staleTimerC = staleTimer.C
			}
		}
		defer func() {
			if staleTimer != nil {
				staleTimer.Stop()
			}
		}()
		resetStaleTimer(expiresIn)

		publish := func(services []Service, discoverErr error) bool {
			if discoverErr != nil {
				slog.Warn("registry: upstream reconciliation failed",
					"service", name, "error", discoverErr)
				services = nil
			}
			services = filterServices(services, options.Filter)
			snapshot, nextExpiry := state.snapshot(services, time.Now(), staleGracePeriod)
			resetStaleTimer(nextExpiry)
			if reflect.DeepEqual(snapshot, lastSent) {
				return true
			}
			if snapshot.Stale {
				logStaleSnapshot(name, snapshot)
			}
			if !sendSnapshot(watchCtx, ch, snapshot) {
				return false
			}
			lastSent = snapshot
			return true
		}

		for {
			select {
			case <-watchCtx.Done():
				return
			case _, ok := <-events:
				if !ok {
					return
				}
				watchOpen := drainEvents(events)
				services, discoverErr := r.Discover(watchCtx, name)
				if !publish(services, discoverErr) {
					return
				}
				if !watchOpen {
					return
				}
			case <-resync.C:
				services, discoverErr := r.Discover(watchCtx, name)
				if !publish(services, discoverErr) {
					return
				}
			case <-staleTimerC:
				staleTimerC = nil
				services, discoverErr := r.Discover(watchCtx, name)
				if !publish(services, discoverErr) {
					return
				}
			}
		}
	}()
	return ch, nil
}

func filterServices(services []Service, filter ServiceFilter) []Service {
	if filter == nil {
		return services
	}
	filtered := make([]Service, 0, len(services))
	for _, svc := range services {
		if filter(cloneService(svc)) {
			filtered = append(filtered, svc)
		}
	}
	return filtered
}

func logStaleSnapshot(name string, snapshot UpstreamSnapshot) {
	slog.Warn("registry: upstream snapshot is stale",
		"service", name,
		"instances", len(snapshot.Services),
		"stale_since", snapshot.StaleSince,
	)
}

// Config for the registry.
type Config struct {
	Type                    string    `mapstructure:"type"`                    // "etcd" (default) or "consul"
	Endpoints               []string  `mapstructure:"endpoints"`               // etcd endpoints, e.g. ["127.0.0.1:2379"]
	Address                 string    `mapstructure:"address"`                 // consul address, e.g. "127.0.0.1:8500"
	Token                   string    `mapstructure:"token"`                   // consul ACL token (optional)
	Username                string    `mapstructure:"username"`                // etcd auth username (optional)
	Password                string    `mapstructure:"password"`                // etcd auth password (optional)
	Prefix                  string    `mapstructure:"prefix"`                  // key prefix, default "/mofang/services/"
	TTL                     int       `mapstructure:"ttl"`                     // lease/check TTL in seconds, default 30
	DialTimeout             int       `mapstructure:"dialTimeout"`             // dial timeout in seconds, default 5
	CheckType               string    `mapstructure:"checkType"`               // consul check: "ttl" (default) or "http"
	CheckPath               string    `mapstructure:"checkPath"`               // consul HTTP readiness path, default "/ready"
	CheckTLS                bool      `mapstructure:"checkTLS"`                // use HTTPS for Consul service health checks; independent from registry TLS
	DeregisterCriticalAfter int       `mapstructure:"deregisterCriticalAfter"` // consul critical deregister delay in seconds, default 300
	TLS                     TLSConfig `mapstructure:"tls"`
}

// TLSConfig configures TLS for etcd or Consul transport.
type TLSConfig struct {
	Enable     bool   `mapstructure:"enable"`
	CAFile     string `mapstructure:"caFile"`
	CertFile   string `mapstructure:"certFile"`
	KeyFile    string `mapstructure:"keyFile"`
	ServerName string `mapstructure:"serverName"`
}

func (c *Config) consulCheckType() (string, error) {
	checkType := strings.ToLower(strings.TrimSpace(c.CheckType))
	if checkType == "" {
		return "ttl", nil
	}
	if checkType != "ttl" && checkType != "http" {
		return "", fmt.Errorf("registry: unsupported consul check type %q (expected ttl or http)", c.CheckType)
	}
	return checkType, nil
}

func (c *Config) registryType() string {
	if value := strings.ToLower(strings.TrimSpace(c.Type)); value != "" {
		return value
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
	if value := strings.TrimSpace(c.Prefix); value != "" {
		if len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") || strings.Contains(value, "..") {
			return "/mofang/services/"
		}
		return strings.TrimRight(value, "/") + "/"
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

type upstreamState struct {
	lastGood   []Service
	staleSince time.Time
}

func (s *upstreamState) snapshot(services []Service, now time.Time, gracePeriod time.Duration) (UpstreamSnapshot, time.Duration) {
	services = normalizeServices(services)
	if len(services) > 0 {
		s.lastGood = cloneServices(services)
		s.staleSince = time.Time{}
		return UpstreamSnapshot{Services: services}, 0
	}
	if s.staleSince.IsZero() {
		s.staleSince = now
	}
	snapshot := UpstreamSnapshot{Stale: true, StaleSince: s.staleSince}
	remaining := gracePeriod - now.Sub(s.staleSince)
	if len(s.lastGood) > 0 && remaining > 0 {
		snapshot.Services = cloneServices(s.lastGood)
		return snapshot, remaining
	}
	return snapshot, 0
}

func drainEvents(events <-chan Event) bool {
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return false
			}
		default:
			return true
		}
	}
}

func sendSnapshot(ctx context.Context, ch chan<- UpstreamSnapshot, snapshot UpstreamSnapshot) bool {
	select {
	case ch <- snapshot:
		return true
	case <-ctx.Done():
		return false
	}
}

func normalizeServices(services []Service) []Service {
	byID := make(map[string]Service, len(services))
	for _, svc := range services {
		byID[svc.InstanceID] = svc
	}
	return mapServices(byID)
}

func cloneServices(services []Service) []Service {
	cloned := make([]Service, len(services))
	for i, svc := range services {
		cloned[i] = cloneService(svc)
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
	if err == nil && strings.TrimSpace(svc.Version) == "" && svc.Meta != nil {
		// Compatibility with registrations that encoded the release as a
		// metadata label before Version was introduced.
		svc.Version = strings.TrimSpace(svc.Meta["version"])
	}
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
	if err := validateServiceName(svc.Name); err != nil {
		return err
	}
	if len(svc.Version) > 128 || strings.ContainsAny(svc.Version, "\r\n\x00") {
		return fmt.Errorf("registry: invalid service version %q", svc.Version)
	}
	if svc.InstanceID == "" || len(svc.InstanceID) > 256 || strings.Contains(svc.InstanceID, "/") || strings.ContainsAny(svc.InstanceID, "\r\n\x00") {
		return fmt.Errorf("registry: invalid instance ID %q", svc.InstanceID)
	}
	if err := validateServiceAddr(svc.Addr, "addr"); err != nil {
		return err
	}
	if svc.GRPCAddr != "" {
		if err := validateServiceAddr(svc.GRPCAddr, "grpc addr"); err != nil {
			return err
		}
	}
	if len(svc.Meta) > 128 {
		return fmt.Errorf("registry: too many service metadata entries")
	}
	for key, value := range svc.Meta {
		if key == "" || len(key) > 128 || len(value) > 1024 || strings.ContainsAny(key+value, "\r\n\x00") {
			return fmt.Errorf("registry: invalid service metadata")
		}
	}
	return nil
}

func validateServiceName(name string) error {
	if name == "" || len(name) > 128 || strings.Contains(name, "/") || strings.ContainsAny(name, "\r\n\x00") {
		return fmt.Errorf("registry: invalid service name %q", name)
	}
	return nil
}

func validateServiceAddr(addr, field string) error {
	if addr == "" || len(addr) > 256 || strings.ContainsAny(addr, "\r\n\x00") {
		return fmt.Errorf("registry: invalid %s %q", field, addr)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("registry: parse %s %q: %w", field, addr, err)
	}
	if port == "" {
		return fmt.Errorf("registry: %s %q has no port", field, addr)
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("registry: invalid %s port %q", field, port)
	}
	return nil
}
