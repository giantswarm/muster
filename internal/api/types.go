package api

import (
	"context"
	"time"
)

// ToolUpdateEvent represents a tool availability change event in the MCP ecosystem.
// These events are published when MCP servers are registered/deregistered or when
// their available tools change, allowing components to react to tool availability changes.
//
// Events are delivered asynchronously through the tool update subscription system,
// enabling real-time reactivity to changes in the tool landscape.
//
// Example event types:
//   - "server_registered": A new MCP server has been registered
//   - "server_deregistered": An MCP server has been removed
//   - "tools_updated": Tools available from a server have changed
type ToolUpdateEvent struct {
	// Type specifies the kind of tool update event.
	// Valid values: "server_registered", "server_deregistered", "tools_updated"
	Type string `json:"type"`

	// ServerName identifies the MCP server that triggered this event
	ServerName string `json:"server_name"`

	// Tools contains the list of tool names affected by this event.
	// For "server_registered": all tools provided by the server
	// For "server_deregistered": all tools that were removed
	// For "tools_updated": the current complete tool list
	Tools []string `json:"tools"`

	// Timestamp records when this event occurred
	Timestamp time.Time `json:"timestamp"`

	// Error contains error information if the event represents a failure condition.
	// Only populated for error events, empty for successful operations.
	Error string `json:"error,omitempty"`
}

// CallToolResult represents the result of a tool execution or capability call.
// This standardized result format is used across all tool calling interfaces
// to provide consistent response handling throughout the muster system.
//
// The result can represent either successful execution (IsError=false) or
// failure conditions (IsError=true), with Content containing the appropriate
// response data or error information.
type CallToolResult struct {
	// Content contains the actual result data from the tool execution.
	// Can be strings, objects, or any other JSON-serializable data.
	//
	// For successful executions: contains the tool's output data
	// For errors: contains error messages and diagnostic information
	Content []interface{} `json:"content"`

	// IsError indicates whether the tool execution resulted in an error.
	// true: The execution failed and Content contains error information
	// false: The execution succeeded and Content contains the result data
	IsError bool `json:"isError,omitempty"`
}

// ToolMetadata describes a tool that can be exposed through the MCP protocol.
// This metadata is used for tool discovery, documentation generation, and
// parameter validation during tool execution.
//
// Tools are the primary mechanism for exposing functionality through muster,
// allowing workflows, capabilities, and other components to be discoverable
// and executable through the standard MCP protocol.
type ToolMetadata struct {
	// Name is the unique identifier for the tool (e.g., "workflow_list", "auth_login")
	// Must be unique within the scope of the tool provider
	Name string

	// Description provides human-readable documentation about what the tool does
	// and how to use it effectively
	Description string

	// Parameters defines the input parameters accepted by this tool,
	// including validation rules and documentation
	Parameters []ParameterMetadata
}

// ParameterMetadata describes a single parameter for a tool.
// This is used for validation, documentation, and UI generation
// for tool parameters in various interfaces.
//
// Parameter metadata enables automatic validation of tool calls
// and helps generate helpful error messages when parameters are invalid.
type ParameterMetadata struct {
	// Name is the parameter identifier used in tool calls
	Name string

	// Type specifies the expected parameter type for validation.
	// Valid values: "string", "number", "boolean", "object", "array"
	Type string

	// Required indicates whether this parameter must be provided in tool calls
	Required bool

	// Description provides human-readable documentation for this parameter,
	// explaining its purpose and expected format
	Description string

	// Default specifies the default value used when the parameter is not provided.
	// Only used when Required is false. Must match the specified Type.
	Default interface{}
}

