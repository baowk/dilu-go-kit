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
	"time"

	"github.com/baowk/dilu-go-kit/mid"
	"google.golang.org/grpc"
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
// - Insecure transport (for internal service communication)
func Dial(addr string, opts ...DialOption) (*grpc.ClientConn, error) {
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

	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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
