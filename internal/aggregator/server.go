package aggregator

import (
	"context"
	"fmt"
	"muster/internal/config"
	"muster/pkg/logging"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AggregatorServer implements an MCP server that aggregates multiple backend MCP servers
type AggregatorServer struct {
	config   AggregatorConfig
	registry *ServerRegistry
	server   *server.MCPServer

	// Transport-specific servers
	sseServer            *server.SSEServer
	streamableHTTPServer *server.StreamableHTTPServer
	stdioServer          *server.StdioServer

	// HTTP server for SSE endpoint
	httpServer *http.Server

	// Lifecycle management
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.RWMutex

	// Handler tracking - tracks which handlers are currently active
	toolManager     *activeItemManager
	promptManager   *activeItemManager
	resourceManager *activeItemManager
}

// NewAggregatorServer creates a new aggregator server
func NewAggregatorServer(aggConfig AggregatorConfig) *AggregatorServer {
	return &AggregatorServer{
		config:          aggConfig,
		registry:        NewServerRegistry(aggConfig.MusterPrefix),
		toolManager:     newActiveItemManager(itemTypeTool),
		promptManager:   newActiveItemManager(itemTypePrompt),
		resourceManager: newActiveItemManager(itemTypeResource),
	}
}

// Start starts the aggregator server
func (a *AggregatorServer) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.server != nil {
		a.mu.Unlock()
		return fmt.Errorf("aggregator server already started")
	}

	// Create cancellable context
	a.ctx, a.cancelFunc = context.WithCancel(ctx)

	// Create MCP server with capabilities
	mcpServer := server.NewMCPServer(
		"muster-aggregator",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true), // subscribe and listChanged
		server.WithPromptCapabilities(true),
	)

	a.server = mcpServer

	// Initialize workflow adapter if config directory is provided
	if a.config.ConfigDir != "" {
		// Initialize workflow adapter in a separate method
		workflowAdapter := a.createWorkflowAdapter()
		if workflowAdapter != nil {
			workflowAdapter.Register()
			logging.Info("Aggregator", "Initialized workflow adapter")
		}
	}

	// Start registry update monitor
	a.wg.Add(1)
	go a.monitorRegistryUpdates()

	// Subscribe to tool update events from workflow and other managers
	logging.Info("Aggregator", "Subscribing to tool update events...")
	api.SubscribeToToolUpdates(a)
	logging.Info("Aggregator", "Successfully subscribed to tool update events")

	// Release the lock before calling updateCapabilities to avoid deadlock
	a.mu.Unlock()

	// Update initial capabilities
	a.updateCapabilities()

	// Start the configured transport server
	addr := fmt.Sprintf("%s:%d", a.config.Host, a.config.Port)

	switch a.config.Transport {
	case config.MCPTransportSSE:
		logging.Info("Aggregator", "Starting MCP aggregator server with SSE transport on %s", addr)
		baseURL := fmt.Sprintf("http://%s:%d", a.config.Host, a.config.Port)
		a.sseServer = server.NewSSEServer(
			a.server,
			server.WithBaseURL(baseURL),
			server.WithSSEEndpoint("/sse"),
			server.WithMessageEndpoint("/message"),
			server.WithKeepAlive(true),
			server.WithKeepAliveInterval(30*time.Second),
		)
		sseServer := a.sseServer
		if sseServer != nil {
			go func() {
				if err := sseServer.Start(addr); err != nil && err != http.ErrServerClosed {
					logging.Error("Aggregator", err, "SSE server error")
				}
			}()
		}

	case config.MCPTransportStdio:
		logging.Info("Aggregator", "Starting MCP aggregator server with stdio transport")
		a.stdioServer = server.NewStdioServer(a.server)
		stdioServer := a.stdioServer
		if stdioServer != nil {
			go func() {
				if err := stdioServer.Listen(a.ctx, os.Stdin, os.Stdout); err != nil {
					logging.Error("Aggregator", err, "Stdio server error")
				}
			}()
		}

	case config.MCPTransportStreamableHTTP:
		fallthrough
	default:
		logging.Info("Aggregator", "Starting MCP aggregator server with streamable-http transport on %s", addr)
		a.streamableHTTPServer = server.NewStreamableHTTPServer(a.server)
		streamableServer := a.streamableHTTPServer
		if streamableServer != nil {
			go func() {
				if err := streamableServer.Start(addr); err != nil && err != http.ErrServerClosed {
					logging.Error("Aggregator", err, "Streamable HTTP server error")
				}
			}()
		}
	}

	return nil
}

