package capability

import (
	"context"
	"fmt"

	"muster/internal/api"
	"muster/internal/config"
	"muster/pkg/logging"
)

// Adapter provides the API adapter for capability management
type Adapter struct {
	manager    *CapabilityManager
	toolCaller *api.ToolCaller
}

// NewAdapter creates a new capability API adapter
func NewAdapter(toolChecker *api.ToolChecker, toolCaller *api.ToolCaller, storage *config.Storage) (*Adapter, error) {
	manager, err := NewCapabilityManager(storage, toolChecker)
	if err != nil {
		return nil, fmt.Errorf("failed to create capability manager: %w", err)
	}

	return &Adapter{
		manager:    manager,
		toolCaller: toolCaller,
	}, nil
}

// Register registers this adapter with the API layer
func (a *Adapter) Register() {
	api.RegisterCapability(a)
	logging.Debug("CapabilityAdapter", "Registered capability adapter with API layer")
}

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "capability_list",
			Description: "List all capability definitions",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "available_only",
					Type:        "boolean",
					Required:    false,
					Description: "Only show capabilities that have all required tools available",
					Default:     false,
				},
			},
		},
		{
			Name:        "capability_get",
			Description: "Get details of a specific capability definition",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the capability to retrieve",
				},
			},
		},
		{
			Name:        "capability_check",
			Description: "Check if a capability is available (all required tools are available)",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the capability to check",
				},
			},
		},
		{
			Name:        "capability_create",
			Description: "Create a new capability definition",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Capability name"},
				{Name: "type", Type: "string", Required: true, Description: "Capability type identifier"},
				{Name: "version", Type: "string", Required: false, Description: "Capability version"},
				{Name: "description", Type: "string", Required: false, Description: "Capability description"},
				{
					Name:        "operations",
					Type:        "object",
					Required:    true,
					Description: "Operations map with operation definitions",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Operations map with operation definitions",
						"additionalProperties": map[string]interface{}{
							"type":        "object",
							"description": "Operation definition",
							"properties": map[string]interface{}{
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable description of what this operation does",
								},
								"parameters": map[string]interface{}{
									"type":        "object",
									"description": "Input parameters for this operation",
									"additionalProperties": map[string]interface{}{
										"type":        "object",
										"description": "Parameter definition",
										"properties": map[string]interface{}{
											"type": map[string]interface{}{
												"type":        "string",
												"description": "Parameter type (string, number, boolean, object, array)",
											},
											"required": map[string]interface{}{
												"type":        "boolean",
												"description": "Whether this parameter is required",
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
								"requires": map[string]interface{}{
									"type":        "array",
									"description": "List of tools this operation requires to be available",
									"items": map[string]interface{}{
										"type":        "string",
										"description": "Tool name",
									},
								},
								"workflow": map[string]interface{}{
									"type":        "string",
									"description": "Name of the workflow to execute for this operation (optional)",
								},
							},
							"required": []string{"description"},
							"additionalProperties": false,
						},
					},
				},
			},
		},
		{
			Name:        "capability_update",
			Description: "Update an existing capability definition",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Name of the capability to update"},
				{Name: "type", Type: "string", Required: false, Description: "Capability type identifier"},
				{Name: "version", Type: "string", Required: false, Description: "Capability version"},
				{Name: "description", Type: "string", Required: false, Description: "Capability description"},
				{
					Name:        "operations",
					Type:        "object",
					Required:    false,
					Description: "Operations map with operation definitions",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Operations map with operation definitions",
						"additionalProperties": map[string]interface{}{
							"type":        "object",
							"description": "Operation definition",
							"properties": map[string]interface{}{
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable description of what this operation does",
								},
								"parameters": map[string]interface{}{
									"type":        "object",
									"description": "Input parameters for this operation",
									"additionalProperties": map[string]interface{}{
										"type":        "object",
										"description": "Parameter definition",
										"properties": map[string]interface{}{
											"type": map[string]interface{}{
												"type":        "string",
												"description": "Parameter type (string, number, boolean, object, array)",
											},
											"required": map[string]interface{}{
												"type":        "boolean",
												"description": "Whether this parameter is required",
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
								"requires": map[string]interface{}{
									"type":        "array",
									"description": "List of tools this operation requires to be available",
									"items": map[string]interface{}{
										"type":        "string",
										"description": "Tool name",
									},
								},
								"workflow": map[string]interface{}{
									"type":        "string",
									"description": "Name of the workflow to execute for this operation (optional)",
								},
							},
							"required": []string{"description"},
							"additionalProperties": false,
						},
					},
				},
			},
		},
		{
			Name:        "capability_delete",
			Description: "Delete a capability definition",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the capability to delete",
				},
			},
		},
		{
			Name:        "capability_validate",
			Description: "Validate a capability definition",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Capability name"},
				{Name: "type", Type: "string", Required: true, Description: "Capability type identifier"},
				{Name: "version", Type: "string", Required: false, Description: "Capability version"},
				{Name: "description", Type: "string", Required: false, Description: "Capability description"},
				{
					Name:        "operations",
					Type:        "object",
					Required:    true,
					Description: "Operations map with operation definitions",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Operations map with operation definitions",
						"additionalProperties": map[string]interface{}{
							"type":        "object",
							"description": "Operation definition",
							"properties": map[string]interface{}{
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable description of what this operation does",
								},
								"parameters": map[string]interface{}{
									"type":        "object",
									"description": "Input parameters for this operation",
									"additionalProperties": map[string]interface{}{
										"type":        "object",
										"description": "Parameter definition",
										"properties": map[string]interface{}{
											"type": map[string]interface{}{
												"type":        "string",
												"description": "Parameter type (string, number, boolean, object, array)",
											},
											"required": map[string]interface{}{
												"type":        "boolean",
												"description": "Whether this parameter is required",
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
								"requires": map[string]interface{}{
									"type":        "array",
									"description": "List of tools this operation requires to be available",
									"items": map[string]interface{}{
										"type":        "string",
										"description": "Tool name",
									},
								},
								"workflow": map[string]interface{}{
									"type":        "string",
									"description": "Name of the workflow to execute for this operation (optional)",
								},
							},
							"required": []string{"description"},
							"additionalProperties": false,
						},
					},
				},
			},
		},
	}
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "capability_list":
		availableOnly := false
		if val, ok := args["available_only"].(bool); ok {
			availableOnly = val
		}
		if availableOnly {
			return a.listAvailableCapabilities(ctx)
		}
		return a.listCapabilities(ctx)

	case "capability_get":
		name, ok := args["name"].(string)
		if !ok {
			return &api.CallToolResult{
				Content: []interface{}{"name parameter is required"},
				IsError: true,
			}, nil
		}
		return a.getCapability(ctx, name)

	case "capability_check":
		name, ok := args["name"].(string)
		if !ok {
			return &api.CallToolResult{
				Content: []interface{}{"name parameter is required"},
				IsError: true,
			}, nil
		}
		return a.checkCapabilityAvailable(ctx, name)

	case "capability_create":
		return a.handleCapabilityCreateFromArgs(ctx, args)

	case "capability_update":
		return a.handleCapabilityUpdateFromArgs(ctx, args)

	case "capability_delete":
		name, ok := args["name"].(string)
		if !ok {
			return &api.CallToolResult{
				Content: []interface{}{"name parameter is required"},
				IsError: true,
			}, nil
		}
		return a.handleCapabilityDelete(ctx, name)

	case "capability_validate":
		return a.handleCapabilityValidate(ctx, args)

	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// Helper methods for handling creation and update from args
