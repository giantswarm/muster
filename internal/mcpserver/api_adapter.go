package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"muster/internal/api"
	"muster/internal/client"
	"muster/internal/events"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
	"muster/pkg/logging"
)

// convertCRDSecretRefToAPI converts a CRD ClientCredentialsSecretRef to an API ClientCredentialsSecretRef.
// Returns nil if the input is nil.
func convertCRDSecretRefToAPI(src *musterv1alpha1.ClientCredentialsSecretRef) *api.ClientCredentialsSecretRef {
	if src == nil {
		return nil
	}
	return &api.ClientCredentialsSecretRef{
		Name:            src.Name,
		Namespace:       src.Namespace,
		ClientIDKey:     src.ClientIDKey,
		ClientSecretKey: src.ClientSecretKey,
	}
}

// convertAPISecretRefToCRD converts an API ClientCredentialsSecretRef to a CRD ClientCredentialsSecretRef.
// Returns nil if the input is nil.
func convertAPISecretRefToCRD(src *api.ClientCredentialsSecretRef) *musterv1alpha1.ClientCredentialsSecretRef {
	if src == nil {
		return nil
	}
	return &musterv1alpha1.ClientCredentialsSecretRef{
		Name:            src.Name,
		Namespace:       src.Namespace,
		ClientIDKey:     src.ClientIDKey,
		ClientSecretKey: src.ClientSecretKey,
	}
}

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
		logging.Warn("MCPServer", "Failed to list MCPServers: %v", err)
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
		Name:                server.ObjectMeta.Name,
		Type:                server.Spec.Type,
		Description:         server.Spec.Description,
		ToolPrefix:          server.Spec.ToolPrefix,
		AutoStart:           server.Spec.AutoStart,
		Command:             server.Spec.Command,
		Args:                server.Spec.Args,
		URL:                 server.Spec.URL,
		Env:                 server.Spec.Env,
		Headers:             server.Spec.Headers,
		Timeout:             server.Spec.Timeout,
		Error:               server.Status.LastError,
		State:               string(server.Status.State),
		ConsecutiveFailures: server.Status.ConsecutiveFailures,
	}

	// Convert time fields from metav1.Time to time.Time
	if server.Status.LastAttempt != nil {
		t := server.Status.LastAttempt.Time
		info.LastAttempt = &t
	}
	if server.Status.NextRetryAfter != nil {
		t := server.Status.NextRetryAfter.Time
		info.NextRetryAfter = &t
	}

	// Convert auth configuration if present
	if server.Spec.Auth != nil {
		info.Auth = &api.MCPServerAuth{
			Type:              server.Spec.Auth.Type,
			ForwardToken:      server.Spec.Auth.ForwardToken,
			RequiredAudiences: server.Spec.Auth.RequiredAudiences,
			FallbackToOwnAuth: server.Spec.Auth.FallbackToOwnAuth,
		}
		// Convert TokenExchange config if present
		if server.Spec.Auth.TokenExchange != nil {
			info.Auth.TokenExchange = &api.TokenExchangeConfig{
				Enabled:          server.Spec.Auth.TokenExchange.Enabled,
				DexTokenEndpoint: server.Spec.Auth.TokenExchange.DexTokenEndpoint,
				ExpectedIssuer:   server.Spec.Auth.TokenExchange.ExpectedIssuer,
				ConnectorID:      server.Spec.Auth.TokenExchange.ConnectorID,
				Scopes:           server.Spec.Auth.TokenExchange.Scopes,
			}
			info.Auth.TokenExchange.ClientCredentialsSecretRef = convertCRDSecretRefToAPI(
				server.Spec.Auth.TokenExchange.ClientCredentialsSecretRef,
			)
		}
		// Convert Teleport config if present
		if server.Spec.Auth.Teleport != nil {
			info.Auth.Teleport = &api.TeleportAuth{
				IdentityDir:             server.Spec.Auth.Teleport.IdentityDir,
				IdentitySecretName:      server.Spec.Auth.Teleport.IdentitySecretName,
				IdentitySecretNamespace: server.Spec.Auth.Teleport.IdentitySecretNamespace,
				AppName:                 server.Spec.Auth.Teleport.AppName,
			}
		}
	}

	// Generate user-friendly status message based on state and error
	info.StatusMessage = generateStatusMessage(info.State, info.Error, server.ObjectMeta.Name)

	return info
}

