package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"muster/internal/api"
	"muster/internal/config"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	"gopkg.in/yaml.v3"
)

// WorkflowManager manages workflows and their execution
type WorkflowManager struct {
	storage          *config.Storage          // Use the new Storage
	workflows        map[string]*api.Workflow // In-memory workflow storage
	executor         *WorkflowExecutor
	executionTracker *ExecutionTracker // NEW: Execution tracking
	toolChecker      config.ToolAvailabilityChecker
	configPath       string // Optional custom config path
	mu               sync.RWMutex
	stopped          bool
}

// NewWorkflowManager creates a new workflow manager
func NewWorkflowManager(storage *config.Storage, toolCaller ToolCaller, toolChecker config.ToolAvailabilityChecker) (*WorkflowManager, error) {
	executor := NewWorkflowExecutor(toolCaller)

	// Extract config path from storage if it has one
	var configPath string
	if storage != nil {
		// We can't directly access the configPath from storage, so we'll pass it via parameter later
		// For now, leave it empty
	}

	// Initialize execution storage and tracker
	executionStorage := NewExecutionStorage(configPath)
	executionTracker := NewExecutionTracker(executionStorage)

	wm := &WorkflowManager{
		storage:          storage,
		workflows:        make(map[string]*api.Workflow),
		executor:         executor,
		executionTracker: executionTracker,
		toolChecker:      toolChecker,
		configPath:       configPath,
	}

	// Subscribe to tool update events for logging (workflows use dynamic checking)
	api.SubscribeToToolUpdates(wm)
	logging.Debug("WorkflowManager", "Subscribed to tool update events")

	return wm, nil
}

// SetConfigPath sets the custom configuration path
func (wm *WorkflowManager) SetConfigPath(configPath string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.configPath = configPath

	// Reinitialize execution storage and tracker with new config path
	executionStorage := NewExecutionStorage(configPath)
	wm.executionTracker = NewExecutionTracker(executionStorage)
	logging.Debug("WorkflowManager", "Updated execution tracker with config path: %s", configPath)
}

// SetToolCaller sets the ToolCaller for workflow execution
func (wm *WorkflowManager) SetToolCaller(toolCaller ToolCaller) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.executor = NewWorkflowExecutor(toolCaller)
	logging.Debug("WorkflowManager", "Updated workflow executor with new ToolCaller")
}

// LoadDefinitions loads all workflow definitions from YAML files.
// All workflows are just YAML files, regardless of how they were created.
func (wm *WorkflowManager) LoadDefinitions() error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Clear existing in-memory workflows
	wm.workflows = make(map[string]*api.Workflow)

	// Load all workflow YAML files using the config path-aware helper
	validator := func(def api.Workflow) error {
		return wm.validateWorkflowDefinition(&def)
	}

	definitions, errorCollection, err := config.LoadAndParseYAMLWithConfig(wm.configPath, "workflows", validator)
	if err != nil {
		logging.Warn("WorkflowManager", "Error loading workflows: %v", err)
		return err
	}

	// Log any validation errors but continue with valid definitions
	if errorCollection.HasErrors() {
		logging.Warn("WorkflowManager", "Some workflow files had errors:\n%s", errorCollection.GetSummary())
	}

	// Add all valid definitions to in-memory store
	for i := range definitions {
		def := definitions[i] // Important: take a copy
		wm.workflows[def.Name] = &def
	}

	logging.Info("WorkflowManager", "Loaded %d workflows from YAML files", len(definitions))
	return nil
}

