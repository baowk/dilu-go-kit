package clientx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/baowk/dilu-go-kit/resp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDoRetriesRetryableError(t *testing.T) {
	calls := 0
	err := Do(context.Background(), RetryConfig{
		MaxAttempts: 3,
		Sleep:       func(context.Context, time.Duration) error { return nil },
	}, func(context.Context) error {
		calls++
		if calls < 3 {
			return status.Error(codes.Unavailable, "down")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestDoDoesNotRetryNonRetryableError(t *testing.T) {
	calls := 0
	err := Do(context.Background(), RetryConfig{
		MaxAttempts: 3,
		Sleep:       func(context.Context, time.Duration) error { return nil },
	}, func(context.Context) error {
		calls++
		return status.Error(codes.InvalidArgument, "bad")
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestBreakerOpensAfterFailuresAndHalfOpensAfterCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	b := NewBreaker(BreakerConfig{
		FailureThreshold: 2,
		Cooldown:         time.Second,
		Now:              func() time.Time { return now },
	})

	fail := errors.New("fail")
	_ = b.Do(context.Background(), func(context.Context) error { return fail })
	_ = b.Do(context.Background(), func(context.Context) error { return fail })
	if b.State() != BreakerOpen {
		t.Fatalf("state = %v", b.State())
	}
	if err := b.Do(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected circuit open, got %v", err)
	}
	now = now.Add(time.Second)
	if b.State() != BreakerHalfOpen {
		t.Fatalf("state = %v", b.State())
	}
	if err := b.Do(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("half-open call error: %v", err)
	}
	if b.State() != BreakerClosed {
		t.Fatalf("state = %v", b.State())
	}
}

func TestMapGRPCError(t *testing.T) {
	err := MapGRPCError(status.Error(codes.NotFound, "missing"))
	if CodeOf(err) != resp.CodeNotFound {
		t.Fatalf("code = %d", CodeOf(err))
	}

	err = MapGRPCError(status.Error(codes.Unavailable, "down"))
	if CodeOf(err) != resp.CodeServiceDown {
		t.Fatalf("code = %d", CodeOf(err))
	}
}
