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

// AggregatorServer implements a comprehensive MCP server that aggregates multiple backend MCP servers.
//
// The AggregatorServer is the core component responsible for:
//   - Collecting and exposing tools, resources, and prompts from multiple backend servers
//   - Managing multiple transport protocols (SSE, stdio, streamable-http)
//   - Integrating core muster tools alongside external MCP servers
//   - Providing intelligent name collision resolution
//   - Implementing security filtering through the denylist system
//   - Real-time capability updates when backend servers change
//
// Architecture:
// The server maintains a registry of backend MCP servers and dynamically updates its
// exposed capabilities as servers are registered/deregistered. It supports multiple
// transport protocols simultaneously and provides both external MCP compatibility
// and internal tool calling capabilities.
//
// Thread Safety:
// All public methods are thread-safe and can be called concurrently. Internal state
// is protected by appropriate synchronization mechanisms.
type AggregatorServer struct {
	config   AggregatorConfig  // Configuration args for the aggregator
	registry *ServerRegistry   // Registry of backend MCP servers
	server   *server.MCPServer // Core MCP server implementation

	// Transport-specific server instances for different communication protocols
	sseServer            *server.SSEServer            // Server-Sent Events transport
	streamableHTTPServer *server.StreamableHTTPServer // Streamable HTTP transport
	stdioServer          *server.StdioServer          // Standard I/O transport

	// HTTP server for SSE endpoint (when using SSE transport)
	httpServer *http.Server

	// Lifecycle management for coordinating startup and shutdown
	ctx        context.Context    // Context for coordinating shutdown
	cancelFunc context.CancelFunc // Function to cancel the context
	wg         sync.WaitGroup     // WaitGroup for background goroutines
	mu         sync.RWMutex       // Protects server state during lifecycle operations

	// Active capability tracking - manages which tools/prompts/resources are currently exposed
	toolManager     *activeItemManager // Tracks active tools and their handlers
	promptManager   *activeItemManager // Tracks active prompts and their handlers
	resourceManager *activeItemManager // Tracks active resources and their handlers
}

// NewAggregatorServer creates a new aggregator server with the specified configuration.
//
// This constructor initializes all necessary components but does not start any servers.
// The returned server must be started with the Start method before it can handle requests.
//
// The server is configured with:
//   - A server registry using the specified muster prefix
//   - Active item managers for tracking capabilities
//   - Default transport settings based on configuration
//
// Args:
//   - aggConfig: Configuration args defining server behavior, transport, and security settings
//
// Returns a configured but unstarted aggregator server ready for initialization.
func NewAggregatorServer(aggConfig AggregatorConfig) *AggregatorServer {
	return &AggregatorServer{
		config:          aggConfig,
		registry:        NewServerRegistry(aggConfig.MusterPrefix),
		toolManager:     newActiveItemManager(itemTypeTool),
		promptManager:   newActiveItemManager(itemTypePrompt),
		resourceManager: newActiveItemManager(itemTypeResource),
	}
}

