package mcpserver

import (
	"context"
	"fmt"

	"muster/internal/api"
)

// Adapter provides MCP server management functionality
type Adapter struct {
	manager *MCPServerManager
}

// NewAdapter creates a new MCP server API adapter
func NewAdapter(manager *MCPServerManager) *Adapter {
	return &Adapter{
		manager: manager,
	}
}

// Register registers the adapter with the API
func (a *Adapter) Register() {
	api.RegisterMCPServerManager(a)
}

// ListMCPServers returns all MCP server definitions
func (a *Adapter) ListMCPServers() []api.MCPServerInfo {
	if a.manager == nil {
		return []api.MCPServerInfo{}
	}

	definitions := a.manager.ListDefinitions()
	result := make([]api.MCPServerInfo, len(definitions))

	for i, def := range definitions {
		result[i] = api.MCPServerInfo{
			Name:        def.Name,
			Type:        string(def.Type),
			AutoStart:   def.AutoStart,
			Description: def.Description,
			Command:     def.Command,
			Image:       def.Image,
			Env:         def.Env,
			Error:       def.Error,
		}
	}

	return result
}

// GetMCPServer returns information about a specific MCP server
func (a *Adapter) GetMCPServer(name string) (*api.MCPServerInfo, error) {
	if a.manager == nil {
		return nil, fmt.Errorf("MCP server manager not available")
	}

	def, exists := a.manager.GetDefinition(name)
	if !exists {
		return nil, api.NewMCPServerNotFoundError(name)
	}

	return &api.MCPServerInfo{
		Name:        def.Name,
		Type:        string(def.Type),
		AutoStart:   def.AutoStart,
		Description: def.Description,
		Command:     def.Command,
		Image:       def.Image,
		Env:         def.Env,
		Error:       def.Error,
	}, nil
}

// GetManager returns the underlying MCPServerManager (for internal use)
// This should only be used by other internal packages that need direct access
func (a *Adapter) GetManager() *MCPServerManager {
	return a.manager
}

