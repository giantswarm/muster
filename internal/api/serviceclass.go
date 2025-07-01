package api

import (
	"fmt"
	"time"
)

// ArgDefinition defines validation rules and metadata for a service creation argument.
// This structure provides comprehensive argument validation capabilities for ServiceClass
// templates, ensuring that service instances are created with valid configuration values.
type ArgDefinition struct {
	// Type specifies the expected data type for this arg.
	// Valid types are "string", "integer", "boolean", and "number".
	// This is used for runtime type validation during service creation.
	Type string `yaml:"type" json:"type"`

	// Required indicates whether this arg must be provided when creating a service instance.
	// Required args without default values will cause service creation to fail if not provided.
	Required bool `yaml:"required" json:"required"`

	// Default provides a default value to use if this arg is not provided during service creation.
	// The default value must match the specified Type. If no default is provided and the arg
	// is not required, the arg will be omitted from the service configuration.
	Default interface{} `yaml:"default,omitempty" json:"default,omitempty"`

	// Description provides human-readable documentation for this arg.
	// This is used for generating help text, UI forms, and API documentation.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// ServiceClass represents a service template that defines how to create and manage service instances.
// It consolidates ServiceClassDefinition, ServiceClassInfo, and ServiceClassConfig into a unified type
// that serves as both a configuration blueprint and runtime information container.
//
// ServiceClasses are templates that define the structure, args, and lifecycle management
// for services. They specify which tools should be called for various lifecycle events and
// provide arg validation for service instance creation.
type ServiceClass struct {
	// Name is the unique identifier for this ServiceClass.
	// This name is used when creating service instances and for ServiceClass management operations.
	Name string `yaml:"name" json:"name"`

	// Version specifies the version of this ServiceClass definition.
	// This can be used for compatibility checks and configuration migration.
	Version string `yaml:"version" json:"version"`

	// Description provides a human-readable explanation of what this ServiceClass does
	// and what kind of services can be created from it.
	Description string `yaml:"description" json:"description"`

	// Args defines the validation rules and metadata for service creation arguments.
	// These definitions are used to validate arguments when creating service instances
	// and to provide documentation for the service creation API.
	Args map[string]ArgDefinition `yaml:"args,omitempty" json:"args,omitempty"`

	// ServiceConfig contains the core configuration for service lifecycle management.
	// This defines how services created from this class should be managed, including
	// tool mappings, health checks, and operational args.
	ServiceConfig ServiceConfig `yaml:"serviceConfig" json:"serviceConfig"`

	// Operations defines custom operations that can be performed on services created from this class.
	// These operations extend the basic lifecycle management with service-specific functionality.
	Operations map[string]OperationDefinition `yaml:"operations" json:"operations"`

	// ServiceType indicates the general category of service that this class creates.
	// This is runtime information derived from the ServiceConfig and used for categorization.
	ServiceType string `json:"serviceType,omitempty" yaml:"-"`

	// Available indicates whether this ServiceClass can currently be used to create service instances.
	// This depends on the availability of required tools and other dependencies.
	Available bool `json:"available,omitempty" yaml:"-"`

	// State represents the current operational state of the ServiceClass itself.
	// This is runtime information and not persisted to YAML files.
	State string `json:"state,omitempty" yaml:"-"`

	// Health indicates the health status of the ServiceClass.
	// This reflects whether the class's dependencies and tools are functioning properly.
	Health string `json:"health,omitempty" yaml:"-"`

	// Error contains any error message related to this ServiceClass.
	// This is populated if the class cannot be used due to missing tools or other issues.
	Error string `json:"error,omitempty" yaml:"-"`

	// CreateToolAvailable indicates whether the tool required for service creation is available.
	// This is part of the tool availability assessment for the ServiceClass.
	CreateToolAvailable bool `json:"createToolAvailable,omitempty" yaml:"-"`

	// DeleteToolAvailable indicates whether the tool required for service deletion is available.
	// This is part of the tool availability assessment for the ServiceClass.
	DeleteToolAvailable bool `json:"deleteToolAvailable,omitempty" yaml:"-"`

	// HealthCheckToolAvailable indicates whether the health check tool is available.
	// This affects whether health monitoring can be performed for services created from this class.
	HealthCheckToolAvailable bool `json:"healthCheckToolAvailable,omitempty" yaml:"-"`

	// StatusToolAvailable indicates whether the status query tool is available.
	// This affects whether detailed status information can be retrieved for services.
	StatusToolAvailable bool `json:"statusToolAvailable,omitempty" yaml:"-"`

	// RequiredTools lists all tools that must be available for this ServiceClass to function.
	// This is computed from the ServiceConfig tool mappings and used for dependency checking.
	RequiredTools []string `json:"requiredTools,omitempty" yaml:"-"`

	// MissingTools lists any required tools that are currently not available.
	// This is used to determine why a ServiceClass might not be available for use.
	MissingTools []string `json:"missingTools,omitempty" yaml:"-"`
}

// ServiceConfig defines the service lifecycle management configuration for a ServiceClass.
// This structure specifies how services should be managed, including tool mappings for
// lifecycle events, health check configuration, and arg handling.
type ServiceConfig struct {
	// ServiceType categorizes the kind of service this configuration manages.
	// This is used for grouping, filtering, and applying type-specific logic.
	ServiceType string `yaml:"serviceType" json:"serviceType"`

	// DefaultName provides a default name pattern for services created from this class.
	// This can include templating placeholders that are replaced during service creation.
	DefaultName string `yaml:"defaultName" json:"defaultName"`

	// Dependencies lists other ServiceClasses that must be available before services
	// of this type can be created. This ensures proper ordering and availability.
	Dependencies []string `yaml:"dependencies" json:"dependencies"`

	// LifecycleTools maps service lifecycle events to specific aggregator tools.
	// These tools are called by the orchestrator to manage service instances.
	LifecycleTools LifecycleTools `yaml:"lifecycleTools" json:"lifecycleTools"`

	// HealthCheck configures health monitoring for services created from this class.
	// This determines how often health checks are performed and what constitutes healthy/unhealthy states.
	HealthCheck HealthCheckConfig `yaml:"healthCheck" json:"healthCheck"`

	// Timeout specifies timeout values for various service operations.
	// These timeouts help prevent operations from hanging indefinitely.
	Timeout TimeoutConfig `yaml:"timeout" json:"timeout"`

	// CreateArgs defines how service creation args should be mapped to tool arguments.
	// This allows ServiceClass args to be transformed and passed to the appropriate tools.
	CreateArgs map[string]ArgMapping `yaml:"createArgs" json:"createArgs"`
}

// LifecycleTools maps service lifecycle events to aggregator tools.
// This structure defines which tools should be called for each lifecycle operation,
// enabling the orchestrator to manage services without knowing about specific implementations.
type LifecycleTools struct {
	// Start specifies the tool to call when starting a service instance.
	// This tool is responsible for initializing and launching the service.
	Start ToolCall `yaml:"start" json:"start"`

	// Stop specifies the tool to call when stopping a service instance.
	// This tool should gracefully shut down the service and clean up resources.
	Stop ToolCall `yaml:"stop" json:"stop"`

	// Restart specifies the tool to call when restarting a service instance.
	// If not provided, restart operations will use Stop followed by Start.
	Restart *ToolCall `yaml:"restart,omitempty" json:"restart,omitempty"`

	// HealthCheck specifies the tool to call for health monitoring.
	// This tool should return information about the service's current health status.
	HealthCheck *ToolCall `yaml:"healthCheck,omitempty" json:"healthCheck,omitempty"`

	// Status specifies the tool to call for retrieving detailed service status.
	// This tool should return comprehensive information about the service's current state.
	Status *ToolCall `yaml:"status,omitempty" json:"status,omitempty"`
}

// ServiceClassManagerHandler defines the interface for service class management operations.
// This interface provides functionality for managing ServiceClass definitions, validating
// their availability, and accessing their configuration for service orchestration.
type ServiceClassManagerHandler interface {
	// ListServiceClasses returns information about all available ServiceClasses.
	// This includes both configuration and runtime availability information.
	//
	// Returns:
	//   - []ServiceClass: Slice of ServiceClass information (empty if none exist)
	ListServiceClasses() []ServiceClass

	// GetServiceClass retrieves detailed information about a specific ServiceClass.
	// This includes configuration, runtime state, and tool availability information.
	//
	// Args:
	//   - name: The unique name of the ServiceClass to retrieve
	//
	// Returns:
	//   - *ServiceClass: ServiceClass information, or nil if not found
	//   - error: nil on success, or an error if the ServiceClass could not be retrieved
	GetServiceClass(name string) (*ServiceClass, error)

	// IsServiceClassAvailable checks whether a ServiceClass can currently be used.
	// This verifies that all required tools are available and dependencies are met.
	//
	// Args:
	//   - name: The unique name of the ServiceClass to check
	//
	// Returns:
	//   - bool: true if the ServiceClass is available for use, false otherwise
	IsServiceClassAvailable(name string) bool

	// GetStartTool retrieves the tool configuration for starting services of this class.
	// This provides the orchestrator with the information needed to start service instances.
	//
	// Args:
	//   - name: The unique name of the ServiceClass
	//
	// Returns:
	//   - toolName: The name of the tool to call
	//   - arguments: Tool arguments to use
	//   - responseMapping: How to interpret the tool response
	//   - err: nil on success, or an error if the tool configuration could not be retrieved
	GetStartTool(name string) (toolName string, args map[string]interface{}, responseMapping map[string]string, err error)

	// GetStopTool retrieves the tool configuration for stopping services of this class.
	// This provides the orchestrator with the information needed to stop service instances.
	//
	// Args:
	//   - name: The unique name of the ServiceClass
	//
	// Returns:
	//   - toolName: The name of the tool to call
	//   - arguments: Tool arguments to use
	//   - responseMapping: How to interpret the tool response
	//   - err: nil on success, or an error if the tool configuration could not be retrieved
	GetStopTool(name string) (toolName string, args map[string]interface{}, responseMapping map[string]string, err error)

	// GetRestartTool retrieves the tool configuration for restarting services of this class.
	// If no restart tool is configured, this may return an indication to use stop+start.
	//
	// Args:
	//   - name: The unique name of the ServiceClass
	//
	// Returns:
	//   - toolName: The name of the tool to call
	//   - arguments: Tool arguments to use
	//   - responseMapping: How to interpret the tool response
	//   - err: nil on success, or an error if the tool configuration could not be retrieved
	GetRestartTool(name string) (toolName string, args map[string]interface{}, responseMapping map[string]string, err error)

	// GetHealthCheckTool retrieves the tool configuration for health checking services of this class.
	// This provides the health monitoring system with the information needed to check service health.
	//
	// Args:
	//   - name: The unique name of the ServiceClass
	//
	// Returns:
	//   - toolName: The name of the tool to call
	//   - arguments: Tool arguments to use
	//   - responseMapping: How to interpret the tool response
	//   - err: nil on success, or an error if the tool configuration could not be retrieved
	GetHealthCheckTool(name string) (toolName string, args map[string]interface{}, responseMapping map[string]string, err error)

	// GetHealthCheckConfig retrieves the health check configuration for services of this class.
	// This provides the health monitoring system with timing and threshold information.
	//
	// Args:
	//   - name: The unique name of the ServiceClass
	//
	// Returns:
	//   - enabled: Whether health checking is enabled for this service class
	//   - interval: How often health checks should be performed
	//   - failureThreshold: Number of consecutive failures before marking unhealthy
	//   - successThreshold: Number of consecutive successes before marking healthy
	//   - err: nil on success, or an error if the configuration could not be retrieved
	GetHealthCheckConfig(name string) (enabled bool, interval time.Duration, failureThreshold, successThreshold int, err error)

	// GetServiceDependencies retrieves the list of dependencies for services of this class.
	// This is used by the orchestrator to ensure proper startup ordering.
	//
	// Args:
	//   - name: The unique name of the ServiceClass
	//
	// Returns:
	//   - []string: List of dependency ServiceClass names
	//   - error: nil on success, or an error if dependencies could not be retrieved
	GetServiceDependencies(name string) ([]string, error)

	// ToolProvider interface for exposing ServiceClass management tools.
	// This allows ServiceClass operations to be performed through the aggregator
	// tool system, enabling programmatic and user-driven class management.
	ToolProvider
}

// ValidateServiceArgs validates service creation args against ServiceClass arg definitions.
// This method ensures that all required args are provided, that arg types are correct,
// and that no unknown args are specified. It also applies default values where appropriate.
//
// The validation process:
// 1. Checks that all required args are provided
// 2. Applies default values for missing optional args
// 3. Validates arg types match their definitions
// 4. Rejects unknown args not defined in the ServiceClass
//
// Args:
//   - args: The arg map to validate and potentially modify
//
// Returns:
//   - error: nil if validation succeeds, or a descriptive error if validation fails
func (sc *ServiceClass) ValidateServiceArgs(args map[string]interface{}) error {
	if sc.Args == nil {
		// If no argument definitions, accept any arguments
		return nil
	}

	// Check for required arguments
	for argName, argDef := range sc.Args {
		value, provided := args[argName]

		if !provided {
			if argDef.Required {
				return fmt.Errorf("missing required argument: %s", argName)
			}
			// Apply default value if not provided
			if argDef.Default != nil {
				args[argName] = argDef.Default
			}
			continue
		}

		// Validate argument type
		if err := validateArgType(argName, value, argDef.Type); err != nil {
			return err
		}
	}

	// Check for unknown arguments
	for argName := range args {
		if _, defined := sc.Args[argName]; !defined {
			return fmt.Errorf("unknown argument: %s", argName)
		}
	}

	return nil
}

// validateArgType validates that a arg value matches the expected type.
// This function performs type checking for the supported arg types: string, integer,
// boolean, and number. It handles type coercion where appropriate (e.g., float64 to int).
//
// Args:
//   - argName: The name of the arg being validated (for error messages)
//   - value: The actual arg value to validate
//   - expectedType: The expected type as defined in the ArgDefinition
//
// Returns:
//   - error: nil if the type is valid, or an error describing the type mismatch
func validateArgType(argName string, value interface{}, expectedType string) error {
	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("invalid type for argument '%s': expected string, got %T", argName, value)
		}
	case "integer":
		switch value.(type) {
		case int, int32, int64, float64:
			// Accept numeric types that can represent integers
			if f, ok := value.(float64); ok && f != float64(int64(f)) {
				return fmt.Errorf("invalid type for argument '%s': expected integer, got float", argName)
			}
		default:
			return fmt.Errorf("invalid type for argument '%s': expected integer, got %T", argName, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("invalid type for argument '%s': expected boolean, got %T", argName, value)
		}
	case "number":
		switch value.(type) {
		case int, int32, int64, float32, float64:
			// Accept any numeric type
		default:
			return fmt.Errorf("invalid type for argument '%s': expected number, got %T", argName, value)
		}
	default:
		return fmt.Errorf("unknown argument type '%s' for argument '%s'", expectedType, argName)
	}
	return nil
}
