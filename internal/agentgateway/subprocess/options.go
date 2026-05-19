package subprocess

import (
	"fmt"
	"time"
)

// Option configures a Manager at construction time.
type Option func(*options) error

type options struct {
	drainTimeout   time.Duration
	startupTimeout time.Duration
	backoffInitial time.Duration
	backoffMax     time.Duration
	maxRestarts    int
}

func defaultOptions() options {
	return options{
		drainTimeout:   10 * time.Second,
		startupTimeout: 30 * time.Second,
		backoffInitial: 200 * time.Millisecond,
		backoffMax:     30 * time.Second,
		maxRestarts:    -1,
	}
}

// WithDrainTimeout sets how long Stop waits after SIGTERM before
// escalating to SIGKILL.
func WithDrainTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("drain timeout must be positive, got %s", d)
		}
		o.drainTimeout = d
		return nil
	}
}

// WithStartupTimeout caps how long Start waits for the readiness probe
// to succeed on the initial launch.
func WithStartupTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("startup timeout must be positive, got %s", d)
		}
		o.startupTimeout = d
		return nil
	}
}

// WithBackoff overrides the initial and maximum restart backoff delays.
// Each unexpected exit doubles the previous delay, capped at max.
func WithBackoff(initial, max time.Duration) Option {
	return func(o *options) error {
		if initial <= 0 {
			return fmt.Errorf("initial backoff must be positive, got %s", initial)
		}
		if max < initial {
			return fmt.Errorf("max backoff %s must be >= initial backoff %s", max, initial)
		}
		o.backoffInitial = initial
		o.backoffMax = max
		return nil
	}
}

// WithMaxRestarts caps how many times the Manager restarts the process
// after unexpected exits. -1 means unlimited.
func WithMaxRestarts(n int) Option {
	return func(o *options) error {
		if n < -1 {
			return fmt.Errorf("max restarts must be -1 (unlimited) or >= 0, got %d", n)
		}
		o.maxRestarts = n
		return nil
	}
}
