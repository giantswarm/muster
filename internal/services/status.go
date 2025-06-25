package services

// StatusUpdate represents a generic status update from any service type
type StatusUpdate struct {
	Name    string // Service name
	Status  string // Status string (service-specific)
	Detail  string // Optional detail message
	IsError bool   // Whether this is an error condition
	IsReady bool   // Whether the service is ready/active
	Error   error  // Error if any
}

// StatusUpdateFunc is a callback for receiving status updates
type StatusUpdateFunc func(update StatusUpdate)

// StatusReporter interface for services that report detailed status updates
type StatusReporter interface {
	// SetStatusUpdateCallback sets a callback for detailed status updates
	// This is in addition to the state change callback
	SetStatusUpdateCallback(callback StatusUpdateFunc)
}

// Common status constants that services can use
const (
	StatusInitializing = "Initializing"
	StatusStarting     = "Starting"
	StatusRunning      = "Running"
	StatusActive       = "Active"
	StatusWaiting      = "Waiting"
	StatusStopping     = "Stopping"
	StatusStopped      = "Stopped"
	StatusFailed       = "Failed"
	StatusUnhealthy    = "Unhealthy"
)

// MapStatusToState provides a common mapping from status strings to ServiceState
func MapStatusToState(status string) ServiceState {
	switch status {
	case StatusInitializing, StatusStarting:
		return StateStarting
	case StatusRunning, StatusActive:
		return StateRunning
	case StatusWaiting:
		return StateWaiting
	case StatusStopping:
		return StateStopping
	case StatusStopped:
		return StateStopped
	case StatusFailed:
		return StateFailed
	default:
		return StateUnknown
	}
}

// MapStatusToHealth provides a common mapping from status strings to HealthStatus
func MapStatusToHealth(status string, isError bool) HealthStatus {
	switch status {
	case StatusRunning, StatusActive:
		if isError {
			return HealthUnhealthy
		}
		return HealthHealthy
	case StatusUnhealthy:
		return HealthUnhealthy
	case StatusInitializing, StatusStarting:
		return HealthChecking
	case StatusWaiting:
		return HealthUnknown
	case StatusFailed:
		return HealthUnhealthy
	case StatusStopped, StatusStopping:
		return HealthUnknown
	default:
		return HealthUnknown
	}
}
