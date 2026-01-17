package aggregator

import (
	"sync"
	"time"

	"muster/pkg/logging"
)

// AuthRateLimiter provides per-session rate limiting for authentication operations.
// This prevents OAuth flow abuse by limiting the number of auth attempts per session.
//
// Rate limiting is implemented using a sliding window approach:
//   - Each session can make at most MaxAuthAttempts auth attempts within the time window
//   - Attempts are tracked per session, not globally
//   - Old attempts are automatically cleaned up
type AuthRateLimiter struct {
	mu sync.RWMutex

	// Configuration
	maxAttempts int           // Maximum attempts per session within the window
	window      time.Duration // Time window for rate limiting

	// Per-session attempt tracking
	attempts map[string][]time.Time // sessionID -> list of attempt timestamps
}

// AuthRateLimiterConfig holds configuration for the rate limiter.
type AuthRateLimiterConfig struct {
	// MaxAttempts is the maximum number of auth attempts per session within the window.
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
func NewAuthRateLimiter(config AuthRateLimiterConfig) *AuthRateLimiter {
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 10
	}
	if config.Window <= 0 {
		config.Window = time.Minute
	}

	return &AuthRateLimiter{
		maxAttempts: config.MaxAttempts,
		window:      config.Window,
		attempts:    make(map[string][]time.Time),
	}
}

// Allow checks if an auth attempt is allowed for the given session.
// If allowed, the attempt is recorded and true is returned.
// If rate limited, false is returned and the attempt is NOT recorded.
//
// Args:
//   - sessionID: The session making the auth attempt
//   - serverName: The server being authenticated to (for logging)
//
// Returns true if the attempt is allowed, false if rate limited.
func (rl *AuthRateLimiter) Allow(sessionID, serverName string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	// Get existing attempts and filter out old ones
	existing := rl.attempts[sessionID]
	var recent []time.Time
	for _, t := range existing {
		if t.After(windowStart) {
			recent = append(recent, t)
		}
	}

	// Check if we've exceeded the limit
	if len(recent) >= rl.maxAttempts {
		logging.Warn("AuthRateLimiter", "Rate limit exceeded for session %s attempting to auth to server %s (%d attempts in %v)",
			logging.TruncateSessionID(sessionID), serverName, len(recent), rl.window)
		// Update the filtered list even though we're rejecting
		rl.attempts[sessionID] = recent
		return false
	}

	// Record this attempt
	recent = append(recent, now)
	rl.attempts[sessionID] = recent

	logging.Debug("AuthRateLimiter", "Auth attempt allowed for session %s server %s (%d/%d attempts)",
		logging.TruncateSessionID(sessionID), serverName, len(recent), rl.maxAttempts)

	return true
}

// RemainingAttempts returns the number of remaining auth attempts for a session.
func (rl *AuthRateLimiter) RemainingAttempts(sessionID string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	existing := rl.attempts[sessionID]
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

// Reset clears all rate limiting state for a session.
// This can be called after successful authentication to reset the counter.
func (rl *AuthRateLimiter) Reset(sessionID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	delete(rl.attempts, sessionID)
	logging.Debug("AuthRateLimiter", "Rate limit reset for session %s", logging.TruncateSessionID(sessionID))
}

// Cleanup removes stale entries from the rate limiter.
// This should be called periodically to prevent memory leaks.
func (rl *AuthRateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	for sessionID, attempts := range rl.attempts {
		var recent []time.Time
		for _, t := range attempts {
			if t.After(windowStart) {
				recent = append(recent, t)
			}
		}

		if len(recent) == 0 {
			delete(rl.attempts, sessionID)
		} else {
			rl.attempts[sessionID] = recent
		}
	}
}