func (a *Adapter) handleCapabilityCreateFromArgs(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.CapabilityCreateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	return a.handleCapabilityCreate(ctx, req.Name, req.Type, req.Version, req.Description, req.Operations)
}

func (a *Adapter) handleCapabilityUpdateFromArgs(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.CapabilityUpdateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	return a.handleCapabilityUpdate(ctx, req.Name, req.Type, req.Version, req.Description, req.Operations)
}

// listAvailableCapabilities lists only available capabilities
func (a *Adapter) listAvailableCapabilities(ctx context.Context) (*api.CallToolResult, error) {
	definitions := a.manager.ListAvailableDefinitions()

	result := make([]map[string]interface{}, len(definitions))
	for i, def := range definitions {
		result[i] = map[string]interface{}{
			"name":        def.Name,
			"type":        def.Type,
			"version":     def.Version,
			"description": def.Description,
			"available":   true, // All listed are available
			"operations":  len(def.Operations),
		}
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Found %d available capability definitions", len(result)), result},
		IsError: false,
	}, nil
}

// ExecuteCapability executes a capability operation (implements CapabilityHandler interface)
func (a *Adapter) ExecuteCapability(ctx context.Context, capabilityType, operation string, params map[string]interface{}) (*api.CallToolResult, error) {
	// Find the operation
	toolName := fmt.Sprintf("api_%s_%s", capabilityType, operation)
	opDef, capDef, err := a.manager.GetOperationForTool(toolName)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Operation not found: %v", err)},
			IsError: true,
		}, nil
	}

	// Check if operation is available
	if !a.manager.IsAvailable(capDef.Name) {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Capability %s is not available (missing required tools)", capabilityType)},
			IsError: true,
		}, nil
	}

	// Execute the capability operation
	logging.Info("CapabilityAdapter", "Executing capability operation: %s.%s (description: %s)", capabilityType, operation, opDef.Description)

	// For now, return a placeholder result
	// TODO: Implement actual capability execution logic
	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Executed %s.%s successfully", capabilityType, operation)},
		IsError: false,
	}, nil
}