// validateWorkflowDefinition performs comprehensive validation on a workflow definition
func (wm *WorkflowManager) validateWorkflowDefinition(def *api.Workflow) error {
	var errors config.ValidationErrors

	// Validate entity name using common helper
	if err := config.ValidateEntityName(def.Name, "workflow"); err != nil {
		errors = append(errors, err.(config.ValidationError))
	}

	// Validate description (optional but recommended)
	if def.Description != "" {
		if err := config.ValidateMaxLength("description", def.Description, 500); err != nil {
			errors = append(errors, err.(config.ValidationError))
		}
	}

	// Validate steps - workflows must have at least one step
	if len(def.Steps) == 0 {
		errors.Add("steps", "must have at least one step for workflow")
	} else {
		stepIDs := make(map[string]bool)

		// Validate each step
		for i, step := range def.Steps {
			if step.ID == "" {
				errors.Add(fmt.Sprintf("steps[%d].id", i), "step ID cannot be empty")
				continue
			}

			// Check for duplicate step IDs
			if stepIDs[step.ID] {
				errors.Add(fmt.Sprintf("steps[%d].id", i), fmt.Sprintf("duplicate step ID '%s'", step.ID))
			}
			stepIDs[step.ID] = true

			// Validate step tool
			if step.Tool == "" {
				errors.Add(fmt.Sprintf("steps[%d].tool", i), "tool name cannot be empty")
			}

			// Note: WorkflowStep doesn't have a description field
		}
	}

	// Validate input schema if present
	if def.InputSchema.Type != "" {
		validTypes := []string{"object", "array", "string", "number", "boolean"}
		if err := config.ValidateOneOf("inputSchema.type", def.InputSchema.Type, validTypes); err != nil {
			errors = append(errors, err.(config.ValidationError))
		}

		// Validate required fields if specified
		for i, required := range def.InputSchema.Required {
			if required == "" {
				errors.Add(fmt.Sprintf("inputSchema.required[%d]", i), "required field name cannot be empty")
			}
		}

		// Validate properties if specified
		for propName, prop := range def.InputSchema.Properties {
			if propName == "" {
				errors.Add("inputSchema.properties", "property name cannot be empty")
				continue
			}

			if prop.Type == "" {
				errors.Add(fmt.Sprintf("inputSchema.properties.%s.type", propName), "property type is required")
			} else {
				if err := config.ValidateOneOf(fmt.Sprintf("inputSchema.properties.%s.type", propName), prop.Type, validTypes); err != nil {
					errors = append(errors, err.(config.ValidationError))
				}
			}
		}
	}

	if errors.HasErrors() {
		return config.FormatValidationError("workflow", def.Name, errors)
	}

	return nil
}

// GetDefinition returns a workflow definition by name (implements common manager interface)
func (wm *WorkflowManager) GetDefinition(name string) (api.Workflow, bool) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	workflow, exists := wm.workflows[name]
	if !exists {
		return api.Workflow{}, false
	}
	return *workflow, true
}

// ListDefinitions returns all workflow definitions (implements common manager interface)
func (wm *WorkflowManager) ListDefinitions() []api.Workflow {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	workflows := make([]api.Workflow, 0, len(wm.workflows))
	for _, wf := range wm.workflows {
		workflows = append(workflows, *wf)
	}
	return workflows
}

// ListAvailableDefinitions returns only workflow definitions that have all required tools available
func (wm *WorkflowManager) ListAvailableDefinitions() []api.Workflow {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	var available []api.Workflow
	for _, wf := range wm.workflows {
		if wm.isWorkflowAvailable(wf) {
			available = append(available, *wf)
		}
	}

	return available
}

// IsAvailable checks if a workflow is available (has all required tools) (implements common manager interface)
func (wm *WorkflowManager) IsAvailable(name string) bool {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	workflow, exists := wm.workflows[name]
	if !exists {
		return false
	}

	return wm.isWorkflowAvailable(workflow)
}

// isWorkflowAvailable checks if a workflow has all required tools available
func (wm *WorkflowManager) isWorkflowAvailable(workflow *api.Workflow) bool {
	if wm.toolChecker == nil {
		return true // Assume available if no tool checker
	}

	// Check each step's tool availability
	for _, step := range workflow.Steps {
		if !wm.toolChecker.IsToolAvailable(step.Tool) {
			return false
		}
	}

	return true
}

// RefreshAvailability refreshes the availability status of all workflows (implements common manager interface)
func (wm *WorkflowManager) RefreshAvailability() {
	// Workflow availability is checked dynamically, so no caching needed
	logging.Debug("WorkflowManager", "Refreshed workflow availability (dynamic checking)")
}