// generateStatusMessage creates a user-friendly, actionable status message
// based on the server's state and error information.
//
// State is infrastructure-only:
//   - Running/Connected: Infrastructure reachable (no message needed)
//   - Starting/Connecting: In progress
//   - Stopped/Disconnected: Not running (no message needed)
//   - Failed: Infrastructure unavailable (include error context)
func generateStatusMessage(state, errorMsg, serverName string) string {
	switch state {
	case "Running", "Connected":
		return ""
	case "Starting", "Connecting":
		return "Starting..."
	case "Stopped", "Disconnected":
		return ""
	case "Failed":
		return generateFailedMessage(errorMsg, serverName)
	default:
		return ""
	}
}

// generateFailedMessage creates a user-friendly message for failed servers
func generateFailedMessage(errorMsg, serverName string) string {
	if errorMsg == "" {
		return "Server failed to start"
	}

	lowerErr := strings.ToLower(errorMsg)

	// Check for specific error patterns and provide actionable messages
	switch {
	case strings.Contains(lowerErr, "certificate") || strings.Contains(lowerErr, "x509"):
		return "Certificate error - verify TLS configuration"
	case strings.Contains(lowerErr, "tls handshake"):
		return "TLS error - check server certificate and TLS configuration"
	case strings.Contains(lowerErr, "command not found") || strings.Contains(lowerErr, "executable file not found"):
		return "Command not found - check the executable path"
	case strings.Contains(lowerErr, "permission denied"):
		return "Permission denied - check file permissions"
	case strings.Contains(lowerErr, "401") || strings.Contains(lowerErr, "unauthorized"):
		return fmt.Sprintf("Authentication required - run: muster auth login --server %s", serverName)
	case strings.Contains(lowerErr, "403") || strings.Contains(lowerErr, "forbidden"):
		return "Access forbidden - check server permissions and credentials"
	case strings.Contains(lowerErr, "connection reset") || strings.Contains(lowerErr, "econnreset"):
		return "Connection was reset by server - check server logs"
	case strings.Contains(lowerErr, "protocol") || strings.Contains(lowerErr, "unsupported"):
		return "Protocol error - check server type configuration"
	case strings.Contains(lowerErr, "json") || strings.Contains(lowerErr, "parse"):
		return "Invalid response from server - check server compatibility"
	default:
		return "Server failed to start"
	}
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

	// Convert auth configuration if present
	if req.Auth != nil {
		crd.Spec.Auth = &musterv1alpha1.MCPServerAuth{
			Type:              req.Auth.Type,
			ForwardToken:      req.Auth.ForwardToken,
			RequiredAudiences: req.Auth.RequiredAudiences,
			FallbackToOwnAuth: req.Auth.FallbackToOwnAuth,
		}

		// Convert TokenExchange if present
		if req.Auth.TokenExchange != nil {
			crd.Spec.Auth.TokenExchange = &musterv1alpha1.TokenExchangeConfig{
				Enabled:                    req.Auth.TokenExchange.Enabled,
				DexTokenEndpoint:           req.Auth.TokenExchange.DexTokenEndpoint,
				ExpectedIssuer:             req.Auth.TokenExchange.ExpectedIssuer,
				ConnectorID:                req.Auth.TokenExchange.ConnectorID,
				Scopes:                     req.Auth.TokenExchange.Scopes,
				ClientCredentialsSecretRef: convertAPISecretRefToCRD(req.Auth.TokenExchange.ClientCredentialsSecretRef),
			}
		}

		// Convert Teleport auth if present
		if req.Auth.Teleport != nil {
			crd.Spec.Auth.Teleport = &musterv1alpha1.TeleportAuthConfig{
				IdentityDir:             req.Auth.Teleport.IdentityDir,
				IdentitySecretName:      req.Auth.Teleport.IdentitySecretName,
				IdentitySecretNamespace: req.Auth.Teleport.IdentitySecretNamespace,
				AppName:                 req.Auth.Teleport.AppName,
			}
		}
	}

	return crd
}

// ToolProvider implementation

