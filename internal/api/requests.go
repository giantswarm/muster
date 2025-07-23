package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Request types for all core API operations

// ServiceClass Request Types

// ServiceClassCreateRequest represents a request to create a new ServiceClass definition.
// ServiceClasses serve as templates for creating service instances with predefined
// lifecycle tools, arg validation, and configuration.
//
// Example:
//
//	request := ServiceClassCreateRequest{
//	    Name: "postgres-database",
//	    Version: "1.0",
//	    Description: "PostgreSQL database service",
//	    ServiceConfig: ServiceConfig{
//	        ServiceType: "database",
//	        DefaultName: "postgres",
//	        LifecycleTools: LifecycleTools{
//	            Start: ToolCall{
//	                Tool: "docker_run",
//	                Arguments: map[string]interface{}{
//	                    "image": "postgres:13",
//	                },
//	            },
//	        },
//	    },
//	}
type ServiceClassCreateRequest struct {
	// Name is the unique identifier for the ServiceClass (required).
	// Must be unique across all ServiceClasses. Should be descriptive
	// and follow naming conventions.
	Name string `json:"name" validate:"required"`

	// Version indicates the ServiceClass version for compatibility tracking.
	// Recommended to use semantic versioning for better change management.
	Version string `json:"version,omitempty"`

	// Description provides human-readable documentation for the ServiceClass.
	// Should explain what type of service this class creates and its purpose.
	Description string `json:"description,omitempty"`

	// Args defines the validation rules and metadata for service creation arguments.
	// These definitions are used to validate arguments when creating service instances
	// and to provide documentation for the service creation API.
	Args map[string]ArgDefinition `json:"args,omitempty"`

	// ServiceConfig defines the service lifecycle management configuration (required).
	// Specifies how services created from this class should be managed.
	ServiceConfig ServiceConfig `json:"serviceConfig" validate:"required"`
}

// ServiceClassUpdateRequest represents a request to update an existing ServiceClass.
// This allows modification of ServiceClass configuration and lifecycle tools.
type ServiceClassUpdateRequest struct {
	// Name is the unique identifier of the ServiceClass to update (required).
	Name string `json:"name" validate:"required"`

	// Version can be updated to reflect changes in the ServiceClass.
	Version string `json:"version,omitempty"`

	// Description can be updated to improve documentation.
	Description string `json:"description,omitempty"`

	// Args defines the validation rules and metadata for service creation arguments.
	// These definitions are used to validate arguments when creating service instances
	// and to provide documentation for the service creation API.
	Args map[string]ArgDefinition `json:"args,omitempty"`

	// ServiceConfig can be updated to modify lifecycle behavior.
	// Changes may affect existing service instances.
	ServiceConfig ServiceConfig `json:"serviceConfig,omitempty"`
}

// ServiceClassValidateRequest represents a request to validate a ServiceClass definition
// without creating it. Useful for testing configurations before deployment.
type ServiceClassValidateRequest struct {
	// Name for validation (required).
	Name string `json:"name" validate:"required"`

	// Version for validation.
	Version string `json:"version,omitempty"`

	// Description for validation.
	Description string `json:"description,omitempty"`

	// Args defines the validation rules and metadata for service creation arguments.
	// These definitions are used to validate arguments when creating service instances
	// and to provide documentation for the service creation API.
	Args map[string]ArgDefinition `json:"args,omitempty"`

	// ServiceConfig to validate (required). All lifecycle tools will be checked
	// for availability and proper configuration.
	ServiceConfig ServiceConfig `json:"serviceConfig" validate:"required"`
}

// MCPServer Request Types

