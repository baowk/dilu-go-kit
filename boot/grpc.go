package boot

import (
	"context"

	"github.com/baowk/dilu-go-kit/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const grpcTraceKey = "x-trace-id"

// grpcUnaryServerTrace extracts trace_id from gRPC metadata into context.
func grpcUnaryServerTrace() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = extractTrace(ctx)
		return handler(ctx, req)
	}
}

// grpcStreamServerTrace extracts trace_id for stream calls.
func grpcStreamServerTrace() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := extractTrace(ss.Context())
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

func extractTrace(ctx context.Context) context.Context {
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

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
