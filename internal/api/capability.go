package api

import (
	"context"
	"time"
)

// Capability represents a single capability definition and runtime state.
// This consolidates CapabilityDefinition, CapabilityInfo, and runtime Capability into one type
// to provide a unified view of capability information across configuration and runtime contexts.
//
// Capabilities define reusable operations that can be performed by the system,
// such as authentication, database operations, or external service integrations.
// They provide a way to abstract complex operations behind simple, discoverable interfaces.
type Capability struct {
	// Configuration fields (from YAML) - Static capability definition

	// Name is the unique identifier for this capability
	Name string `yaml:"name" json:"name"`

	// Type categorizes the capability (e.g., "auth", "database", "monitoring")
	Type string `yaml:"type" json:"type"`

	// Version indicates the capability definition version for compatibility tracking
	Version string `yaml:"version" json:"version"`

	// Description provides human-readable documentation for the capability
	Description string `yaml:"description" json:"description"`

	// Operations defines the available operations within this capability.
	// Each operation specifies its parameters, requirements, and associated workflow.
	Operations map[string]OperationDefinition `yaml:"operations" json:"operations"`

	// Runtime state fields (for API responses) - Dynamic runtime information

	// Available indicates whether this capability is currently available for execution
	Available bool `json:"available,omitempty" yaml:"-"`

	// State represents the current operational state of the capability
	State CapabilityState `json:"state,omitempty" yaml:"-"`

	// Health indicates the health status of the capability
	Health HealthStatus `json:"health,omitempty" yaml:"-"`

	// Error contains error information if the capability is in an error state
	Error string `json:"error,omitempty" yaml:"-"`

	// Provider identifies the component providing this capability
	Provider string `json:"provider,omitempty" yaml:"-"`

	// Runtime configuration - Additional runtime metadata

	// ID is a runtime-generated unique identifier for this capability instance
	ID string `json:"id,omitempty" yaml:"-"`

	// Features lists the specific features supported by this capability instance
	Features []string `json:"features,omitempty" yaml:"-"`

	// Config contains runtime configuration specific to this capability instance
	Config map[string]interface{} `json:"config,omitempty" yaml:"-"`

	// LastCheck indicates when the capability availability was last verified
	LastCheck time.Time `json:"lastCheck,omitempty" yaml:"-"`
}

// CapabilityRequest represents a request for a capability.
// This is used when components need to request specific capability functionality
// with particular features and configuration requirements.
type CapabilityRequest struct {
	// Type specifies the category of capability being requested
	Type string `json:"type"`

	// Features lists the specific features required from the capability
	Features []string `json:"features"`

	// Config provides configuration parameters for the capability request
	Config map[string]interface{} `json:"config"`

	// Timeout specifies the maximum time to wait for capability fulfillment
	Timeout time.Duration `json:"timeout"`
}