// MCPServerCreateRequest represents a request to create a new MCP server definition.
// This request supports both local and remote MCP servers with type-specific configuration.
//
// Supports local command MCP servers that execute local command/process and remote
// MCP servers that connect to external endpoints.
//
// Example for local command:
//
//	request := MCPServerCreateRequest{
//	    Name: "git-tools",
//	    Type: "local",
//	    Local: &MCPServerLocalConfig{
//	        AutoStart: true,
//	        Command: []string{"npx", "@modelcontextprotocol/server-git"},
//	        Env: map[string]string{
//	            "GIT_ROOT": "/workspace",
//	        },
//	    },
//	}
//
// Example for remote server:
//
//	request := MCPServerCreateRequest{
//	    Name: "remote-tools",
//	    Type: "remote",
//	    Remote: &MCPServerRemoteConfig{
//	        Endpoint: "https://api.example.com/mcp",
//	        Transport: "http",
//	        Timeout: 30,
//	    },
//	}

type MCPServerCreateRequest struct {
	// Name is the unique identifier for the MCP server (required).
	Name string `json:"name" validate:"required"`

	// Type specifies the MCP server type (required).
	// Valid values: "local", "remote"
	Type string `json:"type" validate:"required"`

	// ToolPrefix is prepended to all tool names from this server to avoid conflicts.
	// Optional; if not specified, tools are exposed with their original names.
	ToolPrefix string `json:"toolPrefix,omitempty"`

	// Description for the MCP server
	Description string `json:"description,omitempty"`

	// Local contains configuration for local MCP servers (type=local)
	Local *MCPServerLocalConfig `json:"local,omitempty"`

	// Remote contains configuration for remote MCP servers (type=remote)
	Remote *MCPServerRemoteConfig `json:"remote,omitempty"`
}

// MCPServerUpdateRequest represents a request to update an existing MCP server definition.
// All fields except Name can be modified. Changes may require server restart.
type MCPServerUpdateRequest struct {
	// Name of the MCP server to update (required).
	Name string `json:"name" validate:"required"`

	// Type can be changed, but may require significant reconfiguration.
	Type string `json:"type" validate:"required"`

	// ToolPrefix can be updated, affecting tool naming.
	ToolPrefix string `json:"toolPrefix,omitempty"`

	// Description for the MCP server
	Description string `json:"description,omitempty"`

	// Local contains configuration for local MCP servers (type=local)
	Local *MCPServerLocalConfig `json:"local,omitempty"`

	// Remote contains configuration for remote MCP servers (type=remote)
	Remote *MCPServerRemoteConfig `json:"remote,omitempty"`
}

// MCPServerValidateRequest represents a request to validate an MCP server definition
// without creating it. Validates configuration consistency and tool availability.
type MCPServerValidateRequest struct {
	// Name for validation (required).
	Name string `json:"name" validate:"required"`

	// Type for validation (required).
	Type string `json:"type" validate:"required"`

	// ToolPrefix for validation.
	ToolPrefix string `json:"toolPrefix,omitempty"`

	// Local contains configuration for local MCP servers (type=local)
	Local *MCPServerLocalConfig `json:"local,omitempty"`

	// Remote contains configuration for remote MCP servers (type=remote)
	Remote *MCPServerRemoteConfig `json:"remote,omitempty"`

	// Description for validation and documentation.
	Description string `json:"description,omitempty"`
}

// Workflow Request Types

// WorkflowCreateRequest represents a request to create a new workflow definition.
// Workflows define multi-step processes that orchestrate tool calls with
// arg templating and conditional logic.
//
// Example:
//
//	request := WorkflowCreateRequest{
//	    Name: "deploy-service",
//	    Description: "Deploy a service to production",
//	    Args: map[string]ArgDefinition{
//	        "service_name": {
//	            Type:        "string",
//	            Required:    true,
//	            Description: "Name of the service to deploy",
//	        },
//	    },
//	    Steps: []WorkflowStep{
//	        {
//	            ID:   "build",
//	            Tool: "docker_build",
//	            Args: map[string]interface{}{
//	                "name": "{{service_name}}",
//	            },
//	        },
//	    },
//	}
type WorkflowCreateRequest struct {
	// Name is the unique identifier for the workflow (required).
	// Must be unique across all workflows in the system.
	Name string `json:"name" validate:"required"`

	// Version indicates the workflow version for compatibility tracking.
	// Recommended to use semantic versioning.
	Version string `json:"version,omitempty"`

	// Description provides human-readable documentation for the workflow.
	// Should explain the workflow's purpose and expected outcomes.
	Description string `json:"description,omitempty"`

	// Args defines the expected input arguments for workflow execution.
	// Used for arg validation and documentation generation.
	// If not specified, the workflow accepts any args.
	Args map[string]ArgDefinition `json:"args,omitempty"`

	// Steps defines the sequence of operations to perform (required).
	// Each step executes a tool with specified arguments and processing logic.
	// Must contain at least one step for a valid workflow.
	Steps []WorkflowStep `json:"steps" validate:"required"`
}