// mcpServerArgs returns the common argument metadata for MCP server tools.
// typeRequired controls whether the "type" field is required (true for create/validate, false for update).
func mcpServerArgs(typeRequired bool) []api.ArgMetadata {
	return []api.ArgMetadata{
		{Name: "name", Type: "string", Required: true, Description: "MCP server name"},
		{Name: "type", Type: "string", Required: typeRequired, Description: "MCP server type (stdio, streamable-http, or sse)"},
		{Name: "toolPrefix", Type: "string", Required: false, Description: "Tool prefix for namespacing"},
		{Name: "description", Type: "string", Required: false, Description: "MCP server description"},
		{Name: "autoStart", Type: "boolean", Required: false, Description: "Whether server should auto-start"},
		{Name: "command", Type: "string", Required: false, Description: "Command executable path (required for stdio)"},
		{Name: "args", Type: "array", Required: false, Description: "Command arguments (stdio only)", Schema: map[string]interface{}{
			"type":        "array",
			"items":       map[string]interface{}{"type": "string"},
			"description": "Command line arguments for stdio servers",
		}},
		{Name: "url", Type: "string", Required: false, Description: "Server endpoint URL (required for streamable-http and sse)"},
		{Name: "env", Type: "object", Required: false, Description: "Environment variables", Schema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": map[string]interface{}{"type": "string"},
			"description":          "Environment variables for the server",
		}},
		{Name: "headers", Type: "object", Required: false, Description: "HTTP headers (streamable-http and sse only)", Schema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": map[string]interface{}{"type": "string"},
			"description":          "HTTP headers for remote servers",
		}},
		{Name: "timeout", Type: "integer", Required: false, Description: "Connection timeout in seconds"},
		{Name: "auth", Type: "object", Required: false, Description: "Authentication configuration for remote servers", Schema: map[string]interface{}{
			"type":        "object",
			"description": "Authentication configuration (oauth, teleport, or none)",
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Authentication type: oauth, teleport, or none",
					"enum":        []string{"oauth", "teleport", "none"},
				},
				"forwardToken": map[string]interface{}{
					"type":        "boolean",
					"description": "Enable SSO token forwarding (oauth only)",
				},
				"requiredAudiences": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Additional audiences to request from IdP for token forwarding (e.g., dex-k8s-authenticator for Kubernetes OIDC)",
				},
				"fallbackToOwnAuth": map[string]interface{}{
					"type":        "boolean",
					"description": "Fall back to server-specific auth on failure",
				},
				"teleport": map[string]interface{}{
					"type":        "object",
					"description": "Teleport authentication configuration",
					"properties": map[string]interface{}{
						"identityDir": map[string]interface{}{
							"type":        "string",
							"description": "Filesystem path to tbot identity files",
						},
						"identitySecretName": map[string]interface{}{
							"type":        "string",
							"description": "Kubernetes Secret name containing tbot identity files",
						},
						"identitySecretNamespace": map[string]interface{}{
							"type":        "string",
							"description": "Kubernetes namespace of the identity secret",
						},
						"appName": map[string]interface{}{
							"type":        "string",
							"description": "Teleport application name for routing",
						},
					},
				},
			},
		}},
	}
}

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "mcpserver_list",
			Description: "List all MCP server definitions with their status. By default, unreachable servers are hidden.",
			Args: []api.ArgMetadata{
				{Name: "showAll", Type: "boolean", Required: false, Description: "Show all servers including unreachable ones (default: false)"},
				{Name: "verbose", Type: "boolean", Required: false, Description: "Show detailed error information for failed/unreachable servers (default: false)"},
			},
		},
		{
			Name:        "mcpserver_get",
			Description: "Get detailed information about a specific MCP server definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Name of the MCP server to retrieve"},
			},
		},
		{
			Name:        "mcpserver_validate",
			Description: "Validate an mcpserver definition",
			Args:        mcpServerArgs(true), // type is required for validation
		},
		{
			Name:        "mcpserver_create",
			Description: "Create a new MCP server definition",
			Args:        mcpServerArgs(true), // type is required for creation
		},
		{
			Name:        "mcpserver_update",
			Description: "Update an existing MCP server definition",
			Args:        mcpServerArgs(false), // type is optional for update
		},
		{
			Name:        "mcpserver_delete",
			Description: "Delete an MCP server definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Name of the MCP server to delete"},
			},
		},
	}
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "mcpserver_list":
		return a.handleMCPServerList(args)
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

