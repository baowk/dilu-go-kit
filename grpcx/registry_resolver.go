package grpcx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/baowk/dilu-go-kit/mid"
	kitregistry "github.com/baowk/dilu-go-kit/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
	grpcresolver "google.golang.org/grpc/resolver"
)

// RegistryResolverBuilder adapts a dilu registry to gRPC's resolver API.
// Targets use the form registry:///service-name. The builder keeps the gRPC
// ClientConn updated as instances register, deregister, or become unhealthy.
type RegistryResolverBuilder struct {
	Registry kitregistry.Registry
	// InitialDiscoveryTimeout bounds the synchronous Watch/Discover calls used
	// to seed the resolver. Zero uses five seconds.
	InitialDiscoveryTimeout time.Duration
	// Version optionally restricts discovery to a service release.
	Version string
	// Metadata optionally requires exact instance labels.
	Metadata map[string]string
	// ServiceFilter provides additional custom routing policy.
	ServiceFilter kitregistry.ServiceFilter
	// Filter is an alias for ServiceFilter for consistency with registry
	// WatchUpstreamsOptions.
	Filter           kitregistry.ServiceFilter
	StaleGracePeriod time.Duration
	ResyncInterval   time.Duration
}

// Scheme returns the target scheme accepted by this builder.
func (b *RegistryResolverBuilder) Scheme() string { return "registry" }

// Build starts watching the target service and publishes its gRPC addresses.
func (b *RegistryResolverBuilder) Build(target grpcresolver.Target, cc grpcresolver.ClientConn, _ grpcresolver.BuildOptions) (grpcresolver.Resolver, error) {
	if b == nil || b.Registry == nil {
		return nil, errors.New("grpcx: registry resolver requires a registry")
	}
	service := strings.TrimSpace(target.Endpoint())
	if service == "" || strings.ContainsAny(service, "/\r\n\x00") {
		return nil, fmt.Errorf("grpcx: invalid registry service %q", service)
	}
	ctx, cancel := context.WithCancel(context.Background())
	snapshots, err := kitregistry.WatchUpstreamsWithOptions(ctx, b.Registry, service, kitregistry.WatchUpstreamsOptions{
		StaleGracePeriod:        b.StaleGracePeriod,
		ResyncInterval:          b.ResyncInterval,
		InitialDiscoveryTimeout: b.InitialDiscoveryTimeout,
		Filter: kitregistry.AndFilters(
			kitregistry.VersionFilter(b.Version),
			kitregistry.MetadataFilter(b.Metadata),
			b.ServiceFilter,
			b.Filter,
		),
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grpcx: watch registry service %s: %w", service, err)
	}
	initial, ok := <-snapshots
	if !ok {
		cancel()
		return nil, fmt.Errorf("grpcx: registry service %s returned no initial snapshot", service)
	}
	if err := updateResolverState(cc, initial.Services); err != nil {
		cancel()
		return nil, err
	}
	r := &registryResolver{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for snapshot := range snapshots {
			if err := updateResolverState(cc, snapshot.Services); err != nil {
				cc.ReportError(err)
			}
		}
	}()
	return r, nil
}

type registryResolver struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (r *registryResolver) ResolveNow(grpcresolver.ResolveNowOptions) {}

func (r *registryResolver) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
	})
}

func updateResolverState(cc grpcresolver.ClientConn, services []kitregistry.Service) error {
	addresses := make([]grpcresolver.Address, 0, len(services))
	for _, service := range services {
		addr := strings.TrimSpace(service.GRPCAddr)
		if addr == "" {
			addr = strings.TrimSpace(service.Addr)
		}
		if addr == "" || strings.ContainsAny(addr, "\r\n\x00") {
			continue
		}
		addresses = append(addresses, grpcresolver.Address{Addr: addr})
	}
	if err := grpcresolver.ValidateEndpoints([]grpcresolver.Endpoint{{Addresses: addresses}}); err != nil {
		// An empty snapshot is a valid transient state. gRPC will move the
		// connection to transient failure until the next registry update.
		if len(addresses) == 0 {
			return cc.UpdateState(grpcresolver.State{Addresses: nil})
		}
		return err
	}
	return cc.UpdateState(grpcresolver.State{Addresses: addresses})
}

// DialService dials a service by registry name and uses gRPC round_robin over
// the currently healthy instances. It preserves all DialOption behavior,
// including TLS, retries, keepalive, and telemetry.
func DialService(ctx context.Context, r kitregistry.Registry, service string, opts ...DialOption) (*grpc.ClientConn, error) {
	service = strings.TrimSpace(service)
	if r == nil {
		return nil, errors.New("grpcx: registry is nil")
	}
	if service == "" || strings.ContainsAny(service, "/\r\n\x00") {
		return nil, fmt.Errorf("grpcx: invalid registry service %q", service)
	}
	opt := normalizeDialOption(opts...)
	return dialContextWithResolver(ctx, "registry:///"+service, opt, &RegistryResolverBuilder{
		Registry:                r,
		InitialDiscoveryTimeout: opt.Timeout,
		Version:                 opt.Version,
		Metadata:                opt.Metadata,
		ServiceFilter:           opt.ServiceFilter,
		Filter:                  opt.Filter,
	})
}

func dialContextWithResolver(ctx context.Context, addr string, opt DialOption, builder grpcresolver.Builder) (*grpc.ClientConn, error) {
	if builder == nil {
		return dialContext(ctx, addr, opt)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opt.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
	}
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("grpcx: empty address")
	}
	if err := opt.validate(); err != nil {
		return nil, err
	}
	serviceConfig, err := opt.retryServiceConfigValidated()
	if err != nil {
		return nil, err
	}
	serviceConfig = withRoundRobin(serviceConfig)
	grpcOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(opt.transportCredentials()),
		grpc.WithResolvers(builder),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: opt.KeepaliveTime, Timeout: opt.KeepaliveTimeout, PermitWithoutStream: true,
		}),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithChainUnaryInterceptor(mid.GRPCUnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(mid.GRPCStreamClientInterceptor()),
	}
	if opt.Telemetry != nil {
		grpcOptions = append(grpcOptions, opt.Telemetry.GRPCDialOption())
	}
	conn, err := grpc.NewClient(addr, grpcOptions...)
	if err != nil {
		return nil, err
	}
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return conn, nil
		}
		if state == connectivity.Shutdown {
			_ = conn.Close()
			return nil, fmt.Errorf("grpcx: connection shut down before ready")
		}
		if !conn.WaitForStateChange(ctx, state) {
			_ = conn.Close()
			return nil, fmt.Errorf("grpcx: connect %s: %w", addr, ctx.Err())
		}
	}
}

// withRoundRobin augments the existing retry service config without changing
// the stable JSON emitted by Dial for direct-address connections.
func withRoundRobin(serviceConfig string) string {
	var config map[string]any
	if json.Unmarshal([]byte(serviceConfig), &config) != nil {
		return serviceConfig
	}
	config["loadBalancingConfig"] = []map[string]any{{"round_robin": map[string]any{}}}
	data, err := json.Marshal(config)
	if err != nil {
		return serviceConfig
	}
	return string(data)
}