// Start initializes and starts the aggregator server with all configured transports.
//
// This method performs a comprehensive startup sequence:
//  1. Creates and configures the core MCP server with full capabilities
//  2. Initializes workflow adapter if config directory is provided
//  3. Starts background monitoring of registry updates
//  4. Subscribes to tool update events from other muster components
//  5. Performs initial capability discovery and registration
//  6. Starts the appropriate transport server(s) based on configuration
//
// Transport Support:
//   - SSE: Server-Sent Events with HTTP endpoints (/sse, /message)
//   - Stdio: Standard input/output for CLI integration
//   - Streamable HTTP: HTTP-based streaming protocol (default)
//
// The method is idempotent for the same context - calling it multiple times with
// the same context will return an error indicating the server is already started.
//
// Args:
//   - ctx: Context for controlling the server lifecycle and coordinating shutdown
//
// Returns an error if startup fails at any stage, or nil on successful startup.
func (a *AggregatorServer) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.server != nil {
		a.mu.Unlock()
		return fmt.Errorf("aggregator server already started")
	}

	// Create cancellable context for coordinating shutdown across all components
	a.ctx, a.cancelFunc = context.WithCancel(ctx)

	// Create MCP server with full capabilities enabled
	mcpServer := server.NewMCPServer(
		"muster-aggregator",
		"1.0.0",
		server.WithToolCapabilities(true), // Enable tool execution
		server.WithResourceCapabilities(true, true), // Enable resources with subscribe and listChanged
		server.WithPromptCapabilities(true),         // Enable prompt retrieval
	)

	a.server = mcpServer

	// Initialize workflow adapter if config directory is provided
	// This allows workflow definitions to be exposed as tools
	if a.config.ConfigDir != "" {
		workflowAdapter := a.createWorkflowAdapter()
		if workflowAdapter != nil {
			workflowAdapter.Register()
			logging.Info("Aggregator", "Initialized workflow adapter")
		}
	}

	// Start background monitoring for registry changes
	a.wg.Add(1)
	go a.monitorRegistryUpdates()

	// Subscribe to tool update events from workflow and other managers
	// This ensures the aggregator stays synchronized with core muster components
	logging.Info("Aggregator", "Subscribing to tool update events...")
	api.SubscribeToToolUpdates(a)
	logging.Info("Aggregator", "Successfully subscribed to tool update events")

	// Release the lock before calling updateCapabilities to avoid deadlock
	a.mu.Unlock()

	// Perform initial capability discovery and registration
	a.updateCapabilities()

	// Start the configured transport server
	addr := fmt.Sprintf("%s:%d", a.config.Host, a.config.Port)

	switch a.config.Transport {
	case config.MCPTransportSSE:
		// Server-Sent Events transport with HTTP endpoints
		logging.Info("Aggregator", "Starting MCP aggregator server with SSE transport on %s", addr)
		baseURL := fmt.Sprintf("http://%s:%d", a.config.Host, a.config.Port)
		a.sseServer = server.NewSSEServer(
			a.server,
			server.WithBaseURL(baseURL),
			server.WithSSEEndpoint("/sse"),               // Main SSE endpoint for events
			server.WithMessageEndpoint("/message"),       // Endpoint for sending messages
			server.WithKeepAlive(true),                   // Enable keep-alive for connection stability
			server.WithKeepAliveInterval(30*time.Second), // Keep-alive interval
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
		// Standard I/O transport for CLI integration
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
		// Streamable HTTP transport (default) - HTTP-based streaming protocol
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

// Stop gracefully shuts down the aggregator server and all its components.
//
// This method performs a coordinated shutdown sequence:
//  1. Cancels the context to signal shutdown to all background routines
//  2. Shuts down all transport servers with a timeout
//  3. Waits for background routines to complete
//  4. Deregisters all backend servers to clean up connections
//  5. Resets internal state to allow for restart
//
// The shutdown process includes:
//   - Graceful shutdown of HTTP-based transports with a 5-second timeout
//   - Automatic shutdown of stdio transport via context cancellation
//   - Cleanup of all registered backend MCP servers
//   - Synchronization with background monitoring routines
//
// The method is idempotent - calling it multiple times is safe and will not
// cause errors or duplicate cleanup operations.
//
// Args:
//   - ctx: Context for controlling the shutdown timeout and operations
//
// Returns an error if shutdown encounters issues, though cleanup continues regardless.
func (a *AggregatorServer) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.server == nil {
		a.mu.Unlock()
		return fmt.Errorf("aggregator server not started")
	}

	logging.Info("Aggregator", "Stopping MCP aggregator server")

	// Capture references before releasing lock to avoid race conditions
	cancelFunc := a.cancelFunc
	sseServer := a.sseServer
	streamableServer := a.streamableHTTPServer
	a.mu.Unlock()

	// Cancel context to signal shutdown to all background routines
	if cancelFunc != nil {
		cancelFunc()
	}

	// Shutdown transport servers with timeout
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

	// Note: Stdio server stops automatically on context cancellation, no explicit shutdown needed

	// Wait for all background routines to complete
	a.wg.Wait()

	// Clean up all registered backend servers
	for name := range a.registry.GetAllServers() {
		if err := a.registry.Deregister(name); err != nil {
			logging.Warn("Aggregator", "Error deregistering server %s: %v", name, err)
		}
	}

	// Reset internal state to allow for clean restart
	a.mu.Lock()
	a.server = nil
	a.sseServer = nil
	a.streamableHTTPServer = nil
	a.stdioServer = nil
	a.httpServer = nil
	a.mu.Unlock()

	return nil
}

// RegisterServer registers a new backend MCP server with the aggregator.
//
// This method adds a backend MCP server to the aggregator's registry, making its
// tools, resources, and prompts available through the aggregated interface.
// The registration process includes client initialization and capability discovery.
//
// Args:
//   - ctx: Context for the registration operation and capability queries
//   - name: Unique identifier for the server within the aggregator
//   - client: MCP client interface for communicating with the backend server
//   - toolPrefix: Server-specific prefix for name collision resolution
//
// Returns an error if registration fails due to naming conflicts, client issues,
// or communication problems with the backend server.
func (a *AggregatorServer) RegisterServer(ctx context.Context, name string, client MCPClient, toolPrefix string) error {
	logging.Debug("Aggregator", "RegisterServer called for %s at %s", name, time.Now().Format("15:04:05.000"))
	return a.registry.Register(ctx, name, client, toolPrefix)
}

// DeregisterServer removes a backend MCP server from the aggregator.
//
// This method cleanly removes a backend server from the aggregator, which will
// cause all tools, resources, and prompts from that server to become unavailable.
// The backend client connection is closed as part of the deregistration process.
//
// Args:
//   - name: Unique identifier of the server to remove
//
// Returns an error if the server is not found or deregistration fails.
func (a *AggregatorServer) DeregisterServer(name string) error {
	logging.Debug("Aggregator", "DeregisterServer called for %s at %s", name, time.Now().Format("15:04:05.000"))
	return a.registry.Deregister(name)
}

// GetRegistry returns the server registry for direct access to backend server information.
//
// This method provides access to the underlying registry for advanced operations
// such as inspecting server status, accessing raw capabilities, or performing
// administrative tasks. It should be used carefully to avoid disrupting the
// aggregator's normal operation.
//
// Returns the ServerRegistry instance managing all backend servers.
func (a *AggregatorServer) GetRegistry() *ServerRegistry {
	return a.registry
}

// monitorRegistryUpdates runs a background monitoring loop for registry changes.
//
// This method continuously monitors the registry for changes (server registrations,
// deregistrations, or capability updates) and triggers appropriate responses:
//   - Updates the aggregator's exposed capabilities
//   - Publishes tool update events to notify dependent managers
//   - Maintains synchronization between backend servers and the aggregated interface
//
// The monitoring continues until the server's context is cancelled during shutdown.
// This method is designed to run as a background goroutine.
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

// publishToolUpdateEvent publishes a tool update event to notify dependent managers.
//
// This method creates and publishes an event containing the current set of available
// tools, which notifies other muster components (like ServiceClass and Capability
// managers) that the tool landscape has changed. This ensures system-wide consistency
// when tools become available or unavailable.
//
// The event uses "aggregator" as the server name since it represents the aggregated
// view of all tools from multiple sources.
func (a *AggregatorServer) publishToolUpdateEvent() {
	// Get current tool inventory from all sources
	tools := a.GetAvailableTools()

	// Create and publish the tool update event
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

// updateCapabilities performs a comprehensive update of the aggregator's exposed capabilities.
//
// This method is the core of the aggregator's dynamic capability management. It:
//  1. Collects all available items from backend servers and core providers
//  2. Removes capabilities that are no longer available (cleanup)
//  3. Adds new capabilities that have become available
//  4. Updates the MCP server's advertised capabilities
//  5. Publishes events to notify dependent components
//
// The update process is designed to be efficient and minimize disruption to active
// connections. Items are added and removed in batches where possible, and the
// operation is thread-safe for concurrent access.
//
// This method is called:
//   - During server startup for initial capability discovery
//   - When backend servers are registered or deregistered
//   - When tool update events are received from core components
func (a *AggregatorServer) updateCapabilities() {
	a.mu.RLock()
	if a.server == nil {
		a.mu.RUnlock()
		return
	}
	a.mu.RUnlock()

	logging.Debug("Aggregator", "Updating capabilities dynamically")

	// Get all registered backend servers
	servers := a.registry.GetAllServers()

	// Collect all items from connected servers AND core providers
	collected := collectItemsFromServersAndProviders(servers, a.registry, a)

	// Remove obsolete items that are no longer available
	a.removeObsoleteItems(collected)

	// Add new items that have become available
	a.addNewItems(servers)

	// Log summary of current capabilities
	a.logCapabilitiesSummary(servers)

	// Publish tool update event to notify dependent managers (like ServiceClass manager)
	// This ensures subscribers are notified when core tools become available during startup
	a.publishToolUpdateEvent()
}

// removeObsoleteItems removes capabilities that are no longer available from any source.
//
// This method performs cleanup by identifying tools, prompts, and resources that
// were previously exposed but are no longer available from any backend server or
// core provider. It removes these obsolete items from the MCP server to maintain
// an accurate capability inventory.
//
// The removal process handles different item types appropriately:
//   - Tools and prompts: Batch removal using DeleteTools/DeletePrompts
//   - Resources: Individual removal due to MCP library limitations
//
// Args:
//   - collected: Result of capability collection containing current available items
func (a *AggregatorServer) removeObsoleteItems(collected *collectResult) {
	// Remove obsolete tools using batch operation
	removeObsoleteItems(
		a.toolManager,
		collected.newTools,
		func(items []string) {
			a.server.DeleteTools(items...)
		},
	)

	// Remove obsolete prompts using batch operation
	removeObsoleteItems(
		a.promptManager,
		collected.newPrompts,
		func(items []string) {
			a.server.DeletePrompts(items...)
		},
	)

	// Remove obsolete resources (individual removal required)
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

// addNewItems discovers and adds new capabilities from all available sources.
//
// This method processes all registered backend servers and core providers to
// identify new tools, prompts, and resources that should be exposed through
// the aggregator. It creates appropriate MCP handlers for each item and
// registers them with the MCP server in batches for efficiency.
//
// The process includes:
//   - Processing each connected backend server for new capabilities
//   - Integrating core tools from muster components (workflow, capability, etc.)
//   - Creating MCP-compatible handlers for all new items
//   - Batch registration to minimize client notifications
//
// Args:
//   - servers: Map of all registered backend servers and their information
func (a *AggregatorServer) addNewItems(servers map[string]*ServerInfo) {
	var toolsToAdd []server.ServerTool
	var promptsToAdd []server.ServerPrompt
	var resourcesToAdd []server.ServerResource

	// Process each registered backend server
	for serverName, info := range servers {
		if !info.IsConnected() {
			continue
		}

		// Process tools for this server and create handlers
		toolsToAdd = append(toolsToAdd, processToolsForServer(a, serverName, info)...)

		// Process prompts for this server and create handlers
		promptsToAdd = append(promptsToAdd, processPromptsForServer(a, serverName, info)...)

		// Process resources for this server and create handlers
		resourcesToAdd = append(resourcesToAdd, processResourcesForServer(a, serverName, info)...)
	}

	// Add tools from core muster components (workflow, capability, etc.)
	toolsToAdd = append(toolsToAdd, a.createToolsFromProviders()...)

	// Register all new items in batches to minimize client notifications
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

// logCapabilitiesSummary logs a comprehensive summary of current capabilities.
//
// This method provides diagnostic information about the current state of the
// aggregator by counting and logging the total number of tools, resources,
// and prompts available from all connected backend servers.
//
// The summary helps with:
//   - Monitoring aggregator health and functionality
//   - Debugging capability discovery issues
//   - Understanding the current tool landscape
//
// Args:
//   - servers: Map of all registered backend servers for capability counting
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

// GetEndpoint returns the aggregator's primary endpoint URL based on the configured transport.
//
// The endpoint format varies by transport type:
//   - SSE: http://host:port/sse (Server-Sent Events endpoint)
//   - Streamable HTTP: http://host:port/mcp (default HTTP streaming path)
//   - Stdio: "stdio" (indicates standard I/O communication)
//
// This endpoint can be used by MCP clients to connect to the aggregator and
// access all aggregated capabilities from backend servers.
//
// Returns the endpoint URL as a string, or "stdio" for standard I/O transport.
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

// GetTools returns all available tools from all sources with intelligent name prefixing.
//
// This method aggregates tools from all registered backend servers, applying
// smart prefixing to avoid name conflicts. The prefixing is only applied when
// conflicts would otherwise occur, following the pattern:
// {muster_prefix}_{server_prefix}_{original_name}
//
// Returns a slice of MCP tools ready for client consumption.
func (a *AggregatorServer) GetTools() []mcp.Tool {
	return a.registry.GetAllTools()
}

// GetToolsWithStatus returns all available tools along with their security blocking status.
//
// This method provides enhanced tool information that includes whether each tool
// is blocked by the security denylist. The blocking status depends on:
//   - The tool's classification as destructive in the denylist
//   - The current yolo mode setting (yolo=true allows all tools)
//
// The tool names are resolved to their original names (before prefixing) for
// accurate denylist checking, ensuring consistent security behavior regardless
// of how tools are exposed.
//
// Returns a slice of ToolWithStatus containing both tool definitions and security status.
func (a *AggregatorServer) GetToolsWithStatus() []ToolWithStatus {
	a.mu.RLock()
	yolo := a.config.Yolo
	a.mu.RUnlock()

	tools := a.registry.GetAllTools()
	result := make([]ToolWithStatus, 0, len(tools))

	for _, tool := range tools {
		// Resolve the tool to get the original name for accurate denylist checking
		var originalName string
		if serverName, origName, err := a.registry.ResolveToolName(tool.Name); err == nil {
			originalName = origName
			_ = serverName // unused in this context
		} else {
			// If we can't resolve, use the exposed name as fallback
			originalName = tool.Name
		}

		result = append(result, ToolWithStatus{
			Tool:    tool,
			Blocked: !yolo && isDestructiveTool(originalName),
		})
	}

	return result
}

// GetResources returns all available resources from all registered backend servers.
//
// This method aggregates resources from all connected backend servers, applying
// appropriate URI prefixing to avoid conflicts. Resources maintain their original
// functionality while being properly namespaced within the aggregated environment.
//
// Returns a slice of MCP resources ready for client access.
func (a *AggregatorServer) GetResources() []mcp.Resource {
	return a.registry.GetAllResources()
}

// GetPrompts returns all available prompts from all sources with intelligent name prefixing.
//
// This method aggregates prompts from all registered backend servers, applying
// smart prefixing similar to tools to avoid name conflicts. The prefixing ensures
// that prompts from different servers can coexist without naming collisions.
//
// Returns a slice of MCP prompts ready for client consumption.
func (a *AggregatorServer) GetPrompts() []mcp.Prompt {
	return a.registry.GetAllPrompts()
}

// ToggleToolBlock toggles the blocked status of a specific tool (placeholder implementation).
//
// This method is intended to provide runtime control over individual tool blocking,
// allowing administrators to override the default denylist behavior for specific tools.
// Currently, this functionality is not fully implemented and returns an error.
//
// Future Enhancement:
// The full implementation would maintain a runtime override list that could
// selectively enable or disable specific tools regardless of the global yolo setting.
//
// Args:
//   - toolName: Name of the tool to toggle blocking status for
//
// Returns an error indicating the feature is not yet implemented.
func (a *AggregatorServer) ToggleToolBlock(toolName string) error {
	// For now, we can only toggle between fully enabled (yolo) or default denylist
	// In a future enhancement, we could maintain a runtime override list
	// For now, we just return an error indicating this needs more work
	return fmt.Errorf("individual tool blocking toggle not yet implemented")
}

// IsYoloMode returns whether yolo mode is currently enabled.
//
// Yolo mode disables the security denylist, allowing all tools to be executed
// regardless of their destructive potential. This mode should only be enabled
// in development or testing environments where the risk is acceptable.
//
// Returns true if yolo mode is enabled, false if security filtering is active.
func (a *AggregatorServer) IsYoloMode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Yolo
}

// CallToolInternal provides internal tool calling capability for muster components.
//
// This method allows internal muster components to execute tools through the
// aggregator without going through the external MCP protocol. It supports both:
//   - Tools from registered backend servers (resolved through the registry)
//   - Core tools from muster components (called directly through providers)
//
// The method performs intelligent tool resolution:
//  1. First attempts to resolve the tool through the server registry
//  2. If not found, checks if it's a core tool from muster components
//  3. Routes the call to the appropriate handler based on tool type
//
// This internal calling mechanism is essential for:
//   - Inter-component communication within muster
//   - Workflow execution that needs to call other tools
//   - Administrative operations that require tool access
//
// Args:
//   - ctx: Context for the tool execution
//   - toolName: Name of the tool to execute (may be prefixed)
//   - args: Arguments to pass to the tool as key-value pairs
//
// Returns the tool execution result or an error if the tool cannot be found or executed.
func (a *AggregatorServer) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	logging.Debug("Aggregator", "CallToolInternal called for tool: %s", toolName)

	// First, try to resolve the tool name through the registry (backend servers)
	serverName, originalName, err := a.registry.ResolveToolName(toolName)
	if err == nil {
		logging.Debug("Aggregator", "Tool %s found in registry (server: %s, original: %s)", toolName, serverName, originalName)
		// Found in registry - call through the registered server
		serverInfo, exists := a.registry.GetServerInfo(serverName)
		if !exists || serverInfo == nil {
			return nil, fmt.Errorf("server not found: %s", serverName)
		}

		// Call the tool through the backend client using the original name
		return serverInfo.Client.CallTool(ctx, originalName, args)
	}

	logging.Debug("Aggregator", "Tool %s not found in registry (error: %v), checking core tools", toolName, err)

	// If not found in registry, check if it's a core tool from muster components
	a.mu.RLock()
	server := a.server
	a.mu.RUnlock()

	if server != nil {
		// Check if this is a core tool by comparing with our core tool inventory
		coreTools := a.createToolsFromProviders()
		logging.Debug("Aggregator", "Created %d core tools, checking for %s", len(coreTools), toolName)
		for _, tool := range coreTools {
			logging.Debug("Aggregator", "Comparing tool %s with core tool %s", toolName, tool.Tool.Name)
			if tool.Tool.Name == toolName {
				logging.Debug("Aggregator", "Found core tool %s, calling directly", toolName)
				// This is a core tool - call it directly through the provider
				return a.callCoreToolDirectly(ctx, toolName, args)
			}
		}
		logging.Debug("Aggregator", "Tool %s not found in %d core tools", toolName, len(coreTools))
	} else {
		logging.Debug("Aggregator", "Aggregator server is nil")
	}

	return nil, fmt.Errorf("tool not found: %s", toolName)
}

// callCoreToolDirectly routes core tool calls to the appropriate muster component providers.
//
// This method handles the execution of core muster tools that are not provided by
// external backend servers but rather by internal muster components. It performs
// intelligent routing based on tool name prefixes to determine which component
// should handle the tool execution.
//
// Tool Routing Logic:
//   - workflow_*: Routed to the workflow manager for workflow operations
//   - capability_*, api_*: Routed to the capability manager for capability operations
//   - service_*: Routed to the service manager for service lifecycle operations
//   - config_*: Routed to the config manager for configuration operations
//   - serviceclass_*: Routed to the service class manager for service class operations
//   - mcpserver_*: Routed to the MCP server manager for MCP server operations
//
// The method removes the "core_" prefix from tool names before routing to ensure
// proper tool resolution within each component's tool provider interface.
//
// Args:
//   - ctx: Context for the tool execution
//   - toolName: Name of the core tool to execute (with core_ prefix)
//   - args: Arguments to pass to the tool as key-value pairs
//
// Returns the tool execution result converted to MCP format, or an error if
// no appropriate handler is found or execution fails.
func (a *AggregatorServer) callCoreToolDirectly(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	logging.Debug("Aggregator", "callCoreToolDirectly called for tool: %s", toolName)

	// Remove the core_ prefix to get the original tool name for routing
	originalToolName := strings.TrimPrefix(toolName, "core_")
	logging.Debug("Aggregator", "Original tool name after prefix removal: %s", originalToolName)

	// Route to the appropriate provider based on tool name prefix
	switch {
	case strings.HasPrefix(originalToolName, "workflow_"):
		// Workflow management and execution tools
		handler := api.GetWorkflow()
		if handler == nil {
			return nil, fmt.Errorf("workflow handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			// Map workflow_ tools back to action_ for internal workflow handler
			internalToolName := strings.Replace(originalToolName, "workflow_", "action_", 1)
			logging.Debug("Aggregator", "Mapping workflow tool %s to internal name %s", originalToolName, internalToolName)
			result, err := provider.ExecuteTool(ctx, internalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}

	case strings.HasPrefix(originalToolName, "capability_") || strings.HasPrefix(originalToolName, "api_"):
		// Capability management and API operations
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
		// Service lifecycle management operations
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
		// Configuration management operations
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
		// Service class management operations
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
		// MCP server management operations
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

// createWorkflowAdapter creates a workflow adapter for exposing workflow definitions as tools.
//
// This method is part of the legacy workflow integration pattern and is currently
// disabled in favor of the unified component architecture. The aggregator uses
// a different initialization pattern than the main services, making the standard
// workflow adapter creation inappropriate.
//
// Current Implementation:
// The method logs a warning and returns nil, indicating that workflow adapter
// creation is skipped for the aggregator. Workflow tools are instead integrated
// through the standard tool provider pattern via createToolsFromProviders().
//
// Returns nil (no workflow adapter created) with a warning log message.
func (a *AggregatorServer) createWorkflowAdapter() interface {
	Register()
} {
	// Use the new unified pattern instead of the deprecated factory
	// Note: For aggregator, we can't use the full manager pattern since we need different initialization
	// This is acceptable since aggregator has a different lifecycle than main services
	logging.Warn("Aggregator", "Workflow adapter creation skipped - aggregator uses different initialization pattern")
	return nil
}

// IsToolAvailable implements the ToolAvailabilityChecker interface.
//
// This method determines whether a specific tool is available through the aggregator,
// checking both external backend servers (via the registry) and core muster tools
// (via the tool providers). It provides a unified way for other components to
// verify tool availability before attempting to use them.
//
// The check process:
//  1. Attempts to resolve the tool through the server registry
//  2. If not found, checks the current core tool inventory
//  3. Returns true if found in either location
//
// This method is used by:
//   - Workflow manager for validating workflow step tools
//   - Capability manager for dependency checking
//   - Service class manager for tool availability validation
//
// Args:
//   - toolName: Name of the tool to check (may be prefixed)
//
// Returns true if the tool is available, false otherwise.
func (a *AggregatorServer) IsToolAvailable(toolName string) bool {
	// Check if the tool exists in any registered backend server
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

// GetAvailableTools implements the ToolAvailabilityChecker interface.
//
// This method returns a comprehensive list of all tools currently available
// through the aggregator, including both external tools from backend servers
// and core tools from muster components. The returned list represents the
// complete tool inventory that can be used by workflows, capabilities, and
// other muster components.
//
// The aggregation process:
//  1. Collects all tools from registered backend servers via the registry
//  2. Collects all core tools from muster component providers
//  3. Combines both lists into a unified tool inventory
//  4. Returns tool names (with appropriate prefixing applied)
//
// This method is used by:
//   - Workflow manager for populating available tool lists
//   - Capability manager for building capability dependencies
//   - Service class manager for tool validation
//   - Administrative interfaces for tool discovery
//
// Returns a slice of tool names representing all available tools.
func (a *AggregatorServer) GetAvailableTools() []string {
	// Get tools from external servers via registry
	registryTools := a.registry.GetAllTools()

	// Get core tools by recreating them using the same logic as updateCapabilities
	coreTools := a.createToolsFromProviders()

	// Combine all tool names from both sources
	allToolNames := make([]string, 0, len(registryTools)+len(coreTools))

	// Add tool names from registered backend servers
	for _, tool := range registryTools {
		allToolNames = append(allToolNames, tool.Name)
	}

	// Add tool names from core muster components
	for _, tool := range coreTools {
		allToolNames = append(allToolNames, tool.Tool.Name)
	}

	return allToolNames
}

// UpdateCapabilities provides public access to capability updates for external components.
//
// This method exposes the internal updateCapabilities functionality to allow
// other muster components (particularly the workflow manager) to trigger
// capability refreshes when they detect changes in their tool inventory.
//
// The method is thread-safe and can be called concurrently without causing
// issues. It performs the same comprehensive capability update as the internal
// method, including cleanup of obsolete items and addition of new capabilities.
//
// Use Cases:
//   - Workflow manager triggering updates when workflow definitions change
//   - Administrative tools forcing capability refresh
//   - Integration testing scenarios requiring capability synchronization
//
// This is a lightweight wrapper around the internal updateCapabilities method.
func (a *AggregatorServer) UpdateCapabilities() {
	a.updateCapabilities()
}

// OnToolsUpdated implements the ToolUpdateSubscriber interface for handling tool change events.
//
// This method responds to tool update events from other muster components,
// particularly the workflow manager, to maintain synchronization of the
// aggregator's exposed capabilities with the current tool landscape.
//
// Event Processing:
//   - Filters events to focus on workflow-related tool changes
//   - Triggers capability refresh for workflow tool updates
//   - Uses asynchronous processing with a small delay to avoid mutex conflicts
//   - Logs all received events for debugging and monitoring
//
// The asynchronous processing pattern ensures that:
//   - The event publisher (workflow manager) doesn't block waiting for aggregator updates
//   - Mutex conflicts are avoided by allowing the publisher to complete first
//   - Capability updates happen promptly but safely
//
// Args:
//   - event: Tool update event containing change information, tool lists, and metadata
//
// The method processes events selectively, focusing on workflow manager events
// that indicate changes to workflow-based tools.
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
