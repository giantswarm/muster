package api

import (
	"time"
)

// ServiceInstance represents a single service instance (runtime data for services created from ServiceClass templates)
// This consolidates ServiceInstance, ServiceInstanceDefinition, and ServiceClassInstanceInfo into one type
type ServiceInstance struct {
	// Instance identification
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name,omitempty" yaml:"name"`

	// ServiceClass reference
	ServiceClassName string `json:"serviceClassName" yaml:"serviceClassName"`
	ServiceClassType string `json:"serviceClassType,omitempty" yaml:"serviceClassType,omitempty"`

	// Configuration fields (for persistence)
	Parameters   map[string]interface{} `yaml:"parameters" json:"parameters"`
	Dependencies []string               `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	AutoStart    bool                   `yaml:"autoStart,omitempty" json:"autoStart,omitempty"`
	Enabled      bool                   `yaml:"enabled" json:"enabled"`
	Description  string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Version      string                 `yaml:"version,omitempty" json:"version,omitempty"`

	// Runtime state fields (for API responses)
	State                ServiceState           `json:"state,omitempty" yaml:"-"`
	Health               HealthStatus           `json:"health,omitempty" yaml:"-"`
	LastError            string                 `json:"lastError,omitempty" yaml:"-"`
	ServiceData          map[string]interface{} `json:"serviceData,omitempty" yaml:"-"`
	CreatedAt            time.Time              `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
	UpdatedAt            time.Time              `json:"updatedAt,omitempty" yaml:"-"`
	LastChecked          *time.Time             `json:"lastChecked,omitempty" yaml:"-"`
	HealthCheckFailures  int                    `json:"healthCheckFailures,omitempty" yaml:"-"`
	HealthCheckSuccesses int                    `json:"healthCheckSuccesses,omitempty" yaml:"-"`
}

// CreateServiceInstanceRequest represents a request to create a new ServiceClass-based service instance
type CreateServiceInstanceRequest struct {
	// ServiceClass to use
	ServiceClassName string `json:"serviceClassName"`

	// Name for the service instance (must be unique)
	Name string `json:"name"`

	// Parameters for service creation
	Parameters map[string]interface{} `json:"parameters"`

	// Whether to persist this service instance definition to YAML files
	Persist bool `json:"persist,omitempty"`

	// Optional: Whether this instance should be started automatically on system startup
	AutoStart bool `json:"autoStart,omitempty"`

	// Override default timeouts (future use)
	CreateTimeout *time.Duration `json:"createTimeout,omitempty"`
	DeleteTimeout *time.Duration `json:"deleteTimeout,omitempty"`
}

// ServiceInstanceEvent represents a ServiceClass-based service instance state change event
type ServiceInstanceEvent struct {
	Name        string                 `json:"name"`
	ServiceType string                 `json:"serviceType"`
	OldState    string                 `json:"oldState"`
	NewState    string                 `json:"newState"`
	OldHealth   string                 `json:"oldHealth"`
	NewHealth   string                 `json:"newHealth"`
	Error       string                 `json:"error,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}
