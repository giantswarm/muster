package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"muster/pkg/logging"
	pkgoauth "muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// itemType represents the type of MCP item (tool, prompt, or resource)
type itemType string

const (
	itemTypeTool     itemType = "tool"
	itemTypePrompt   itemType = "prompt"
	itemTypeResource itemType = "resource"
)

// activeItemManager manages the active state of MCP items (tools, prompts, resources)
type activeItemManager struct {
	mu       sync.RWMutex
	items    map[string]bool
	itemType itemType
}

// newActiveItemManager creates a new active item manager
func newActiveItemManager(iType itemType) *activeItemManager {
	return &activeItemManager{
		items:    make(map[string]bool),
		itemType: iType,
	}
}

// isActive checks if an item is active
func (m *activeItemManager) isActive(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.items[name]
}

// setActive marks an item as active
func (m *activeItemManager) setActive(name string, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active {
		m.items[name] = true
	} else {
		delete(m.items, name)
	}
}

// getInactiveItems returns items that are no longer in the new set
func (m *activeItemManager) getInactiveItems(newItems map[string]struct{}) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var inactive []string
	for name := range m.items {
		if _, exists := newItems[name]; !exists {
			inactive = append(inactive, name)
		}
	}
	return inactive
}

// removeItems removes the specified items from the active set
func (m *activeItemManager) removeItems(items []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range items {
		delete(m.items, item)
	}
}

// collectResult holds the results of collecting items from servers
type collectResult struct {
	newTools     map[string]struct{}
	newPrompts   map[string]struct{}
	newResources map[string]struct{}
}

// collectItemsFromServers collects all items from connected servers and auth_required servers
func collectItemsFromServers(servers map[string]*ServerInfo, registry *ServerRegistry) *collectResult {
	result := &collectResult{
		newTools:     make(map[string]struct{}),
		newPrompts:   make(map[string]struct{}),
		newResources: make(map[string]struct{}),
	}

	for serverName, info := range servers {
		// Handle servers requiring authentication - collect synthetic auth tools
		if info.Status == StatusAuthRequired {
			info.mu.RLock()
			for _, tool := range info.Tools {
				exposedName := registry.nameTracker.GetExposedToolName(serverName, tool.Name)
				result.newTools[exposedName] = struct{}{}
			}
			info.mu.RUnlock()
			continue
		}

		if !info.IsConnected() {
			continue
		}

		info.mu.RLock()
		// Collect tools
		for _, tool := range info.Tools {
			exposedName := registry.nameTracker.GetExposedToolName(serverName, tool.Name)
			result.newTools[exposedName] = struct{}{}
		}
		// Collect prompts
		for _, prompt := range info.Prompts {
			exposedName := registry.nameTracker.GetExposedPromptName(serverName, prompt.Name)
			result.newPrompts[exposedName] = struct{}{}
		}
		// Collect resources
		for _, resource := range info.Resources {
			exposedURI := registry.nameTracker.GetExposedResourceURI(serverName, resource.URI)
			result.newResources[exposedURI] = struct{}{}
		}
		info.mu.RUnlock()
	}

	return result
}

// collectItemsFromServersAndProviders collects all items from connected servers AND core providers
func collectItemsFromServersAndProviders(servers map[string]*ServerInfo, registry *ServerRegistry, a *AggregatorServer) *collectResult {
	// Start with regular server items
	result := collectItemsFromServers(servers, registry)

	// Add core tools from providers (workflow, orchestrator, config, mcp)
	// These are the tools that get added by createToolsFromProviders()
	coreTools := a.createToolsFromProviders()
	for _, tool := range coreTools {
		result.newTools[tool.Tool.Name] = struct{}{}
	}

	return result
}

// removeObsoleteItems is a generic function to remove items that no longer exist
func removeObsoleteItems(
	manager *activeItemManager,
	newItems map[string]struct{},
	removeFunc func(items []string),
) {
	itemsToRemove := manager.getInactiveItems(newItems)

	if len(itemsToRemove) > 0 {
		logging.Debug("Aggregator", "Removing %d %ss: %v", len(itemsToRemove), manager.itemType, itemsToRemove)
		removeFunc(itemsToRemove)
		manager.removeItems(itemsToRemove)
	}
}

// itemInfo represents a generic MCP item (tool, prompt, or resource)
type itemInfo struct {
	itemType     itemType
	name         string      // Name for tools/prompts, URI for resources
	originalItem interface{} // The original mcp.Tool, mcp.Prompt, or mcp.Resource
}

// getItemIdentifier returns the identifier for an item (name or URI)
func getItemIdentifier(item interface{}) string {
	switch v := item.(type) {
	case mcp.Tool:
		return v.Name
	case mcp.Prompt:
		return v.Name
	case mcp.Resource:
		return v.URI
	default:
		return ""
	}
}