// IsCapabilityAvailable checks if a capability operation is available (implements CapabilityHandler interface)
func (a *Adapter) IsCapabilityAvailable(capabilityType, operation string) bool {
	toolName := fmt.Sprintf("api_%s_%s", capabilityType, operation)
	_, _, err := a.manager.GetOperationForTool(toolName)
	if err != nil {
		return false
	}

	// Check if the capability itself is available
	def, exists := a.manager.GetDefinition(capabilityType)
	if !exists {
		return false
	}

	return a.manager.IsAvailable(def.Name)
}

// ListCapabilities returns information about all available capabilities (implements CapabilityHandler interface)
func (a *Adapter) ListCapabilities() []api.Capability {
	definitions := a.manager.ListDefinitions()
	result := make([]api.Capability, len(definitions))

	for i, def := range definitions {
		result[i] = api.Capability{
			Name:        def.Name,
			Type:        def.Type,
			Description: def.Description,
			Version:     def.Version,
			Operations:  def.Operations,
			Available:   a.manager.IsAvailable(def.Name),
		}
	}

	return result
}

// listCapabilities lists all capability definitions
func (a *Adapter) listCapabilities(ctx context.Context) (*api.CallToolResult, error) {
	definitions := a.manager.ListDefinitions()

	result := make([]map[string]interface{}, len(definitions))
	for i, def := range definitions {
		available := a.manager.IsAvailable(def.Name)
		result[i] = map[string]interface{}{
			"name":        def.Name,
			"type":        def.Type,
			"version":     def.Version,
			"description": def.Description,
			"available":   available,
			"operations":  len(def.Operations),
		}
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Found %d capability definitions", len(result)), result},
		IsError: false,
	}, nil
}