// GetDefinitionsPath returns the paths where workflow definitions are loaded from (implements common manager interface)
func (wm *WorkflowManager) GetDefinitionsPath() string {
	userDir, projectDir, err := config.GetConfigurationPaths()
	if err != nil {
		logging.Error("WorkflowManager", err, "Failed to get configuration paths")
		return "error determining paths"
	}

	userPath := fmt.Sprintf("%s/workflows", userDir)
	projectPath := fmt.Sprintf("%s/workflows", projectDir)

	return fmt.Sprintf("User: %s, Project: %s", userPath, projectPath)
}

// GetWorkflows returns all available workflows as MCP tools
func (wm *WorkflowManager) GetWorkflows() []mcp.Tool {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	tools := make([]mcp.Tool, 0, len(wm.workflows))

	for _, wf := range wm.workflows {
		// Only include workflows that have all required tools available
		if wm.isWorkflowAvailable(wf) {
			tool := wm.workflowToTool(*wf)
			tools = append(tools, tool)
		}
	}

	return tools
}

// ExecuteWorkflow executes a workflow by name with automatic execution tracking
func (wm *WorkflowManager) ExecuteWorkflow(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	workflow, exists := wm.workflows[name]
	if !exists {
		return nil, api.NewWorkflowNotFoundError(name)
	}

	// Check if workflow is available before execution
	if !wm.isWorkflowAvailable(workflow) {
		return nil, fmt.Errorf("workflow %s is not available (missing required tools)", name)
	}

	// Execute workflow with automatic tracking
	result, execution, err := wm.executionTracker.TrackExecution(ctx, name, args, func() (*mcp.CallToolResult, error) {
		return wm.executor.ExecuteWorkflow(ctx, workflow, args)
	})

	// Include execution_id in the response for test scenarios and API consumers
	// This should happen even for failed workflows since they still have execution tracking
	if execution != nil {
		if result != nil {
			result = wm.enhanceResultWithExecutionID(result, execution.ExecutionID)
		} else if err != nil {
			// For failed workflows with no result, create a minimal result with execution_id
			result = &mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent(fmt.Sprintf(`{"execution_id": "%s", "error": "%s"}`, execution.ExecutionID, err.Error()))},
				IsError: true,
			}
		}
	}

	return result, err
}

// enhanceResultWithExecutionID modifies the workflow execution result to include the execution_id
// This allows test scenarios and API consumers to access the execution_id for further operations
func (wm *WorkflowManager) enhanceResultWithExecutionID(result *mcp.CallToolResult, executionID string) *mcp.CallToolResult {
	if result == nil || len(result.Content) == 0 {
		// Create a basic result with execution_id if no content exists
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.NewTextContent(fmt.Sprintf(`{"execution_id": "%s"}`, executionID))},
			IsError: result.IsError,
		}
	}

	// Try to enhance the first content item with execution_id
	firstContent := result.Content[0]
	if textContent, ok := firstContent.(mcp.TextContent); ok {
		// Try to parse the existing content as JSON
		var contentData map[string]interface{}
		if err := json.Unmarshal([]byte(textContent.Text), &contentData); err == nil {
			// Successfully parsed as JSON - add execution_id
			contentData["execution_id"] = executionID

			// Re-marshal to JSON
			enhancedJSON, marshalErr := json.Marshal(contentData)
			if marshalErr == nil {
				// Create new result with enhanced content
				enhancedResult := &mcp.CallToolResult{
					Content: []mcp.Content{mcp.NewTextContent(string(enhancedJSON))},
					IsError: result.IsError,
				}

				// Add any additional content items
				if len(result.Content) > 1 {
					enhancedResult.Content = append(enhancedResult.Content, result.Content[1:]...)
				}

				return enhancedResult
			}
		}
	}

	// Fallback: prepend execution_id as new content item if we can't enhance existing content
	executionContent := mcp.NewTextContent(fmt.Sprintf(`{"execution_id": "%s"}`, executionID))
	enhancedResult := &mcp.CallToolResult{
		Content: append([]mcp.Content{executionContent}, result.Content...),
		IsError: result.IsError,
	}

	return enhancedResult
}

