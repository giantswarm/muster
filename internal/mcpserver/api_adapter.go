package mcpserver

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"muster/internal/api"
	"muster/internal/client"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
)

// Adapter provides MCP server management functionality using the unified client
type Adapter struct {
	client    client.MusterClient
	namespace string
}

// NewAdapter creates a new MCP server API adapter with unified client support
func NewAdapter() (*Adapter, error) {
	musterClient, err := client.NewMusterClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create muster client: %w", err)
	}

	return &Adapter{
		client:    musterClient,
		namespace: "default", // TODO: Make configurable
	}, nil
}

// NewAdapterWithClient creates a new adapter with a specific client (for testing)
func NewAdapterWithClient(musterClient client.MusterClient, namespace string) *Adapter {
	if namespace == "" {
		namespace = "default"
	}
	return &Adapter{
		client:    musterClient,
		namespace: namespace,
	}
}

// Register registers the adapter with the API
func (a *Adapter) Register() {
	api.RegisterMCPServerManager(a)
}

// Close performs cleanup for the adapter
func (a *Adapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

// ListMCPServers returns all MCP server definitions
func (a *Adapter) ListMCPServers() []api.MCPServerInfo {
	ctx := context.Background()

	servers, err := a.client.ListMCPServers(ctx, a.namespace)
	if err != nil {
		// Log error and return empty list
		fmt.Printf("Warning: Failed to list MCPServers: %v\n", err)
		return []api.MCPServerInfo{}
	}

	result := make([]api.MCPServerInfo, len(servers))
	for i, server := range servers {
		result[i] = convertCRDToInfo(&server)
	}

	return result
}

// GetMCPServer returns information about a specific MCP server
func (a *Adapter) GetMCPServer(name string) (*api.MCPServerInfo, error) {
	ctx := context.Background()

	server, err := a.client.GetMCPServer(ctx, name, a.namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, api.NewMCPServerNotFoundError(name)
		}
		return nil, fmt.Errorf("failed to get MCPServer %s: %w", name, err)
	}

	info := convertCRDToInfo(server)
	return &info, nil
}

// convertCRDToInfo converts a MCPServer CRD to MCPServerInfo
func convertCRDToInfo(server *musterv1alpha1.MCPServer) api.MCPServerInfo {
	info := api.MCPServerInfo{
		Name:        server.ObjectMeta.Name,
		Type:        server.Spec.Type,
		Description: server.Spec.Description,
		ToolPrefix:  server.Spec.ToolPrefix,
		AutoStart:   server.Spec.AutoStart,
		Command:     server.Spec.Command,
		Args:        server.Spec.Args,
		URL:         server.Spec.URL,
		Env:         server.Spec.Env,
		Headers:     server.Spec.Headers,
		Timeout:     server.Spec.Timeout,
		Error:       server.Status.LastError,
	}

	return info
}

// convertRequestToCRD converts a request to a MCPServer CRD using the flat structure
func (a *Adapter) convertRequestToCRD(req *api.MCPServerCreateRequest) *musterv1alpha1.MCPServer {
	crd := &musterv1alpha1.MCPServer{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "muster.giantswarm.io/v1alpha1",
			Kind:       "MCPServer",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: a.namespace,
		},
		Spec: musterv1alpha1.MCPServerSpec{
			Type:        req.Type,
			ToolPrefix:  req.ToolPrefix,
			Description: req.Description,
			AutoStart:   req.AutoStart,
			Command:     req.Command,
			Args:        req.Args,
			URL:         req.URL,
			Env:         req.Env,
			Headers:     req.Headers,
			Timeout:     req.Timeout,
		},
	}

	return crd
}

