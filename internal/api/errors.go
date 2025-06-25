package api

import (
	"errors"
	"fmt"
)

// NotFoundError represents a resource not found error
type NotFoundError struct {
	ResourceType string // e.g., "workflow", "serviceclass", "service"
	ResourceName string
	Message      string
}

// Error implements the error interface
func (e *NotFoundError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("%s %s not found", e.ResourceType, e.ResourceName)
}

// IsNotFound checks if an error is a NotFoundError
func IsNotFound(err error) bool {
	var notFoundErr *NotFoundError
	return errors.As(err, &notFoundErr)
}

// NewNotFoundError creates a new NotFoundError
func NewNotFoundError(resourceType, resourceName string) *NotFoundError {
	return &NotFoundError{
		ResourceType: resourceType,
		ResourceName: resourceName,
	}
}

// NewNotFoundErrorWithMessage creates a new NotFoundError with a custom message
func NewNotFoundErrorWithMessage(resourceType, resourceName, message string) *NotFoundError {
	return &NotFoundError{
		ResourceType: resourceType,
		ResourceName: resourceName,
		Message:      message,
	}
}

// Specific NotFoundError instances for each resource type
var (
	// NewWorkflowNotFoundError creates a workflow not found error
	NewWorkflowNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("workflow", name)
	}

	// NewServiceClassNotFoundError creates a service class not found error
	NewServiceClassNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("service class", name)
	}

	// NewServiceNotFoundError creates a service not found error
	NewServiceNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("service", name)
	}

	// NewCapabilityNotFoundError creates a capability not found error
	NewCapabilityNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("capability", name)
	}

	// NewMCPServerNotFoundError creates an MCP server not found error
	NewMCPServerNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("MCP server", name)
	}

	// NewToolNotFoundError creates a tool not found error
	NewToolNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("tool", name)
	}

	// NewResourceNotFoundError creates a resource not found error
	NewResourceNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("resource", name)
	}

	// NewPromptNotFoundError creates a prompt not found error
	NewPromptNotFoundError = func(name string) *NotFoundError {
		return NewNotFoundError("prompt", name)
	}
)

// Common errors for API operations
var (
	// Handler not registered errors
	ErrOrchestratorNotRegistered  = errors.New("orchestrator handler not registered")
	ErrMCPServiceNotRegistered    = errors.New("MCP service handler not registered")
	ErrPortForwardNotRegistered   = errors.New("port forward handler not registered")
	ErrK8sServiceNotRegistered    = errors.New("K8s service handler not registered")
	ErrConfigServiceNotRegistered = errors.New("config service handler not registered")
	ErrCapabilityNotRegistered    = errors.New("capability handler not registered")
	ErrWorkflowNotRegistered      = errors.New("workflow handler not registered")
	ErrAggregatorNotRegistered    = errors.New("aggregator handler not registered")

	// Legacy workflow error (deprecated - use NewWorkflowNotFoundError instead)
	ErrWorkflowNotFound = errors.New("workflow not found")
)

// HandleError creates an appropriate CallToolResult based on the error type
// NotFoundError results in IsError: true (unsuccessful operation) but with clear messaging
// Other errors result in IsError: true (error condition)
func HandleError(err error) *CallToolResult {
	return &CallToolResult{
		Content: []interface{}{fmt.Sprintf("Failed to get resource: %v", err)},
		IsError: true, // All failures are treated as errors for test framework compatibility
	}
}

// HandleErrorWithPrefix creates an appropriate CallToolResult with a custom prefix
func HandleErrorWithPrefix(err error, prefix string) *CallToolResult {
	return &CallToolResult{
		Content: []interface{}{fmt.Sprintf("%s: %v", prefix, err)},
		IsError: true, // All failures are treated as errors for test framework compatibility
	}
}
