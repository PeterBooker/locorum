package health

import (
	"sync"
	"time"
)

// Breaker is a tiny circuit breaker for the disk-usage check. After
// [Breaker.MaxFailures] consecutive timeouts it opens; while open, calls
// fail fast for [Breaker.Cooldown] before half-opening for one trial.
//
// Stand-alone, no goroutine, lock-only-while-state-changes. Safe for
// concurrent use.
type Breaker struct {
	// MaxFailures is the threshold at which the breaker opens. Default
	// 3 — enough to avoid one-off flakes opening the circuit.
	MaxFailures int

	// Cooldown is how long the breaker stays open before trialling
	// half-open. Default 1 hour for the disk-usage breaker.
	Cooldown time.Duration

	// Now overrides time.Now for tests.
	Now func() time.Time

	mu       sync.Mutex
	failures int
	openedAt time.Time
}

// State returns "closed", "open", or "half-open" — the latter is the
// transient state where exactly one trial is allowed.
func (b *Breaker) State() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stateLocked()
}

func (b *Breaker) stateLocked() string {
	if b.failures < b.maxFailuresOrDefault() {
		return "closed"
	}
	if b.timeNow().Sub(b.openedAt) > b.cooldownOrDefault() {
		return "half-open"
	}
	return "open"
}

// Allow reports whether the call should proceed. Returns true when closed
// or half-open. The caller must record the outcome via [Breaker.OnSuccess]
// or [Breaker.OnFailure] so the breaker advances correctly.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.stateLocked()
	return state != "open"
}

// OnSuccess resets the failure counter. Call after a successful trial in
// half-open or any successful call in closed.
func (b *Breaker) OnSuccess() {
	b.mu.Lock()
	b.failures = 0
	b.openedAt = time.Time{}
	b.mu.Unlock()
}

// OnFailure increments the failure counter; opens the breaker on the
// transition past MaxFailures.
func (b *Breaker) OnFailure() {
	b.mu.Lock()
	b.failures++
	if b.failures == b.maxFailuresOrDefault() {
		b.openedAt = b.timeNow()
	} else if b.failures > b.maxFailuresOrDefault() {
		// In half-open, a failure resets the cooldown clock so we
		// don't immediately try again.
		b.openedAt = b.timeNow()
	}
	b.mu.Unlock()
}

func (b *Breaker) timeNow() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Breaker) maxFailuresOrDefault() int {
	if b.MaxFailures > 0 {
		return b.MaxFailures
	}
	return 3
}

func (b *Breaker) cooldownOrDefault() time.Duration {
	if b.Cooldown > 0 {
		return b.Cooldown
	}
	return time.Hour
}