// ToolProvider implementation

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "mcpserver_list",
			Description: "List all MCP server definitions with their status",
		},
		{
			Name:        "mcpserver_get",
			Description: "Get detailed information about a specific MCP server definition",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the MCP server to retrieve",
				},
			},
		},
		{
			Name:        "mcpserver_validate",
			Description: "Validate an mcpserver definition",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "MCP server name"},
				{Name: "type", Type: "string", Required: true, Description: "MCP server type (localCommand or container)"},
				{Name: "autoStart", Type: "boolean", Required: false, Description: "Whether server should auto-start"},
				{
					Name:        "command",
					Type:        "array",
					Required:    false,
					Description: "Command and arguments (for localCommand type)",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Command and arguments for localCommand type servers",
						"items": map[string]interface{}{
							"type":        "string",
							"description": "Command executable or argument",
						},
						"minItems": 1,
					},
				},
				{Name: "image", Type: "string", Required: false, Description: "Container image (for container type)"},
				{
					Name:        "env",
					Type:        "object",
					Required:    false,
					Description: "Environment variables",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Environment variables as key-value pairs",
						"additionalProperties": map[string]interface{}{
							"type":        "string",
							"description": "Environment variable value",
						},
					},
				},
				{
					Name:        "containerPorts",
					Type:        "array",
					Required:    false,
					Description: "Port mappings (for container type)",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Port mappings for container type servers",
						"items": map[string]interface{}{
							"type":        "string",
							"description": "Port mapping in format 'hostPort:containerPort' or just 'port'",
							"pattern":     "^\\d+(:\\d+)?$",
						},
					},
				},
				{Name: "description", Type: "string", Required: false, Description: "MCP server description"},
			},
		},
		{
			Name:        "mcpserver_create",
			Description: "Create a new MCP server definition",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "MCP server name"},
				{Name: "type", Type: "string", Required: true, Description: "MCP server type (localCommand or container)"},
				{Name: "autoStart", Type: "boolean", Required: false, Description: "Whether server should auto-start"},
				{
					Name:        "command",
					Type:        "array",
					Required:    false,
					Description: "Command and arguments (for localCommand type)",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Command and arguments for localCommand type servers",
						"items": map[string]interface{}{
							"type":        "string",
							"description": "Command executable or argument",
						},
						"minItems": 1,
					},
				},
				{Name: "image", Type: "string", Required: false, Description: "Container image (for container type)"},
				{
					Name:        "env",
					Type:        "object",
					Required:    false,
					Description: "Environment variables",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Environment variables as key-value pairs",
						"additionalProperties": map[string]interface{}{
							"type":        "string",
							"description": "Environment variable value",
						},
					},
				},
				{
					Name:        "containerPorts",
					Type:        "array",
					Required:    false,
					Description: "Port mappings (for container type)",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Port mappings for container type servers",
						"items": map[string]interface{}{
							"type":        "string",
							"description": "Port mapping in format 'hostPort:containerPort' or just 'port'",
							"pattern":     "^\\d+(:\\d+)?$",
						},
					},
				},
				{Name: "description", Type: "string", Required: false, Description: "MCP server description"},
			},
		},
		{
			Name:        "mcpserver_update",
			Description: "Update an existing MCP server definition",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "MCP server name"},
				{Name: "type", Type: "string", Required: false, Description: "MCP server type (localCommand or container)"},
				{Name: "autoStart", Type: "boolean", Required: false, Description: "Whether server should auto-start"},
				{
					Name:        "command",
					Type:        "array",
					Required:    false,
					Description: "Command and arguments (for localCommand type)",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Command and arguments for localCommand type servers",
						"items": map[string]interface{}{
							"type":        "string",
							"description": "Command executable or argument",
						},
						"minItems": 1,
					},
				},
				{Name: "image", Type: "string", Required: false, Description: "Container image (for container type)"},
				{
					Name:        "env",
					Type:        "object",
					Required:    false,
					Description: "Environment variables",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Environment variables as key-value pairs",
						"additionalProperties": map[string]interface{}{
							"type":        "string",
							"description": "Environment variable value",
						},
					},
				},
				{
					Name:        "containerPorts",
					Type:        "array",
					Required:    false,
					Description: "Port mappings (for container type)",
					Schema: map[string]interface{}{
						"type":        "array",
						"description": "Port mappings for container type servers",
						"items": map[string]interface{}{
							"type":        "string",
							"description": "Port mapping in format 'hostPort:containerPort' or just 'port'",
							"pattern":     "^\\d+(:\\d+)?$",
						},
					},
				},
				{Name: "description", Type: "string", Required: false, Description: "MCP server description"},
			},
		},
		{
			Name:        "mcpserver_delete",
			Description: "Delete an MCP server definition",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the MCP server to delete",
				},
			},
		},
	}
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "mcpserver_list":
		return a.handleMCPServerList()
	case "mcpserver_get":
		return a.handleMCPServerGet(args)
	case "mcpserver_validate":
		return a.handleMCPServerValidate(args)
	case "mcpserver_create":
		return a.handleMCPServerCreate(args)
	case "mcpserver_update":
		return a.handleMCPServerUpdate(args)
	case "mcpserver_delete":
		return a.handleMCPServerDelete(args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// Tool handlers

func (a *Adapter) handleMCPServerList() (*api.CallToolResult, error) {
	mcpServers := a.ListMCPServers()

	result := map[string]interface{}{
		"mcpServers": mcpServers,
		"total":      len(mcpServers),
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

func (a *Adapter) handleMCPServerGet(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name parameter is required"},
			IsError: true,
		}, nil
	}

	mcpServer, err := a.GetMCPServer(name)
	if err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to get MCP server"), nil
	}

	return &api.CallToolResult{
		Content: []interface{}{mcpServer},
		IsError: false,
	}, nil
}

