package api

import (
	"fmt"
	"time"
)

// ParameterDefinition defines validation rules for a service creation parameter
type ParameterDefinition struct {
	Type        string      `yaml:"type" json:"type"`                                   // "string", "integer", "boolean", "number"
	Required    bool        `yaml:"required" json:"required"`                           // Whether this parameter is required
	Default     interface{} `yaml:"default,omitempty" json:"default,omitempty"`         // Default value if not provided
	Description string      `yaml:"description,omitempty" json:"description,omitempty"` // Human-readable description
}

// ServiceClass represents a single service class definition and runtime state
// This consolidates ServiceClassDefinition, ServiceClassInfo, and ServiceClassConfig into one type
type ServiceClass struct {
	// Configuration fields (from YAML) - Template/Blueprint definition
	Name        string `yaml:"name" json:"name"`
	Version     string `yaml:"version" json:"version"`
	Description string `yaml:"description" json:"description"`

	// Parameter definitions for service creation validation
	Parameters map[string]ParameterDefinition `yaml:"parameters,omitempty" json:"parameters,omitempty"`

	// Service lifecycle configuration
	ServiceConfig ServiceConfig                  `yaml:"serviceConfig" json:"serviceConfig"`
	Operations    map[string]OperationDefinition `yaml:"operations" json:"operations"`

	// Runtime state fields (for API responses)
	ServiceType string `json:"serviceType,omitempty" yaml:"-"`
	Available   bool   `json:"available,omitempty" yaml:"-"`
	State       string `json:"state,omitempty" yaml:"-"`
	Health      string `json:"health,omitempty" yaml:"-"`
	Error       string `json:"error,omitempty" yaml:"-"`

	// Tool availability
	CreateToolAvailable      bool     `json:"createToolAvailable,omitempty" yaml:"-"`
	DeleteToolAvailable      bool     `json:"deleteToolAvailable,omitempty" yaml:"-"`
	HealthCheckToolAvailable bool     `json:"healthCheckToolAvailable,omitempty" yaml:"-"`
	StatusToolAvailable      bool     `json:"statusToolAvailable,omitempty" yaml:"-"`
	RequiredTools            []string `json:"requiredTools,omitempty" yaml:"-"`
	MissingTools             []string `json:"missingTools,omitempty" yaml:"-"`
}

// ServiceConfig defines the service lifecycle management configuration
type ServiceConfig struct {
	// Service metadata
	ServiceType  string   `yaml:"serviceType" json:"serviceType"`
	DefaultName  string   `yaml:"defaultName" json:"defaultName"`
	Dependencies []string `yaml:"dependencies" json:"dependencies"`

	// Lifecycle tool mappings - these tools will be called by the orchestrator
	LifecycleTools LifecycleTools `yaml:"lifecycleTools" json:"lifecycleTools"`

	// Service behavior configuration
	HealthCheck HealthCheckConfig `yaml:"healthCheck" json:"healthCheck"`
	Timeout     TimeoutConfig     `yaml:"timeout" json:"timeout"`

	// Parameter mapping for service creation
	CreateParameters map[string]ParameterMapping `yaml:"createParameters" json:"createParameters"`
}

// LifecycleTools maps service lifecycle events to aggregator tools
type LifecycleTools struct {
	// Tool to call when starting the service (maps to Service.Start)
	Start ToolCall `yaml:"start" json:"start"`

	// Tool to call when stopping the service (maps to Service.Stop)
	Stop ToolCall `yaml:"stop" json:"stop"`

	// Tool to call for restarting the service (optional, maps to Service.Restart)
	Restart *ToolCall `yaml:"restart,omitempty" json:"restart,omitempty"`

	// Tool to call for health checks (optional)
	HealthCheck *ToolCall `yaml:"healthCheck,omitempty" json:"healthCheck,omitempty"`

	// Tool to call to get service status/info (optional)
	Status *ToolCall `yaml:"status,omitempty" json:"status,omitempty"`
}

// ServiceClassManagerHandler defines the interface for service class management operations
type ServiceClassManagerHandler interface {
	// Service class definition management
	ListServiceClasses() []ServiceClass
	GetServiceClass(name string) (*ServiceClass, error)
	IsServiceClassAvailable(name string) bool

	// Lifecycle tool access (for service orchestration without direct coupling)
	GetStartTool(name string) (toolName string, arguments map[string]interface{}, responseMapping map[string]string, err error)
	GetStopTool(name string) (toolName string, arguments map[string]interface{}, responseMapping map[string]string, err error)
	GetRestartTool(name string) (toolName string, arguments map[string]interface{}, responseMapping map[string]string, err error)
	GetHealthCheckTool(name string) (toolName string, arguments map[string]interface{}, responseMapping map[string]string, err error)
	GetHealthCheckConfig(name string) (enabled bool, interval time.Duration, failureThreshold, successThreshold int, err error)
	GetServiceDependencies(name string) ([]string, error)

	// Tool provider interface for exposing ServiceClass management tools
	ToolProvider
}

// ValidateServiceParameters validates service creation parameters against ServiceClass parameter definitions
func (sc *ServiceClass) ValidateServiceParameters(parameters map[string]interface{}) error {
	if sc.Parameters == nil {
		// If no parameter definitions, accept any parameters (backward compatibility)
		return nil
	}

	// Check for required parameters
	for paramName, paramDef := range sc.Parameters {
		value, provided := parameters[paramName]

		if !provided {
			if paramDef.Required {
				return fmt.Errorf("missing required parameter: %s", paramName)
			}
			// Apply default value if not provided
			if paramDef.Default != nil {
				parameters[paramName] = paramDef.Default
			}
			continue
		}

		// Validate parameter type
		if err := validateParameterType(paramName, value, paramDef.Type); err != nil {
			return err
		}
	}

	// Check for unknown parameters
	for paramName := range parameters {
		if _, defined := sc.Parameters[paramName]; !defined {
			return fmt.Errorf("unknown parameter: %s", paramName)
		}
	}

	return nil
}

// validateParameterType validates that a parameter value matches the expected type
func validateParameterType(paramName string, value interface{}, expectedType string) error {
	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("invalid type for parameter '%s': expected string, got %T", paramName, value)
		}
	case "integer":
		switch value.(type) {
		case int, int32, int64, float64:
			// Accept numeric types that can represent integers
			if f, ok := value.(float64); ok && f != float64(int64(f)) {
				return fmt.Errorf("invalid type for parameter '%s': expected integer, got float", paramName)
			}
		default:
			return fmt.Errorf("invalid type for parameter '%s': expected integer, got %T", paramName, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("invalid type for parameter '%s': expected boolean, got %T", paramName, value)
		}
	case "number":
		switch value.(type) {
		case int, int32, int64, float32, float64:
			// Accept any numeric type
		default:
			return fmt.Errorf("invalid type for parameter '%s': expected number, got %T", paramName, value)
		}
	default:
		return fmt.Errorf("unknown parameter type '%s' for parameter '%s'", expectedType, paramName)
	}
	return nil
}
