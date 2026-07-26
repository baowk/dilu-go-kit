package stream

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRunGroupConsumerBacksOffWhenRedisDialFailsImmediately(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:       "redis.invalid:6379",
		MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("connection refused")
		},
	})
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Unix(0, 0)
	var delays []time.Duration
	var failures []PollFailure
	handled := 0
	options := consumerLoopOptions{}
	options.now = func() time.Time { return now }
	options.jitter = func(delay time.Duration) time.Duration { return delay }
	options.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		now = now.Add(delay)
		if len(delays) == 3 {
			cancel()
		}
		return nil
	}
	options.onFailure = func(report PollFailure) { failures = append(failures, report) }

	config := GroupConsumerConfig{
		Name: "dial-failure-test", Stream: "events", Group: "workers", Consumer: "worker-1",
		Block: time.Millisecond, loopOptions: options,
	}
	RunGroupConsumer(ctx, client, config, func(context.Context, []Message) { handled++ })

	wantDelays := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second}
	if !reflect.DeepEqual(delays, wantDelays) {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
	if len(failures) != 1 || failures[0].Attempt != 1 || failures[0].Err == nil {
		t.Fatalf("failure reports = %+v, want one initial report", failures)
	}
	if handled != 0 {
		t.Fatalf("handler calls = %d, want 0", handled)
	}
}

func TestRunConsumerLoopCapsBackoffAndRateLimitsEquivalentFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Unix(0, 0)
	var delays []time.Duration
	var failures []PollFailure
	options := consumerLoopOptions{
		initialBackoff: 250 * time.Millisecond,
		maxBackoff:     30 * time.Second,
		reportInterval: time.Minute,
		now:            func() time.Time { return now },
		jitter:         func(delay time.Duration) time.Duration { return delay },
		onFailure:      func(report PollFailure) { failures = append(failures, report) },
		onRecovery:     func(PollRecovery) {},
	}
	options.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		now = now.Add(delay)
		if len(delays) == 9 {
			cancel()
		}
		return nil
	}

	err := runConsumerLoop(ctx, options, func(context.Context) error {
		return pollError{operation: "read_group", err: errors.New("connection refused")}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context canceled", err)
	}
	wantDelays := []time.Duration{
		250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second,
		4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	if !reflect.DeepEqual(delays, wantDelays) {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
	if len(failures) != 2 || failures[1].Suppressed != 7 {
		t.Fatalf("failure reports = %+v, want initial and one summary with 7 suppressed", failures)
	}
}

func TestRunConsumerLoopResetsBackoffAfterSuccessfulPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Unix(0, 0)
	var delays []time.Duration
	var recoveries []PollRecovery
	polls := 0
	options := consumerLoopOptions{
		initialBackoff: 250 * time.Millisecond,
		maxBackoff:     30 * time.Second,
		reportInterval: time.Minute,
		now:            func() time.Time { return now },
		jitter:         func(delay time.Duration) time.Duration { return delay },
		onFailure:      func(PollFailure) {},
		onRecovery:     func(report PollRecovery) { recoveries = append(recoveries, report) },
	}
	options.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		now = now.Add(delay)
		if len(delays) == 3 {
			cancel()
		}
		return nil
	}

	err := runConsumerLoop(ctx, options, func(context.Context) error {
		polls++
		if polls == 3 {
			return nil
		}
		return errors.New("redis unavailable")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context canceled", err)
	}
	wantDelays := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 250 * time.Millisecond}
	if !reflect.DeepEqual(delays, wantDelays) {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
	if len(recoveries) != 1 || recoveries[0].Attempts != 2 {
		t.Fatalf("recoveries = %+v, want one recovery after two failures", recoveries)
	}
}
