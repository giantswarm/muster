package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"muster/internal/api"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	"gopkg.in/yaml.v3"
)

// Adapter provides the API adapter for workflow management
type Adapter struct {
	manager *WorkflowManager
}

// NewAdapter creates a new workflow adapter
func NewAdapter(manager *WorkflowManager, toolCaller *api.ToolCaller) *Adapter {
	manager.SetToolCaller(toolCaller)
	return &Adapter{
		manager: manager,
	}
}

// Register registers this adapter with the API layer
func (a *Adapter) Register() {
	api.RegisterWorkflow(a)
	logging.Debug("WorkflowAdapter", "Registered workflow adapter with API layer")
}

// ExecuteWorkflow executes a workflow and returns MCP result
func (a *Adapter) ExecuteWorkflow(ctx context.Context, workflowName string, args map[string]interface{}) (*api.CallToolResult, error) {
	logging.Debug("WorkflowAdapter", "Executing workflow: %s", workflowName)

	// Get the MCP result
	mcpResult, err := a.manager.ExecuteWorkflow(ctx, workflowName, args)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Convert mcp.CallToolResult to api.CallToolResult
	var content []interface{}
	for _, mcpContent := range mcpResult.Content {
		if textContent, ok := mcpContent.(mcp.TextContent); ok {
			content = append(content, textContent.Text)
		} else {
			content = append(content, mcpContent)
		}
	}

	return &api.CallToolResult{
		Content: content,
		IsError: mcpResult.IsError,
	}, nil
}

// GetWorkflows returns information about all workflows
func (a *Adapter) GetWorkflows() []api.Workflow {
	workflows := a.manager.ListDefinitions()
	return workflows // Already using api.Workflow type
}

// GetWorkflow returns a specific workflow definition
func (a *Adapter) GetWorkflow(name string) (*api.Workflow, error) {
	workflow, exists := a.manager.GetDefinition(name)
	if !exists {
		return nil, api.NewWorkflowNotFoundError(name)
	}
	return &workflow, nil
}

// CreateWorkflowFromStructured creates a new workflow from structured parameters
func (a *Adapter) CreateWorkflowFromStructured(args map[string]interface{}) error {
	// Convert structured parameters to api.Workflow
	wf, err := convertToWorkflow(args)
	if err != nil {
		return err
	}

	return a.manager.CreateWorkflow(wf)
}

// UpdateWorkflowFromStructured updates an existing workflow from structured parameters
func (a *Adapter) UpdateWorkflowFromStructured(name string, args map[string]interface{}) error {
	// Convert structured parameters to api.Workflow
	wf, err := convertToWorkflow(args)
	if err != nil {
		return err
	}

	return a.manager.UpdateWorkflow(name, wf)
}

// ValidateWorkflowFromStructured validates a workflow definition from structured parameters
func (a *Adapter) ValidateWorkflowFromStructured(args map[string]interface{}) error {
	// Convert structured parameters to validate structure
	wf, err := convertToWorkflow(args)
	if err != nil {
		return err
	}

	// Basic validation
	if wf.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if len(wf.Steps) == 0 {
		return fmt.Errorf("workflow must have at least one step")
	}

	return nil
}

// DeleteWorkflow deletes a workflow
func (a *Adapter) DeleteWorkflow(name string) error {
	return a.manager.DeleteWorkflow(name)
}

// CallToolInternal calls a tool internally - required by ToolCaller interface
func (a *Adapter) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if a.manager == nil {
		return nil, fmt.Errorf("workflow manager not available")
	}

	// Delegate to the manager's tool caller (which should be the API-based one)
	return a.manager.executor.toolCaller.CallToolInternal(ctx, toolName, args)
}

// Stop stops the workflow adapter
func (a *Adapter) Stop() {
	if a.manager != nil {
		a.manager.Stop()
	}
}