// ToolProvider interface defines the contract for components that can provide tools
// to the MCP ecosystem. This interface is implemented by workflow, capability, and
// other tool-providing packages.
//
// Components implementing this interface can expose their functionality as MCP tools
// that can be discovered and executed through the aggregator, making them available
// to external clients and internal orchestration.
//
// All tool providers must implement both tool discovery (GetTools) and execution
// (ExecuteTool) to participate in the tool ecosystem.
type ToolProvider interface {
	// GetTools returns metadata for all tools this provider offers.
	// This is used for tool discovery and documentation generation.
	//
	// The returned metadata should be stable and consistent across calls,
	// as it's used for caching and tool registration purposes.
	//
	// Returns:
	//   - []ToolMetadata: List of all tools provided by this component
	//
	// Example:
	//
	//	func (p *MyProvider) GetTools() []ToolMetadata {
	//	    return []ToolMetadata{
	//	        {
	//	            Name:        "my_operation",
	//	            Description: "Performs my custom operation",
	//	            Parameters: []ParameterMetadata{
	//	                {
	//	                    Name:        "input",
	//	                    Type:        "string",
	//	                    Required:    true,
	//	                    Description: "Input data for processing",
	//	                },
	//	            },
	//	        },
	//	    }
	//	}
	GetTools() []ToolMetadata

	// ExecuteTool executes a specific tool by name with the provided arguments.
	// This is the main entry point for tool execution and must handle
	// parameter validation, execution logic, and result formatting.
	//
	// Parameters:
	//   - ctx: Context for the operation, including cancellation and timeout
	//   - toolName: The name of the tool to execute (must match a tool from GetTools)
	//   - args: Arguments for the tool execution, should be validated against tool metadata
	//
	// Returns:
	//   - *CallToolResult: The result of the tool execution
	//   - error: Error if the tool doesn't exist or execution fails
	//
	// Example:
	//
	//	func (p *MyProvider) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error) {
	//	    switch toolName {
	//	    case "my_operation":
	//	        input, ok := args["input"].(string)
	//	        if !ok {
	//	            return &CallToolResult{
	//	                Content: []interface{}{"input parameter must be a string"},
	//	                IsError: true,
	//	            }, nil
	//	        }
	//	        // Perform operation
	//	        result := processInput(input)
	//	        return &CallToolResult{
	//	            Content: []interface{}{result},
	//	            IsError: false,
	//	        }, nil
	//	    default:
	//	        return nil, fmt.Errorf("unknown tool: %s", toolName)
	//	    }
	//	}
	ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error)
}

// ToolUpdateSubscriber interface defines the contract for components that want to
// receive notifications about tool availability changes.
//
// Components implementing this interface can react to changes in the tool landscape,
// such as updating their own availability status, refreshing cached tool lists,
// or triggering recalculation of dependent functionality.
//
// Subscribers are called asynchronously and should implement non-blocking operations
// to prevent affecting the overall system performance.
type ToolUpdateSubscriber interface {
	// OnToolsUpdated is called when tool availability changes in the system.
	// Implementations should be non-blocking as this is called from goroutines.
	//
	// This method will be called for various tool availability events:
	// - MCP server registration/deregistration
	// - Changes in available tools from existing servers
	// - Tool configuration updates
	//
	// Parameters:
	//   - event: ToolUpdateEvent containing details about what changed
	//
	// Note: This method is called asynchronously and should not block.
	// Panics in this method are recovered and logged as errors.
	//
	// Example:
	//
	//	func (s *MySubscriber) OnToolsUpdated(event api.ToolUpdateEvent) {
	//	    switch event.Type {
	//	    case "server_registered":
	//	        log.Printf("New server %s registered with %d tools",
	//	            event.ServerName, len(event.Tools))
	//	        s.refreshCapabilities()
	//	    case "server_deregistered":
	//	        log.Printf("Server %s deregistered", event.ServerName)
	//	        s.markToolsUnavailable(event.Tools)
	//	    case "tools_updated":
	//	        log.Printf("Tools updated for server %s", event.ServerName)
	//	        s.updateToolCache(event.ServerName, event.Tools)
	//	    }
	//	}
	OnToolsUpdated(event ToolUpdateEvent)
}

// ToolCall defines how to call an aggregator tool for a lifecycle event.
// This is used in ServiceClass definitions to specify which tools should be
// called for service lifecycle operations (start, stop, health check, etc.).
//
// ToolCall provides the declarative configuration for how ServiceClass
// lifecycle operations map to actual tool executions, including argument
// preparation and response processing.
type ToolCall struct {
	// Tool specifies the name of the tool to call.
	// Must correspond to an available tool in the aggregator.
	Tool string `yaml:"tool" json:"tool"`

	// Arguments provides static arguments to pass to the tool.
	// These can be combined with dynamic arguments from service parameters.
	Arguments map[string]interface{} `yaml:"arguments" json:"arguments"`

	// ResponseMapping defines how to extract information from tool responses.
	// This allows ServiceClass lifecycle tools to provide structured information
	// about service status, health, and metadata.
	ResponseMapping ResponseMapping `yaml:"responseMapping" json:"responseMapping"`
}

// HealthStatus represents the health status of a service, capability, or other component.
// This standardized status is used across all muster components for consistent health reporting.
//
// Health status provides a unified way to represent component operational state,
// enabling consistent monitoring and alerting across the entire system.
type HealthStatus string

const (
	// HealthUnknown indicates the health status cannot be determined.
	// This is the default state when no health check has been performed.
	HealthUnknown HealthStatus = "unknown"

	// HealthHealthy indicates the component is operating normally.
	// All health checks are passing and the component is fully functional.
	HealthHealthy HealthStatus = "healthy"

	// HealthDegraded indicates the component has some issues but is still functional.
	// Some non-critical features may be impaired but core functionality works.
	HealthDegraded HealthStatus = "degraded"

	// HealthUnhealthy indicates the component has significant issues.
	// Core functionality may be impaired and manual intervention may be required.
	HealthUnhealthy HealthStatus = "unhealthy"

	// HealthChecking indicates a health check is currently in progress.
	// This is a transient state during health check execution.
	HealthChecking HealthStatus = "checking"
)

