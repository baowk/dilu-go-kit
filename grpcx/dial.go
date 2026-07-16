// Package grpcx provides gRPC client utilities with automatic reconnection,
// traceId propagation, and initial connection readiness checks.
//
// Usage:
//
//	conn, err := grpcx.Dial("mf-workspace:7889")
//	defer conn.Close()
//	client := pb.NewWorkspaceServiceClient(conn)
package grpcx

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/baowk/dilu-go-kit/mid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// DialOption configures a gRPC client connection.
type DialOption struct {
	// Timeout for initial connection (default 5s)
	Timeout time.Duration
	// KeepaliveTime is how often to ping the server (default 30s)
	KeepaliveTime time.Duration
	// KeepaliveTimeout is how long to wait for a ping ack (default 10s)
	KeepaliveTimeout time.Duration
	// TLSConfig enables TLS transport credentials.
	TLSConfig *tls.Config
	// TransportCredentials overrides TLSConfig/insecure credentials.
	TransportCredentials credentials.TransportCredentials
	// RetryMaxAttempts is the gRPC retry max attempts (default 1, disabled).
	RetryMaxAttempts int
	// RetryMethods contains explicitly idempotent full method names, for example
	// "/package.Service/Get". It is required when RetryMaxAttempts is greater than 1.
	RetryMethods []string
	// RetryInitialBackoff is the initial gRPC retry backoff (default 100ms).
	RetryInitialBackoff time.Duration
	// RetryMaxBackoff is the max gRPC retry backoff (default 1s).
	RetryMaxBackoff time.Duration
	// RetryBackoffMultiplier is the retry backoff multiplier (default 2).
	RetryBackoffMultiplier float64
}

var defaultOpts = DialOption{
	Timeout:                5 * time.Second,
	KeepaliveTime:          30 * time.Second,
	KeepaliveTimeout:       10 * time.Second,
	RetryMaxAttempts:       1,
	RetryInitialBackoff:    100 * time.Millisecond,
	RetryMaxBackoff:        1 * time.Second,
	RetryBackoffMultiplier: 2,
}

// Dial creates a gRPC client connection with:
// - Automatic reconnection (built-in to gRPC)
// - TraceId propagation (unary + stream interceptors)
// - Keepalive pings
// - Insecure transport by default (for trusted internal service communication)
// - TLS or custom transport credentials when configured
func Dial(addr string, opts ...DialOption) (*grpc.ClientConn, error) {
	opt := normalizeDialOption(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), opt.Timeout)
	defer cancel()
	return dialContext(ctx, addr, opt)
}

// DialContext creates a connection and waits until it is ready or ctx expires.
func DialContext(ctx context.Context, addr string, opts ...DialOption) (*grpc.ClientConn, error) {
	opt := normalizeDialOption(opts...)
	if opt.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
	}
	return dialContext(ctx, addr, opt)
}

func dialContext(ctx context.Context, addr string, opt DialOption) (*grpc.ClientConn, error) {
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

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(opt.transportCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                opt.KeepaliveTime,
			Timeout:             opt.KeepaliveTimeout,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithChainUnaryInterceptor(mid.GRPCUnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(mid.GRPCStreamClientInterceptor()),
	)
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

func normalizeDialOption(opts ...DialOption) DialOption {
	opt := defaultOpts
	if len(opts) > 0 {
		opt = opts[0]
		if opt.Timeout == 0 {
			opt.Timeout = defaultOpts.Timeout
		}
		if opt.KeepaliveTime == 0 {
			opt.KeepaliveTime = defaultOpts.KeepaliveTime
		}
		if opt.KeepaliveTimeout == 0 {
			opt.KeepaliveTimeout = defaultOpts.KeepaliveTimeout
		}
		if opt.RetryMaxAttempts == 0 {
			opt.RetryMaxAttempts = defaultOpts.RetryMaxAttempts
		}
		if opt.RetryInitialBackoff == 0 {
			opt.RetryInitialBackoff = defaultOpts.RetryInitialBackoff
		}
		if opt.RetryMaxBackoff == 0 {
			opt.RetryMaxBackoff = defaultOpts.RetryMaxBackoff
		}
		if opt.RetryBackoffMultiplier == 0 {
			opt.RetryBackoffMultiplier = defaultOpts.RetryBackoffMultiplier
		}
	}
	return opt
}

func (o DialOption) transportCredentials() credentials.TransportCredentials {
	if o.TransportCredentials != nil {
		return o.TransportCredentials
	}
	if o.TLSConfig != nil {
		return credentials.NewTLS(o.TLSConfig)
	}
	return insecure.NewCredentials()
}

func (o DialOption) retryServiceConfig() string {
	config, _ := o.retryServiceConfigValidated()
	return config
}

func (o DialOption) retryServiceConfigValidated() (string, error) {
	if o.RetryMaxAttempts <= 1 {
		return `{"methodConfig":[]}`, nil
	}
	type methodName struct {
		Service string `json:"service"`
		Method  string `json:"method"`
	}
	type retryPolicy struct {
		MaxAttempts          int      `json:"maxAttempts"`
		InitialBackoff       string   `json:"initialBackoff"`
		MaxBackoff           string   `json:"maxBackoff"`
		BackoffMultiplier    float64  `json:"backoffMultiplier"`
		RetryableStatusCodes []string `json:"retryableStatusCodes"`
	}
	type methodConfig struct {
		Name        []methodName `json:"name"`
		RetryPolicy retryPolicy  `json:"retryPolicy"`
	}
	names := make([]methodName, 0, len(o.RetryMethods))
	for _, fullMethod := range o.RetryMethods {
		parts := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", fmt.Errorf("grpcx: invalid retry method %q", fullMethod)
		}
		names = append(names, methodName{Service: parts[0], Method: parts[1]})
	}
	config := struct {
		MethodConfig []methodConfig `json:"methodConfig"`
	}{MethodConfig: []methodConfig{{
		Name: names,
		RetryPolicy: retryPolicy{
			MaxAttempts:          o.RetryMaxAttempts,
			InitialBackoff:       durationString(o.RetryInitialBackoff),
			MaxBackoff:           durationString(o.RetryMaxBackoff),
			BackoffMultiplier:    o.RetryBackoffMultiplier,
			RetryableStatusCodes: []string{"UNAVAILABLE", "RESOURCE_EXHAUSTED"},
		},
	}}}
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("grpcx: encode retry config: %w", err)
	}
	return string(data), nil
}

func (o DialOption) validate() error {
	if o.Timeout <= 0 || o.KeepaliveTime <= 0 || o.KeepaliveTimeout <= 0 {
		return fmt.Errorf("grpcx: timeout and keepalive durations must be positive")
	}
	if o.RetryMaxAttempts < 1 || o.RetryMaxAttempts > 5 {
		return fmt.Errorf("grpcx: retry max attempts must be between 1 and 5")
	}
	if o.RetryMaxAttempts > 1 && len(o.RetryMethods) == 0 {
		return fmt.Errorf("grpcx: retry methods are required when retries are enabled")
	}
	if o.RetryInitialBackoff <= 0 || o.RetryMaxBackoff <= 0 || o.RetryBackoffMultiplier <= 0 {
		return fmt.Errorf("grpcx: retry backoff values must be positive")
	}
	return nil
}

func durationString(d time.Duration) string {
	return fmt.Sprintf("%.3fs", d.Seconds())
}
