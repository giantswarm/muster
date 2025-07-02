package api

import (
	"errors"
	"fmt"
)

// NotFoundError represents a resource not found error with contextual information.
// This standardized error type provides consistent error handling across all API operations
// for cases where requested resources don't exist in the system.
//
// The error includes resource type and name for precise error reporting and
// supports custom error messages for specific use cases.
type NotFoundError struct {
	// ResourceType categorizes the type of resource that was not found
	// (e.g., "workflow", "serviceclass", "service", "capability")
	ResourceType string

	// ResourceName is the specific identifier of the resource that was not found
	ResourceName string

	// Message provides a custom error message if the default format is insufficient
	Message string
}

// Error implements the error interface for NotFoundError.
// Returns either the custom message if provided, or a formatted default message
// using the resource type and name.
//
// Returns:
//   - string: The error message describing the not found condition
func (e *NotFoundError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("%s %s not found", e.ResourceType, e.ResourceName)
}

// IsNotFound checks if an error is a NotFoundError using error unwrapping.
// This function provides a type-safe way to check for not found conditions
// in error handling code, supporting wrapped errors.
//
// Args:
//   - err: The error to check
//
// Returns:
//   - bool: true if the error is or wraps a NotFoundError, false otherwise
//
// Example:
//
//	result, err := GetResource("nonexistent")
//	if api.IsNotFound(err) {
//	    // Handle not found case
//	    return nil, fmt.Errorf("resource does not exist")
//	}
func IsNotFound(err error) bool {
	var notFoundErr *NotFoundError
	return errors.As(err, &notFoundErr)
}

// NewNotFoundError creates a new NotFoundError with the specified resource type and name.
// This is the standard way to create not found errors throughout the API.
//
// Args:
//   - resourceType: The category of resource (e.g., "workflow", "service")
//   - resourceName: The specific identifier of the resource
//
// Returns:
//   - *NotFoundError: A new NotFoundError instance
//
// Example:
//
//	return api.NewNotFoundError("workflow", "deploy-app")
func NewNotFoundError(resourceType, resourceName string) *NotFoundError {
	return &NotFoundError{
		ResourceType: resourceType,
		ResourceName: resourceName,
	}
}

// NewNotFoundErrorWithMessage creates a new NotFoundError with a custom message.
// This is used when the default error format doesn't provide sufficient context.
//
// Args:
//   - resourceType: The category of resource
//   - resourceName: The specific identifier of the resource
//   - message: Custom error message to use instead of the default format
//
// Returns:
//   - *NotFoundError: A new NotFoundError instance with custom message
//
// Example:
//
//	return api.NewNotFoundErrorWithMessage("service", "database",
//	    "database service is not available in this environment")
func NewNotFoundErrorWithMessage(resourceType, resourceName, message string) *NotFoundError {
	return &NotFoundError{
		ResourceType: resourceType,
		ResourceName: resourceName,
		Message:      message,
	}
}

// Specific NotFoundError constructors for each resource type.
// These provide convenient, type-specific error creation with consistent naming.
var (
	// NewWorkflowNotFoundError creates a workflow not found error.
	//
	// Args:
	//   - name: The name of the workflow that was not found
	//
	// Returns:
	//   - *NotFoundError: A NotFoundError for the specified workflow
	NewWorkflowNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("workflow", name)
	}

	// NewServiceClassNotFoundError creates a service class not found error.
	//
	// Args:
	//   - name: The name of the service class that was not found
	//
	// Returns:
	//   - *NotFoundError: A NotFoundError for the specified service class
	NewServiceClassNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("service class", name)
	}

	// NewServiceNotFoundError creates a service not found error.
	//
	// Args:
	//   - name: The name of the service that was not found
	//
	// Returns:
	//   - *NotFoundError: A NotFoundError for the specified service
	NewServiceNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("service", name)
	}

	// NewCapabilityNotFoundError creates a capability not found error.
	//
	// Args:
	//   - name: The name of the capability that was not found
	//
	// Returns:
	//   - *NotFoundError: A NotFoundError for the specified capability
	NewCapabilityNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("capability", name)
	}

	// NewMCPServerNotFoundError creates an MCP server not found error.
	//
	// Args:
	//   - name: The name of the MCP server that was not found
	//
	// Returns:
	//   - *NotFoundError: A NotFoundError for the specified MCP server
	NewMCPServerNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("MCP server", name)
	}

	// NewToolNotFoundError creates a tool not found error.
	//
	// Args:
	//   - name: The name of the tool that was not found
	//
	// Returns:
	//   - *NotFoundError: A NotFoundError for the specified tool
	NewToolNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("tool", name)
	}

	// NewResourceNotFoundError creates a resource not found error.
	//
	// Args:
	//   - name: The name of the resource that was not found
	//
	// Returns:
	//   - *NotFoundError: A NotFoundError for the specified resource
	NewResourceNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("resource", name)
	}

	// NewPromptNotFoundError creates a prompt not found error.
	//
	// Args:
	//   - name: The name of the prompt that was not found
	//
	// Returns:
	//   - *NotFoundError: A NotFoundError for the specified prompt
	NewPromptNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("prompt", name)
	}
)

