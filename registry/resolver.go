package registry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BalancePolicy controls instance selection.
type BalancePolicy int

const (
	RoundRobin BalancePolicy = iota
	Random
)

// ResolverConfig configures a client-side service resolver.
type ResolverConfig struct {
	Registry Registry
	Service  string
	Policy   BalancePolicy
	// Version optionally restricts resolution to a service release.
	Version string
	// Metadata optionally requires exact key/value labels on instances.
	Metadata map[string]string
	// Filter is an additional custom instance filter.
	Filter                  ServiceFilter
	StaleGracePeriod        time.Duration
	ResyncInterval          time.Duration
	InitialDiscoveryTimeout time.Duration
}

// Resolver watches a service and selects healthy instances from a local cache.
type Resolver struct {
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.RWMutex
	services []Service
	policy   BalancePolicy
	next     atomic.Uint64
}

// NewResolver starts watching the configured service. Initial discovery is
// performed synchronously by WatchUpstreamsWithOptions, so a successful return
// always includes an initial snapshot.
func NewResolver(ctx context.Context, cfg ResolverConfig) (*Resolver, error) {
	if cfg.Registry == nil {
		return nil, errors.New("registry: resolver registry is nil")
	}
	if strings.TrimSpace(cfg.Service) == "" {
		return nil, errors.New("registry: resolver service is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	watchCtx, cancel := context.WithCancel(ctx)
	snapshots, err := WatchUpstreamsWithOptions(watchCtx, cfg.Registry, cfg.Service, WatchUpstreamsOptions{
		StaleGracePeriod:        cfg.StaleGracePeriod,
		ResyncInterval:          cfg.ResyncInterval,
		InitialDiscoveryTimeout: cfg.InitialDiscoveryTimeout,
		Filter:                  AndFilters(VersionFilter(cfg.Version), MetadataFilter(cfg.Metadata), cfg.Filter),
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("resolver watch %s: %w", cfg.Service, err)
	}
	r := &Resolver{cancel: cancel, done: make(chan struct{}), policy: cfg.Policy}
	go func() {
		defer close(r.done)
		for snapshot := range snapshots {
			r.mu.Lock()
			r.services = append([]Service(nil), snapshot.Services...)
			r.mu.Unlock()
		}
	}()
	return r, nil
}

// Resolve selects one currently available instance.
func (r *Resolver) Resolve() (Service, error) {
	if r == nil {
		return Service{}, errors.New("registry: resolver is nil")
	}
	r.mu.RLock()
	services := append([]Service(nil), r.services...)
	r.mu.RUnlock()
	if len(services) == 0 {
		return Service{}, fmt.Errorf("registry: no healthy instances")
	}
	var idx int
	if r.policy == Random {
		idx = rand.IntN(len(services))
	} else {
		idx = int(r.next.Add(1)-1) % len(services)
	}
	return cloneService(services[idx]), nil
}

// Snapshot returns a copy of the currently cached instances.
func (r *Resolver) Snapshot() []Service {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneServices(r.services)
}

// Close stops watching and waits for the watcher to exit.
func (r *Resolver) Close() error {
	if r == nil {
		return nil
	}
	r.cancel()
	<-r.done
	return nil
}
