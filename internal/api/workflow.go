package api

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Workflow represents a single workflow definition and runtime state.
// This consolidates WorkflowDefinition, WorkflowInfo, and WorkflowConfig into one type
// to provide a unified view of workflow information across configuration and runtime contexts.
//
// Workflows define multi-step processes that can be executed by the system,
// orchestrating tool calls, arg templating, and conditional logic.
// They provide a way to compose complex operations from simpler tool calls.
type Workflow struct {
	// Configuration fields (from YAML) - Static workflow definition

	// Name is the unique identifier for this workflow
	Name string `yaml:"name" json:"name"`

	// Description provides human-readable documentation for the workflow's purpose
	Description string `yaml:"description" json:"description"`

	// Labels mirrors the Workflow CRD's metadata.labels. They are exposed as
	// discovery facets on the workflow's execution tool so a client can scope a
	// tool lookup to a labelled subset (e.g. by category) instead of dumping the
	// whole catalogue.
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`

	// Args defines the validation rules and metadata for workflow execution arguments.
	// These definitions are used to validate arguments when executing workflows
	// and to provide documentation for the workflow execution API.
	Args map[string]ArgDefinition `yaml:"args,omitempty" json:"args,omitempty"`

	// Steps defines the sequence of operations to be performed during workflow execution.
	// Each step represents a tool call with its arguments and processing logic.
	Steps []WorkflowStep `yaml:"steps" json:"steps"`

	// OnFailure defines best-effort cleanup/rollback steps run when the workflow
	// fails on a step that does not allow failure. Their own failures are tolerated.
	OnFailure []WorkflowSubStep `yaml:"onFailure,omitempty" json:"onFailure,omitempty"`

	// Output is an optional templated projection that shapes the returned document.
	// It is rendered once after the steps complete, against .input / .results /
	// .vars, and replaces the default envelope. Each leaf is a Go-template/sprig
	// expression and JSON structure is preserved. When nil, the default envelope
	// is returned.
	Output map[string]interface{} `yaml:"output,omitempty" json:"output,omitempty"`

	// Runtime state fields (for API responses only) - Dynamic runtime information

	// Available indicates whether this workflow is currently available for execution
	Available bool `json:"available,omitempty" yaml:"-"`

	// Metadata fields - Additional workflow information

	// CreatedAt indicates when this workflow was created
	CreatedAt time.Time `yaml:"createdAt,omitempty" json:"createdAt"`

	// LastModified indicates when this workflow was last updated
	LastModified time.Time `yaml:"lastModified,omitempty" json:"lastModified"`
}

// OutputEnabled resolves the effective "include in returned result" flag for a
// step from its Output pointer and the deprecated Store alias. Output takes
// precedence when set; otherwise Store is used for backwards compatibility.
func OutputEnabled(output *bool, store bool) bool {
	if output != nil {
		return *output
	}
	return store
}

// AuthoringWarnings returns non-fatal authoring lint messages for a workflow.
// Each string is a complete, log-ready sentence describing the workflow itself
// (the caller prefixes it with the workflow name). It is the single source of
// truth shared by the structured create/validate path and the CRD reconciler so
// the same nudge is emitted regardless of how a workflow is authored. Returns an
// empty slice when there is nothing to warn about.
func AuthoringWarnings(wf *Workflow) []string {
	if wf == nil {
		return nil
	}
	var warnings []string
	if ids := deprecatedStoreIDs(wf); len(ids) > 0 {
		warnings = append(warnings, fmt.Sprintf("uses the deprecated 'store' flag on: %s. 'store' is a backwards-compatible alias for 'output' and now only controls result visibility; referencing a step result no longer requires it. Prefer 'output'.", strings.Join(ids, ", ")))
	}
	if len(wf.Output) > 0 {
		if ids := outputFlaggedIDs(wf); len(ids) > 0 {
			warnings = append(warnings, fmt.Sprintf("declares a workflow-level 'output' projection, which replaces the default envelope, so the per-step 'output'/'store' flags on these steps have no effect on the returned document: %s. Remove them or drop the projection.", strings.Join(ids, ", ")))
		}
	}
	return warnings
}

// deprecatedStoreIDs returns the IDs of every step and sub-step that still uses
// the deprecated `store` flag, i.e. store is set while the superseding `output`
// flag is not. Sub-steps are qualified by their parent step and group.
func deprecatedStoreIDs(wf *Workflow) []string {
	usesStore := func(output *bool, store bool) bool { return store && output == nil }
	return collectStepIDs(wf, func(output *bool, store bool) bool { return usesStore(output, store) })
}

// outputFlaggedIDs returns the IDs of every step and sub-step that sets an
// effective output/store flag. It is used to flag flags rendered inert by a
// workflow-level output projection.
func outputFlaggedIDs(wf *Workflow) []string {
	return collectStepIDs(wf, OutputEnabled)
}

// collectStepIDs walks every step, forEach/parallel sub-step, and onFailure
// handler, returning the (qualified) IDs for which match reports true.
func collectStepIDs(wf *Workflow, match func(output *bool, store bool) bool) []string {
	var ids []string
	collect := func(label string, subs []WorkflowSubStep) {
		for _, sub := range subs {
			if match(sub.Output, sub.Store) {
				ids = append(ids, label+sub.ID)
			}
		}
	}
	for _, step := range wf.Steps {
		if match(step.Output, step.Store) {
			ids = append(ids, step.ID)
		}
		if step.ForEach != nil {
			collect(step.ID+".forEach.", step.ForEach.Steps)
		}
		collect(step.ID+".parallel.", step.Parallel)
	}
	collect("onFailure.", wf.OnFailure)
	return ids
}

// Arg defines an argument for operations and workflows.
// This provides a standardized way to define input validation and documentation
// for both workflow inputs and operation arguments.
type Arg struct {
	// Type specifies the expected data type (string, number, boolean, object, array)
	Type string `yaml:"type" json:"type"`

	// Required indicates whether this argument must be provided
	Required bool `yaml:"required" json:"required"`

	// Description provides human-readable documentation for the argument
	Description string `yaml:"description" json:"description"`

	// Default specifies the default value used when the argument is not provided.
	// Only applicable when Required is false.
	Default interface{} `yaml:"default,omitempty" json:"default,omitempty"`
}

// OperationDefinition defines an operation that can be performed within a workflow.
// Operations represent discrete actions that can be invoked, with their own
// argument requirements and execution logic (either direct workflow calls or references).
type OperationDefinition struct {
	// Description provides human-readable documentation for the operation's purpose
	Description string `yaml:"description" json:"description"`

	// Args defines the input arguments accepted by this operation.
	// Used for validation and documentation generation.
	Args map[string]Arg `yaml:"args" json:"args"`

	// Requires lists the tools or capabilities that must be available for this operation.
	// This is used for availability checking and dependency validation.
	Requires []string `yaml:"requires" json:"requires"`
}

// WorkflowCondition defines a condition that determines whether a workflow step should execute.
// Conditions allow for dynamic workflow execution based on runtime state evaluation.
type WorkflowCondition struct {
	// Template is a boolean Go-template gate. When set, the step executes only
	// if the template renders to a truthy value (e.g. "{{ eq .input.env \"production\" }}").
	// Mutually exclusive with Tool/FromStep; when present, Expect/ExpectNot are ignored.
	Template string `yaml:"template,omitempty" json:"template,omitempty"`

	// Tool specifies the name of the tool to execute for condition evaluation.
	// Must correspond to an available tool in the aggregator.
	// Optional when FromStep or Template is used.
	Tool string `yaml:"tool,omitempty" json:"tool,omitempty"`

	// Args provides the arguments to pass to the condition tool.
	// Can include templated values that are resolved at runtime.
	Args map[string]interface{} `yaml:"args,omitempty" json:"args,omitempty"`

	// FromStep specifies the step ID to reference for condition evaluation.
	// When specified, the condition evaluates against the result of the referenced step
	// instead of executing a new tool call.
	FromStep string `yaml:"from_step,omitempty" json:"from_step,omitempty"`

	// Expect defines the expected result for the condition to be considered true.
	// If the condition tool result matches these expectations, the step will execute.
	// If not, the step will be skipped.
	Expect WorkflowConditionExpectation `yaml:"expect,omitempty" json:"expect,omitempty"`

	// ExpectNot defines the negated expected result for the condition to be considered true.
	// If the condition tool result does NOT match these expectations, the step will execute.
	// If it matches, the step will be skipped.
	ExpectNot WorkflowConditionExpectation `yaml:"expect_not,omitempty" json:"expect_not,omitempty"`
}

// WorkflowConditionExpectation defines what result is expected from a condition tool
// for the condition to be considered true.
type WorkflowConditionExpectation struct {
	// Success indicates whether the condition tool should succeed (true) or fail (false)
	// for the condition to be met.
	Success bool `yaml:"success" json:"success"`

	// JsonPath defines optional JSON path expressions that must match specific values
	// in the condition tool's response. All specified paths must match for the condition
	// to be considered true. This allows for content-based condition validation beyond
	// just success/failure status.
	JsonPath map[string]interface{} `yaml:"json_path,omitempty" json:"json_path,omitempty"`

	// TODO: Future enhancements could include:
	// - Content expectations (specific return values) - partially implemented via JsonPath
	// - JSONPath expressions for complex result validation - implemented via JsonPath
	// - Multiple condition combinations (AND/OR logic)
}

// WorkflowStep defines a single step in a workflow execution.
// Each step represents a tool call with its arguments, result processing,
// and conditional execution logic.
type WorkflowStep struct {
	// ID is a unique identifier for this step within the workflow.
	// Used for step referencing, error reporting, and execution flow control.
	ID string `yaml:"id" json:"id"`

	// Condition defines an optional condition that determines whether this step should execute.
	// If specified, the condition tool is executed first. If the condition is not met,
	// the step is skipped and marked as "skipped" in the execution results.
	Condition *WorkflowCondition `yaml:"condition,omitempty" json:"condition,omitempty"`

	// Tool specifies the name of the tool to execute for this step.
	// Must correspond to an available tool in the aggregator.
	Tool string `yaml:"tool" json:"tool"`

	// Args provides the arguments to pass to the tool.
	// Can include templated values that are resolved at runtime using previous step results.
	Args map[string]interface{} `yaml:"args,omitempty" json:"args,omitempty"`

	// ForEach executes a body of sub-steps once per item of a list.
	// Mutually exclusive with Tool and Parallel.
	ForEach *WorkflowForEach `yaml:"forEach,omitempty" json:"forEach,omitempty"`

	// Parallel executes a group of sub-steps concurrently.
	// Mutually exclusive with Tool and ForEach.
	Parallel []WorkflowSubStep `yaml:"parallel,omitempty" json:"parallel,omitempty"`

	// AllowFailure indicates whether this step is allowed to fail without failing the workflow.
	// When true, step failures are recorded but the workflow continues execution.
	// The step result will be available for subsequent step conditions to reference.
	AllowFailure bool `yaml:"allow_failure,omitempty" json:"allow_failure,omitempty"`

	// Output indicates whether this step's result is included in the workflow's
	// returned document. Every step result is always referenceable by later steps
	// regardless of this flag; Output only controls visibility in the returned
	// result. When nil, the deprecated Store flag is used as a fallback.
	Output *bool `yaml:"output,omitempty" json:"output,omitempty"`

	// Store is a deprecated alias for Output, kept for backwards compatibility.
	// Referencing a step result no longer requires Store; it now only affects
	// result visibility. Prefer Output.
	Store bool `yaml:"store,omitempty" json:"store,omitempty"`

	// Description provides human-readable documentation for this step's purpose
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// WorkflowForEach describes a sequential loop over a list of items.
// The body is a flat list of sub-steps executed once per item.
type WorkflowForEach struct {
	// Items is a template expression that must resolve to an array,
	// e.g. "{{ .input.clusters }}".
	Items string `yaml:"items" json:"items"`

	// As is the loop variable name made available to the body as
	// "{{ .vars.<as> }}". Defaults to "item".
	As string `yaml:"as,omitempty" json:"as,omitempty"`

	// Steps is the body executed for each item.
	Steps []WorkflowSubStep `yaml:"steps" json:"steps"`
}

// WorkflowSubStep is a tool-call step used inside forEach bodies, parallel
// groups, and onFailure handlers. It cannot itself contain forEach or parallel.
type WorkflowSubStep struct {
	// ID is a unique identifier for this sub-step.
	ID string `yaml:"id" json:"id"`

	// Condition defines an optional condition that determines whether this sub-step should execute.
	Condition *WorkflowCondition `yaml:"condition,omitempty" json:"condition,omitempty"`

	// Tool specifies the name of the tool to execute.
	Tool string `yaml:"tool" json:"tool"`

	// Args provides the arguments to pass to the tool (supports templating).
	Args map[string]interface{} `yaml:"args,omitempty" json:"args,omitempty"`

	// AllowFailure indicates whether this sub-step is allowed to fail without failing execution.
	AllowFailure bool `yaml:"allow_failure,omitempty" json:"allow_failure,omitempty"`

	// Output indicates whether this sub-step's result is included in the
	// workflow's returned document. The result is always referenceable by later
	// steps regardless of this flag. When nil, the deprecated Store flag is used
	// as a fallback.
	Output *bool `yaml:"output,omitempty" json:"output,omitempty"`

	// Store is a deprecated alias for Output, kept for backwards compatibility.
	// Prefer Output.
	Store bool `yaml:"store,omitempty" json:"store,omitempty"`

	// Description provides human-readable documentation for this sub-step's purpose.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// WorkflowInputSchema defines the input argument schema for a workflow.
// This provides structured validation and documentation for workflow inputs,
// following JSON Schema conventions for argument definition.
//
// DEPRECATED: Use Args map[string]ArgDefinition instead
type WorkflowInputSchema struct {
	// Type specifies the overall schema type (typically "object" for workflow inputs)
	Type string `yaml:"type" json:"type"`

	// Args defines the individual input arguments and their schemas.
	// Each property corresponds to a workflow input argument.
	Args map[string]SchemaProperty `yaml:"args" json:"args"`

	// Required lists the argument names that must be provided for workflow execution
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
	// Args:
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

	// Workflow execution tracking

	// ListWorkflowExecutions returns paginated list of workflow executions with optional filtering.
	// This enables users to view execution history and track workflow usage patterns.
	//
	// Args:
	//   - ctx: Context for the operation, including cancellation and timeout
	//   - req: Request args for filtering, pagination, and sorting
	//
	// Returns:
	//   - *ListWorkflowExecutionsResponse: Paginated list of execution records
	//   - error: Error if the request is invalid or operation fails
	ListWorkflowExecutions(ctx context.Context, req *ListWorkflowExecutionsRequest) (*ListWorkflowExecutionsResponse, error)

	// GetWorkflowExecution returns detailed information about a specific workflow execution.
	// This enables users to examine execution results, step details, and debug failed executions.
	//
	// Args:
	//   - ctx: Context for the operation, including cancellation and timeout
	//   - req: Request args specifying execution ID and optional filtering
	//
	// Returns:
	//   - *WorkflowExecution: Complete execution record with step details
	//   - error: Error if the execution doesn't exist or operation fails
	GetWorkflowExecution(ctx context.Context, req *GetWorkflowExecutionRequest) (*WorkflowExecution, error)

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
	// Args:
	//   - name: The name of the workflow to retrieve
	//
	// Returns:
	//   - *Workflow: Detailed workflow information including definition and runtime state
	//   - error: Error if the workflow doesn't exist
	GetWorkflow(name string) (*Workflow, error)

	// Workflow lifecycle management

	// CreateWorkflowFromStructured creates a new workflow from structured args.
	// This allows dynamic workflow creation at runtime.
	//
	// Args:
	//   - args: Structured workflow definition args
	//
	// Returns:
	//   - error: Error if the workflow definition is invalid or creation fails
	CreateWorkflowFromStructured(args map[string]interface{}) error

	// UpdateWorkflowFromStructured updates an existing workflow from structured args.
	// This allows runtime modification of workflow definitions.
	//
	// Args:
	//   - name: The name of the workflow to update
	//   - args: Updated workflow definition args
	//
	// Returns:
	//   - error: Error if the workflow doesn't exist or update fails
	UpdateWorkflowFromStructured(name string, args map[string]interface{}) error

	// DeleteWorkflow removes a workflow definition from the system.
	//
	// Args:
	//   - name: The name of the workflow to delete
	//
	// Returns:
	//   - error: Error if the workflow doesn't exist or deletion fails
	DeleteWorkflow(name string) error

	// ValidateWorkflowFromStructured validates a workflow definition without creating it.
	// This is useful for validation during workflow development and testing.
	//
	// Args:
	//   - args: Workflow definition args to validate
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

	// Args defines the expected input arguments for workflow execution
	Args map[string]ArgDefinition `yaml:"args,omitempty" json:"args,omitempty"`

	// Steps defines the sequence of operations to be performed during workflow execution
	Steps []WorkflowStep `yaml:"steps" json:"steps"`
}