func (a *Adapter) handleMCPServerList(args map[string]interface{}) (*api.CallToolResult, error) {
	allServers := a.ListMCPServers()

	// Check showAll parameter (default: false)
	showAll := false
	if val, ok := args["showAll"].(bool); ok {
		showAll = val
	}

	// Check verbose parameter (default: false)
	verbose := false
	if val, ok := args["verbose"].(bool); ok {
		verbose = val
	}

	// Filter out failed servers unless showAll is true
	// Per issue #292, Failed phase indicates infrastructure unavailable
	var filteredServers []api.MCPServerInfo
	failedCount := 0
	for _, server := range allServers {
		// Adjust server for display (hide raw errors in non-verbose mode)
		server = adjustServerForDisplay(server, verbose)

		if server.State == "Failed" {
			failedCount++
			if showAll {
				filteredServers = append(filteredServers, server)
			}
		} else {
			filteredServers = append(filteredServers, server)
		}
	}

	result := map[string]interface{}{
		"mcpServers": filteredServers,
		"total":      len(filteredServers),
		"mode":       getClientMode(a.client),
	}

	// Add failed count if any servers were hidden
	if failedCount > 0 && !showAll {
		result["hiddenFailed"] = failedCount
		result["hint"] = fmt.Sprintf("(%d failed servers hidden, use --all to show, --verbose for details)", failedCount)
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

// adjustServerForDisplay adjusts server fields for user-friendly display.
// In non-verbose mode, hide raw error messages (statusMessage provides user-friendly version).
func adjustServerForDisplay(server api.MCPServerInfo, verbose bool) api.MCPServerInfo {
	// If not verbose, don't include the raw error message.
	// The statusMessage field provides a user-friendly version.
	if !verbose {
		server.Error = ""
	}

	return server
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
		Auth:        req.Auth,
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

	// Convert request to CRD once for reuse
	serverCRD := a.convertRequestToCRD(&req)

	// Validate the definition
	if err := a.validateMCPServer(serverCRD); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Create the new MCP server using the unified client
	ctx := context.Background()
	if err := a.client.CreateMCPServer(ctx, serverCRD); err != nil {
		if errors.IsAlreadyExists(err) {
			return simpleError(fmt.Sprintf("MCP server '%s' already exists", req.Name))
		}
		// Generate failure event
		a.generateCRDEvent(req.Name, events.ReasonMCPServerFailed, events.EventData{
			Error:     err.Error(),
			Operation: "create",
		})
		return simpleError(fmt.Sprintf("Failed to create MCP server: %v", err))
	}

	// Generate success event for CRD creation
	a.generateCRDEvent(req.Name, events.ReasonMCPServerCreated, events.EventData{
		Operation: "create",
	})

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
	// Update auth configuration if provided
	if req.Auth != nil {
		existing.Spec.Auth = &musterv1alpha1.MCPServerAuth{
			Type:              req.Auth.Type,
			ForwardToken:      req.Auth.ForwardToken,
			RequiredAudiences: req.Auth.RequiredAudiences,
			FallbackToOwnAuth: req.Auth.FallbackToOwnAuth,
		}
		if req.Auth.TokenExchange != nil {
			existing.Spec.Auth.TokenExchange = &musterv1alpha1.TokenExchangeConfig{
				Enabled:                    req.Auth.TokenExchange.Enabled,
				DexTokenEndpoint:           req.Auth.TokenExchange.DexTokenEndpoint,
				ExpectedIssuer:             req.Auth.TokenExchange.ExpectedIssuer,
				ConnectorID:                req.Auth.TokenExchange.ConnectorID,
				Scopes:                     req.Auth.TokenExchange.Scopes,
				ClientCredentialsSecretRef: convertAPISecretRefToCRD(req.Auth.TokenExchange.ClientCredentialsSecretRef),
			}
		}
		if req.Auth.Teleport != nil {
			existing.Spec.Auth.Teleport = &musterv1alpha1.TeleportAuthConfig{
				IdentityDir:             req.Auth.Teleport.IdentityDir,
				IdentitySecretName:      req.Auth.Teleport.IdentitySecretName,
				IdentitySecretNamespace: req.Auth.Teleport.IdentitySecretNamespace,
				AppName:                 req.Auth.Teleport.AppName,
			}
		}
	}

	// Validate the updated definition (reuse existing CRD object)
	if err := a.validateMCPServer(existing); err != nil {
		return simpleError(fmt.Sprintf("Invalid MCP server definition: %v", err))
	}

	// Update the MCP server using the unified client
	if err := a.client.UpdateMCPServer(ctx, existing); err != nil {
		// Generate failure event
		a.generateCRDEvent(req.Name, events.ReasonMCPServerFailed, events.EventData{
			Error:     err.Error(),
			Operation: "update",
		})
		return api.HandleErrorWithPrefix(err, "Failed to update MCP server"), nil
	}

	// Generate success event for CRD update
	a.generateCRDEvent(req.Name, events.ReasonMCPServerUpdated, events.EventData{
		Operation: "update",
	})

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
		// Generate failure event
		a.generateCRDEvent(name, events.ReasonMCPServerFailed, events.EventData{
			Error:     err.Error(),
			Operation: "delete",
		})
		return api.HandleErrorWithPrefix(err, "Failed to delete MCP server"), nil
	}

	// Generate success event for CRD deletion
	a.generateCRDEvent(name, events.ReasonMCPServerDeleted, events.EventData{
		Operation: "delete",
	})

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
	case string(api.MCPServerTypeStdio):
		if server.Spec.Command == "" {
			return fmt.Errorf("command is required for stdio type")
		}
		// Auth is not supported for stdio servers
		if server.Spec.Auth != nil && server.Spec.Auth.Type != "" && server.Spec.Auth.Type != "none" {
			return fmt.Errorf("auth configuration is only supported for remote server types (streamable-http or sse)")
		}
	case string(api.MCPServerTypeStreamableHTTP), string(api.MCPServerTypeSSE):
		if server.Spec.URL == "" {
			return fmt.Errorf("url is required for streamable-http and sse types")
		}
		// Note: timeout defaults to 30 seconds via CRD kubebuilder:default
	default:
		return fmt.Errorf("unsupported MCP server type: %s (supported: %s, %s, %s)",
			server.Spec.Type, api.MCPServerTypeStdio, api.MCPServerTypeStreamableHTTP, api.MCPServerTypeSSE)
	}

	// Validate Teleport auth configuration if present
	if server.Spec.Auth != nil && server.Spec.Auth.Type == api.AuthTypeTeleport {
		if err := a.validateTeleportAuth(server.Spec.Auth.Teleport); err != nil {
			return err
		}
	}

	return nil
}

