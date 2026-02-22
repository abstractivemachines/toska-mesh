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
type CircuitBreaker struct {
	mu                sync.Mutex
	state             BreakerState
	failureCount      int
	failureThreshold  int
	breakDuration     time.Duration
	openedAt          time.Time
	now               func() time.Time // for testing
}

// NewCircuitBreaker creates a breaker that opens after failureThreshold consecutive
// failures and stays open for breakDuration before transitioning to half-open.
func NewCircuitBreaker(failureThreshold int, breakDuration time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            BreakerClosed,
		failureThreshold: failureThreshold,
		breakDuration:    breakDuration,
		now:              time.Now,
	}
}

// Allow checks whether a request should be allowed through.
// Returns true if the circuit is closed or has transitioned to half-open.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		if cb.now().Sub(cb.openedAt) >= cb.breakDuration {
			cb.state = BreakerHalfOpen
			return true
		}
		return false
	case BreakerHalfOpen:
		return true
	default:
		return true
	}
}

// RecordSuccess records a successful request. Resets the breaker to closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.state = BreakerClosed
}

// RecordFailure records a failed request. Opens the circuit if the threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++

	if cb.state == BreakerHalfOpen || cb.failureCount >= cb.failureThreshold {
		cb.state = BreakerOpen
		cb.openedAt = cb.now()
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
