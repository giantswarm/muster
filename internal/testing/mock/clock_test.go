package mock

import (
	"testing"
	"time"
)

func TestRealClock_Now(t *testing.T) {
	clock := RealClock{}

	before := time.Now()
	clockTime := clock.Now()
	after := time.Now()

	if clockTime.Before(before) || clockTime.After(after) {
		t.Errorf("RealClock.Now() returned time outside expected range")
	}
}

func TestMockClock_Now(t *testing.T) {
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	clock := NewMockClock(fixedTime)

	if !clock.Now().Equal(fixedTime) {
		t.Errorf("Expected time %v, got %v", fixedTime, clock.Now())
	}

	// Calling Now multiple times should return the same time
	if !clock.Now().Equal(fixedTime) {
		t.Errorf("Expected time to remain stable at %v, got %v", fixedTime, clock.Now())
	}
}

func TestMockClock_Advance(t *testing.T) {
	startTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	clock := NewMockClock(startTime)

	// Advance by 1 hour
	clock.Advance(1 * time.Hour)

	expectedTime := startTime.Add(1 * time.Hour)
	if !clock.Now().Equal(expectedTime) {
		t.Errorf("Expected time %v after advance, got %v", expectedTime, clock.Now())
	}

	// Advance by another 30 minutes
	clock.Advance(30 * time.Minute)

	expectedTime = startTime.Add(90 * time.Minute)
	if !clock.Now().Equal(expectedTime) {
		t.Errorf("Expected time %v after second advance, got %v", expectedTime, clock.Now())
	}
}

func TestMockClock_Set(t *testing.T) {
	clock := NewMockClock(time.Time{})

	newTime := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
	clock.Set(newTime)

	if !clock.Now().Equal(newTime) {
		t.Errorf("Expected time %v after Set, got %v", newTime, clock.Now())
	}
}

func TestMockClock_Add(t *testing.T) {
	startTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	clock := NewMockClock(startTime)

	// Add is an alias for Advance
	clock.Add(2 * time.Hour)

	expectedTime := startTime.Add(2 * time.Hour)
	if !clock.Now().Equal(expectedTime) {
		t.Errorf("Expected time %v after Add, got %v", expectedTime, clock.Now())
	}
}

func TestMockClock_ZeroTime(t *testing.T) {
	// When initialized with zero time, should use current time
	before := time.Now()
	clock := NewMockClock(time.Time{})
	after := time.Now()

	clockTime := clock.Now()
	if clockTime.Before(before) || clockTime.After(after) {
		t.Errorf("MockClock with zero time should initialize to current time")
	}
}

func TestDefaultClock(t *testing.T) {
	// Reset to ensure clean state
	ResetDefaultClock()

	// Default should be RealClock
	_, ok := GetDefaultClock().(RealClock)
	if !ok {
		t.Error("Expected default clock to be RealClock")
	}

	// Set a mock clock
	mockClock := NewMockClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	SetDefaultClock(mockClock)

	if GetDefaultClock() != mockClock {
		t.Error("Expected default clock to be the mock clock we set")
	}

	// Reset back to real clock
	ResetDefaultClock()
	_, ok = GetDefaultClock().(RealClock)
	if !ok {
		t.Error("Expected default clock to be RealClock after reset")
	}
}

func TestMockClock_TokenExpiryScenario(t *testing.T) {
	// Simulate a token expiry scenario
	issuedAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	tokenLifetime := 1 * time.Hour
	expiresAt := issuedAt.Add(tokenLifetime)

	clock := NewMockClock(issuedAt)

	// Token should be valid initially
	isValid := clock.Now().Before(expiresAt)
	if !isValid {
		t.Error("Token should be valid at issue time")
	}

	// Advance 30 minutes - still valid
	clock.Advance(30 * time.Minute)
	isValid = clock.Now().Before(expiresAt)
	if !isValid {
		t.Error("Token should still be valid after 30 minutes")
	}

	// Advance another 31 minutes - now expired
	clock.Advance(31 * time.Minute)
	isValid = clock.Now().Before(expiresAt)
	if isValid {
		t.Error("Token should be expired after 61 minutes")
	}
}
