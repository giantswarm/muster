package api

import (
	"context"
	"time"
)

// Workflow represents a single workflow definition and runtime state.
// This consolidates WorkflowDefinition, WorkflowInfo, and WorkflowConfig into one type
// to provide a unified view of workflow information across configuration and runtime contexts.
//
// Workflows define multi-step processes that can be executed by the system,
// orchestrating tool calls, parameter templating, and conditional logic.
// They provide a way to compose complex operations from simpler tool calls.
type Workflow struct {
	// Configuration fields (from YAML) - Static workflow definition

	// Name is the unique identifier for this workflow
	Name string `yaml:"name" json:"name"`

	// Description provides human-readable documentation for the workflow's purpose
	Description string `yaml:"description" json:"description"`

	// Version indicates the workflow definition version for compatibility tracking
	Version int `yaml:"version" json:"version"`

	// InputSchema defines the expected input parameters for workflow execution.
	// This is used for parameter validation and documentation generation.
	InputSchema WorkflowInputSchema `yaml:"inputSchema" json:"inputSchema"`

	// Steps defines the sequence of operations to be performed during workflow execution.
	// Each step represents a tool call with its arguments and processing logic.
	Steps []WorkflowStep `yaml:"steps" json:"steps"`

	// OutputSchema defines the expected output structure from workflow execution.
	// This is used for result validation and documentation.
	OutputSchema map[string]interface{} `yaml:"outputSchema,omitempty" json:"outputSchema,omitempty"`

	// Runtime state fields (for API responses only) - Dynamic runtime information

	// Available indicates whether this workflow is currently available for execution
	Available bool `json:"available,omitempty" yaml:"-"`

	// State represents the current operational state of the workflow
	State string `json:"state,omitempty" yaml:"-"`

	// Error contains error information if the workflow is in an error state
	Error string `json:"error,omitempty" yaml:"-"`

	// Metadata fields - Additional workflow information

	// CreatedBy indicates who created this workflow definition
	CreatedBy string `yaml:"createdBy,omitempty" json:"createdBy,omitempty"`

	// CreatedAt indicates when this workflow was created
	CreatedAt time.Time `yaml:"createdAt,omitempty" json:"createdAt"`

	// LastModified indicates when this workflow was last updated
	LastModified time.Time `yaml:"lastModified,omitempty" json:"lastModified"`
}

// WorkflowConfig represents a configuration file containing multiple workflows.
// This is used for loading workflow definitions from YAML files that contain
// multiple workflow definitions in a single file.
type WorkflowConfig struct {
	// Workflows contains the list of workflow definitions in this configuration
	Workflows []Workflow `yaml:"workflows" json:"workflows"`
}

// Parameter defines a parameter for operations and workflows.
// This provides a standardized way to define input validation and documentation
// for both workflow inputs and operation parameters.
type Parameter struct {
	// Type specifies the expected data type (string, number, boolean, object, array)
	Type string `yaml:"type" json:"type"`

	// Required indicates whether this parameter must be provided
	Required bool `yaml:"required" json:"required"`

	// Description provides human-readable documentation for the parameter
	Description string `yaml:"description" json:"description"`

	// Default specifies the default value used when the parameter is not provided.
	// Only applicable when Required is false.
	Default interface{} `yaml:"default,omitempty" json:"default,omitempty"`
}

// OperationDefinition defines an operation that can be performed within a capability.
// Operations represent discrete actions that can be invoked, with their own
// parameter requirements and execution logic (either direct workflow calls or references).
type OperationDefinition struct {
	// Description provides human-readable documentation for the operation's purpose
	Description string `yaml:"description" json:"description"`

	// Parameters defines the input parameters accepted by this operation.
	// Used for validation and documentation generation.
	Parameters map[string]Parameter `yaml:"parameters" json:"parameters"`

	// Requires lists the tools or capabilities that must be available for this operation.
	// This is used for availability checking and dependency validation.
	Requires []string `yaml:"requires" json:"requires"`

	// Workflow specifies the workflow to execute for this operation.
	// This can be either an inline workflow definition or a reference to an existing workflow.
	Workflow *WorkflowReference `yaml:"workflow,omitempty" json:"workflow,omitempty"`
}

// WorkflowReference references a workflow for an operation.
// This is simplified to avoid circular dependencies while still providing
// the necessary information for workflow execution and capability integration.
type WorkflowReference struct {
	// Name identifies the workflow to execute (can be a reference to an existing workflow)
	Name string `yaml:"name" json:"name"`

	// Description provides documentation for this workflow reference
	Description string `yaml:"description" json:"description"`

	// AgentModifiable indicates whether AI agents can modify this workflow during execution.
	// This enables dynamic workflow adaptation based on execution context.
	AgentModifiable bool `yaml:"agentModifiable" json:"agentModifiable"`

	// InputSchema defines the expected input parameters for this workflow reference.
	// This may be a subset or transformation of the parent operation's parameters.
	InputSchema map[string]interface{} `yaml:"inputSchema" json:"inputSchema"`

	// Steps defines the workflow steps if this is an inline workflow definition.
	// If empty, the workflow is resolved by Name from the workflow registry.
	Steps []WorkflowStep `yaml:"steps" json:"steps"`
}