// Common errors for API operations.
// These predefined errors provide consistent error reporting for common failure scenarios
// related to handler registration in the Service Locator Pattern.
var (
	// Handler not registered errors - indicate that required handlers are not available

	// ErrOrchestratorNotRegistered indicates the orchestrator handler is not registered
	ErrOrchestratorNotRegistered = errors.New("orchestrator handler not registered")

	// ErrMCPServiceNotRegistered indicates the MCP service handler is not registered
	ErrMCPServiceNotRegistered = errors.New("MCP service handler not registered")

	// ErrPortForwardNotRegistered indicates the port forward handler is not registered
	ErrPortForwardNotRegistered = errors.New("port forward handler not registered")

	// ErrK8sServiceNotRegistered indicates the Kubernetes service handler is not registered
	ErrK8sServiceNotRegistered = errors.New("K8s service handler not registered")

	// ErrConfigServiceNotRegistered indicates the config service handler is not registered
	ErrConfigServiceNotRegistered = errors.New("config service handler not registered")

	// ErrCapabilityNotRegistered indicates the capability handler is not registered
	ErrCapabilityNotRegistered = errors.New("capability handler not registered")

	// ErrWorkflowNotRegistered indicates the workflow handler is not registered
	ErrWorkflowNotRegistered = errors.New("workflow handler not registered")

	// ErrAggregatorNotRegistered indicates the aggregator handler is not registered
	ErrAggregatorNotRegistered = errors.New("aggregator handler not registered")

	// Legacy workflow error (deprecated - use NewWorkflowNotFoundError instead)
	// This error is maintained for backward compatibility but should not be used in new code.
	//
	// Deprecated: Use NewWorkflowNotFoundError(name) instead for better error context.
	ErrWorkflowNotFound = errors.New("workflow not found")
)

// HandleError creates an appropriate CallToolResult based on the error type.
// This function provides standardized error response formatting for API operations.
//
// All errors (including NotFoundError) are treated as error conditions for
// compatibility with the test framework and consistent API behavior.
//
// Args:
//   - err: The error to handle and format
//
// Returns:
//   - *CallToolResult: A CallToolResult with error information and IsError set to true
//
// Example:
//
//	if err != nil {
//	    return api.HandleError(err)
//	}
func HandleError(err error) *CallToolResult {
	return &CallToolResult{
		Content: []interface{}{fmt.Sprintf("Failed to get resource: %v", err)},
		IsError: true, // All failures are treated as errors for test framework compatibility
	}
}

// HandleErrorWithPrefix creates an appropriate CallToolResult with a custom prefix.
// This function is similar to HandleError but allows customizing the error message prefix
// for more specific error context.
//
// Args:
//   - err: The error to handle and format
//   - prefix: Custom prefix to prepend to the error message
//
// Returns:
//   - *CallToolResult: A CallToolResult with prefixed error information and IsError set to true
//
// Example:
//
//	if err != nil {
//	    return api.HandleErrorWithPrefix(err, "Failed to create service")
//	}
func HandleErrorWithPrefix(err error, prefix string) *CallToolResult {
	return &CallToolResult{
		Content: []interface{}{fmt.Sprintf("%s: %v", prefix, err)},
		IsError: true, // All failures are treated as errors for test framework compatibility
	}
}
