package mid

import (
	"context"

	"github.com/baowk/dilu-go-kit/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const grpcTraceKey = "x-trace-id"

// GRPCUnaryClientInterceptor injects trace_id from context into gRPC metadata
// for outgoing unary calls. Use when dialing another service.
//
//	conn, _ := grpc.NewClient(addr, grpc.WithUnaryInterceptor(mid.GRPCUnaryClientInterceptor()))
func GRPCUnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if traceID := log.GetTraceID(ctx); traceID != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, grpcTraceKey, traceID)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// GRPCStreamClientInterceptor injects trace_id for outgoing stream calls.
func GRPCStreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if traceID := log.GetTraceID(ctx); traceID != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, grpcTraceKey, traceID)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// GRPCUnaryServerInterceptor extracts trace_id from incoming gRPC metadata
// and stores it in the context. Use when registering a gRPC server.
//
//	grpc.NewServer(grpc.UnaryInterceptor(mid.GRPCUnaryServerInterceptor()))
func GRPCUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = extractTraceFromMetadata(ctx)
		return handler(ctx, req)
	}
}

// GRPCStreamServerInterceptor extracts trace_id for incoming stream calls.
func GRPCStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := extractTraceFromMetadata(ss.Context())
		wrapped := &wrappedStream{ServerStream: ss, ctx: ctx}
		return handler(srv, wrapped)
	}
}

func extractTraceFromMetadata(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	vals := md.Get(grpcTraceKey)
	if len(vals) > 0 && vals[0] != "" {
		ctx = log.WithTraceID(ctx, vals[0])
	}
	return ctx
}

// wrappedStream overrides Context() to return our enriched context.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