// Stop gracefully stops the workflow manager
func (wm *WorkflowManager) Stop() {
	wm.mu.Lock()
	wm.stopped = true
	wm.mu.Unlock()
}

// workflowToTool converts a workflow definition to an MCP tool
func (wm *WorkflowManager) workflowToTool(workflow api.Workflow) mcp.Tool {
	// Convert workflow input schema to MCP tool input schema
	properties := make(map[string]interface{})
	required := workflow.InputSchema.Required

	for name, prop := range workflow.InputSchema.Properties {
		propSchema := map[string]interface{}{
			"type":        prop.Type,
			"description": prop.Description,
		}
		if prop.Default != nil {
			propSchema["default"] = prop.Default
		}
		properties[name] = propSchema
	}

	inputSchema := mcp.ToolInputSchema{
		Type:       workflow.InputSchema.Type,
		Properties: properties,
		Required:   required,
	}

	// Prefix workflow tools with "action_" to indicate they are high-level actions
	toolName := fmt.Sprintf("action_%s", workflow.Name)

	return mcp.Tool{
		Name:        toolName,
		Description: workflow.Description,
		InputSchema: inputSchema,
	}
}

// CreateWorkflow creates and persists a new workflow
func (wm *WorkflowManager) CreateWorkflow(wf api.Workflow) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.workflows[wf.Name]; exists {
		return fmt.Errorf("workflow '%s' already exists", wf.Name)
	}

	// Validate before saving
	if err := wm.validateWorkflowDefinition(&wf); err != nil {
		return fmt.Errorf("workflow validation failed: %w", err)
	}

	data, err := yaml.Marshal(wf)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow %s: %w", wf.Name, err)
	}

	if err := wm.storage.Save("workflows", wf.Name, data); err != nil {
		return fmt.Errorf("failed to save workflow %s: %w", wf.Name, err)
	}

	// Add to in-memory store after successful save
	wm.workflows[wf.Name] = &wf
	logging.Info("WorkflowManager", "Created workflow %s with tool name: action_%s", wf.Name, wf.Name)

	return nil
}

// UpdateWorkflow updates and persists an existing workflow
func (wm *WorkflowManager) UpdateWorkflow(name string, wf api.Workflow) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.workflows[name]; !exists {
		return api.NewWorkflowNotFoundError(name)
	}
	// Ensure the name in the object matches the name being updated
	wf.Name = name

	// Validate before saving
	if err := wm.validateWorkflowDefinition(&wf); err != nil {
		return fmt.Errorf("workflow validation failed: %w", err)
	}

	data, err := yaml.Marshal(wf)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow %s: %w", name, err)
	}

	if err := wm.storage.Save("workflows", name, data); err != nil {
		return fmt.Errorf("failed to save workflow %s: %w", name, err)
	}

	// Update in-memory store after successful save
	wm.workflows[name] = &wf
	logging.Info("WorkflowManager", "Updated workflow %s with tool name: action_%s", name, name)

	return nil
}

// DeleteWorkflow deletes a workflow from memory and storage
func (wm *WorkflowManager) DeleteWorkflow(name string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.workflows[name]; !exists {
		return api.NewWorkflowNotFoundError(name)
	}

	if err := wm.storage.Delete("workflows", name); err != nil {
		return fmt.Errorf("failed to delete workflow %s from YAML files: %w", name, err)
	}

	// Delete from in-memory store after successful deletion from YAML files
	delete(wm.workflows, name)
	logging.Info("WorkflowManager", "Deleted workflow %s (was tool: action_%s)", name, name)

	return nil
}

// OnToolsUpdated implements ToolUpdateSubscriber interface
func (wm *WorkflowManager) OnToolsUpdated(event api.ToolUpdateEvent) {
	logging.Debug("WorkflowManager", "Received tool update event: type=%s, server=%s, tools=%d (workflows use dynamic checking)",
		event.Type, event.ServerName, len(event.Tools))

	// Note: Workflows use dynamic checking, so no explicit refresh needed
	// This subscription is mainly for logging and potential future enhancements
}