// validateTeleportAuth validates Teleport authentication configuration
func (a *Adapter) validateTeleportAuth(teleport *musterv1alpha1.TeleportAuthConfig) error {
	if teleport == nil {
		return fmt.Errorf("teleport auth requires either identityDir or identitySecretName")
	}

	// Validate mutual exclusivity
	if teleport.IdentityDir != "" && teleport.IdentitySecretName != "" {
		return fmt.Errorf("teleport auth: identityDir and identitySecretName are mutually exclusive")
	}

	// Require at least one identity source
	if teleport.IdentityDir == "" && teleport.IdentitySecretName == "" {
		return fmt.Errorf("teleport auth requires either identityDir or identitySecretName")
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

// generateCRDEvent creates a Kubernetes event for MCPServer CRD operations
func (a *Adapter) generateCRDEvent(name string, reason events.EventReason, data events.EventData) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		// Event manager not available, skip event generation
		return
	}

	// Create an object reference for the MCPServer CRD
	objectRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      name,
		Namespace: a.namespace,
	}

	// Populate event data
	data.Name = name
	if data.Namespace == "" {
		data.Namespace = a.namespace
	}

	err := eventManager.CreateEvent(context.Background(), objectRef, string(reason), "", string(events.EventTypeNormal))
	if err != nil {
		// Log error but don't fail the operation
		logging.Debug("MCPServer", "Failed to generate event %s for MCPServer %s: %v", string(reason), name, err)
	} else {
		logging.Debug("MCPServer", "Generated event %s for MCPServer %s", string(reason), name)
	}
}