// getCapability gets a specific capability definition
func (a *Adapter) getCapability(ctx context.Context, name string) (*api.CallToolResult, error) {
	def, exists := a.manager.GetDefinition(name)
	if !exists {
		return api.HandleErrorWithPrefix(api.NewCapabilityNotFoundError(name), "Failed to get capability"), nil
	}

	available := a.manager.IsAvailable(name)

	result := map[string]interface{}{
		"name":        def.Name,
		"type":        def.Type,
		"version":     def.Version,
		"description": def.Description,
		"available":   available,
		"operations":  def.Operations,
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

// checkCapabilityAvailable checks if a capability is available
func (a *Adapter) checkCapabilityAvailable(ctx context.Context, name string) (*api.CallToolResult, error) {
	available := a.manager.IsAvailable(name)

	result := map[string]interface{}{
		"name":      name,
		"available": available,
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

// handleCapabilityValidate validates a capability definition
func (a *Adapter) handleCapabilityValidate(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.CapabilityValidateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	def := &api.Capability{
		Name:        req.Name,
		Type:        req.Type,
		Version:     req.Version,
		Description: req.Description,
		Operations:  req.Operations, // Already properly typed
	}

	if err := a.manager.ValidateDefinition(def); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Validation failed: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Validation successful for capability %s", req.Name)},
		IsError: false,
	}, nil
}

// GetCapability returns a specific capability definition (implements CapabilityHandler interface)
func (a *Adapter) GetCapability(name string) (interface{}, error) {
	def, exists := a.manager.GetDefinition(name)
	if !exists {
		return nil, api.NewCapabilityNotFoundError(name)
	}
	return &def, nil
}

// LoadDefinitions loads capability definitions from YAML files
func (a *Adapter) LoadDefinitions() error {
	return a.manager.LoadDefinitions()
}

// SetConfigPath sets the custom configuration path for the capability manager
func (a *Adapter) SetConfigPath(configPath string) {
	a.manager.SetConfigPath(configPath)
}

// Handler methods for new CRUD tools

func (a *Adapter) handleCapabilityCreate(ctx context.Context, name, capType, version, description string, operations map[string]api.OperationDefinition) (*api.CallToolResult, error) {
	def := &api.Capability{
		Name:        name,
		Type:        capType,
		Version:     version,
		Description: description,
		Operations:  operations,
	}

	// Validate the definition
	if err := a.manager.ValidateDefinition(def); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Invalid capability definition: %v", err)},
			IsError: true,
		}, nil
	}

	// Check if it already exists
	if _, exists := a.manager.GetDefinition(def.Name); exists {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Capability '%s' already exists", def.Name)},
			IsError: true,
		}, nil
	}

	// Create the new capability
	if err := a.manager.CreateCapability(def); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to create capability: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Capability '%s' created successfully", def.Name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleCapabilityUpdate(ctx context.Context, name, capType, version, description string, operations map[string]api.OperationDefinition) (*api.CallToolResult, error) {
	// Check if capability exists and get the current definition
	existingDef, exists := a.manager.GetDefinition(name)
	if !exists {
		return api.HandleErrorWithPrefix(api.NewCapabilityNotFoundError(name), "Failed to update capability"), nil
	}

	// Create updated definition by merging provided fields with existing definition
	updatedDef := &api.Capability{
		Name:        name, // Name cannot be changed
		Type:        existingDef.Type,
		Version:     existingDef.Version,
		Description: existingDef.Description,
		Operations:  existingDef.Operations,
	}

	// Apply updates for non-empty fields
	if capType != "" {
		updatedDef.Type = capType
	}
	if version != "" {
		updatedDef.Version = version
	}
	if description != "" {
		updatedDef.Description = description
	}
	if operations != nil && len(operations) > 0 {
		updatedDef.Operations = operations
	}

	// Validate the merged definition
	if err := a.manager.ValidateDefinition(updatedDef); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Invalid capability definition: %v", err)},
			IsError: true,
		}, nil
	}

	// Update the capability
	if err := a.manager.UpdateCapability(updatedDef); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to update capability"), nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Capability '%s' updated successfully", name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleCapabilityDelete(ctx context.Context, name string) (*api.CallToolResult, error) {
	// Check if the capability exists
	_, exists := a.manager.GetDefinition(name)
	if !exists {
		return api.HandleErrorWithPrefix(api.NewCapabilityNotFoundError(name), "Failed to delete capability"), nil
	}

	// Delete the capability
	if err := a.manager.DeleteCapability(name); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to delete capability"), nil
	}

	result := map[string]interface{}{
		"action": "deleted",
		"name":   name,
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Successfully deleted capability: %s", name), result},
		IsError: false,
	}, nil
}
