package clientx

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker open")

type BreakerState int

const (
	BreakerClosed BreakerState = iota
	BreakerOpen
	BreakerHalfOpen
)

// Breaker is a small client-side circuit breaker.
type Breaker struct {
	mu        sync.Mutex
	state     BreakerState
	failures  int
	threshold int
	openUntil time.Time
	cooldown  time.Duration
	now       func() time.Time
}

// BreakerConfig configures a circuit breaker.
type BreakerConfig struct {
	FailureThreshold int
	Cooldown         time.Duration
	Now              func() time.Time
}

// NewBreaker creates a circuit breaker.
func NewBreaker(cfg BreakerConfig) *Breaker {
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = 5
	}
	cooldown := cfg.Cooldown
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Breaker{threshold: threshold, cooldown: cooldown, now: now}
}

// Do executes fn when the circuit allows it.
func (b *Breaker) Do(ctx context.Context, fn func(context.Context) error) error {
	if err := b.before(); err != nil {
		return err
	}
	err := fn(ctx)
	b.after(err)
	return err
}

// State returns the current breaker state.
func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentStateLocked()
}

func (b *Breaker) before() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentStateLocked() == BreakerOpen {
		return ErrCircuitOpen
	}
	return nil
}

func (b *Breaker) after(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		b.failures = 0
		b.state = BreakerClosed
		return
	}
	b.failures++
	if b.failures >= b.threshold {
		b.state = BreakerOpen
		b.openUntil = b.now().Add(b.cooldown)
	}
}

func (b *Breaker) currentStateLocked() BreakerState {
	if b.state == BreakerOpen && !b.openUntil.After(b.now()) {
		b.state = BreakerHalfOpen
	}
	return b.state
}