// processItemsGeneric is a generic function to process any type of MCP item
func processItemsGeneric[T any](
	a *AggregatorServer,
	serverName string,
	items []T,
	iType itemType,
	manager *activeItemManager,
	getExposedName func(string, string) string,
	createHandler func(*AggregatorServer, string) interface{},
) []interface{} {
	var itemsToAdd []interface{}

	for _, item := range items {
		// Get the item identifier
		originalID := getItemIdentifier(item)
		if originalID == "" {
			continue
		}

		// Get exposed name/URI
		exposedID := getExposedName(serverName, originalID)

		// Check if already active
		if manager.isActive(exposedID) {
			continue
		}

		// Mark as active
		manager.setActive(exposedID, true)

		// Create exposed item with updated identifier
		var exposedItem interface{}
		switch v := any(item).(type) {
		case mcp.Tool:
			tool := v
			tool.Name = exposedID
			exposedItem = tool
		case mcp.Prompt:
			prompt := v
			prompt.Name = exposedID
			exposedItem = prompt
		case mcp.Resource:
			resource := v
			resource.URI = exposedID
			exposedItem = resource
		}

		// Create handler
		handler := createHandler(a, exposedID)

		// Add to batch based on type
		switch iType {
		case itemTypeTool:
			itemsToAdd = append(itemsToAdd, mcpserver.ServerTool{
				Tool:    exposedItem.(mcp.Tool),
				Handler: handler.(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)),
			})
		case itemTypePrompt:
			itemsToAdd = append(itemsToAdd, mcpserver.ServerPrompt{
				Prompt:  exposedItem.(mcp.Prompt),
				Handler: handler.(func(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error)),
			})
		case itemTypeResource:
			itemsToAdd = append(itemsToAdd, mcpserver.ServerResource{
				Resource: exposedItem.(mcp.Resource),
				Handler:  handler.(func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error)),
			})
		}
	}

	return itemsToAdd
}

// toolHandlerFactory creates a handler for a tool
func toolHandlerFactory(a *AggregatorServer, exposedName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Note: Per ADR-008, synthetic auth tools are no longer used.
		// Users must use core_auth_login to authenticate to OAuth-protected servers.

		// Check if this tool is still active
		if !a.toolManager.isActive(exposedName) {
			return nil, fmt.Errorf("tool '%s' is no longer available", exposedName)
		}

		// Resolve the exposed name back to server and original tool name
		sName, originalName, err := a.registry.ResolveToolName(exposedName)
		if err != nil {
			// If not found in registry, check session connections for OAuth-protected servers
			sessionID := getSessionIDFromContext(ctx)
			serverName, client, origName, resolveErr := a.resolveSessionTool(sessionID, exposedName)
			if resolveErr != nil {
				return nil, fmt.Errorf("failed to resolve tool name: %w", err)
			}
			// Found in session connection - call through the session-specific client
			args := make(map[string]interface{})
			if req.Params.Arguments != nil {
				if argsMap, ok := req.Params.Arguments.(map[string]interface{}); ok {
					args = argsMap
				}
			}
			result, callErr := client.CallTool(ctx, origName, args)
			if callErr != nil {
				// Check if this is a 401 error - token may have expired and refresh failed
				if pkgoauth.Is401Error(callErr) {
					logging.Warn("Aggregator", "Tool call to %s got 401 for session %s - token expired/refresh failed",
						serverName, logging.TruncateSessionID(sessionID))
					return nil, fmt.Errorf("authentication to %s expired - please re-authenticate and try again", serverName)
				}
				return nil, fmt.Errorf("tool execution failed: %w", callErr)
			}
			return result, nil
		}

		// Check if tool is destructive and yolo mode is not enabled
		if !a.config.Yolo && isDestructiveTool(originalName) {
			logging.Warn("Aggregator", "Blocked destructive tool call: %s (enable --yolo flag to allow)", originalName)
			return nil, fmt.Errorf("tool '%s' is blocked as it is destructive. Use --yolo flag to allow destructive operations", originalName)
		}

		// Check if this server is OAuth-protected and needs session-specific client
		serverInfo, exists := a.registry.GetServerInfo(sName)
		if exists && serverInfo.Status == StatusAuthRequired {
			// This is an OAuth-protected server - get the client from session connection
			sessionID := getSessionIDFromContext(ctx)
			session := a.sessionRegistry.GetOrCreateSession(sessionID)
			conn, hasConn := session.GetConnection(sName)
			if !hasConn || conn.Client == nil {
				return nil, fmt.Errorf("not authenticated to server %s", sName)
			}

			// Forward the request using the session-specific client
			// The client uses DynamicAuthClient which automatically refreshes tokens
			args := make(map[string]interface{})
			if req.Params.Arguments != nil {
				if argsMap, ok := req.Params.Arguments.(map[string]interface{}); ok {
					args = argsMap
				}
			}

			result, err := conn.Client.CallTool(ctx, originalName, args)
			if err != nil {
				// Check if this is a 401 error - token may have expired and refresh failed
				if pkgoauth.Is401Error(err) {
					logging.Warn("Aggregator", "Tool call to %s got 401 for session %s - token expired/refresh failed",
						sName, logging.TruncateSessionID(sessionID))
					return nil, fmt.Errorf("authentication to %s expired - please re-authenticate and try again", sName)
				}
				return nil, fmt.Errorf("tool execution failed: %w", err)
			}
			return result, nil
		}

		// Get the backend client from the global registry (for non-OAuth servers)
		client, err := a.registry.GetClient(sName)
		if err != nil {
			return nil, fmt.Errorf("server not available: %w", err)
		}

		// Forward the request with the original tool name
		args := make(map[string]interface{})
		if req.Params.Arguments != nil {
			if argsMap, ok := req.Params.Arguments.(map[string]interface{}); ok {
				args = argsMap
			}
		}

		result, err := client.CallTool(ctx, originalName, args)
		if err != nil {
			return nil, fmt.Errorf("tool execution failed: %w", err)
		}

		return result, nil
	}
}

