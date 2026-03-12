package aggregator

import (
	"sync"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// AuthRateLimiter provides per-user rate limiting for authentication operations.
// This prevents OAuth flow abuse by limiting the number of auth attempts per user.
//
// Rate limiting is implemented using a sliding window approach:
//   - Each user can make at most MaxAuthAttempts auth attempts within the time window
//   - Attempts are tracked per user (sub claim), not globally
//   - Old attempts are automatically cleaned up via a background goroutine
//
// Callers MUST call Stop() when done to prevent goroutine leaks.
type AuthRateLimiter struct {
	mu sync.RWMutex

	// Configuration
	maxAttempts int           // Maximum attempts per user within the window
	window      time.Duration // Time window for rate limiting

	// Per-user attempt tracking
	attempts map[string][]time.Time // userID (sub claim) -> list of attempt timestamps

	// Lifecycle management
	stopCh chan struct{} // Closed to signal the cleanup goroutine to exit
}

// AuthRateLimiterConfig holds configuration for the rate limiter.
type AuthRateLimiterConfig struct {
	// MaxAttempts is the maximum number of auth attempts per user within the window.
	// Default: 10 attempts
	MaxAttempts int

	// Window is the time window for rate limiting.
	// Default: 1 minute
	Window time.Duration
}

// DefaultAuthRateLimiterConfig returns the default rate limiter configuration.
func DefaultAuthRateLimiterConfig() AuthRateLimiterConfig {
	return AuthRateLimiterConfig{
		MaxAttempts: 10,
		Window:      time.Minute,
	}
}

// NewAuthRateLimiter creates a new rate limiter with the given configuration.
// It starts a background goroutine that periodically calls Cleanup() to remove
// stale entries. Callers MUST call Stop() when done to prevent goroutine leaks.
func NewAuthRateLimiter(config AuthRateLimiterConfig) *AuthRateLimiter {
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 10
	}
	if config.Window <= 0 {
		config.Window = time.Minute
	}

	rl := &AuthRateLimiter{
		maxAttempts: config.MaxAttempts,
		window:      config.Window,
		attempts:    make(map[string][]time.Time),
		stopCh:      make(chan struct{}),
	}

	go rl.cleanupLoop()

	return rl
}

// Allow checks if an auth attempt is allowed for the given user.
// If allowed, the attempt is recorded and true is returned.
// If rate limited, false is returned and the attempt is NOT recorded.
//
// Args:
//   - userID: The user making the auth attempt (sub claim or session ID fallback)
//   - serverName: The server being authenticated to (for logging)
//
// Returns true if the attempt is allowed, false if rate limited.
func (rl *AuthRateLimiter) Allow(userID, serverName string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	// Get existing attempts and filter out old ones
	existing := rl.attempts[userID]
	var recent []time.Time
	for _, t := range existing {
		if t.After(windowStart) {
			recent = append(recent, t)
		}
	}

	// Check if we've exceeded the limit
	if len(recent) >= rl.maxAttempts {
		logging.Warn("AuthRateLimiter", "Rate limit exceeded for user %s attempting to auth to server %s (%d attempts in %v)",
			logging.TruncateIdentifier(userID), serverName, len(recent), rl.window)
		// Update the filtered list even though we're rejecting
		rl.attempts[userID] = recent
		return false
	}

	// Record this attempt
	recent = append(recent, now)
	rl.attempts[userID] = recent

	logging.Debug("AuthRateLimiter", "Auth attempt allowed for user %s server %s (%d/%d attempts)",
		logging.TruncateIdentifier(userID), serverName, len(recent), rl.maxAttempts)

	return true
}

// RemainingAttempts returns the number of remaining auth attempts for a user.
func (rl *AuthRateLimiter) RemainingAttempts(userID string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	existing := rl.attempts[userID]
	count := 0
	for _, t := range existing {
		if t.After(windowStart) {
			count++
		}
	}

	remaining := rl.maxAttempts - count
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

// Reset clears all rate limiting state for a user.
// This can be called after successful authentication to reset the counter.
func (rl *AuthRateLimiter) Reset(userID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	delete(rl.attempts, userID)
	logging.Debug("AuthRateLimiter", "Rate limit reset for user %s", logging.TruncateIdentifier(userID))
}

// Stop terminates the background cleanup goroutine.
// It is safe to call Stop multiple times.
func (rl *AuthRateLimiter) Stop() {
	select {
	case <-rl.stopCh:
		// Already stopped
	default:
		close(rl.stopCh)
		logging.Debug("AuthRateLimiter", "Stopped background cleanup goroutine")
	}
}

// minAuthRateLimiterCleanupInterval is the minimum cleanup interval to prevent
// excessive cleanup frequency when the window is very short.
const minAuthRateLimiterCleanupInterval = time.Second

// cleanupLoop periodically removes stale entries from the rate limiter.
func (rl *AuthRateLimiter) cleanupLoop() {
	interval := rl.window * 2
	if interval < minAuthRateLimiterCleanupInterval {
		interval = minAuthRateLimiterCleanupInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.Cleanup()
		case <-rl.stopCh:
			return
		}
	}
}

// Cleanup removes stale entries from the rate limiter.
// This is called periodically by the background cleanup goroutine.
func (rl *AuthRateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	for userID, attempts := range rl.attempts {
		var recent []time.Time
		for _, t := range attempts {
			if t.After(windowStart) {
				recent = append(recent, t)
			}
		}

		if len(recent) == 0 {
			delete(rl.attempts, userID)
		} else {
			rl.attempts[userID] = recent
		}
	}
}
