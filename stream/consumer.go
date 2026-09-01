package stream

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	kitlog "github.com/baowk/dilu-go-kit/log"
	"github.com/redis/go-redis/v9"
)

const (
	defaultConsumerInitialBackoff = 250 * time.Millisecond
	defaultConsumerMaxBackoff     = 30 * time.Second
	defaultConsumerReportInterval = time.Minute
	defaultConsumerJitterFraction = 0.2
	defaultClaimInterval          = 30 * time.Second
	defaultClaimMinIdle           = 30 * time.Second
)

// PollFailure describes a reported consumer polling failure. Suppressed is
// the number of equivalent failures omitted since the preceding report.
type PollFailure struct {
	Operation  string
	Err        error
	Attempt    int
	Delay      time.Duration
	Suppressed int
}

// PollRecovery summarizes a consumer outage after polling becomes healthy.
type PollRecovery struct {
	Attempts   int
	Suppressed int
	Duration   time.Duration
}

// GroupConsumerConfig configures a long-running Redis Stream consumer group.
// RunGroupConsumer keeps the one-shot ReadGroup and ClaimStale APIs unchanged.
type GroupConsumerConfig struct {
	Name            string
	Stream          string
	Group           string
	Consumer        string
	StartID         string
	Count           int64
	Block           time.Duration
	ClaimInterval   time.Duration
	ClaimMinIdle    time.Duration
	SkipEnsureGroup bool
	DisableClaim    bool

	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	ReportInterval time.Duration
	JitterFraction float64
	DisableJitter  bool

	OnFailure  func(context.Context, PollFailure)
	OnRecovery func(context.Context, PollRecovery)

	loopOptions consumerLoopOptions
}

// RunGroupConsumer ensures the group, reads new messages, and periodically
// claims stale pending messages until ctx is cancelled. Redis failures use
// jittered exponential backoff and rate-limited reporting.
func RunGroupConsumer(
	ctx context.Context,
	client redis.Cmdable,
	config GroupConsumerConfig,
	handle func(context.Context, []Message),
) error {
	if err := validateGroupConsumer(client, config, handle); err != nil {
		return err
	}
	config = normalizeGroupConsumer(config)
	options := consumerOptions(ctx, config)
	groupReady := config.SkipEnsureGroup
	claimCursor := ""

	var claimTicker *time.Ticker
	if !config.DisableClaim {
		claimTicker = time.NewTicker(config.ClaimInterval)
		defer claimTicker.Stop()
	}

	return runConsumerLoop(ctx, options, func(ctx context.Context) error {
		if !groupReady {
			if err := EnsureGroup(ctx, client, config.Stream, config.Group, config.StartID); err != nil {
				return pollError{operation: "ensure_group", err: err}
			}
			groupReady = true
		}

		messages, err := ReadGroup(ctx, client, ReaderConfig{
			Stream: config.Stream, Group: config.Group, Consumer: config.Consumer,
			Count: config.Count, Block: config.Block,
		})
		if err != nil {
			if strings.Contains(err.Error(), "NOGROUP") {
				groupReady = false
			}
			return pollError{operation: "read_group", err: err}
		}
		if len(messages) > 0 {
			if err := invokeHandler(ctx, handle, messages); err != nil {
				return pollError{operation: "handle", err: err}
			}
		}

		if claimTicker == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-claimTicker.C:
			stale, next, err := ClaimStale(ctx, client, ClaimConfig{
				Stream: config.Stream, Group: config.Group, Consumer: config.Consumer,
				MinIdle: config.ClaimMinIdle, Count: config.Count, Start: claimCursor,
			})
			if err != nil {
				return pollError{operation: "claim_stale", err: err}
			}
			claimCursor = next
			if len(stale) > 0 {
				if err := invokeHandler(ctx, handle, stale); err != nil {
					return pollError{operation: "handle", err: err}
				}
			}
		default:
		}
		return nil
	})
}

func invokeHandler(ctx context.Context, handle func(context.Context, []Message), messages []Message) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("stream: consumer handler panic")
			kitlog.ErrorContext(ctx, "stream consumer handler panic", "panic", recovered)
		}
	}()
	if len(messages) > 0 {
		ctx = ContextForMessage(ctx, messages[0])
	}
	handle(ctx, messages)
	return nil
}

func validateGroupConsumer(client redis.Cmdable, config GroupConsumerConfig, handle func(context.Context, []Message)) error {
	if client == nil {
		return errors.New("stream: consumer redis client is required")
	}
	if strings.TrimSpace(config.Stream) == "" || strings.TrimSpace(config.Group) == "" || strings.TrimSpace(config.Consumer) == "" {
		return errors.New("stream: consumer stream, group, and consumer are required")
	}
	if handle == nil {
		return errors.New("stream: consumer handler is required")
	}
	return nil
}

