package api

import (
	"context"
	"time"
)

// Workflow represents a single workflow definition and runtime state
// This consolidates WorkflowDefinition, WorkflowInfo, and WorkflowConfig into one type
type Workflow struct {
	// Configuration fields (from YAML)
	Name         string                 `yaml:"name" json:"name"`
	Description  string                 `yaml:"description" json:"description"`
	Version      int                    `yaml:"version" json:"version"`
	InputSchema  WorkflowInputSchema    `yaml:"inputSchema" json:"inputSchema"`
	Steps        []WorkflowStep         `yaml:"steps" json:"steps"`
	OutputSchema map[string]interface{} `yaml:"outputSchema,omitempty" json:"outputSchema,omitempty"`

	// Runtime state fields (for API responses only)
	Available bool   `json:"available,omitempty" yaml:"-"`
	State     string `json:"state,omitempty" yaml:"-"`
	Error     string `json:"error,omitempty" yaml:"-"`

	// Metadata fields
	CreatedBy    string    `yaml:"createdBy,omitempty" json:"createdBy,omitempty"`
	CreatedAt    time.Time `yaml:"createdAt,omitempty" json:"createdAt"`
	LastModified time.Time `yaml:"lastModified,omitempty" json:"lastModified"`
}

// WorkflowConfig for separate workflow files (used for loading multiple workflows)
type WorkflowConfig struct {
	Workflows []Workflow `yaml:"workflows" json:"workflows"`
}

// Parameter defines a parameter for operations and workflows
type Parameter struct {
	Type        string      `yaml:"type" json:"type"`
	Required    bool        `yaml:"required" json:"required"`
	Description string      `yaml:"description" json:"description"`
	Default     interface{} `yaml:"default,omitempty" json:"default,omitempty"`
}

// OperationDefinition defines an operation that can be performed
type OperationDefinition struct {
	Description string               `yaml:"description" json:"description"`
	Parameters  map[string]Parameter `yaml:"parameters" json:"parameters"`
	Requires    []string             `yaml:"requires" json:"requires"`
	Workflow    *WorkflowReference   `yaml:"workflow,omitempty" json:"workflow,omitempty"`
}

// WorkflowReference references a workflow for an operation (simplified to avoid circular deps)
type WorkflowReference struct {
	Name            string                 `yaml:"name" json:"name"`
	Description     string                 `yaml:"description" json:"description"`
	AgentModifiable bool                   `yaml:"agentModifiable" json:"agentModifiable"`
	InputSchema     map[string]interface{} `yaml:"inputSchema" json:"inputSchema"`
	Steps           []WorkflowStep         `yaml:"steps" json:"steps"`
}

// WorkflowStep defines a step in a workflow
type WorkflowStep struct {
	ID          string                 `yaml:"id" json:"id"`
	Tool        string                 `yaml:"tool" json:"tool"`
	Args        map[string]interface{} `yaml:"args,omitempty" json:"args,omitempty"`
	Store       string                 `yaml:"store,omitempty" json:"store,omitempty"`
	Condition   string                 `yaml:"condition,omitempty" json:"condition,omitempty"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
}

// WorkflowInputSchema defines the input parameters for a workflow
type WorkflowInputSchema struct {
	Type       string                    `yaml:"type" json:"type"`
	Properties map[string]SchemaProperty `yaml:"properties" json:"properties"`
	Required   []string                  `yaml:"required,omitempty" json:"required,omitempty"`
}

// WorkflowHandler defines the interface for workflow operations
type WorkflowHandler interface {
	// ExecuteWorkflow executes a workflow
	ExecuteWorkflow(ctx context.Context, workflowName string, args map[string]interface{}) (*CallToolResult, error)

	// GetWorkflows returns information about all workflows
	GetWorkflows() []Workflow

	// GetWorkflow returns a specific workflow definition
	GetWorkflow(name string) (*Workflow, error)

	// CreateWorkflowFromStructured creates a new workflow from structured parameters
	CreateWorkflowFromStructured(args map[string]interface{}) error

	// UpdateWorkflowFromStructured updates an existing workflow from structured parameters
	UpdateWorkflowFromStructured(name string, args map[string]interface{}) error

	// DeleteWorkflow deletes a workflow
	DeleteWorkflow(name string) error

	// ValidateWorkflowFromStructured validates a workflow definition from structured parameters
	ValidateWorkflowFromStructured(args map[string]interface{}) error

	// Embed ToolProvider for tool generation
	ToolProvider
}

// CreateWorkflowRequest represents a request to create a new workflow
type CreateWorkflowRequest struct {
	Name        string                 `yaml:"name" json:"name"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	InputSchema map[string]interface{} `yaml:"inputSchema" json:"inputSchema"`
	Steps       []WorkflowStep         `yaml:"steps" json:"steps"`
}
