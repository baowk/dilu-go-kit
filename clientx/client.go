// Package clientx provides shared client-side reliability helpers.
package clientx

import (
	"context"
	"errors"
	"time"
)

// RetryConfig configures retry behavior for outbound service calls.
type RetryConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
	Retryable      func(error) bool
	Sleep          func(context.Context, time.Duration) error
}

// Do executes fn with retry. MaxAttempts includes the first attempt.
func Do(ctx context.Context, cfg RetryConfig, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	cfg = normalizeRetryConfig(cfg)
	var last error
	backoff := cfg.InitialBackoff
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		last = err
		if attempt == cfg.MaxAttempts || !cfg.Retryable(err) {
			return err
		}
		if sleepErr := cfg.Sleep(ctx, backoff); sleepErr != nil {
			return sleepErr
		}
		backoff = nextBackoff(backoff, cfg.MaxBackoff, cfg.Multiplier)
	}
	return last
}

func normalizeRetryConfig(cfg RetryConfig) RetryConfig {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = time.Second
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = 2
	}
	if cfg.Retryable == nil {
		cfg.Retryable = IsRetryable
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	return cfg
}

func nextBackoff(current, max time.Duration, multiplier float64) time.Duration {
	next := time.Duration(float64(current) * multiplier)
	if next > max {
		return max
	}
	return next
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ClientError is a normalized outbound service call error.
type ClientError struct {
	Code    int
	Message string
	Err     error
}

func (e *ClientError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "client error"
}

func (e *ClientError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewError creates a normalized client error.
func NewError(code int, message string, err error) error {
	return &ClientError{Code: code, Message: message, Err: err}
}

// CodeOf returns a normalized code from err when possible.
func CodeOf(err error) int {
	var ce *ClientError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return 0
}