// ReloadWorkflows reloads workflow definitions from disk
func (a *Adapter) ReloadWorkflows() error {
	if a.manager != nil {
		return a.manager.LoadDefinitions()
	}
	return nil
}

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	tools := []api.ToolMetadata{
		// Workflow management tools
		{
			Name:        "workflow_list",
			Description: "List all workflows",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "include_system",
					Type:        "boolean",
					Required:    false,
					Description: "Include system-defined workflows",
					Default:     true,
				},
			},
		},
		{
			Name:        "workflow_get",
			Description: "Get workflow details",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the workflow",
				},
			},
		},
		{
			Name:        "workflow_create",
			Description: "Create a new workflow",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Workflow name"},
				{Name: "description", Type: "string", Required: false, Description: "Workflow description"},
				{
					Name:        "inputSchema",
					Type:        "object",
					Required:    true,
					Description: "JSON Schema definition for workflow input validation",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "JSON Schema definition for workflow input validation",
						"properties": map[string]interface{}{
							"type": map[string]interface{}{
								"type":        "string",
								"description": "Schema type (typically 'object')",
								"default":     "object",
							},
							"properties": map[string]interface{}{
								"type":                 "object",
								"description":          "Property definitions for input parameters",
								"additionalProperties": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"type": map[string]interface{}{
											"type":        "string",
											"description": "Parameter type (string, number, boolean, object, array)",
										},
										"description": map[string]interface{}{
											"type":        "string",
											"description": "Parameter description",
										},
										"default": map[string]interface{}{
											"description": "Default value for the parameter",
										},
									},
								},
							},
							"required": map[string]interface{}{
								"type":        "array",
								"description": "List of required parameter names",
								"items": map[string]interface{}{
									"type": "string",
								},
							},
						},
						"required": []string{"type"},
					},
				},
				{
					Name:        "steps",
					Type:        "array",
					Required:    true,
					Description: "Array of workflow steps defining the execution sequence",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Array of workflow steps defining the execution sequence",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type":        "string",
									"description": "Unique identifier for this step within the workflow",
								},
								"tool": map[string]interface{}{
									"type":        "string",
									"description": "Name of the tool to execute for this step",
								},
								"args": map[string]interface{}{
									"type":        "object",
									"description": "Arguments to pass to the tool (optional)",
								},
								"store": map[string]interface{}{
									"type":        "string",
									"description": "Variable name to store the step result for use in later steps (optional)",
								},
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable description of what this step does (optional)",
								},
							},
							"required": []string{"id", "tool"},
							"additionalProperties": false,
						},
						"minItems": 1,
					},
				},
			},
		},
		{
			Name:        "workflow_update",
			Description: "Update an existing workflow",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Name of the workflow to update"},
				{Name: "description", Type: "string", Required: false, Description: "Workflow description"},
				{
					Name:        "inputSchema",
					Type:        "object",
					Required:    true,
					Description: "JSON Schema definition for workflow input validation",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "JSON Schema definition for workflow input validation",
						"properties": map[string]interface{}{
							"type": map[string]interface{}{
								"type":        "string",
								"description": "Schema type (typically 'object')",
								"default":     "object",
							},
							"properties": map[string]interface{}{
								"type":                 "object",
								"description":          "Property definitions for input parameters",
								"additionalProperties": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"type": map[string]interface{}{
											"type":        "string",
											"description": "Parameter type (string, number, boolean, object, array)",
										},
										"description": map[string]interface{}{
											"type":        "string",
											"description": "Parameter description",
										},
										"default": map[string]interface{}{
											"description": "Default value for the parameter",
										},
									},
								},
							},
							"required": map[string]interface{}{
								"type":        "array",
								"description": "List of required parameter names",
								"items": map[string]interface{}{
									"type": "string",
								},
							},
						},
						"required": []string{"type"},
					},
				},
				{
					Name:        "steps",
					Type:        "array",
					Required:    true,
					Description: "Array of workflow steps defining the execution sequence",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Array of workflow steps defining the execution sequence",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type":        "string",
									"description": "Unique identifier for this step within the workflow",
								},
								"tool": map[string]interface{}{
									"type":        "string",
									"description": "Name of the tool to execute for this step",
								},
								"args": map[string]interface{}{
									"type":        "object",
									"description": "Arguments to pass to the tool (optional)",
								},
								"store": map[string]interface{}{
									"type":        "string",
									"description": "Variable name to store the step result for use in later steps (optional)",
								},
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable description of what this step does (optional)",
								},
							},
							"required": []string{"id", "tool"},
							"additionalProperties": false,
						},
						"minItems": 1,
					},
				},
			},
		},
		{
			Name:        "workflow_delete",
			Description: "Delete a workflow",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the workflow to delete",
				},
			},
		},
		{
			Name:        "workflow_validate",
			Description: "Validate a workflow definition",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Workflow name"},
				{Name: "description", Type: "string", Required: false, Description: "Workflow description"},
				{
					Name:        "inputSchema",
					Type:        "object",
					Required:    true,
					Description: "JSON Schema definition for workflow input validation",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "JSON Schema definition for workflow input validation",
						"properties": map[string]interface{}{
							"type": map[string]interface{}{
								"type":        "string",
								"description": "Schema type (typically 'object')",
								"default":     "object",
							},
							"properties": map[string]interface{}{
								"type":                 "object",
								"description":          "Property definitions for input parameters",
								"additionalProperties": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"type": map[string]interface{}{
											"type":        "string",
											"description": "Parameter type (string, number, boolean, object, array)",
										},
										"description": map[string]interface{}{
											"type":        "string",
											"description": "Parameter description",
										},
										"default": map[string]interface{}{
											"description": "Default value for the parameter",
										},
									},
								},
							},
							"required": map[string]interface{}{
								"type":        "array",
								"description": "List of required parameter names",
								"items": map[string]interface{}{
									"type": "string",
								},
							},
						},
						"required": []string{"type"},
					},
				},
				{
					Name:        "steps",
					Type:        "array",
					Required:    true,
					Description: "Array of workflow steps defining the execution sequence",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Array of workflow steps defining the execution sequence",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type":        "string",
									"description": "Unique identifier for this step within the workflow",
								},
								"tool": map[string]interface{}{
									"type":        "string",
									"description": "Name of the tool to execute for this step",
								},
								"args": map[string]interface{}{
									"type":        "object",
									"description": "Arguments to pass to the tool (optional)",
								},
								"store": map[string]interface{}{
									"type":        "string",
									"description": "Variable name to store the step result for use in later steps (optional)",
								},
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable description of what this step does (optional)",
								},
							},
							"required": []string{"id", "tool"},
							"additionalProperties": false,
						},
						"minItems": 1,
					},
				},
			},
		},
	}

	// Add a tool for each workflow
	workflows := a.GetWorkflows()
	logging.Info("WorkflowAdapter", "GetTools called: found %d workflows", len(workflows))

	for _, wf := range workflows {
		toolName := fmt.Sprintf("action_%s", wf.Name)
		logging.Info("WorkflowAdapter", "Adding workflow tool: %s for workflow %s", toolName, wf.Name)
		tools = append(tools, api.ToolMetadata{
			Name:        toolName,
			Description: wf.Description,
			Parameters:  a.convertWorkflowParameters(wf.Name),
		})
	}

	logging.Info("WorkflowAdapter", "GetTools returning %d total tools (6 management + %d workflow execution)", len(tools), len(workflows))

	return tools
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch {
	case toolName == "workflow_list":
		return a.handleList(args)
	case toolName == "workflow_get":
		return a.handleGet(args)
	case toolName == "workflow_create":
		return a.handleCreate(args)
	case toolName == "workflow_update":
		return a.handleUpdate(args)
	case toolName == "workflow_delete":
		return a.handleDelete(args)
	case toolName == "workflow_validate":
		return a.handleValidate(args)

	case strings.HasPrefix(toolName, "action_"):
		// Execute workflow
		workflowName := strings.TrimPrefix(toolName, "action_")
		return a.ExecuteWorkflow(ctx, workflowName, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// convertWorkflowParameters converts workflow input schema to parameter metadata
func (a *Adapter) convertWorkflowParameters(workflowName string) []api.ParameterMetadata {
	workflow, err := a.GetWorkflow(workflowName)
	if err != nil {
		return nil
	}

	var params []api.ParameterMetadata

	// Extract properties from input schema
	for name, prop := range workflow.InputSchema.Properties {
		param := api.ParameterMetadata{
			Name:        name,
			Type:        prop.Type,
			Description: prop.Description,
			Default:     prop.Default,
		}

		// Check if required
		for _, req := range workflow.InputSchema.Required {
			if req == name {
				param.Required = true
				break
			}
		}

		params = append(params, param)
	}

	return params
}

// Helper methods for handling management operations
func (a *Adapter) handleList(args map[string]interface{}) (*api.CallToolResult, error) {
	workflows := a.GetWorkflows()

	var result []map[string]interface{}
	for _, wf := range workflows {
		workflowInfo := map[string]interface{}{
			"name":        wf.Name,
			"description": wf.Description,
			"available":   wf.Available,
		}
		result = append(result, workflowInfo)
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

func (a *Adapter) handleGet(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	workflow, err := a.GetWorkflow(name)
	if err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to get workflow"), nil
	}

	// Convert to YAML for easier viewing
	yamlData, err := yaml.Marshal(workflow)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to marshal workflow: %v", err)},
			IsError: true,
		}, nil
	}

	result := map[string]interface{}{
		"workflow": workflow,
		"yaml":     string(yamlData),
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

func (a *Adapter) handleCreate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.WorkflowCreateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Convert structured parameters to api.Workflow
	wf, err := convertToWorkflow(args)
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}

	// Create workflow through manager
	if err := a.manager.CreateWorkflow(wf); err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}

	// Refresh aggregator capabilities to include the new workflow tool
	if aggregator := api.GetAggregator(); aggregator != nil {
		logging.Info("WorkflowAdapter", "Refreshing aggregator capabilities after creating workflow %s", wf.Name)
		aggregator.UpdateCapabilities()
	}

	return &api.CallToolResult{
		Content: []interface{}{"Workflow created successfully"},
	}, nil
}

