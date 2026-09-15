package mock

import (
	"sync"
	"sync/atomic"
	"time"
)

// Clock provides an interface for time operations to enable testing
// without relying on real time. This allows tests to simulate token
// expiry without waiting for actual time to pass.
type Clock interface {
	// Now returns the current time according to this clock
	Now() time.Time
}

// RealClock implements Clock using the actual system time.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time {
	return time.Now()
}

// MockClock implements Clock with a controllable time value.
// This enables testing time-sensitive operations like token expiry
// without waiting for real time to pass.
type MockClock struct {
	mu      sync.RWMutex
	current time.Time
}

// NewMockClock creates a new mock clock initialized to the given time.
// If t is zero, the clock is initialized to the current time.
func NewMockClock(t time.Time) *MockClock {
	if t.IsZero() {
		t = time.Now()
	}
	return &MockClock{current: t}
}

// Now returns the current time according to this mock clock.
func (m *MockClock) Now() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Advance moves the clock forward by the given duration.
func (m *MockClock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = m.current.Add(d)
}

// Set sets the clock to a specific time.
func (m *MockClock) Set(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = t
}

// Add is an alias for Advance for API familiarity.
func (m *MockClock) Add(d time.Duration) {
	m.Advance(d)
}

// OffsetClock is the system time plus an offset that Advance grows: the
// clock of a mock authorization server that follows wall time and is moved
// forward together with muster's own clock by test_advance_clock. Unlike
// MockClock it does not stand still between advances.
type OffsetClock struct {
	offset atomic.Int64
}

// NewOffsetClock returns a clock at the system time with no offset.
func NewOffsetClock() *OffsetClock {
	return &OffsetClock{}
}

// Now returns the system time plus the offset.
func (c *OffsetClock) Now() time.Time {
	return time.Now().Add(time.Duration(c.offset.Load()))
}

// Advance moves the clock forward by d.
func (c *OffsetClock) Advance(d time.Duration) {
	c.offset.Add(int64(d))
}

// Advancer is a clock that can be moved forward: MockClock and OffsetClock.
type Advancer interface {
	Advance(d time.Duration)
}

// defaultClock is the default clock used by the package.
// WARNING: This is global state and NOT thread-safe for concurrent modification.
// Only use SetDefaultClock/ResetDefaultClock in test setup/teardown, never in
// production code or parallel tests. Prefer passing Clock instances explicitly
// via configuration (e.g., OAuthServerConfig.Clock) for thread-safe testing.
var defaultClock Clock = RealClock{} //nolint:unused
