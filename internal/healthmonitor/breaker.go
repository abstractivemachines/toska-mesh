package healthmonitor

import (
	"sync"
	"time"
)

// BreakerState represents the current state of a circuit breaker.
type BreakerState int

const (
	BreakerClosed   BreakerState = iota // Normal operation, requests pass through
	BreakerOpen                         // Tripped, all requests fail fast
	BreakerHalfOpen                     // Testing, one request allowed through
)

func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "closed"
	case BreakerOpen:
		return "open"
	case BreakerHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements a simple circuit breaker pattern.
// It tracks consecutive failures and opens the circuit when a threshold is reached.
// In half-open state, only one probe request is allowed through at a time.
// The breaker requires recoveryThreshold consecutive successes in half-open
// before fully closing.
type CircuitBreaker struct {
	mu                 sync.Mutex
	state              BreakerState
	failureCount       int
	failureThreshold   int
	recoveryThreshold  int
	recoveryCount      int  // consecutive successes in half-open
	breakDuration      time.Duration
	openedAt           time.Time
	halfOpenUsed       bool           // true once a request has been admitted in half-open
	now                func() time.Time // for testing
}

// NewCircuitBreaker creates a breaker that opens after failureThreshold consecutive
// failures and stays open for breakDuration before transitioning to half-open.
// Recovery requires 1 consecutive success in half-open.
func NewCircuitBreaker(failureThreshold int, breakDuration time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:             BreakerClosed,
		failureThreshold:  failureThreshold,
		recoveryThreshold: 1,
		breakDuration:     breakDuration,
		now:               time.Now,
	}
}

// NewCircuitBreakerWithRecovery creates a breaker like NewCircuitBreaker but
// requires recoveryThreshold consecutive successes in half-open before closing.
func NewCircuitBreakerWithRecovery(failureThreshold, recoveryThreshold int, breakDuration time.Duration) *CircuitBreaker {
	if recoveryThreshold < 1 {
		recoveryThreshold = 1
	}
	return &CircuitBreaker{
		state:             BreakerClosed,
		failureThreshold:  failureThreshold,
		recoveryThreshold: recoveryThreshold,
		breakDuration:     breakDuration,
		now:               time.Now,
	}
}

// Allow checks whether a request should be allowed through.
// Returns true if the circuit is closed or has just transitioned to half-open.
// In half-open state, only one probe request is permitted; subsequent callers
// are blocked until the probe outcome is recorded.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		if cb.now().Sub(cb.openedAt) >= cb.breakDuration {
			cb.state = BreakerHalfOpen
			cb.halfOpenUsed = false
			// Allow the first probe request through.
			cb.halfOpenUsed = true
			return true
		}
		return false
	case BreakerHalfOpen:
		if !cb.halfOpenUsed {
			cb.halfOpenUsed = true
			return true
		}
		return false
	default:
		return true
	}
}

// RecordSuccess records a successful request. In half-open state, the breaker
// closes only after recoveryThreshold consecutive successes.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0

	if cb.state == BreakerHalfOpen {
		cb.recoveryCount++
		if cb.recoveryCount >= cb.recoveryThreshold {
			cb.state = BreakerClosed
			cb.recoveryCount = 0
		}
		// Allow the next probe request through.
		cb.halfOpenUsed = false
		return
	}

	cb.state = BreakerClosed
	cb.halfOpenUsed = false
}

// RecordFailure records a failed request. Opens the circuit if the threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.recoveryCount = 0

	if cb.state == BreakerHalfOpen || cb.failureCount >= cb.failureThreshold {
		cb.state = BreakerOpen
		cb.openedAt = cb.now()
		cb.halfOpenUsed = false
	}
}

// State returns the current breaker state.
func (cb *CircuitBreaker) State() BreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check for time-based transition.
	if cb.state == BreakerOpen && cb.now().Sub(cb.openedAt) >= cb.breakDuration {
		cb.state = BreakerHalfOpen
	}
	return cb.state
}