func (a *Adapter) handleUpdate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.WorkflowUpdateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	if err := a.UpdateWorkflowFromStructured(req.Name, args); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to update workflow"), nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Workflow '%s' updated successfully", req.Name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleDelete(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	if err := a.DeleteWorkflow(name); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to delete workflow"), nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Workflow '%s' deleted successfully", name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleValidate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.WorkflowValidateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	if err := a.ValidateWorkflowFromStructured(args); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Validation failed: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Validation successful for workflow %s", req.Name)},
		IsError: false,
	}, nil
}

// convertToWorkflow converts structured parameters to api.Workflow
func convertToWorkflow(args map[string]interface{}) (api.Workflow, error) {
	var wf api.Workflow

	// Required fields
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return wf, fmt.Errorf("name parameter is required")
	}
	wf.Name = name

	// Optional fields
	if desc, ok := args["description"].(string); ok {
		wf.Description = desc
	}


	// Convert inputSchema
	if inputSchemaParam, ok := args["inputSchema"].(map[string]interface{}); ok {
		inputSchema, err := convertInputSchema(inputSchemaParam)
		if err != nil {
			return wf, fmt.Errorf("invalid inputSchema: %v", err)
		}
		wf.InputSchema = inputSchema
	} else {
		return wf, fmt.Errorf("inputSchema parameter is required")
	}

	// Convert steps
	if stepsParam, ok := args["steps"].([]interface{}); ok {
		steps, err := convertWorkflowSteps(stepsParam)
		if err != nil {
			return wf, fmt.Errorf("invalid steps: %v", err)
		}
		wf.Steps = steps
	} else {
		return wf, fmt.Errorf("steps parameter is required")
	}

	// Set timestamps
	wf.CreatedAt = time.Now()
	wf.LastModified = time.Now()

	return wf, nil
}