// promptHandlerFactory creates a handler for a prompt
func promptHandlerFactory(a *AggregatorServer, exposedName string) func(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		// Check if this prompt is still active
		if !a.promptManager.isActive(exposedName) {
			return nil, fmt.Errorf("prompt %s is no longer available", exposedName)
		}

		// Resolve the exposed name back to server and original prompt name
		sName, originalName, err := a.registry.ResolvePromptName(exposedName)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve prompt name: %w", err)
		}

		// Get the backend client
		client, err := a.registry.GetClient(sName)
		if err != nil {
			return nil, fmt.Errorf("server not available: %w", err)
		}

		// Forward the request with the original prompt name
		args := make(map[string]interface{})
		if req.Params.Arguments != nil {
			for k, v := range req.Params.Arguments {
				args[k] = v
			}
		}

		result, err := client.GetPrompt(ctx, originalName, args)
		if err != nil {
			return nil, fmt.Errorf("prompt retrieval failed: %w", err)
		}

		return result, nil
	}
}

// resourceHandlerFactory creates a handler for a resource
func resourceHandlerFactory(a *AggregatorServer, exposedURI string) func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		// Check if this resource is still active
		if !a.resourceManager.isActive(exposedURI) {
			return nil, fmt.Errorf("resource %s is no longer available", exposedURI)
		}

		// Resolve the exposed URI back to server and original URI
		sName, originalURI, err := a.registry.ResolveResourceName(exposedURI)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve resource URI: %w", err)
		}

		// Get the backend client
		client, err := a.registry.GetClient(sName)
		if err != nil {
			return nil, fmt.Errorf("server not available: %w", err)
		}

		// Forward the request with the original URI
		result, err := client.ReadResource(ctx, originalURI)
		if err != nil {
			return nil, fmt.Errorf("resource read failed: %w", err)
		}

		var contents []mcp.ResourceContents
		if result != nil && len(result.Contents) > 0 {
			contents = result.Contents
		}
		return contents, nil
	}
}

// processToolsForServer processes all tools from a server and returns handlers to add
func processToolsForServer(a *AggregatorServer, serverName string, info *ServerInfo) []mcpserver.ServerTool {
	info.mu.RLock()
	defer info.mu.RUnlock()

	results := processItemsGeneric(
		a,
		serverName,
		info.Tools,
		itemTypeTool,
		a.toolManager,
		a.registry.nameTracker.GetExposedToolName,
		func(agg *AggregatorServer, exposedName string) interface{} {
			return toolHandlerFactory(agg, exposedName)
		},
	)

	// Convert results to []mcpserver.ServerTool
	var toolsToAdd []mcpserver.ServerTool
	for _, item := range results {
		if tool, ok := item.(mcpserver.ServerTool); ok {
			toolsToAdd = append(toolsToAdd, tool)
		}
	}
	return toolsToAdd
}

