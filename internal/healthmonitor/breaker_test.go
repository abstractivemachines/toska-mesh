package healthmonitor

import (
	"testing"
	"time"
)

func TestBreaker_StartsClosedAndAllows(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	if cb.State() != BreakerClosed {
		t.Fatalf("expected closed, got %v", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected Allow() = true for closed breaker")
	}
}

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != BreakerClosed {
		t.Fatal("should still be closed after 2 failures")
	}

	cb.RecordFailure() // 3rd failure = threshold

	if cb.State() != BreakerOpen {
		t.Fatalf("expected open after 3 failures, got %v", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow() = false for open breaker")
	}
}

func TestBreaker_TransitionsToHalfOpenAfterDuration(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	// Inject controllable clock.
	now := time.Now()
	cb.now = func() time.Time { return now }

	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != BreakerOpen {
		t.Fatal("expected open")
	}

	// Advance time past break duration.
	now = now.Add(200 * time.Millisecond)

	if cb.State() != BreakerHalfOpen {
		t.Fatalf("expected half-open after break duration, got %v", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected Allow() = true for half-open breaker")
	}
}

func TestBreaker_SuccessInHalfOpenCloses(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)

	now := time.Now()
	cb.now = func() time.Time { return now }

	cb.RecordFailure()

	// Advance past break duration.
	now = now.Add(100 * time.Millisecond)
	cb.Allow() // triggers transition to half-open

	cb.RecordSuccess()

	if cb.State() != BreakerClosed {
		t.Fatalf("expected closed after success in half-open, got %v", cb.State())
	}
}

func TestBreaker_FailureInHalfOpenReopens(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	now := time.Now()
	cb.now = func() time.Time { return now }

	cb.RecordFailure()
	cb.RecordFailure()

	// Advance past break duration.
	now = now.Add(100 * time.Millisecond)
	cb.Allow() // triggers transition to half-open

	cb.RecordFailure()

	if cb.State() != BreakerOpen {
		t.Fatalf("expected open after failure in half-open, got %v", cb.State())
	}
}

func TestBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // reset
	cb.RecordFailure()
	cb.RecordFailure()

	// Should still be closed because success reset the count.
	if cb.State() != BreakerClosed {
		t.Fatalf("expected closed, got %v", cb.State())
	}
}