// ToolProvider implementation

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	tools := []api.ToolMetadata{
		{
			Name:        "mcpserver_list",
			Description: "List all MCP server definitions with their status",
		},
		{
			Name:        "mcpserver_get",
			Description: "Get detailed information about a specific MCP server definition",
			Args: []api.ArgMetadata{
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
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "MCP server name"},
				{Name: "type", Type: "string", Required: true, Description: "MCP server type (local or remote)"},
				{Name: "toolPrefix", Type: "string", Required: false, Description: "Tool prefix for namespacing"},
				{
					Name:        "local",
					Type:        "object",
					Required:    false,
					Description: "Local MCP server configuration (for type=local)",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Local MCP server configuration",
						"properties": map[string]interface{}{
							"autoStart": map[string]interface{}{
								"type":        "boolean",
								"description": "Whether server should auto-start",
							},
							"command": map[string]interface{}{
								"type":        "array",
								"description": "Command and arguments",
								"items": map[string]interface{}{
									"type": "string",
								},
								"minItems": 1,
							},
							"env": map[string]interface{}{
								"type":        "object",
								"description": "Environment variables",
								"additionalProperties": map[string]interface{}{
									"type": "string",
								},
							},
						},
					},
				},
				{
					Name:        "remote",
					Type:        "object",
					Required:    false,
					Description: "Remote MCP server configuration (for type=remote)",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Remote MCP server configuration",
						"properties": map[string]interface{}{
							"endpoint": map[string]interface{}{
								"type":        "string",
								"description": "Remote server endpoint URL",
							},
							"transport": map[string]interface{}{
								"type":        "string",
								"description": "Transport protocol (http, sse)",
								"enum":        []string{"http", "sse"},
							},
							"timeout": map[string]interface{}{
								"type":        "integer",
								"description": "Connection timeout in seconds",
								"minimum":     1,
								"maximum":     300,
							},
						},
					},
				},
				{Name: "description", Type: "string", Required: false, Description: "MCP server description"},
			},
		},
		{
			Name:        "mcpserver_create",
			Description: "Create a new MCP server definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "MCP server name"},
				{Name: "type", Type: "string", Required: true, Description: "MCP server type (local or remote)"},
				{Name: "toolPrefix", Type: "string", Required: false, Description: "Tool prefix for namespacing"},
				{Name: "description", Type: "string", Required: false, Description: "MCP server description"},
				{
					Name:        "local",
					Type:        "object",
					Required:    false,
					Description: "Local MCP server configuration (for type=local)",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Local MCP server configuration",
						"properties": map[string]interface{}{
							"autoStart": map[string]interface{}{
								"type":        "boolean",
								"description": "Whether server should auto-start",
							},
							"command": map[string]interface{}{
								"type":        "array",
								"description": "Command and arguments",
								"items": map[string]interface{}{
									"type": "string",
								},
								"minItems": 1,
							},
							"env": map[string]interface{}{
								"type":        "object",
								"description": "Environment variables",
								"additionalProperties": map[string]interface{}{
									"type": "string",
								},
							},
						},
					},
				},
				{
					Name:        "remote",
					Type:        "object",
					Required:    false,
					Description: "Remote MCP server configuration (for type=remote)",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Remote MCP server configuration",
						"properties": map[string]interface{}{
							"endpoint": map[string]interface{}{
								"type":        "string",
								"description": "Remote server endpoint URL",
							},
							"transport": map[string]interface{}{
								"type":        "string",
								"description": "Transport protocol (http, sse)",
								"enum":        []string{"http", "sse"},
							},
							"timeout": map[string]interface{}{
								"type":        "integer",
								"description": "Connection timeout in seconds",
								"minimum":     1,
								"maximum":     300,
							},
						},
					},
				},
			},
		},
		{
			Name:        "mcpserver_update",
			Description: "Update an existing MCP server definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "MCP server name"},
				{Name: "type", Type: "string", Required: false, Description: "MCP server type (local or remote)"},
				{Name: "toolPrefix", Type: "string", Required: false, Description: "Tool prefix for namespacing"},
				{Name: "description", Type: "string", Required: false, Description: "MCP server description"},
				{
					Name:        "local",
					Type:        "object",
					Required:    false,
					Description: "Local MCP server configuration (for type=local)",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Local MCP server configuration",
						"properties": map[string]interface{}{
							"autoStart": map[string]interface{}{
								"type":        "boolean",
								"description": "Whether server should auto-start",
							},
							"command": map[string]interface{}{
								"type":        "array",
								"description": "Command and arguments",
								"items": map[string]interface{}{
									"type": "string",
								},
								"minItems": 1,
							},
							"env": map[string]interface{}{
								"type":        "object",
								"description": "Environment variables",
								"additionalProperties": map[string]interface{}{
									"type": "string",
								},
							},
						},
					},
				},
				{
					Name:        "remote",
					Type:        "object",
					Required:    false,
					Description: "Remote MCP server configuration (for type=remote)",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Remote MCP server configuration",
						"properties": map[string]interface{}{
							"endpoint": map[string]interface{}{
								"type":        "string",
								"description": "Remote server endpoint URL",
							},
							"transport": map[string]interface{}{
								"type":        "string",
								"description": "Transport protocol (http, sse)",
								"enum":        []string{"http", "sse"},
							},
							"timeout": map[string]interface{}{
								"type":        "integer",
								"description": "Connection timeout in seconds",
								"minimum":     1,
								"maximum":     300,
							},
						},
					},
				},
			},
		},
		{
			Name:        "mcpserver_delete",
			Description: "Delete an MCP server definition",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the MCP server to delete",
				},
			},
		},
	}
	return tools
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
		"mode":       getClientMode(a.client),
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
			Content: []interface{}{"name argument is required"},
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

	// Create MCPServer CRD for validation
	server := a.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name:        req.Name,
		Type:        req.Type,
		ToolPrefix:  req.ToolPrefix,
		Description: req.Description,
		AutoStart:   req.AutoStart,
		Command:     req.Command,
		Args:        req.Args,
		URL:         req.URL,
		Env:         req.Env,
		Headers:     req.Headers,
		Timeout:     req.Timeout,
	})

	// Basic validation (more comprehensive validation would be done by the CRD schema)
	if err := a.validateMCPServer(server); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Validation failed: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Validation successful for mcpserver %s", req.Name)},
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

	// Validate the definition
	if err := a.validateMCPServer(a.convertRequestToCRD(&req)); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Create the new MCP server using the unified client
	ctx := context.Background()
	if err := a.client.CreateMCPServer(ctx, a.convertRequestToCRD(&req)); err != nil {
		if errors.IsAlreadyExists(err) {
			return simpleError(fmt.Sprintf("MCP server '%s' already exists", req.Name))
		}
		return simpleError(fmt.Sprintf("Failed to create MCP server: %v", err))
	}

	return simpleOK(fmt.Sprintf("MCP server '%s' created successfully", req.Name))
}