// Stop stops the aggregator server
func (a *AggregatorServer) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.server == nil {
		a.mu.Unlock()
		return fmt.Errorf("aggregator server not started")
	}

	logging.Info("Aggregator", "Stopping MCP aggregator server")

	// Cancel context to stop background routines
	cancelFunc := a.cancelFunc
	sseServer := a.sseServer
	streamableServer := a.streamableHTTPServer
	a.mu.Unlock()

	if cancelFunc != nil {
		cancelFunc()
	}

	// Shutdown transport servers
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if sseServer != nil {
		if err := sseServer.Shutdown(shutdownCtx); err != nil {
			logging.Error("Aggregator", err, "Error shutting down SSE server")
		}
	}

	if streamableServer != nil {
		if err := streamableServer.Shutdown(shutdownCtx); err != nil {
			logging.Error("Aggregator", err, "Error shutting down streamable HTTP server")
		}
	}

	// Stdio server stops on context cancellation, no explicit shutdown needed.

	// Wait for background routines
	a.wg.Wait()

	// Deregister all servers
	for name := range a.registry.GetAllServers() {
		if err := a.registry.Deregister(name); err != nil {
			logging.Warn("Aggregator", "Error deregistering server %s: %v", name, err)
		}
	}

	a.mu.Lock()
	a.server = nil
	a.sseServer = nil
	a.streamableHTTPServer = nil
	a.stdioServer = nil
	a.httpServer = nil
	a.mu.Unlock()

	return nil
}

// RegisterServer registers a new backend MCP server
func (a *AggregatorServer) RegisterServer(ctx context.Context, name string, client MCPClient, toolPrefix string) error {
	logging.Debug("Aggregator", "RegisterServer called for %s at %s", name, time.Now().Format("15:04:05.000"))
	return a.registry.Register(ctx, name, client, toolPrefix)
}

// DeregisterServer removes a backend MCP server
func (a *AggregatorServer) DeregisterServer(name string) error {
	logging.Debug("Aggregator", "DeregisterServer called for %s at %s", name, time.Now().Format("15:04:05.000"))
	return a.registry.Deregister(name)
}

// GetRegistry returns the server registry
func (a *AggregatorServer) GetRegistry() *ServerRegistry {
	return a.registry
}

// monitorRegistryUpdates monitors for changes in the registry and updates capabilities
func (a *AggregatorServer) monitorRegistryUpdates() {
	defer a.wg.Done()

	updateChan := a.registry.GetUpdateChannel()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-updateChan:
			// Update server capabilities based on registered servers
			a.updateCapabilities()

			// Publish tool update event to trigger refresh in dependent managers
			a.publishToolUpdateEvent()
		}
	}
}

// publishToolUpdateEvent publishes a tool update event to notify dependent managers
func (a *AggregatorServer) publishToolUpdateEvent() {
	// Get all available tools
	tools := a.GetAvailableTools()

	// Create and publish the event
	event := api.ToolUpdateEvent{
		Type:       "tools_updated",
		ServerName: "aggregator", // Use aggregator as the source since it aggregates all tools
		Tools:      tools,
		Timestamp:  time.Now(),
	}

	// Publish the event - this will notify ServiceClass and Capability managers
	api.PublishToolUpdateEvent(event)

	logging.Debug("Aggregator", "Published tool update event with %d tools", len(tools))
}

// updateCapabilities updates the aggregator's advertised capabilities
func (a *AggregatorServer) updateCapabilities() {
	a.mu.RLock()
	if a.server == nil {
		a.mu.RUnlock()
		return
	}
	a.mu.RUnlock()

	logging.Debug("Aggregator", "Updating capabilities dynamically")

	// Get all servers
	servers := a.registry.GetAllServers()

	// Collect all items from connected servers AND core providers
	collected := collectItemsFromServersAndProviders(servers, a.registry, a)

	// Remove obsolete items
	a.removeObsoleteItems(collected)

	// Add new items
	a.addNewItems(servers)

	// Log summary
	a.logCapabilitiesSummary(servers)

	// Publish tool update event to notify dependent managers (like ServiceClass manager)
	// This ensures subscribers are notified when core tools become available during startup
	a.publishToolUpdateEvent()
}

