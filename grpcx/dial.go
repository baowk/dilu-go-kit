// Package grpcx provides gRPC client utilities with automatic reconnection,
// traceId propagation, and health checking.
//
// Usage:
//
//	conn, err := grpcx.Dial("mf-workspace:7889")
//	defer conn.Close()
//	client := pb.NewWorkspaceServiceClient(conn)
package grpcx

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/baowk/dilu-go-kit/mid"
	"google.golang.org/grpc"
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
	// RetryMaxAttempts is the gRPC retry max attempts (default 3; set 1 to disable retries).
	RetryMaxAttempts int
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
	RetryMaxAttempts:       3,
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

	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(opt.transportCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                opt.KeepaliveTime,
			Timeout:             opt.KeepaliveTimeout,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultServiceConfig(opt.retryServiceConfig()),
		grpc.WithChainUnaryInterceptor(mid.GRPCUnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(mid.GRPCStreamClientInterceptor()),
	)
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
	if o.RetryMaxAttempts <= 1 {
		return `{"methodConfig":[{"name":[{}]}]}`
	}
	return fmt.Sprintf(`{
		"methodConfig": [{
			"name": [{}],
			"retryPolicy": {
				"maxAttempts": %d,
				"initialBackoff": %q,
				"maxBackoff": %q,
				"backoffMultiplier": %g,
				"retryableStatusCodes": ["UNAVAILABLE", "DEADLINE_EXCEEDED", "RESOURCE_EXHAUSTED"]
			}
		}]
	}`, o.RetryMaxAttempts, durationString(o.RetryInitialBackoff), durationString(o.RetryMaxBackoff), o.RetryBackoffMultiplier)
}

func durationString(d time.Duration) string {
	return fmt.Sprintf("%.3fs", d.Seconds())
}
