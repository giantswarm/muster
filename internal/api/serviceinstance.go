package api

import (
	"time"
)

// ServiceInstance represents a single service instance that is created from a ServiceClass template.
// It combines both the configuration data (for persistence) and runtime state (for API responses).
// ServiceInstance consolidates ServiceInstance, ServiceInstanceDefinition, and ServiceClassInstanceInfo
// into a unified type that can be used across different layers of the system.
//
// The struct is designed to support both YAML serialization for persistence and JSON serialization
// for API responses, with some fields excluded from YAML when they represent transient runtime state.
type ServiceInstance struct {
	// ID is the unique identifier for this service instance.
	// This is typically generated automatically when the instance is created.
	ID string `json:"id" yaml:"id"`

	// Name is the human-readable name for this service instance.
	// If not provided, it may be derived from the ServiceClass defaultName or ID.
	Name string `json:"name,omitempty" yaml:"name"`

	// ServiceClassName specifies which ServiceClass template this instance was created from.
	// This establishes the relationship between the instance and its blueprint.
	ServiceClassName string `json:"serviceClassName" yaml:"serviceClassName"`

	// ServiceClassType indicates the type of service as defined in the ServiceClass.
	// This is populated from the ServiceClass configuration and used for categorization.
	ServiceClassType string `json:"serviceClassType,omitempty" yaml:"serviceClassType,omitempty"`

	// Args contains the configuration values provided when creating this service instance.
	// These values are validated against the ServiceClass arg definitions.
	Args map[string]interface{} `yaml:"args" json:"args"`

	// Dependencies lists other service instances that must be running before this instance can start.
	// The orchestrator uses this for proper startup ordering.
	Dependencies []string `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`

	// AutoStart determines whether this service instance should be automatically started
	// when the system boots up or when dependencies become available.
	AutoStart bool `yaml:"autoStart,omitempty" json:"autoStart,omitempty"`

	// Enabled indicates whether this service instance is enabled for operation.
	// Disabled instances will not be started even if AutoStart is true.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Description provides a human-readable description of this service instance's purpose.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Version tracks the version of this service instance configuration.
	// This can be used for configuration migration and compatibility checks.
	Version string `yaml:"version,omitempty" json:"version,omitempty"`

	// State represents the current operational state of the service instance.
	// This is runtime information and not persisted to YAML files.
	State ServiceState `json:"state,omitempty" yaml:"-"`

	// Health indicates the current health status of the service instance.
	// This is determined by health checks and not persisted to YAML files.
	Health HealthStatus `json:"health,omitempty" yaml:"-"`

	// LastError contains the most recent error message from service operations.
	// This is runtime information and not persisted to YAML files.
	LastError string `json:"lastError,omitempty" yaml:"-"`

	// ServiceData contains additional runtime data specific to this service instance.
	// This might include connection information, status details, or other service-specific data.
	ServiceData map[string]interface{} `json:"serviceData,omitempty" yaml:"-"`

	// Outputs contains the resolved outputs from the ServiceClass outputs definition.
	// These are generated during service creation by resolving templates with service args and runtime data.
	// Outputs are available for workflows and other consumers that need access to service-generated values.
	Outputs map[string]interface{} `json:"outputs,omitempty" yaml:"-"`

	// CreatedAt records when this service instance was initially created.
	// This timestamp is persisted and used for auditing and lifecycle management.
	CreatedAt time.Time `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`

	// UpdatedAt tracks when this service instance configuration was last modified.
	// This is runtime information and not persisted to YAML files.
	UpdatedAt time.Time `json:"updatedAt,omitempty" yaml:"-"`

	// LastChecked records the timestamp of the most recent health check.
	// This is runtime information and not persisted to YAML files.
	LastChecked *time.Time `json:"lastChecked,omitempty" yaml:"-"`

	// HealthCheckFailures counts consecutive health check failures.
	// This is used by the health monitoring system to determine service health trends.
	HealthCheckFailures int `json:"healthCheckFailures,omitempty" yaml:"-"`

	// HealthCheckSuccesses counts consecutive health check successes.
	// This is used by the health monitoring system to determine recovery patterns.
	HealthCheckSuccesses int `json:"healthCheckSuccesses,omitempty" yaml:"-"`
}

// CreateServiceInstanceRequest represents a request to create a new service instance from a ServiceClass template.
// This request contains all the information needed to instantiate and configure a new service based on
// a predefined ServiceClass blueprint.
type CreateServiceInstanceRequest struct {
	// ServiceClassName specifies which ServiceClass template to use for creating the instance.
	// The ServiceClass must exist and be available in the system.
	ServiceClassName string `json:"serviceClassName"`

	// Name provides a unique name for the new service instance.
	// This name must be unique across all service instances in the system.
	Name string `json:"name"`

	// Args contains the configuration values for the new service instance.
	// These args are validated against the ServiceClass arg definitions
	// and used to customize the service behavior.
	Args map[string]interface{} `json:"args"`

	// Persist determines whether this service instance definition should be saved to YAML files.
	// When true, the instance configuration will be persisted and survive system restarts.
	// When false, the instance exists only in memory for the current session.
	Persist bool `json:"persist,omitempty"`

	// AutoStart specifies whether this service instance should be automatically started
	// when the system boots up or when all dependencies become available.
	AutoStart bool `json:"autoStart,omitempty"`

	// CreateTimeout overrides the default timeout for service creation operations.
	// If not specified, the system default timeout will be used.
	CreateTimeout *time.Duration `json:"createTimeout,omitempty"`

	// DeleteTimeout overrides the default timeout for service deletion operations.
	// If not specified, the system default timeout will be used.
	DeleteTimeout *time.Duration `json:"deleteTimeout,omitempty"`
}

// ServiceInstanceEvent represents a state change event for a ServiceClass-based service instance.
// These events are emitted when service instances change state, health status, or encounter errors.
// Clients can subscribe to these events to monitor service instance lifecycle and health.
type ServiceInstanceEvent struct {
	// Name is the unique name of the service instance that triggered this event.
	Name string `json:"name"`

	// ServiceType indicates the type of service as defined in the ServiceClass.
	ServiceType string `json:"serviceType"`

	// OldState represents the previous operational state before this event.
	OldState string `json:"oldState"`

	// NewState represents the current operational state after this event.
	NewState string `json:"newState"`

	// OldHealth represents the previous health status before this event.
	OldHealth string `json:"oldHealth"`

	// NewHealth represents the current health status after this event.
	NewHealth string `json:"newHealth"`

	// Error contains any error message associated with this state change event.
	// This field is only populated if the state change was caused by an error condition.
	Error string `json:"error,omitempty"`

	// Timestamp records when this event occurred.
	// This is used for event ordering and audit trails.
	Timestamp time.Time `json:"timestamp"`

	// Metadata contains additional context-specific information about this event.
	// The content depends on the service type and the nature of the state change.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}