// convertInputSchema converts a map[string]interface{} to api.WorkflowInputSchema
func convertInputSchema(schemaParam map[string]interface{}) (api.WorkflowInputSchema, error) {
	var schema api.WorkflowInputSchema

	// Type field
	if schemaType, ok := schemaParam["type"].(string); ok {
		schema.Type = schemaType
	}

	// Properties field
	if propsParam, ok := schemaParam["properties"].(map[string]interface{}); ok {
		properties := make(map[string]api.SchemaProperty)
		for name, prop := range propsParam {
			if propMap, ok := prop.(map[string]interface{}); ok {
				var property api.SchemaProperty
				if propType, ok := propMap["type"].(string); ok {
					property.Type = propType
				}
				if desc, ok := propMap["description"].(string); ok {
					property.Description = desc
				}
				if def, ok := propMap["default"]; ok {
					property.Default = def
				}
				properties[name] = property
			}
		}
		schema.Properties = properties
	}

	// Required field
	if requiredParam, ok := schemaParam["required"].([]interface{}); ok {
		var required []string
		for _, req := range requiredParam {
			if reqStr, ok := req.(string); ok {
				required = append(required, reqStr)
			}
		}
		schema.Required = required
	}

	return schema, nil
}

// convertWorkflowSteps converts []interface{} to []api.WorkflowStep
func convertWorkflowSteps(stepsParam []interface{}) ([]api.WorkflowStep, error) {
	var steps []api.WorkflowStep

	for i, stepParam := range stepsParam {
		stepMap, ok := stepParam.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("step %d is not a valid object", i)
		}

		var step api.WorkflowStep

		// ID is required
		if id, ok := stepMap["id"].(string); ok {
			step.ID = id
		} else {
			return nil, fmt.Errorf("step %d: id is required", i)
		}

		// Tool is required
		if tool, ok := stepMap["tool"].(string); ok {
			step.Tool = tool
		} else {
			return nil, fmt.Errorf("step %d: tool is required", i)
		}

		// Args (optional)
		if args, ok := stepMap["args"].(map[string]interface{}); ok {
			step.Args = args
		}

		// Store (optional)
		if store, ok := stepMap["store"].(string); ok {
			step.Store = store
		}



		// Description (optional)
		if description, ok := stepMap["description"].(string); ok {
			step.Description = description
		}

		steps = append(steps, step)
	}

	return steps, nil
}