// CapabilityHandle represents an active capability fulfillment.
// This is returned when a capability request is successfully fulfilled,
// providing a handle for ongoing capability usage.
type CapabilityHandle struct {
	// ID is the unique identifier for this capability handle
	ID string `json:"id"`

	// Provider identifies the component that fulfilled this capability request
	Provider string `json:"provider"`

	// Type indicates the category of capability this handle represents
	Type string `json:"type"`

	// Config contains the actual configuration used for this capability instance
	Config map[string]interface{} `json:"config"`

	// ValidUntil indicates when this capability handle expires, if applicable
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

// CapabilityRequirement represents a capability requirement for a service.
// This is used in service definitions to specify what capabilities are needed
// for the service to function properly.
type CapabilityRequirement struct {
	// Type specifies the category of capability required
	Type string `json:"type"`

	// Features lists the specific features needed from the capability
	Features []string `json:"features"`

	// Config provides configuration requirements for the capability
	Config map[string]interface{} `json:"config"`

	// Optional indicates whether this capability is required or optional
	// for the service to function
	Optional bool `json:"optional"`
}

// CapabilityRegistration represents the data sent when registering a capability.
// This is used by capability providers to register their available capabilities
// with the capability management system.
type CapabilityRegistration struct {
	// Type categorizes the capability being registered
	Type string `json:"type"`

	// Name provides a unique identifier for the capability instance
	Name string `json:"name"`

	// Description provides human-readable documentation for the capability
	Description string `json:"description"`

	// Features lists the specific features provided by this capability
	Features []string `json:"features"`

	// Config contains the configuration parameters for this capability
	Config map[string]interface{} `json:"config"`
}

// CapabilityUpdate represents an update to a capability's status.
// These updates are used to notify the capability management system
// about changes in capability availability or health.
type CapabilityUpdate struct {
	// CapabilityID identifies the capability being updated
	CapabilityID string `json:"capability_id"`

	// State indicates the new operational state of the capability
	State CapabilityState `json:"state"`

	// Error contains error information if the update represents a failure
	Error string `json:"error,omitempty"`
}

// CapabilityState represents the operational state of a capability.
// This provides a standardized way to track capability lifecycle and health.
type CapabilityState string

const (
	// CapabilityStateRegistering indicates the capability is being registered
	CapabilityStateRegistering CapabilityState = "registering"

	// CapabilityStateActive indicates the capability is ready and available for use
	CapabilityStateActive CapabilityState = "active"

	// CapabilityStateUnhealthy indicates the capability has health issues but may still function
	CapabilityStateUnhealthy CapabilityState = "unhealthy"

	// CapabilityStateInactive indicates the capability is not available for use
	CapabilityStateInactive CapabilityState = "inactive"
)

// IsValidCapabilityType checks if a capability type is valid.
// A valid capability type is any non-empty string with valid characters.
//
// This function allows flexible capability type definitions, enabling users
// to define custom capability types like "database", "monitoring", "auth", etc.
//
// Parameters:
//   - capType: The capability type string to validate
//
// Returns:
//   - bool: true if the capability type is valid, false otherwise
func IsValidCapabilityType(capType string) bool {
	// Allow any non-empty string as a capability type
	// Users can define their own capability types like "database", "monitoring", etc.
	return len(capType) > 0 && capType != ""
}

// CapabilityHandler defines the interface for capability operations within the Service Locator Pattern.
// This handler provides the primary interface for capability management, execution, and discovery.
//
// The handler abstracts capability complexity behind a simple interface, allowing components
// to execute capability operations without knowing the underlying implementation details.
// It integrates with the ToolProvider interface to expose capabilities as discoverable MCP tools.
type CapabilityHandler interface {
	// Capability execution

	// ExecuteCapability executes a specific capability operation with the provided parameters.
	// This is the primary method for invoking capability functionality.
	//
	// Parameters:
	//   - ctx: Context for the operation, including cancellation and timeout
	//   - capabilityType: The type/category of capability to execute (e.g., "auth", "database")
	//   - operation: The specific operation within the capability (e.g., "login", "backup")
	//   - params: Parameters for the capability operation, validated against operation definition
	//
	// Returns:
	//   - *CallToolResult: The result of the capability execution
	//   - error: Error if the capability/operation doesn't exist or execution fails
	//
	// Example:
	//
	//	result, err := handler.ExecuteCapability(ctx, "auth", "login", map[string]interface{}{
	//	    "username": "user@example.com",
	//	    "password": "secret",
	//	})
	ExecuteCapability(ctx context.Context, capabilityType, operation string, params map[string]interface{}) (*CallToolResult, error)

	// Capability information and availability

	// ListCapabilities returns information about all available capabilities in the system.
	// This includes both static capability definitions and runtime availability status.
	//
	// Returns:
	//   - []Capability: List of all capability definitions with runtime information
	ListCapabilities() []Capability

	// GetCapability retrieves detailed information about a specific capability.
	// This provides more comprehensive information than what's available in ListCapabilities.
	//
	// Parameters:
	//   - name: The name of the capability to retrieve
	//
	// Returns:
	//   - interface{}: Detailed capability information (type depends on implementation)
	//   - error: Error if the capability doesn't exist
	GetCapability(name string) (interface{}, error)

	// IsCapabilityAvailable checks if a specific capability operation is available for execution.
	// This is useful for conditional logic that depends on capability availability.
	//
	// Parameters:
	//   - capabilityType: The type of capability to check (e.g., "auth", "database")
	//   - operation: The specific operation within the capability (e.g., "login", "backup")
	//
	// Returns:
	//   - bool: true if the capability operation is available, false otherwise
	IsCapabilityAvailable(capabilityType, operation string) bool

	// ToolProvider integration for exposing capabilities as discoverable MCP tools.
	// This allows capabilities to be discovered and executed through the standard
	// tool discovery and execution mechanisms.
	ToolProvider
}