func normalizeGroupConsumer(config GroupConsumerConfig) GroupConsumerConfig {
	if config.Name == "" {
		config.Name = config.Group + ":" + config.Stream
	}
	if config.StartID == "" {
		config.StartID = "0"
	}
	if config.Count <= 0 {
		config.Count = DefaultCount
	}
	if config.Block <= 0 {
		config.Block = DefaultBlock
	}
	if config.ClaimInterval <= 0 {
		config.ClaimInterval = defaultClaimInterval
	}
	if config.ClaimInterval > 24*time.Hour {
		config.ClaimInterval = 24 * time.Hour
	}
	if config.ClaimMinIdle <= 0 {
		config.ClaimMinIdle = defaultClaimMinIdle
	}
	if config.ClaimMinIdle > 24*time.Hour {
		config.ClaimMinIdle = 24 * time.Hour
	}
	if config.InitialBackoff <= 0 {
		config.InitialBackoff = defaultConsumerInitialBackoff
	}
	if config.InitialBackoff > 1*time.Minute {
		config.InitialBackoff = 1 * time.Minute
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = defaultConsumerMaxBackoff
	}
	if config.MaxBackoff > 10*time.Minute {
		config.MaxBackoff = 10 * time.Minute
	}
	if config.MaxBackoff < config.InitialBackoff {
		config.MaxBackoff = config.InitialBackoff
	}
	if config.ReportInterval <= 0 {
		config.ReportInterval = defaultConsumerReportInterval
	}
	if config.DisableJitter {
		config.JitterFraction = 0
	} else if config.JitterFraction <= 0 {
		config.JitterFraction = defaultConsumerJitterFraction
	}
	if config.JitterFraction > 1 {
		config.JitterFraction = 1
	}
	return config
}

func consumerOptions(ctx context.Context, config GroupConsumerConfig) consumerLoopOptions {
	options := config.loopOptions
	options.name = config.Name
	options.initialBackoff = config.InitialBackoff
	options.maxBackoff = config.MaxBackoff
	options.reportInterval = config.ReportInterval
	if options.jitter == nil {
		options.jitter = func(delay time.Duration) time.Duration {
			return jitterConsumerBackoff(delay, config.JitterFraction)
		}
	}
	if options.onFailure == nil {
		if config.OnFailure != nil {
			options.onFailure = func(report PollFailure) { config.OnFailure(ctx, report) }
		} else {
			options.onFailure = func(report PollFailure) {
				kitlog.ErrorContext(ctx, "stream consumer polling failed",
					"consumer", config.Name, "operation", report.Operation,
					"attempt", report.Attempt, "retry_in", report.Delay,
					"suppressed", report.Suppressed, "error", report.Err)
			}
		}
	}
	if options.onRecovery == nil {
		if config.OnRecovery != nil {
			options.onRecovery = func(report PollRecovery) { config.OnRecovery(ctx, report) }
		} else {
			options.onRecovery = func(report PollRecovery) {
				kitlog.InfoContext(ctx, "stream consumer polling recovered",
					"consumer", config.Name, "attempts", report.Attempts,
					"suppressed", report.Suppressed, "outage_duration", report.Duration)
			}
		}
	}
	return options
}

type pollError struct {
	operation string
	err       error
}

func (e pollError) Error() string { return e.operation + ": " + e.err.Error() }
func (e pollError) Unwrap() error { return e.err }

type consumerLoopOptions struct {
	name           string
	initialBackoff time.Duration
	maxBackoff     time.Duration
	reportInterval time.Duration
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	jitter         func(time.Duration) time.Duration
	onFailure      func(PollFailure)
	onRecovery     func(PollRecovery)
}

func runConsumerLoop(ctx context.Context, options consumerLoopOptions, poll func(context.Context) error) error {
	if poll == nil {
		return errors.New("stream: consumer poll function is required")
	}
	if options.now == nil {
		options.now = time.Now
	}
	if options.sleep == nil {
		options.sleep = sleepConsumerContext
	}
	backoff := options.initialBackoff
	attempts := 0
	totalSuppressed := 0
	sinceReport := 0
	reported := false
	lastReport := time.Time{}
	lastError := ""
	outageStarted := time.Time{}

	for ctx.Err() == nil {
		err := poll(ctx)
		if err == nil {
			if attempts > 0 {
				options.onRecovery(PollRecovery{Attempts: attempts, Suppressed: totalSuppressed, Duration: options.now().Sub(outageStarted)})
			}
			backoff = options.initialBackoff
			attempts = 0
			totalSuppressed = 0
			sinceReport = 0
			reported = false
			lastError = ""
			outageStarted = time.Time{}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		now := options.now()
		attempts++
		if outageStarted.IsZero() {
			outageStarted = now
		}
		delay := options.jitter(backoff)
		if delay < 0 {
			delay = 0
		}
		if delay > options.maxBackoff {
			delay = options.maxBackoff
		}
		operation := "poll"
		var typed pollError
		if errors.As(err, &typed) {
			operation = typed.operation
		}
		errorKey := operation + "\x00" + err.Error()
		if !reported || errorKey != lastError || now.Sub(lastReport) >= options.reportInterval {
			options.onFailure(PollFailure{Operation: operation, Err: err, Attempt: attempts, Delay: delay, Suppressed: sinceReport})
			reported = true
			lastReport = now
			lastError = errorKey
			sinceReport = 0
		} else {
			sinceReport++
			totalSuppressed++
		}

		if err := options.sleep(ctx, delay); err != nil {
			return err
		}
		backoff = nextConsumerBackoff(backoff, options.maxBackoff)
	}
	return ctx.Err()
}

func nextConsumerBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}

func jitterConsumerBackoff(delay time.Duration, fraction float64) time.Duration {
	if delay <= 0 || fraction <= 0 {
		return delay
	}
	delta := (rand.Float64()*2 - 1) * fraction
	return time.Duration(float64(delay) * (1 + delta))
}

func sleepConsumerContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