// WorkflowUpdateRequest represents a request to update an existing workflow definition.
// This allows modification of workflow steps, input args, and metadata.
type WorkflowUpdateRequest struct {
	// Name of the workflow to update (required).
	Name string `json:"name" validate:"required"`

	// Version can be updated to reflect changes.
	Version string `json:"version,omitempty"`

	// Description can be updated to improve documentation.
	Description string `json:"description,omitempty"`

	// Args can be modified to change arg requirements.
	// Changes may affect existing callers of this workflow.
	Args map[string]ArgDefinition `json:"args,omitempty"`

	// Steps can be added, modified, or reordered.
	// Changes affect workflow execution behavior.
	Steps []WorkflowStep `json:"steps,omitempty"`
}

// WorkflowValidateRequest represents a request to validate a workflow definition
// without creating it. Validates step configuration, tool availability, and arg schemas.
type WorkflowValidateRequest struct {
	// Name for validation (required).
	Name string `json:"name" validate:"required"`

	// Version for validation.
	Version string `json:"version,omitempty"`

	// Description for validation.
	Description string `json:"description,omitempty"`

	// Args for validation.
	Args map[string]ArgDefinition `json:"args,omitempty"`

	// Steps for validation (required). All referenced tools will be checked for availability.
	Steps []WorkflowStep `json:"steps" validate:"required"`
}

// Service Request Types

// ServiceValidateRequest represents a request to validate service creation args
// against a ServiceClass definition. This is useful for validating args
// before actually creating a service instance.
//
// Example:
//
//	request := ServiceValidateRequest{
//	    Name: "my-database",
//	    ServiceClassName: "postgres-database",
//	    Args: map[string]interface{}{
//	        "database_name": "myapp",
//	        "username":      "dbuser",
//	        "port":          5432,
//	    },
//	}
type ServiceValidateRequest struct {
	// Name is the proposed name for the service instance (required).
	// Must be unique across all services (both static and ServiceClass-based).
	Name string `json:"name" validate:"required"`

	// ServiceClassName specifies which ServiceClass to use as template (required).
	// Must reference an existing ServiceClass.
	ServiceClassName string `json:"serviceClassName" validate:"required"`

	// Args provides the configuration for service creation.
	// Must match the argument definitions in the ServiceClass.
	Args map[string]interface{} `json:"args,omitempty"`

	// AutoStart determines if the service should start automatically after creation.
	AutoStart bool `json:"autoStart,omitempty"`

	// Description provides optional documentation for this service instance.
	Description string `json:"description,omitempty"`
}

