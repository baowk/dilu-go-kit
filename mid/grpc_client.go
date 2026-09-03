package mid

import (
	"context"
	"io"
	"time"

	"google.golang.org/grpc"
)

// GRPCUnaryClientTimeout applies a deadline when an outgoing call has none.
func GRPCUnaryClientTimeout(timeout time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if timeout <= 0 {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		if _, ok := ctx.Deadline(); ok {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return invoker(callCtx, method, req, reply, cc, opts...)
	}
}

// GRPCStreamClientTimeout applies a deadline when an outgoing stream has none.
func GRPCStreamClientTimeout(timeout time.Duration) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if timeout <= 0 {
			return streamer(ctx, desc, cc, method, opts...)
		}
		if _, ok := ctx.Deadline(); ok {
			return streamer(ctx, desc, cc, method, opts...)
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		// grpc.ClientStream owns the call context; cancellation occurs when the
		// stream is closed by the caller. A timer fallback prevents leaks if a
		// broken caller never closes it.
		stream, err := streamer(callCtx, desc, cc, method, opts...)
		if err != nil {
			cancel()
			return nil, err
		}
		return &cancelClientStream{ClientStream: stream, cancel: cancel}, nil
	}
}

type cancelClientStream struct {
	grpc.ClientStream
	cancel context.CancelFunc
}

func (s *cancelClientStream) CloseSend() error {
	err := s.ClientStream.CloseSend()
	s.cancel()
	return err
}

func (s *cancelClientStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if err == io.EOF {
		s.cancel()
	}
	return err
}