func (a *Adapter) handleMCPServerUpdate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.MCPServerUpdateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Validate mixed configuration for proper nested structure
	if err := a.validateMCPServer(a.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name:        req.Name,
		Type:        req.Type,
		ToolPrefix:  req.ToolPrefix,
		Description: req.Description,
		AutoStart:   req.AutoStart,
		Command:     req.Command,
		Args:        req.Args,
		URL:         req.URL,
		Env:         req.Env,
		Headers:     req.Headers,
		Timeout:     req.Timeout,
	})); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Get existing server first
	ctx := context.Background()
	existing, err := a.client.GetMCPServer(ctx, req.Name, a.namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return api.HandleErrorWithPrefix(api.NewMCPServerNotFoundError(req.Name), "Failed to update MCP server"), nil
		}
		return simpleError(fmt.Sprintf("Failed to get existing MCP server: %v", err))
	}

	// Update common fields from request
	if req.Type != "" {
		existing.Spec.Type = req.Type
	}
	if req.ToolPrefix != "" {
		existing.Spec.ToolPrefix = req.ToolPrefix
	}
	if req.Description != "" {
		existing.Spec.Description = req.Description
	}
	existing.Spec.AutoStart = req.AutoStart
	if req.Command != "" {
		existing.Spec.Command = req.Command
	}
	if req.Args != nil {
		existing.Spec.Args = req.Args
	}
	if req.URL != "" {
		existing.Spec.URL = req.URL
	}
	if req.Env != nil {
		existing.Spec.Env = req.Env
	}
	if req.Headers != nil {
		existing.Spec.Headers = req.Headers
	}
	if req.Timeout > 0 {
		existing.Spec.Timeout = req.Timeout
	}

	// Validate the definition
	if err := a.validateMCPServer(existing); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Update the MCP server using the unified client
	if err := a.client.UpdateMCPServer(ctx, existing); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to update MCP server"), nil
	}

	return simpleOK(fmt.Sprintf("MCP server '%s' updated successfully", req.Name))
}

func (a *Adapter) handleMCPServerDelete(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return simpleError("name argument is required")
	}

	// Delete the MCP server using the unified client
	ctx := context.Background()
	if err := a.client.DeleteMCPServer(ctx, name, a.namespace); err != nil {
		if errors.IsNotFound(err) {
			return api.HandleErrorWithPrefix(api.NewMCPServerNotFoundError(name), "Failed to delete MCP server"), nil
		}
		return api.HandleErrorWithPrefix(err, "Failed to delete MCP server"), nil
	}

	return simpleOK(fmt.Sprintf("MCP server '%s' deleted successfully", name))
}

// validateMCPServer performs basic validation on an MCP server
func (a *Adapter) validateMCPServer(server *musterv1alpha1.MCPServer) error {
	if server.ObjectMeta.Name == "" {
		return fmt.Errorf("name is required")
	}

	if server.Spec.Type == "" {
		return fmt.Errorf("type is required")
	}

	switch server.Spec.Type {
	case "stdio":
		if server.Spec.Command == "" {
			return fmt.Errorf("command is required for stdio type")
		}
	case "streamable-http", "sse":
		if server.Spec.URL == "" {
			return fmt.Errorf("url is required for streamable-http and sse types")
		}
		if server.Spec.Timeout == 0 {
			return fmt.Errorf("timeout is required for remote types")
		}
	default:
		return fmt.Errorf("unsupported type: %s (supported: stdio, streamable-http, sse)", server.Spec.Type)
	}

	return nil
}

// helper to create simple error CallToolResult
func simpleError(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: true}, nil
}

func simpleOK(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: false}, nil
}

// getClientMode returns a string indicating whether we're in Kubernetes or filesystem mode
func getClientMode(client client.MusterClient) string {
	if client.IsKubernetesMode() {
		return "kubernetes"
	}
	return "filesystem"
}