// handleMCPServerValidate validates an mcpserver definition
func (a *Adapter) handleMCPServerValidate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.MCPServerValidateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Build internal api.MCPServer from structured parameters
	def := api.MCPServer{
		Name:           req.Name,
		Type:           api.MCPServerType(req.Type),
		AutoStart:      req.AutoStart,
		Image:          req.Image,
		Command:        req.Command,
		Env:            req.Env,
		ContainerPorts: req.ContainerPorts,
	}

	// Validate without persisting
	if err := a.manager.ValidateDefinition(&def); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Validation failed: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Validation successful for mcpserver %s", def.Name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleMCPServerCreate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.MCPServerCreateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Convert typed request to api.MCPServer
	def, err := convertCreateRequestToMCPServer(req)
	if err != nil {
		return simpleError(err.Error())
	}

	// Validate the definition
	if err := a.manager.ValidateDefinition(&def); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Check if it already exists
	if _, exists := a.manager.GetDefinition(def.Name); exists {
		return simpleError(fmt.Sprintf("MCP server '%s' already exists", def.Name))
	}

	// Create the new MCP server
	if err := a.manager.CreateMCPServer(def); err != nil {
		return simpleError(fmt.Sprintf("Failed to create MCP server: %v", err))
	}

	return simpleOK(fmt.Sprintf("MCP server '%s' created successfully", def.Name))
}

func (a *Adapter) handleMCPServerUpdate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.MCPServerUpdateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Convert typed request to api.MCPServer
	def, err := convertRequestToMCPServer(req)
	if err != nil {
		return simpleError(err.Error())
	}

	// Validate the definition
	if err := a.manager.ValidateDefinition(&def); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Check if it exists
	if _, exists := a.manager.GetDefinition(req.Name); !exists {
		return api.HandleErrorWithPrefix(api.NewMCPServerNotFoundError(req.Name), "Failed to update MCP server"), nil
	}

	// Update the MCP server
	if err := a.manager.UpdateMCPServer(req.Name, def); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to update MCP server"), nil
	}

	return simpleOK(fmt.Sprintf("MCP server '%s' updated successfully", req.Name))
}

func (a *Adapter) handleMCPServerDelete(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return simpleError("name parameter is required")
	}

	// Check if it exists
	if _, exists := a.manager.GetDefinition(name); !exists {
		return api.HandleErrorWithPrefix(api.NewMCPServerNotFoundError(name), "Failed to delete MCP server"), nil
	}

	// Delete the MCP server
	if err := a.manager.DeleteMCPServer(name); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to delete MCP server"), nil
	}

	return simpleOK(fmt.Sprintf("MCP server '%s' deleted successfully", name))
}

// helper to create simple error CallToolResult
func simpleError(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: true}, nil
}

func simpleOK(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: false}, nil
}

// convertRequestToMCPServer converts a typed request to api.MCPServer
func convertRequestToMCPServer(req api.MCPServerUpdateRequest) (api.MCPServer, error) {
	def := api.MCPServer{
		Name:             req.Name,
		Type:             api.MCPServerType(req.Type),
		AutoStart:        req.AutoStart,
		ToolPrefix:       req.ToolPrefix,
		Image:            req.Image,
		Command:          req.Command,
		Env:              req.Env,
		ContainerPorts:   req.ContainerPorts,
		ContainerEnv:     req.ContainerEnv,
		ContainerVolumes: req.ContainerVolumes,
		Entrypoint:       req.Entrypoint,
		ContainerUser:    req.ContainerUser,
	}

	return def, nil
}

// convertCreateRequestToMCPServer converts a typed request to api.MCPServer
func convertCreateRequestToMCPServer(req api.MCPServerCreateRequest) (api.MCPServer, error) {
	def := api.MCPServer{
		Name:             req.Name,
		Type:             api.MCPServerType(req.Type),
		AutoStart:        req.AutoStart,
		ToolPrefix:       req.ToolPrefix,
		Image:            req.Image,
		Command:          req.Command,
		Env:              req.Env,
		ContainerPorts:   req.ContainerPorts,
		ContainerEnv:     req.ContainerEnv,
		ContainerVolumes: req.ContainerVolumes,
		Entrypoint:       req.Entrypoint,
		ContainerUser:    req.ContainerUser,
	}

	return def, nil
}