// processPromptsForServer processes all prompts from a server and returns handlers to add
func processPromptsForServer(a *AggregatorServer, serverName string, info *ServerInfo) []mcpserver.ServerPrompt {
	info.mu.RLock()
	defer info.mu.RUnlock()

	results := processItemsGeneric(
		a,
		serverName,
		info.Prompts,
		itemTypePrompt,
		a.promptManager,
		a.registry.nameTracker.GetExposedPromptName,
		func(agg *AggregatorServer, exposedName string) interface{} {
			return promptHandlerFactory(agg, exposedName)
		},
	)

	// Convert results to []mcpserver.ServerPrompt
	var promptsToAdd []mcpserver.ServerPrompt
	for _, item := range results {
		if prompt, ok := item.(mcpserver.ServerPrompt); ok {
			promptsToAdd = append(promptsToAdd, prompt)
		}
	}
	return promptsToAdd
}

// processResourcesForServer processes all resources from a server and returns handlers to add
func processResourcesForServer(a *AggregatorServer, serverName string, info *ServerInfo) []mcpserver.ServerResource {
	info.mu.RLock()
	defer info.mu.RUnlock()

	results := processItemsGeneric(
		a,
		serverName,
		info.Resources,
		itemTypeResource,
		a.resourceManager,
		a.registry.nameTracker.GetExposedResourceURI,
		func(agg *AggregatorServer, exposedURI string) interface{} {
			return resourceHandlerFactory(agg, exposedURI)
		},
	)

	// Convert results to []mcpserver.ServerResource
	var resourcesToAdd []mcpserver.ServerResource
	for _, item := range results {
		if resource, ok := item.(mcpserver.ServerResource); ok {
			resourcesToAdd = append(resourcesToAdd, resource)
		}
	}
	return resourcesToAdd
}

// enrichMCPServerWithSessionData adds session-specific state to an MCPServerInfo.
// This includes the session's connection status, authentication status, and tools count.
//
// Args:
//   - serverInfo: The MCPServerInfo map from the mcpserver_list response
//   - session: The session state (may be nil if no session)
//
// Returns the enriched serverInfo map with session fields added.
func enrichMCPServerWithSessionData(serverInfo map[string]interface{}, session *SessionState) map[string]interface{} {
	if session == nil {
		return serverInfo
	}

	serverName, ok := serverInfo["name"].(string)
	if !ok || serverName == "" {
		return serverInfo
	}

	conn, hasConn := session.GetConnection(serverName)
	if !hasConn || conn == nil {
		// No session connection - leave session fields empty
		return serverInfo
	}

	// Add session status
	serverInfo["sessionStatus"] = string(conn.Status)

	// Add auth status (only for non-empty values)
	if conn.AuthStatus != "" && conn.AuthStatus != AuthStatusUnknown {
		serverInfo["sessionAuth"] = string(conn.AuthStatus)
	}

	// Add connected timestamp if available
	if !conn.ConnectedAt.IsZero() {
		serverInfo["connectedAt"] = conn.ConnectedAt
	}

	// Add tools count from session connection
	tools := conn.GetTools()
	if len(tools) > 0 {
		serverInfo["toolsCount"] = len(tools)
	}

	return serverInfo
}

// enrichMCPServerListResponse enriches the mcpserver_list response with session-specific data.
// It modifies the response in place to add sessionStatus, sessionAuth, toolsCount, and connectedAt
// fields to each server based on the current session's connection state.
//
// Args:
//   - result: The mcp.CallToolResult from mcpserver_list
//   - session: The session state (may be nil if no session)
//
// Returns the enriched result.
func enrichMCPServerListResponse(result *mcp.CallToolResult, session *SessionState) *mcp.CallToolResult {
	if session == nil || result == nil || len(result.Content) == 0 {
		return result
	}

	// The mcpserver_list response has Content containing a map with "mcpServers" array
	for i, content := range result.Content {
		textContent, ok := content.(mcp.TextContent)
		if !ok {
			continue
		}

		// Parse the JSON content
		var responseMap map[string]interface{}
		if err := json.Unmarshal([]byte(textContent.Text), &responseMap); err != nil {
			logging.Debug("ServerHelpers", "Failed to parse mcpserver_list response: %v", err)
			continue
		}

		// Find the mcpServers array
		servers, ok := responseMap["mcpServers"].([]interface{})
		if !ok {
			continue
		}

		// Enrich each server with session data
		for j, server := range servers {
			serverMap, ok := server.(map[string]interface{})
			if !ok {
				continue
			}
			servers[j] = enrichMCPServerWithSessionData(serverMap, session)
		}
		responseMap["mcpServers"] = servers

		// Re-serialize and update the content
		enrichedJSON, err := json.Marshal(responseMap)
		if err != nil {
			logging.Debug("ServerHelpers", "Failed to serialize enriched response: %v", err)
			continue
		}

		result.Content[i] = mcp.TextContent{
			Type: "text",
			Text: string(enrichedJSON),
		}
	}

	return result
}