// removeObsoleteItems removes items that are no longer available
func (a *AggregatorServer) removeObsoleteItems(collected *collectResult) {
	// Remove obsolete tools
	removeObsoleteItems(
		a.toolManager,
		collected.newTools,
		func(items []string) {
			a.server.DeleteTools(items...)
		},
	)

	// Remove obsolete prompts
	removeObsoleteItems(
		a.promptManager,
		collected.newPrompts,
		func(items []string) {
			a.server.DeletePrompts(items...)
		},
	)

	// Remove obsolete resources
	removeObsoleteItems(
		a.resourceManager,
		collected.newResources,
		func(items []string) {
			// Note: The MCP server API doesn't provide a batch removal method for resources
			// (unlike DeleteTools and DeletePrompts), so we have to remove them one by one.
			// This will cause multiple notifications to the client.
			// TODO: Consider requesting a RemoveResources/DeleteResources method in the MCP library
			for _, uri := range items {
				a.server.RemoveResource(uri)
			}
		},
	)
}

// addNewItems adds new handlers for items that don't exist yet
func (a *AggregatorServer) addNewItems(servers map[string]*ServerInfo) {
	var toolsToAdd []server.ServerTool
	var promptsToAdd []server.ServerPrompt
	var resourcesToAdd []server.ServerResource

	// Process each server
	for serverName, info := range servers {
		if !info.IsConnected() {
			continue
		}

		// Process tools for this server
		toolsToAdd = append(toolsToAdd, processToolsForServer(a, serverName, info)...)

		// Process prompts for this server
		promptsToAdd = append(promptsToAdd, processPromptsForServer(a, serverName, info)...)

		// Process resources for this server
		resourcesToAdd = append(resourcesToAdd, processResourcesForServer(a, serverName, info)...)
	}

	// Add tools from workflow, capability
	toolsToAdd = append(toolsToAdd, a.createToolsFromProviders()...)

	// Add all items in batches
	if len(toolsToAdd) > 0 {
		logging.Debug("Aggregator", "Adding %d tools in batch", len(toolsToAdd))
		a.server.AddTools(toolsToAdd...)
	}

	if len(promptsToAdd) > 0 {
		logging.Debug("Aggregator", "Adding %d prompts in batch", len(promptsToAdd))
		a.server.AddPrompts(promptsToAdd...)
	}

	if len(resourcesToAdd) > 0 {
		logging.Debug("Aggregator", "Adding %d resources in batch", len(resourcesToAdd))
		a.server.AddResources(resourcesToAdd...)
	}
}

// logCapabilitiesSummary logs a summary of current capabilities
func (a *AggregatorServer) logCapabilitiesSummary(servers map[string]*ServerInfo) {
	toolCount := 0
	resourceCount := 0
	promptCount := 0

	for _, info := range servers {
		if info.IsConnected() {
			info.mu.RLock()
			toolCount += len(info.Tools)
			resourceCount += len(info.Resources)
			promptCount += len(info.Prompts)
			info.mu.RUnlock()
		}
	}

	logging.Debug("Aggregator", "Updated capabilities: %d tools, %d resources, %d prompts",
		toolCount, resourceCount, promptCount)
}

// GetEndpoint returns the aggregator's endpoint URL based on transport
func (a *AggregatorServer) GetEndpoint() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	switch a.config.Transport {
	case config.MCPTransportSSE:
		return fmt.Sprintf("http://%s:%d/sse", a.config.Host, a.config.Port)
	case config.MCPTransportStreamableHTTP:
		return fmt.Sprintf("http://%s:%d/mcp", a.config.Host, a.config.Port) // Default path for streamable
	case config.MCPTransportStdio:
		return "stdio"
	default:
		// Default to streamable-http endpoint
		return fmt.Sprintf("http://%s:%d/mcp", a.config.Host, a.config.Port)
	}
}