// WorkflowStep defines a single step in a workflow execution.
// Each step represents a tool call with its arguments, result processing,
// and conditional execution logic.
type WorkflowStep struct {
	// ID is a unique identifier for this step within the workflow.
	// Used for step referencing, error reporting, and execution flow control.
	ID string `yaml:"id" json:"id"`

	// Tool specifies the name of the tool to execute for this step.
	// Must correspond to an available tool in the aggregator.
	Tool string `yaml:"tool" json:"tool"`

	// Args provides the arguments to pass to the tool.
	// Can include templated values that are resolved at runtime using previous step results.
	Args map[string]interface{} `yaml:"args,omitempty" json:"args,omitempty"`

	// Store specifies the variable name where the step result should be stored.
	// This allows subsequent steps to reference the result of this step.
	Store string `yaml:"store,omitempty" json:"store,omitempty"`

	// Condition specifies a conditional expression that determines whether this step should execute.
	// Uses a simple expression language to evaluate conditions based on previous step results.
	Condition string `yaml:"condition,omitempty" json:"condition,omitempty"`

	// Description provides human-readable documentation for this step's purpose
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// WorkflowInputSchema defines the input parameter schema for a workflow.
// This provides structured validation and documentation for workflow inputs,
// following JSON Schema conventions for parameter definition.
type WorkflowInputSchema struct {
	// Type specifies the overall schema type (typically "object" for workflow inputs)
	Type string `yaml:"type" json:"type"`

	// Properties defines the individual input parameters and their schemas.
	// Each property corresponds to a workflow input parameter.
	Properties map[string]SchemaProperty `yaml:"properties" json:"properties"`

	// Required lists the parameter names that must be provided for workflow execution
	Required []string `yaml:"required,omitempty" json:"required,omitempty"`
}

// WorkflowHandler defines the interface for workflow operations within the Service Locator Pattern.
// This handler provides the primary interface for workflow management, execution, and discovery.
//
// The handler abstracts workflow complexity behind a simple interface, allowing components
// to execute multi-step processes without knowing the underlying orchestration details.
// It integrates with the ToolProvider interface to expose workflows as discoverable MCP tools.
type WorkflowHandler interface {
	// Workflow execution

	// ExecuteWorkflow executes a workflow with the provided arguments.
	// This is the primary method for invoking workflow functionality.
	//
	// Parameters:
	//   - ctx: Context for the operation, including cancellation and timeout
	//   - workflowName: The name of the workflow to execute
	//   - args: Arguments for the workflow execution, validated against input schema
	//
	// Returns:
	//   - *CallToolResult: The result of the workflow execution
	//   - error: Error if the workflow doesn't exist or execution fails
	//
	// Example:
	//
	//	result, err := handler.ExecuteWorkflow(ctx, "deploy-service", map[string]interface{}{
	//	    "service_name": "my-api",
	//	    "environment": "production",
	//	    "replicas": 3,
	//	})
	ExecuteWorkflow(ctx context.Context, workflowName string, args map[string]interface{}) (*CallToolResult, error)

	// Workflow information and discovery

	// GetWorkflows returns information about all available workflows in the system.
	// This includes both static workflow definitions and runtime availability status.
	//
	// Returns:
	//   - []Workflow: List of all workflow definitions with runtime information
	GetWorkflows() []Workflow

	// GetWorkflow returns detailed information about a specific workflow.
	// This provides comprehensive information including steps, input schema, and metadata.
	//
	// Parameters:
	//   - name: The name of the workflow to retrieve
	//
	// Returns:
	//   - *Workflow: Detailed workflow information including definition and runtime state
	//   - error: Error if the workflow doesn't exist
	GetWorkflow(name string) (*Workflow, error)

	// Workflow lifecycle management

	// CreateWorkflowFromStructured creates a new workflow from structured parameters.
	// This allows dynamic workflow creation at runtime.
	//
	// Parameters:
	//   - args: Structured workflow definition parameters
	//
	// Returns:
	//   - error: Error if the workflow definition is invalid or creation fails
	CreateWorkflowFromStructured(args map[string]interface{}) error

	// UpdateWorkflowFromStructured updates an existing workflow from structured parameters.
	// This allows runtime modification of workflow definitions.
	//
	// Parameters:
	//   - name: The name of the workflow to update
	//   - args: Updated workflow definition parameters
	//
	// Returns:
	//   - error: Error if the workflow doesn't exist or update fails
	UpdateWorkflowFromStructured(name string, args map[string]interface{}) error

	// DeleteWorkflow removes a workflow definition from the system.
	//
	// Parameters:
	//   - name: The name of the workflow to delete
	//
	// Returns:
	//   - error: Error if the workflow doesn't exist or deletion fails
	DeleteWorkflow(name string) error

	// ValidateWorkflowFromStructured validates a workflow definition without creating it.
	// This is useful for validation during workflow development and testing.
	//
	// Parameters:
	//   - args: Workflow definition parameters to validate
	//
	// Returns:
	//   - error: Error if the workflow definition is invalid
	ValidateWorkflowFromStructured(args map[string]interface{}) error

	// ToolProvider integration for exposing workflows as discoverable MCP tools.
	// This allows workflows to be discovered and executed through the standard
	// tool discovery and execution mechanisms.
	ToolProvider
}

// CreateWorkflowRequest represents a request to create a new workflow.
// This is used for API-based workflow creation with validation and structured input.
type CreateWorkflowRequest struct {
	// Name is the unique identifier for the new workflow
	Name string `yaml:"name" json:"name"`

	// Description provides human-readable documentation for the workflow
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// InputSchema defines the expected input parameters for workflow execution
	InputSchema map[string]interface{} `yaml:"inputSchema" json:"inputSchema"`

	// Steps defines the sequence of operations to be performed during workflow execution
	Steps []WorkflowStep `yaml:"steps" json:"steps"`
}