// ParseRequest converts a map[string]interface{} to a typed request struct.
// This uses JSON marshaling/unmarshaling for type conversion and validation,
// providing strict arg checking and type safety.
//
// The function validates that no unknown args are present and performs
// basic type validation according to the target struct's field types and tags.
//
// Args:
//   - args: The input arguments to parse and validate
//   - request: Pointer to the target request struct to populate
//
// Returns:
//   - error: Validation error if arguments are invalid or contain unknown fields
//
// Example:
//
//	var req ServiceClassCreateRequest
//	args := map[string]interface{}{
//	    "name": "auth",
//	    "type": "authentication",
//	    "operations": map[string]interface{}{...},
//	}
//	err := ParseRequest(args, &req)
//	if err != nil {
//	    return fmt.Errorf("invalid request: %w", err)
//	}
func ParseRequest[T any](args map[string]interface{}, request *T) error {
	// First validate that no unknown args are present
	if err := validateStrictArgs(args, request); err != nil {
		return err
	}

	// Convert to JSON and back to get proper type conversion
	jsonData, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to marshal request arguments: %w", err)
	}

	// Use strict JSON decoder that fails on unknown fields
	decoder := json.NewDecoder(strings.NewReader(string(jsonData)))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(request); err != nil {
		return fmt.Errorf("failed to parse request: %w", err)
	}

	// Basic validation - check required fields
	if err := validateRequest(request); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	return nil
}

// validateRequest performs basic validation on request structs.
// This is a simple implementation that relies on JSON unmarshaling for type conversion.
// Future versions might integrate with a validation library like github.com/go-playground/validator.
//
// Currently, this function serves as a placeholder for more sophisticated validation
// that could include custom validation rules, cross-field validation, and business logic checks.
func validateRequest(request interface{}) error {
	// Use reflection to check for required fields
	// This is a simplified implementation - in production you might want to use
	// a validation library like github.com/go-playground/validator

	// For now, we rely on the JSON unmarshaling to handle type conversion
	// and the calling code to check for required fields
	return nil
}

// ValidateStrictArgs ensures no unknown args are present in the request.
// This function provides strict arg validation by checking the provided arguments
// against a list of allowed field names.
//
// Args:
//   - args: The arguments to validate
//   - allowedFields: List of arg names that are allowed
//
// Returns:
//   - error: Error listing unknown args if any are found
//
// Example:
//
//	allowed := []string{"name", "type", "description"}
//	err := ValidateStrictArgs(args, allowed)
//	if err != nil {
//	    return fmt.Errorf("arg validation failed: %w", err)
//	}
func ValidateStrictArgs(args map[string]interface{}, allowedFields []string) error {
	allowedMap := make(map[string]bool)
	for _, field := range allowedFields {
		allowedMap[field] = true
	}

	var unknownFields []string
	for field := range args {
		if !allowedMap[field] {
			unknownFields = append(unknownFields, field)
		}
	}

	if len(unknownFields) > 0 {
		return fmt.Errorf("unknown args: %v. Allowed args: %v", unknownFields, allowedFields)
	}

	return nil
}

// validateStrictArgs ensures no unknown args are present by comparing
// against the JSON tags of the target struct. This provides automatic validation
// based on the struct definition without requiring manual field lists.
//
// Args:
//   - args: The arguments to validate
//   - request: The target struct to validate against
//
// Returns:
//   - error: Error listing unknown args if any are found
func validateStrictArgs(args map[string]interface{}, request interface{}) error {
	// Get the struct type
	structType := reflect.TypeOf(request).Elem()

	// Build a map of allowed field names based on JSON tags
	allowedFields := make(map[string]bool)
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		jsonTag := field.Tag.Get("json")

		if jsonTag != "" && jsonTag != "-" {
			// Parse the JSON tag to get the field name
			fieldName := strings.Split(jsonTag, ",")[0]
			if fieldName != "" {
				allowedFields[fieldName] = true
			}
		} else {
			// If no JSON tag, use the field name
			allowedFields[field.Name] = true
		}
	}

	// Check for unknown args
	var unknownParams []string
	for paramName := range args {
		if !allowedFields[paramName] {
			unknownParams = append(unknownParams, paramName)
		}
	}

	if len(unknownParams) > 0 {
		var allowedNames []string
		for name := range allowedFields {
			allowedNames = append(allowedNames, name)
		}
		return fmt.Errorf("unknown args: %v. Allowed args: %v", unknownParams, allowedNames)
	}

	return nil
}