// GetTools returns all available tools with smart prefixing (only prefixed when conflicts exist)
func (a *AggregatorServer) GetTools() []mcp.Tool {
	return a.registry.GetAllTools()
}

// GetToolsWithStatus returns all available tools with their blocked status
func (a *AggregatorServer) GetToolsWithStatus() []ToolWithStatus {
	a.mu.RLock()
	yolo := a.config.Yolo
	a.mu.RUnlock()

	tools := a.registry.GetAllTools()
	result := make([]ToolWithStatus, 0, len(tools))

	for _, tool := range tools {
		// Resolve the tool to get the original name
		var originalName string
		if serverName, origName, err := a.registry.ResolveToolName(tool.Name); err == nil {
			originalName = origName
			_ = serverName // unused
		} else {
			// If we can't resolve, use the exposed name
			originalName = tool.Name
		}

		result = append(result, ToolWithStatus{
			Tool:    tool,
			Blocked: !yolo && isDestructiveTool(originalName),
		})
	}

	return result
}

// GetResources returns all available resources
func (a *AggregatorServer) GetResources() []mcp.Resource {
	return a.registry.GetAllResources()
}

// GetPrompts returns all available prompts with smart prefixing (only prefixed when conflicts exist)
func (a *AggregatorServer) GetPrompts() []mcp.Prompt {
	return a.registry.GetAllPrompts()
}

// ToggleToolBlock toggles the blocked status of a specific tool
// This allows runtime changes to the denylist behavior for individual tools
func (a *AggregatorServer) ToggleToolBlock(toolName string) error {
	// For now, we can only toggle between fully enabled (yolo) or default denylist
	// In a future enhancement, we could maintain a runtime override list
	// For now, we just return an error indicating this needs more work
	return fmt.Errorf("individual tool blocking toggle not yet implemented")
}

// IsYoloMode returns whether yolo mode is enabled
func (a *AggregatorServer) IsYoloMode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Yolo
}

// CallToolInternal allows internal components to call tools directly
func (a *AggregatorServer) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	logging.Debug("Aggregator", "CallToolInternal called for tool: %s", toolName)

	// First, try to resolve the tool name to find which server provides it
	serverName, originalName, err := a.registry.ResolveToolName(toolName)
	if err == nil {
		logging.Debug("Aggregator", "Tool %s found in registry (server: %s, original: %s)", toolName, serverName, originalName)
		// Found in registry - call through the registered server
		serverInfo, exists := a.registry.GetServerInfo(serverName)
		if !exists || serverInfo == nil {
			return nil, fmt.Errorf("server not found: %s", serverName)
		}

		// Call the tool through the client using the original name
		return serverInfo.Client.CallTool(ctx, originalName, args)
	}

	logging.Debug("Aggregator", "Tool %s not found in registry (error: %v), checking core tools", toolName, err)

	// If not found in registry, check if it's a core tool in the aggregator's own server
	a.mu.RLock()
	server := a.server
	a.mu.RUnlock()

	if server != nil {
		// Check if this is a core tool by looking at the tools we created
		coreTools := a.createToolsFromProviders()
		logging.Debug("Aggregator", "Created %d core tools, checking for %s", len(coreTools), toolName)
		for _, tool := range coreTools {
			logging.Debug("Aggregator", "Comparing tool %s with core tool %s", toolName, tool.Tool.Name)
			if tool.Tool.Name == toolName {
				logging.Debug("Aggregator", "Found core tool %s, calling directly", toolName)
				// This is a core tool - call it through the aggregator's own server
				// We need to use the MCP server's tool calling mechanism
				// Since we don't have direct access to the handler, we'll create a temporary MCP call
				return a.callCoreToolDirectly(ctx, toolName, args)
			}
		}
		logging.Debug("Aggregator", "Tool %s not found in %d core tools", toolName, len(coreTools))
	} else {
		logging.Debug("Aggregator", "Aggregator server is nil")
	}

	return nil, fmt.Errorf("tool not found: %s", toolName)
}

