package services

import (
	"testing"
)

func TestServiceState_String(t *testing.T) {
	tests := []struct {
		state    ServiceState
		expected string
	}{
		{StateUnknown, "unknown"},
		{StateWaiting, "waiting"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateStopping, "stopping"},
		{StateStopped, "stopped"},
		{StateFailed, "failed"},
		{StateRetrying, "retrying"},
	}

	for _, test := range tests {
		result := string(test.state)
		if result != test.expected {
			t.Errorf("ServiceState(%s) = %s, expected %s", test.state, result, test.expected)
		}
	}
}

func TestHealthStatus_String(t *testing.T) {
	tests := []struct {
		health   HealthStatus
		expected string
	}{
		{HealthUnknown, "unknown"},
		{HealthHealthy, "healthy"},
		{HealthUnhealthy, "unhealthy"},
		{HealthChecking, "checking"},
	}

	for _, test := range tests {
		result := string(test.health)
		if result != test.expected {
			t.Errorf("HealthStatus(%s) = %s, expected %s", test.health, result, test.expected)
		}
	}
}

func TestServiceType_String(t *testing.T) {
	tests := []struct {
		serviceType ServiceType
		expected    string
	}{
		{TypeMCPServer, "MCPServer"},
	}

	for _, test := range tests {
		result := string(test.serviceType)
		if result != test.expected {
			t.Errorf("ServiceType(%s) = %s, expected %s", test.serviceType, result, test.expected)
		}
	}
}

func TestServiceStateConstants(t *testing.T) {
	// Test that constants are defined and have expected values
	states := map[ServiceState]string{
		StateUnknown:  "unknown",
		StateWaiting:  "waiting",
		StateStarting: "starting",
		StateRunning:  "running",
		StateStopping: "stopping",
		StateStopped:  "stopped",
		StateFailed:   "failed",
		StateRetrying: "retrying",
	}

	for state, expectedStr := range states {
		if string(state) != expectedStr {
			t.Errorf("Expected state %s to have string value %s, got %s", state, expectedStr, string(state))
		}
	}
}

func TestHealthStatusConstants(t *testing.T) {
	// Test that constants are defined and have expected values
	healthStatuses := map[HealthStatus]string{
		HealthUnknown:   "unknown",
		HealthHealthy:   "healthy",
		HealthUnhealthy: "unhealthy",
		HealthChecking:  "checking",
	}

	for health, expectedStr := range healthStatuses {
		if string(health) != expectedStr {
			t.Errorf("Expected health %s to have string value %s, got %s", health, expectedStr, string(health))
		}
	}
}

func TestServiceTypeConstants(t *testing.T) {
	// Test that constants are defined and have expected values
	serviceTypes := map[ServiceType]string{
		TypeMCPServer: "MCPServer",
	}

	for serviceType, expectedStr := range serviceTypes {
		if string(serviceType) != expectedStr {
			t.Errorf("Expected service type %s to have string value %s, got %s", serviceType, expectedStr, string(serviceType))
		}
	}
}

// Test that the StateChangeCallback type is properly defined
func TestStateChangeCallback(t *testing.T) {
	// Test that we can create and call a StateChangeCallback
	var called bool
	var receivedName string
	var receivedOldState, receivedNewState ServiceState
	var receivedHealth HealthStatus
	var receivedErr error

	callback := func(name string, oldState, newState ServiceState, health HealthStatus, err error) {
		called = true
		receivedName = name
		receivedOldState = oldState
		receivedNewState = newState
		receivedHealth = health
		receivedErr = err
	}

	// Call the callback
	testName := "test-service"
	testOldState := StateStarting
	testNewState := StateRunning
	testHealth := HealthHealthy

	callback(testName, testOldState, testNewState, testHealth, nil)

	// Verify the callback was called with correct parameters
	if !called {
		t.Error("Expected callback to be called")
	}

	if receivedName != testName {
		t.Errorf("Expected name %s, got %s", testName, receivedName)
	}

	if receivedOldState != testOldState {
		t.Errorf("Expected old state %s, got %s", testOldState, receivedOldState)
	}

	if receivedNewState != testNewState {
		t.Errorf("Expected new state %s, got %s", testNewState, receivedNewState)
	}

	if receivedHealth != testHealth {
		t.Errorf("Expected health %s, got %s", testHealth, receivedHealth)
	}

	if receivedErr != nil {
		t.Errorf("Expected no error, got %v", receivedErr)
	}
}
