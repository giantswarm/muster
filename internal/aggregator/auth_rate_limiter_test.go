package aggregator

import (
	"testing"
	"time"
)

func TestAuthRateLimiter_Allow(t *testing.T) {
	tests := []struct {
		name        string
		maxAttempts int
		window      time.Duration
		attempts    int
		wantAllowed int // How many should be allowed
	}{
		{
			name:        "allows up to max attempts",
			maxAttempts: 5,
			window:      time.Minute,
			attempts:    5,
			wantAllowed: 5,
		},
		{
			name:        "blocks after max attempts",
			maxAttempts: 5,
			window:      time.Minute,
			attempts:    10,
			wantAllowed: 5,
		},
		{
			name:        "single attempt allowed",
			maxAttempts: 1,
			window:      time.Minute,
			attempts:    3,
			wantAllowed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewAuthRateLimiter(AuthRateLimiterConfig{
				MaxAttempts: tt.maxAttempts,
				Window:      tt.window,
			})

			sessionID := "test-session-123"
			serverName := "test-server"
			allowed := 0

			for i := 0; i < tt.attempts; i++ {
				if rl.Allow(sessionID, serverName) {
					allowed++
				}
			}

			if allowed != tt.wantAllowed {
				t.Errorf("Allow() allowed %d attempts, want %d", allowed, tt.wantAllowed)
			}
		})
	}
}

func TestAuthRateLimiter_RemainingAttempts(t *testing.T) {
	rl := NewAuthRateLimiter(AuthRateLimiterConfig{
		MaxAttempts: 5,
		Window:      time.Minute,
	})

	sessionID := "test-session-123"
	serverName := "test-server"

	// Initially should have all attempts remaining
	if got := rl.RemainingAttempts(sessionID); got != 5 {
		t.Errorf("RemainingAttempts() = %d, want 5", got)
	}

	// Use 3 attempts
	for i := 0; i < 3; i++ {
		rl.Allow(sessionID, serverName)
	}

	// Should have 2 remaining
	if got := rl.RemainingAttempts(sessionID); got != 2 {
		t.Errorf("RemainingAttempts() = %d, want 2", got)
	}

	// Use remaining 2
	rl.Allow(sessionID, serverName)
	rl.Allow(sessionID, serverName)

	// Should have 0 remaining
	if got := rl.RemainingAttempts(sessionID); got != 0 {
		t.Errorf("RemainingAttempts() = %d, want 0", got)
	}
}

func TestAuthRateLimiter_Reset(t *testing.T) {
	rl := NewAuthRateLimiter(AuthRateLimiterConfig{
		MaxAttempts: 3,
		Window:      time.Minute,
	})

	sessionID := "test-session-123"
	serverName := "test-server"

	// Use all attempts
	for i := 0; i < 3; i++ {
		rl.Allow(sessionID, serverName)
	}

	// Should be blocked
	if rl.Allow(sessionID, serverName) {
		t.Error("Allow() should return false when rate limited")
	}

	// Reset
	rl.Reset(sessionID)

	// Should be allowed again
	if !rl.Allow(sessionID, serverName) {
		t.Error("Allow() should return true after reset")
	}

	// Should have remaining attempts
	if got := rl.RemainingAttempts(sessionID); got != 2 {
		t.Errorf("RemainingAttempts() = %d, want 2 after reset and one attempt", got)
	}
}

func TestAuthRateLimiter_PerSessionIsolation(t *testing.T) {
	rl := NewAuthRateLimiter(AuthRateLimiterConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
	})

	session1 := "session-1"
	session2 := "session-2"
	serverName := "test-server"

	// Use all attempts for session1
	rl.Allow(session1, serverName)
	rl.Allow(session1, serverName)

	// session1 should be blocked
	if rl.Allow(session1, serverName) {
		t.Error("session1 should be rate limited")
	}

	// session2 should still be allowed (independent rate limiting)
	if !rl.Allow(session2, serverName) {
		t.Error("session2 should not be affected by session1's rate limiting")
	}
}

func TestAuthRateLimiter_Cleanup(t *testing.T) {
	rl := NewAuthRateLimiter(AuthRateLimiterConfig{
		MaxAttempts: 5,
		Window:      10 * time.Millisecond, // Very short window for testing
	})

	sessionID := "test-session-123"
	serverName := "test-server"

	// Make some attempts
	for i := 0; i < 3; i++ {
		rl.Allow(sessionID, serverName)
	}

	// Wait for window to expire
	time.Sleep(20 * time.Millisecond)

	// Cleanup should remove the stale entries
	rl.Cleanup()

	// Should have all attempts available again
	if got := rl.RemainingAttempts(sessionID); got != 5 {
		t.Errorf("RemainingAttempts() = %d, want 5 after cleanup", got)
	}
}

func TestAuthRateLimiter_DefaultConfig(t *testing.T) {
	config := DefaultAuthRateLimiterConfig()

	if config.MaxAttempts != 10 {
		t.Errorf("DefaultAuthRateLimiterConfig().MaxAttempts = %d, want 10", config.MaxAttempts)
	}

	if config.Window != time.Minute {
		t.Errorf("DefaultAuthRateLimiterConfig().Window = %v, want %v", config.Window, time.Minute)
	}
}

func TestNewAuthRateLimiter_InvalidConfig(t *testing.T) {
	// Test with invalid config values - should use defaults
	rl := NewAuthRateLimiter(AuthRateLimiterConfig{
		MaxAttempts: -1,
		Window:      -time.Second,
	})

	// Should use default values
	if rl.maxAttempts != 10 {
		t.Errorf("maxAttempts = %d, want 10 (default)", rl.maxAttempts)
	}
	if rl.window != time.Minute {
		t.Errorf("window = %v, want %v (default)", rl.window, time.Minute)
	}
}