// callCoreToolDirectly calls a core tool directly through the API handlers
func (a *AggregatorServer) callCoreToolDirectly(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	logging.Debug("Aggregator", "callCoreToolDirectly called for tool: %s", toolName)
	// Remove the core_ prefix to get the original tool name
	originalToolName := strings.TrimPrefix(toolName, "core_")
	logging.Debug("Aggregator", "Original tool name after prefix removal: %s", originalToolName)

	// Determine which provider handles this tool based on the tool name prefix
	switch {
	case strings.HasPrefix(originalToolName, "workflow_"):
		handler := api.GetWorkflow()
		if handler == nil {
			return nil, fmt.Errorf("workflow handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}

	case strings.HasPrefix(originalToolName, "capability_") || strings.HasPrefix(originalToolName, "api_"):
		handler := api.GetCapability()
		if handler == nil {
			return nil, fmt.Errorf("capability handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}

	case strings.HasPrefix(originalToolName, "service_"):
		handler := api.GetServiceManager()
		if handler == nil {
			return nil, fmt.Errorf("service manager handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}

	case strings.HasPrefix(originalToolName, "config_"):
		handler := api.GetConfig()
		if handler == nil {
			return nil, fmt.Errorf("config handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}

	case strings.HasPrefix(originalToolName, "serviceclass_"):
		handler := api.GetServiceClassManager()
		if handler == nil {
			return nil, fmt.Errorf("service class manager handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}

	case strings.HasPrefix(originalToolName, "mcpserver_"):
		handler := api.GetMCPServerManager()
		if handler == nil {
			return nil, fmt.Errorf("MCP server manager handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}
	}

	return nil, fmt.Errorf("no handler found for core tool: %s", toolName)
}

// createWorkflowAdapter creates a workflow adapter using the new unified pattern
func (a *AggregatorServer) createWorkflowAdapter() interface {
	Register()
} {
	// Use the new unified pattern instead of the deprecated factory
	// Note: For aggregator, we can't use the full manager pattern since we need different initialization
	// This is acceptable since aggregator has a different lifecycle than main services
	logging.Warn("Aggregator", "Workflow adapter creation skipped - aggregator uses different initialization pattern")
	return nil
}

// IsToolAvailable implements ToolAvailabilityChecker interface
func (a *AggregatorServer) IsToolAvailable(toolName string) bool {
	// Check if the tool exists in any registered server
	_, _, err := a.registry.ResolveToolName(toolName)
	if err == nil {
		return true // Found in registry
	}

	// Check if it's a core tool by recreating the core tools list
	coreTools := a.createToolsFromProviders()
	for _, tool := range coreTools {
		if tool.Tool.Name == toolName {
			return true // Found in core tools
		}
	}

	return false // Not found anywhere
}

// GetAvailableTools implements ToolAvailabilityChecker interface
func (a *AggregatorServer) GetAvailableTools() []string {
	// Get tools from external servers via registry
	registryTools := a.registry.GetAllTools()

	// Get core tools by recreating them using the same logic as updateCapabilities
	coreTools := a.createToolsFromProviders()

	// Combine all tool names
	allToolNames := make([]string, 0, len(registryTools)+len(coreTools))

	// Add registry tool names
	for _, tool := range registryTools {
		allToolNames = append(allToolNames, tool.Name)
	}

	// Add core tool names
	for _, tool := range coreTools {
		allToolNames = append(allToolNames, tool.Tool.Name)
	}

	return allToolNames
}

// UpdateCapabilities provides public access to update capabilities (for workflow manager)
func (a *AggregatorServer) UpdateCapabilities() {
	a.updateCapabilities()
}

// OnToolsUpdated implements ToolUpdateSubscriber interface to handle workflow tool changes
func (a *AggregatorServer) OnToolsUpdated(event api.ToolUpdateEvent) {
	logging.Info("Aggregator", "Received tool update event: type=%s, server=%s, tools=%d",
		event.Type, event.ServerName, len(event.Tools))

	// Handle workflow tool updates by refreshing capabilities
	if event.ServerName == "workflow-manager" && strings.HasPrefix(event.Type, "workflow_") {
		logging.Info("Aggregator", "Refreshing capabilities due to workflow tool update: %s", event.Type)
		go func() {
			// Small delay to ensure workflow manager has released its mutex
			time.Sleep(10 * time.Millisecond)
			a.updateCapabilities()
		}()
	}
}
