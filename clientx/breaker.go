package clientx

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/baowk/dilu-go-kit/metrics"
)

var ErrCircuitOpen = errors.New("circuit breaker open")
var ErrNilOperation = errors.New("clientx: operation is nil")

type BreakerState int

const (
	BreakerClosed BreakerState = iota
	BreakerOpen
	BreakerHalfOpen
)

// Breaker is a small client-side circuit breaker.
type Breaker struct {
	mu            sync.Mutex
	state         BreakerState
	failures      int
	threshold     int
	openUntil     time.Time
	cooldown      time.Duration
	now           func() time.Time
	shouldCount   func(error) bool
	halfOpenProbe bool
	generation    uint64
	name          string
}

// BreakerConfig configures a circuit breaker.
type BreakerConfig struct {
	Name             string
	FailureThreshold int
	Cooldown         time.Duration
	Now              func() time.Time
	ShouldCount      func(error) bool
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
	shouldCount := cfg.ShouldCount
	if shouldCount == nil {
		shouldCount = func(err error) bool {
			if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return false
			}
			return true
		}
	}
	b := &Breaker{name: cfg.Name, threshold: threshold, cooldown: cooldown, now: now, shouldCount: shouldCount}
	metrics.ObserveCircuitBreaker(b.name, "closed")
	return b
}

// Do executes fn when the circuit allows it.
func (b *Breaker) Do(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return ErrNilOperation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	permit, err := b.before()
	if err != nil {
		return err
	}
	err = fn(ctx)
	b.after(permit, err)
	return err
}

// State returns the current breaker state.
func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentStateLocked()
}

type breakerPermit struct {
	generation uint64
	halfOpen   bool
}

func (b *Breaker) before() (breakerPermit, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentStateLocked() == BreakerOpen {
		return breakerPermit{}, ErrCircuitOpen
	}
	permit := breakerPermit{generation: b.generation}
	if b.state == BreakerHalfOpen {
		if b.halfOpenProbe {
			return breakerPermit{}, ErrCircuitOpen
		}
		b.halfOpenProbe = true
		permit.halfOpen = true
	}
	return permit, nil
}

func (b *Breaker) after(permit breakerPermit, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if permit.generation != b.generation {
		return
	}
	if permit.halfOpen && b.state == BreakerHalfOpen {
		b.halfOpenProbe = false
		if err == nil {
			b.failures = 0
			b.state = BreakerClosed
			b.generation++
			metrics.ObserveCircuitBreaker(b.name, "closed")
			return
		}
		b.state = BreakerOpen
		b.openUntil = b.now().Add(b.cooldown)
		b.generation++
		metrics.ObserveCircuitBreaker(b.name, "open")
		return
	}
	if b.state != BreakerClosed {
		return
	}
	if err == nil {
		b.failures = 0
		b.state = BreakerClosed
		return
	}
	if !b.shouldCount(err) {
		return
	}
	b.failures++
	if b.failures >= b.threshold {
		b.state = BreakerOpen
		b.openUntil = b.now().Add(b.cooldown)
		b.generation++
		metrics.ObserveCircuitBreaker(b.name, "open")
	}
}

func (b *Breaker) currentStateLocked() BreakerState {
	if b.state == BreakerOpen && !b.openUntil.After(b.now()) {
		b.state = BreakerHalfOpen
	}
	return b.state
}
