package mid

import (
	"context"
	"fmt"
	"time"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServerMiddleware is a transport-neutral middleware adapter. A middleware
// may implement only the transports it needs; nil handlers are ignored by the
// corresponding chain helpers.
type ServerMiddleware interface {
	HTTP() gin.HandlerFunc
	UnaryServer() grpc.UnaryServerInterceptor
	StreamServer() grpc.StreamServerInterceptor
}

// MiddlewareAdapter adapts optional HTTP and gRPC handlers to ServerMiddleware.
type MiddlewareAdapter struct {
	HTTPHandler   gin.HandlerFunc
	UnaryHandler  grpc.UnaryServerInterceptor
	StreamHandler grpc.StreamServerInterceptor
}

func (m MiddlewareAdapter) HTTP() gin.HandlerFunc                      { return m.HTTPHandler }
func (m MiddlewareAdapter) UnaryServer() grpc.UnaryServerInterceptor   { return m.UnaryHandler }
func (m MiddlewareAdapter) StreamServer() grpc.StreamServerInterceptor { return m.StreamHandler }

// GRPCAuthFunc authenticates a method and may return an enriched context.
// Authentication should derive identity from verified credentials, never from
// arbitrary metadata supplied by a caller.
type GRPCAuthFunc func(context.Context, string) (context.Context, error)

func GRPCUnaryAuth(auth GRPCAuthFunc) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if auth == nil {
			return handler(ctx, req)
		}
		enriched, err := auth(ctx, info.FullMethod)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		}
		return handler(enriched, req)
	}
}

func GRPCStreamAuth(auth GRPCAuthFunc) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if auth == nil {
			return handler(srv, stream)
		}
		ctx, err := auth(stream.Context(), info.FullMethod)
		if err != nil {
			return status.Error(codes.Unauthenticated, "authentication required")
		}
		return handler(srv, &wrappedStream{ServerStream: stream, ctx: ctx})
	}
}

// GRPCUnaryTimeout applies a deadline only when the caller did not provide
// one. This prevents a framework default from extending an explicit deadline.
func GRPCUnaryTimeout(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if timeout <= 0 {
			return handler(ctx, req)
		}
		if _, ok := ctx.Deadline(); ok {
			return handler(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return handler(ctx, req)
	}
}

func GRPCStreamTimeout(timeout time.Duration) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if timeout <= 0 {
			return handler(srv, stream)
		}
		if _, ok := stream.Context().Deadline(); ok {
			return handler(srv, stream)
		}
		ctx, cancel := context.WithTimeout(stream.Context(), timeout)
		defer cancel()
		return handler(srv, &wrappedStream{ServerStream: stream, ctx: ctx})
	}
}

// GRPCConcurrencyLimiter bounds in-flight RPC handlers. It fails fast with
// ResourceExhausted when the limit is reached instead of queueing unbounded
// work.
type GRPCConcurrencyLimiter struct{ sem chan struct{} }

func NewGRPCConcurrencyLimiter(limit int) *GRPCConcurrencyLimiter {
	if limit <= 0 {
		return nil
	}
	return &GRPCConcurrencyLimiter{sem: make(chan struct{}, limit)}
}

func (l *GRPCConcurrencyLimiter) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if l == nil || l.sem == nil {
			return handler(ctx, req)
		}
		select {
		case l.sem <- struct{}{}:
			defer func() { <-l.sem }()
			return handler(ctx, req)
		default:
			return nil, status.Error(codes.ResourceExhausted, "server busy")
		}
	}
}

func (l *GRPCConcurrencyLimiter) Stream() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if l == nil || l.sem == nil {
			return handler(srv, stream)
		}
		select {
		case l.sem <- struct{}{}:
			defer func() { <-l.sem }()
			return handler(srv, stream)
		default:
			return status.Error(codes.ResourceExhausted, "server busy")
		}
	}
}

// GRPCUnaryAccessLog records method, duration and stable gRPC status code.
func GRPCUnaryAccessLog() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		started := time.Now()
		resp, err = handler(ctx, req)
		code := status.Code(err)
		log.InfoContext(ctx, "grpc request", "method", info.FullMethod, "code", code.String(), "latency_ms", time.Since(started).Milliseconds())
		return resp, err
	}
}

// ValidateGRPCTimeout provides a consistent configuration error for callers.
func ValidateGRPCTimeout(timeout time.Duration) error {
	if timeout < 0 {
		return fmt.Errorf("grpc request timeout must not be negative")
	}
	return nil
}
