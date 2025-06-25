package aggregator

import (
	"context"
	"fmt"
	"sync"

	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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

// collectItemsFromServers collects all items from connected servers
func collectItemsFromServers(servers map[string]*ServerInfo, registry *ServerRegistry) *collectResult {
	result := &collectResult{
		newTools:     make(map[string]struct{}),
		newPrompts:   make(map[string]struct{}),
		newResources: make(map[string]struct{}),
	}

	for serverName, info := range servers {
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

	// Add core tools from providers (workflow, capability, orchestrator, config, mcp)
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
			itemsToAdd = append(itemsToAdd, server.ServerTool{
				Tool:    exposedItem.(mcp.Tool),
				Handler: handler.(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)),
			})
		case itemTypePrompt:
			itemsToAdd = append(itemsToAdd, server.ServerPrompt{
				Prompt:  exposedItem.(mcp.Prompt),
				Handler: handler.(func(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error)),
			})
		case itemTypeResource:
			itemsToAdd = append(itemsToAdd, server.ServerResource{
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
		// Check if this tool is still active
		if !a.toolManager.isActive(exposedName) {
			return nil, fmt.Errorf("tool %s is no longer available", exposedName)
		}

		// Resolve the exposed name back to server and original tool name
		sName, originalName, err := a.registry.ResolveToolName(exposedName)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve tool name: %w", err)
		}

		// Check if tool is destructive and yolo mode is not enabled
		if !a.config.Yolo && isDestructiveTool(originalName) {
			logging.Warn("Aggregator", "Blocked destructive tool call: %s (enable --yolo flag to allow)", originalName)
			return nil, fmt.Errorf("tool '%s' is blocked as it is destructive. Use --yolo flag to allow destructive operations", originalName)
		}

		// Get the backend client
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
func processToolsForServer(a *AggregatorServer, serverName string, info *ServerInfo) []server.ServerTool {
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

	// Convert results to []server.ServerTool
	var toolsToAdd []server.ServerTool
	for _, item := range results {
		if tool, ok := item.(server.ServerTool); ok {
			toolsToAdd = append(toolsToAdd, tool)
		}
	}
	return toolsToAdd
}

// processPromptsForServer processes all prompts from a server and returns handlers to add
func processPromptsForServer(a *AggregatorServer, serverName string, info *ServerInfo) []server.ServerPrompt {
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

	// Convert results to []server.ServerPrompt
	var promptsToAdd []server.ServerPrompt
	for _, item := range results {
		if prompt, ok := item.(server.ServerPrompt); ok {
			promptsToAdd = append(promptsToAdd, prompt)
		}
	}
	return promptsToAdd
}

// processResourcesForServer processes all resources from a server and returns handlers to add
func processResourcesForServer(a *AggregatorServer, serverName string, info *ServerInfo) []server.ServerResource {
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

	// Convert results to []server.ServerResource
	var resourcesToAdd []server.ServerResource
	for _, item := range results {
		if resource, ok := item.(server.ServerResource); ok {
			resourcesToAdd = append(resourcesToAdd, resource)
		}
	}
	return resourcesToAdd
}