// SchemaProperty defines a single property in a JSON schema.
// This is used for input validation and documentation in workflows and capabilities,
// providing structured parameter definition and validation rules.
//
// Schema properties enable automatic validation of inputs and help generate
// helpful error messages and documentation for users.
type SchemaProperty struct {
	// Type specifies the JSON schema type for validation.
	// Valid values: "string", "number", "boolean", "object", "array"
	Type string `yaml:"type" json:"type"`

	// Description provides human-readable documentation for this property,
	// explaining its purpose and expected format
	Description string `yaml:"description" json:"description"`

	// Default specifies the default value used when the property is not provided.
	// Must be compatible with the specified Type.
	Default interface{} `yaml:"default,omitempty" json:"default,omitempty"`
}

// TimeoutConfig defines timeout behavior for various operations.
// This ensures operations don't hang indefinitely and provides predictable behavior
// across different components and operations.
//
// Timeouts are essential for maintaining system stability and preventing
// resource leaks from stuck operations.
type TimeoutConfig struct {
	// Create specifies the maximum time to wait for resource creation operations.
	// Includes service instance creation, capability initialization, etc.
	Create time.Duration `yaml:"create" json:"create"`

	// Delete specifies the maximum time to wait for resource deletion operations.
	// Includes service instance cleanup, resource deallocation, etc.
	Delete time.Duration `yaml:"delete" json:"delete"`

	// HealthCheck specifies the maximum time to wait for health check operations.
	// Individual health checks should complete within this time limit.
	HealthCheck time.Duration `yaml:"healthCheck" json:"healthCheck"`
}

// HealthCheckConfig defines health checking behavior for services and components.
// This configuration controls how often health checks are performed and when
// a component is considered unhealthy based on check results.
//
// Health check configuration enables automated monitoring and helps maintain
// system reliability by detecting and responding to component failures.
type HealthCheckConfig struct {
	// Enabled determines whether health checks should be performed.
	// When false, the component health status remains unknown.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Interval specifies how often to perform health checks.
	// Shorter intervals provide faster failure detection but use more resources.
	Interval time.Duration `yaml:"interval" json:"interval"`

	// FailureThreshold is the number of consecutive failures before marking unhealthy.
	// Higher values reduce false negatives but increase detection time.
	FailureThreshold int `yaml:"failureThreshold" json:"failureThreshold"`

	// SuccessThreshold is the number of consecutive successes before marking healthy.
	// Higher values reduce false positives but increase recovery time.
	SuccessThreshold int `yaml:"successThreshold" json:"successThreshold"`
}

// ParameterMapping defines how service creation parameters map to tool arguments.
// This is used in ServiceClass definitions to specify how user-provided parameters
// are transformed and passed to lifecycle tools.
//
// Parameter mapping enables ServiceClasses to provide a clean interface for
// service creation while translating to the specific tool arguments needed
// for the underlying implementation.
type ParameterMapping struct {
	// ToolParameter specifies the name of the parameter in the tool call.
	// This is how the parameter will be passed to the lifecycle tool.
	ToolParameter string `yaml:"toolParameter" json:"toolParameter"`

	// Default specifies the default value used when the parameter is not provided.
	// Only used when Required is false.
	Default interface{} `yaml:"default,omitempty" json:"default,omitempty"`

	// Required indicates whether this parameter must be provided during service creation.
	Required bool `yaml:"required" json:"required"`

	// Transform specifies an optional transformation to apply to the parameter value.
	// Can be used for format conversion or value mapping.
	Transform string `yaml:"transform,omitempty" json:"transform,omitempty"`
}

// ResponseMapping defines how to extract information from tool responses.
// This allows ServiceClass lifecycle tools to provide structured information
// about service status, health, and metadata in a consistent format.
//
// Response mapping enables the orchestrator to understand tool responses
// and update service state appropriately without knowing the specific
// response format of each tool.
type ResponseMapping struct {
	// Name specifies the JSON path to extract the service name from the response.
	// Used to verify the correct service was operated on.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Status specifies the JSON path to extract the service status from the response.
	Status   string            `yaml:"status,omitempty" json:"status,omitempty"`
	Health   string            `yaml:"health,omitempty" json:"health,omitempty"`
	Error    string            `yaml:"error,omitempty" json:"error,omitempty"`
	Metadata map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}
