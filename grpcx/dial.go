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
}

var defaultOpts = DialOption{
	Timeout:          5 * time.Second,
	KeepaliveTime:    30 * time.Second,
	KeepaliveTimeout: 10 * time.Second,
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
		grpc.WithDefaultServiceConfig(`{
			"methodConfig": [{
				"name": [{}],
				"retryPolicy": {
					"maxAttempts": 3,
					"initialBackoff": "0.1s",
					"maxBackoff": "1s",
					"backoffMultiplier": 2,
					"retryableStatusCodes": ["UNAVAILABLE", "DEADLINE_EXCEEDED"]
				}
			}]
		}`),
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
