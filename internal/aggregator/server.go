package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"muster/internal/api"
	"muster/internal/config"
	"muster/internal/server"
	"muster/pkg/logging"

	"github.com/coreos/go-systemd/v22/activation"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
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
//   - Session-scoped tool visibility for OAuth-protected servers
//
// Architecture:
// The server maintains a registry of backend MCP servers and dynamically updates its
// exposed capabilities as servers are registered/deregistered. It supports multiple
// transport protocols simultaneously and provides both external MCP compatibility
// and internal tool calling capabilities.
//
// Session-Scoped Tool Visibility:
// For OAuth-protected servers, each user session maintains its own view of available
// tools based on which servers they have authenticated with. This is implemented via
// the SessionRegistry which tracks per-session connections and capabilities.
//
// Thread Safety:
// All public methods are thread-safe and can be called concurrently. Internal state
// is protected by appropriate synchronization mechanisms.
type AggregatorServer struct {
	config          AggregatorConfig     // Configuration args for the aggregator
	registry        *ServerRegistry      // Registry of backend MCP servers
	sessionRegistry *SessionRegistry     // Registry of per-session state for OAuth servers
	mcpServer       *mcpserver.MCPServer // Core MCP server implementation
	errorCallback   func(error)          // Callback for propagating async errors in the aggregator upwards

	// Transport-specific server instances for different communication protocols
	sseServer            *mcpserver.SSEServer            // Server-Sent Events transport
	streamableHTTPServer *mcpserver.StreamableHTTPServer // Streamable HTTP transport
	stdioServer          *mcpserver.StdioServer          // Standard I/O transport

	// HTTP servers with socket options (when socket reuse is enabled)
	httpServer []*http.Server

	// OAuth HTTP server for protecting MCP endpoints (when OAuth server is enabled)
	oauthHTTPServer *server.OAuthHTTPServer

	// Lifecycle management for coordinating startup and shutdown
	ctx        context.Context    // Context for coordinating shutdown
	cancelFunc context.CancelFunc // Function to cancel the context
	wg         sync.WaitGroup     // WaitGroup for background goroutines
	mu         sync.RWMutex       // Protects server state during lifecycle operations

	// Active capability tracking - manages which tools/prompts/resources are currently exposed
	toolManager     *activeItemManager // Tracks active tools and their handlers
	promptManager   *activeItemManager // Tracks active prompts and their handlers
	resourceManager *activeItemManager // Tracks active resources and their handlers
	isShuttingDown  bool               // Indicates whether the server is currently stopping
}

// DefaultSessionTimeout is the default timeout for idle session cleanup.
const DefaultSessionTimeout = 30 * time.Minute

// NewAggregatorServer creates a new aggregator server with the specified configuration.
//
// This constructor initializes all necessary components but does not start any servers.
// The returned server must be started with the Start method before it can handle requests.
//
// The server is configured with:
//   - A server registry using the specified muster prefix
//   - A session registry for per-session state management (OAuth)
//   - Active item managers for tracking capabilities
//   - Default transport settings based on configuration
//
// Args:
//   - aggConfig: Configuration args defining server behavior, transport, and security settings
//
// Returns a configured but unstarted aggregator server ready for initialization.
func NewAggregatorServer(aggConfig AggregatorConfig, errorCallback func(error)) *AggregatorServer {
	return &AggregatorServer{
		config:          aggConfig,
		registry:        NewServerRegistry(aggConfig.MusterPrefix),
		sessionRegistry: NewSessionRegistry(DefaultSessionTimeout),
		toolManager:     newActiveItemManager(itemTypeTool),
		promptManager:   newActiveItemManager(itemTypePrompt),
		resourceManager: newActiveItemManager(itemTypeResource),
		errorCallback:   errorCallback,
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
	if a.mcpServer != nil {
		a.mu.Unlock()
		return fmt.Errorf("aggregator server already started")
	}

	// Create cancellable context for coordinating shutdown across all components
	a.ctx, a.cancelFunc = context.WithCancel(ctx)

	// Create MCP server with full capabilities enabled
	// WithToolFilter enables session-specific tool visibility for OAuth-authenticated servers
	// (see ADR-006: Session-Scoped Tool Visibility)
	mcpSrv := mcpserver.NewMCPServer(
		"muster-aggregator",
		"1.0.0",
		mcpserver.WithToolCapabilities(true), // Enable tool execution
		mcpserver.WithResourceCapabilities(true, true), // Enable resources with subscribe and listChanged
		mcpserver.WithPromptCapabilities(true),         // Enable prompt retrieval
		mcpserver.WithToolFilter(a.sessionToolFilter),  // Return session-specific tools for OAuth servers
	)

	a.mcpServer = mcpSrv
	a.isShuttingDown = false

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

	// Register the auth://status resource for exposing authentication state
	// This allows agents to poll for auth requirements and enable SSO detection
	// NOTE: Must be called after releasing lock since registerAuthStatusResource acquires RLock
	a.registerAuthStatusResource()

	// Perform initial capability discovery and registration
	a.updateCapabilities()

	// Start the configured transport server
	addr := fmt.Sprintf("%s:%d", a.config.Host, a.config.Port)

	// Check if we're running under systemd socket activation
	var systemdListeners []net.Listener = nil
	listenersWithNames, err := activation.ListenersWithNames()
	if err != nil {
		logging.Error("Aggregator", err, "Failed to get systemd listeners with names")
	} else {
		for name, listeners := range listenersWithNames {
			for i, l := range listeners {
				logging.Info("Aggregator", "Listener %d for %s", i, name)
				systemdListeners = append(systemdListeners, l)
			}
		}
	}
	useSystemdActivation := len(systemdListeners) > 0
	if useSystemdActivation {
		logging.Info("Aggregator", "Systemd socket activation detected, using %d provided listener(s)", len(systemdListeners))

		if a.config.Transport == config.MCPTransportStdio {
			return fmt.Errorf("stdio transport cannot be used with systemd socket activation")
		}
	}

	a.mu.Lock()

	switch a.config.Transport {
	case config.MCPTransportSSE:
		baseURL := fmt.Sprintf("http://%s:%d", a.config.Host, a.config.Port)
		a.sseServer = mcpserver.NewSSEServer(
			a.mcpServer,
			mcpserver.WithBaseURL(baseURL),
			mcpserver.WithSSEEndpoint("/sse"),               // Main SSE endpoint for events
			mcpserver.WithMessageEndpoint("/message"),       // Endpoint for sending messages
			mcpserver.WithKeepAlive(true),                   // Enable keep-alive for connection stability
			mcpserver.WithKeepAliveInterval(30*time.Second), // Keep-alive interval
		)

		// Create a mux that routes to both MCP and OAuth handlers
		handler, err := a.createHTTPMux(a.sseServer)
		if err != nil {
			return fmt.Errorf("failed to create HTTP mux with OAuth protection: %w", err)
		}

		if useSystemdActivation {
			logging.Info("Aggregator", "Using systemd socket activation for SSE transport")
			for i, listener := range systemdListeners {
				server := &http.Server{
					Handler: handler,
				}
				a.httpServer = append(a.httpServer, server)
				go func(s *http.Server, l net.Listener, index int) {
					if err := s.Serve(l); err != nil && err != http.ErrServerClosed {
						logging.Error("Aggregator", err, "listener %d: SSE server error", index)
						a.errorCallback(err)
					}
				}(server, listener, i)
			}
		} else {
			logging.Info("Aggregator", "Starting MCP aggregator server with SSE transport on %s", addr)
			server := &http.Server{
				Addr:    addr,
				Handler: handler,
			}
			a.httpServer = append(a.httpServer, server)
			go func() {
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logging.Error("Aggregator", err, "SSE server error")
					a.errorCallback(err)
				}
			}()
		}

	case config.MCPTransportStdio:
		// Standard I/O transport for CLI integration
		logging.Info("Aggregator", "Starting MCP aggregator server with stdio transport")
		a.stdioServer = mcpserver.NewStdioServer(a.mcpServer)
		stdioServer := a.stdioServer
		if stdioServer != nil {
			go func() {
				if err := stdioServer.Listen(a.ctx, os.Stdin, os.Stdout); err != nil {
					logging.Error("Aggregator", err, "Stdio server error")
					a.errorCallback(err)
				}
			}()
		}

	case config.MCPTransportStreamableHTTP:
		fallthrough
	default:
		// Streamable HTTP transport (default) - HTTP-based streaming protocol
		a.streamableHTTPServer = mcpserver.NewStreamableHTTPServer(a.mcpServer)

		// Create a mux that routes to both MCP and OAuth handlers
		handler, err := a.createHTTPMux(a.streamableHTTPServer)
		if err != nil {
			return fmt.Errorf("failed to create HTTP mux with OAuth protection: %w", err)
		}

		if useSystemdActivation {
			logging.Info("Aggregator", "Using systemd socket activation for streamable HTTP transport")
			for i, listener := range systemdListeners {
				server := &http.Server{
					Handler: handler,
				}
				a.httpServer = append(a.httpServer, server)
				go func(s *http.Server, l net.Listener, index int) {
					if err := s.Serve(l); err != nil && err != http.ErrServerClosed {
						logging.Error("Aggregator", err, "listener %d: Streamable HTTP server error", index)
						a.errorCallback(err)
					}
				}(server, listener, i)
			}
		} else {
			logging.Info("Aggregator", "Starting MCP aggregator server with streamable-http transport on %s", addr)
			server := &http.Server{
				Addr:    addr,
				Handler: handler,
			}
			a.httpServer = append(a.httpServer, server)
			go func() {
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logging.Error("Aggregator", err, "Streamable HTTP server error")
					a.errorCallback(err)
				}
			}()
		}
	}
	a.mu.Unlock()

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
	if a.isShuttingDown {
		a.mu.Unlock()
		return nil
	} else if a.mcpServer == nil {
		a.mu.Unlock()
		return fmt.Errorf("aggregator server not started")
	}

	a.isShuttingDown = true // Prevent further updates during shutdown
	logging.Info("Aggregator", "Stopping MCP aggregator server")

	// Capture references before releasing lock to avoid race conditions
	cancelFunc := a.cancelFunc
	httpServer := a.httpServer
	a.mu.Unlock()

	// Cancel context to signal shutdown to all background routines
	if cancelFunc != nil {
		cancelFunc()
	}

	// Shutdown transport servers with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Shutdown custom HTTP servers first (they take priority over MCP servers)
	if len(httpServer) > 0 {
		for _, s := range httpServer {
			if err := s.Shutdown(shutdownCtx); err != nil {
				logging.Error("Aggregator", err, "Error shutting down HTTP server")
			}
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

	// Stop session registry (closes all session-specific connections)
	if a.sessionRegistry != nil {
		a.sessionRegistry.Stop()
	}

	// Reset internal state to allow for clean restart
	a.mu.Lock()
	a.mcpServer = nil
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

// GetSessionRegistry returns the session registry for per-session state management.
//
// This method provides access to the session registry for managing per-session
// connections to OAuth-protected servers. It is used for session-scoped tool
// visibility and connection management.
//
// Returns the SessionRegistry instance managing per-session state.
func (a *AggregatorServer) GetSessionRegistry() *SessionRegistry {
	return a.sessionRegistry
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
// tools, which notifies other muster components (like ServiceClass
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

	// Publish the event - this will notify ServiceClass managers
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
	if a.mcpServer == nil {
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
			a.mcpServer.DeleteTools(items...)
		},
	)

	// Remove obsolete prompts using batch operation
	removeObsoleteItems(
		a.promptManager,
		collected.newPrompts,
		func(items []string) {
			a.mcpServer.DeletePrompts(items...)
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
				a.mcpServer.RemoveResource(uri)
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
//   - Processing auth_required servers for synthetic authentication tools
//   - Integrating core tools from muster components (workflow, etc.)
//   - Creating MCP-compatible handlers for all new items
//   - Batch registration to minimize client notifications
//
// Args:
//   - servers: Map of all registered backend servers and their information
func (a *AggregatorServer) addNewItems(servers map[string]*ServerInfo) {
	var toolsToAdd []mcpserver.ServerTool
	var promptsToAdd []mcpserver.ServerPrompt
	var resourcesToAdd []mcpserver.ServerResource

	// Process each registered backend server
	for serverName, info := range servers {
		// Handle servers requiring authentication - add synthetic auth tools
		if info.Status == StatusAuthRequired {
			toolsToAdd = append(toolsToAdd, processToolsForServer(a, serverName, info)...)
			continue
		}

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

	// Add tools from core muster components (workflow, etc.)
	toolsToAdd = append(toolsToAdd, a.createToolsFromProviders()...)

	// Register all new items in batches to minimize client notifications
	if len(toolsToAdd) > 0 {
		logging.Debug("Aggregator", "Adding %d tools in batch", len(toolsToAdd))
		a.mcpServer.AddTools(toolsToAdd...)
	}

	if len(promptsToAdd) > 0 {
		logging.Debug("Aggregator", "Adding %d prompts in batch", len(promptsToAdd))
		a.mcpServer.AddPrompts(promptsToAdd...)
	}

	if len(resourcesToAdd) > 0 {
		logging.Debug("Aggregator", "Adding %d resources in batch", len(resourcesToAdd))
		a.mcpServer.AddResources(resourcesToAdd...)
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

// createHTTPMux creates an HTTP mux that routes to both MCP and OAuth handlers.
// This allows the aggregator to serve both MCP protocol traffic and OAuth callbacks
// on the same port.
//
// If OAuth server protection is enabled (OAuthServer.Enabled), the MCP handler is
// wrapped with OAuth ValidateToken middleware, requiring valid access tokens for
// all MCP requests.
//
// Returns an error if OAuth is enabled but cannot be initialized (security requirement).
func (a *AggregatorServer) createHTTPMux(mcpHandler http.Handler) (http.Handler, error) {
	// Check if OAuth server protection is enabled
	if a.config.OAuthServer.Enabled && a.config.OAuthServer.Config != nil {
		return a.createOAuthProtectedMux(mcpHandler)
	}

	// Standard mux without OAuth server protection
	return a.createStandardMux(mcpHandler), nil
}

// createStandardMux creates a standard HTTP mux without OAuth server protection.
// This is used when OAuth server protection is disabled.
func (a *AggregatorServer) createStandardMux(mcpHandler http.Handler) http.Handler {
	mux := http.NewServeMux()

	// Health check endpoint for Kubernetes probes
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Check if OAuth proxy is enabled and mount OAuth-related handlers (for downstream auth)
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler != nil && oauthHandler.IsEnabled() {
		// Mount the OAuth callback handler
		callbackPath := oauthHandler.GetCallbackPath()
		if callbackPath != "" {
			mux.Handle(callbackPath, oauthHandler.GetHTTPHandler())
			logging.Info("Aggregator", "Mounted OAuth callback handler at %s", callbackPath)
		}

		// Mount the CIMD handler if self-hosting is enabled
		if oauthHandler.ShouldServeCIMD() {
			cimdPath := oauthHandler.GetCIMDPath()
			cimdHandler := oauthHandler.GetCIMDHandler()
			if cimdPath != "" && cimdHandler != nil {
				mux.HandleFunc(cimdPath, cimdHandler)
				logging.Info("Aggregator", "Mounted self-hosted CIMD at %s", cimdPath)
			}
		}
	}

	// Mount the MCP handler as the default for all other paths
	// Wrap with clientSessionIDMiddleware to enable CLI session persistence
	mux.Handle("/", clientSessionIDMiddleware(mcpHandler))

	return mux
}

// createOAuthProtectedMux creates an HTTP mux with OAuth 2.1 protection.
// All MCP endpoints are protected by the ValidateToken middleware.
//
// SECURITY: This function returns an error instead of silently falling back to
// an unprotected mux. When OAuth is enabled, authentication MUST work - running
// without authentication is a security vulnerability.
func (a *AggregatorServer) createOAuthProtectedMux(mcpHandler http.Handler) (http.Handler, error) {
	// Import the config type and create OAuth HTTP server
	cfg, ok := a.config.OAuthServer.Config.(config.OAuthServerConfig)
	if !ok {
		return nil, fmt.Errorf("invalid OAuth server config type: expected OAuthServerConfig")
	}

	// Wrap the MCP handler with clientSessionIDMiddleware to enable CLI session persistence
	// This must be done before OAuth middleware so the session ID is available in context
	wrappedHandler := clientSessionIDMiddleware(mcpHandler)

	oauthHTTPServer, err := server.NewOAuthHTTPServer(cfg, wrappedHandler, a.config.Debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth HTTP server: %w", err)
	}

	// Store the OAuth HTTP server for cleanup during shutdown
	a.oauthHTTPServer = oauthHTTPServer

	logging.Info("Aggregator", "OAuth 2.1 server protection enabled (BaseURL: %s)", cfg.BaseURL)
	return oauthHTTPServer.CreateMux(), nil
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
// Note: This returns the global tool view. For session-specific tool visibility,
// use GetToolsForSession instead.
//
// Returns a slice of MCP tools ready for client consumption.
func (a *AggregatorServer) GetTools() []mcp.Tool {
	return a.registry.GetAllTools()
}

// GetToolsForSession returns a session-specific view of all available tools.
//
// This method computes the tool view based on the session's authentication state:
//   - Global tools from servers that don't require authentication
//   - Tools from OAuth servers where the session has authenticated
//   - Synthetic auth tools for OAuth servers the session hasn't authenticated with
//
// This implements per-session tool visibility as described in ADR-006.
//
// Args:
//   - sessionID: The session to compute the tool view for
//
// Returns a slice of MCP tools specific to this session.
func (a *AggregatorServer) GetToolsForSession(sessionID string) []mcp.Tool {
	return a.registry.GetAllToolsForSession(a.sessionRegistry, sessionID)
}

// GetResourcesForSession returns a session-specific view of all available resources.
//
// Similar to GetToolsForSession, this returns resources based on session authentication state.
func (a *AggregatorServer) GetResourcesForSession(sessionID string) []mcp.Resource {
	return a.registry.GetAllResourcesForSession(a.sessionRegistry, sessionID)
}

// GetPromptsForSession returns a session-specific view of all available prompts.
//
// Similar to GetToolsForSession, this returns prompts based on session authentication state.
func (a *AggregatorServer) GetPromptsForSession(sessionID string) []mcp.Prompt {
	return a.registry.GetAllPromptsForSession(a.sessionRegistry, sessionID)
}

// sessionToolFilter is the WithToolFilter callback that provides session-specific tool views.
//
// This is a critical part of the session-scoped tool visibility feature (ADR-006).
// When a client calls tools/list, this filter intercepts the request and returns
// only the tools that the specific session is authorized to see based on their
// OAuth authentication state.
//
// The filter:
//   - Extracts the session ID from the MCP context
//   - Includes core muster tools (workflow, config, service, etc.)
//   - Returns session-specific MCP server tools via GetToolsForSession
//   - For non-OAuth servers, returns global tools
//   - For OAuth servers, returns tools only if the session has authenticated
//
// Args:
//   - ctx: Context containing the MCP session information
//   - _ : The global tools list (ignored - we compute session-specific tools instead)
//
// Returns a slice of MCP tools specific to the requesting session.
func (a *AggregatorServer) sessionToolFilter(ctx context.Context, _ []mcp.Tool) []mcp.Tool {
	sessionID := getSessionIDFromContext(ctx)

	// Get session-specific MCP server tools (handles OAuth auth state)
	mcpServerTools := a.GetToolsForSession(sessionID)

	// Get core muster tools (these are always available to all sessions)
	coreServerTools := a.createToolsFromProviders()

	// Combine both sets of tools
	allTools := make([]mcp.Tool, 0, len(mcpServerTools)+len(coreServerTools))
	allTools = append(allTools, mcpServerTools...)

	// Convert core ServerTools to mcp.Tool
	for _, serverTool := range coreServerTools {
		allTools = append(allTools, serverTool.Tool)
	}

	logging.Debug("Aggregator", "sessionToolFilter: returning %d tools (%d mcp server, %d core) for session %s",
		len(allTools), len(mcpServerTools), len(coreServerTools), logging.TruncateSessionID(sessionID))

	return allTools
}

// NotifySessionToolsChanged sends a tools/list_changed notification to a specific session.
//
// This method is used to notify a specific session that their tool list has changed,
// typically after they complete OAuth authentication with a new server. This implements
// targeted notifications as described in ADR-006, avoiding broadcast to all sessions.
//
// Args:
//   - sessionID: The session to notify
func (a *AggregatorServer) NotifySessionToolsChanged(sessionID string) {
	a.mu.RLock()
	mcpServer := a.mcpServer
	a.mu.RUnlock()

	if mcpServer == nil {
		logging.Warn("Aggregator", "Cannot notify session %s: MCP server not initialized",
			logging.TruncateSessionID(sessionID))
		return
	}

	// Send targeted notification to the specific session
	err := mcpServer.SendNotificationToSpecificClient(
		sessionID,
		"notifications/tools/list_changed",
		nil,
	)
	if err != nil {
		logging.Warn("Aggregator", "Failed to send tools/list_changed notification to session %s: %v",
			logging.TruncateSessionID(sessionID), err)
	} else {
		logging.Debug("Aggregator", "Sent tools/list_changed notification to session %s",
			logging.TruncateSessionID(sessionID))
	}
}

// NotifySessionResourcesChanged sends a resources/list_changed notification to a specific session.
func (a *AggregatorServer) NotifySessionResourcesChanged(sessionID string) {
	a.mu.RLock()
	mcpServer := a.mcpServer
	a.mu.RUnlock()

	if mcpServer == nil {
		return
	}

	err := mcpServer.SendNotificationToSpecificClient(
		sessionID,
		"notifications/resources/list_changed",
		nil,
	)
	if err != nil {
		logging.Warn("Aggregator", "Failed to send resources/list_changed notification to session %s: %v",
			logging.TruncateSessionID(sessionID), err)
	}
}

// registerSessionTools registers tools from an OAuth-protected server connection with the mcp-go server.
// This ensures that tools from session-specific connections have handlers registered and can be called.
// The handler routes calls through the session's connection client.
func (a *AggregatorServer) registerSessionTools(serverName string, tools []mcp.Tool) {
	a.mu.RLock()
	mcpServer := a.mcpServer
	a.mu.RUnlock()

	if mcpServer == nil {
		logging.Warn("Aggregator", "Cannot register session tools - MCP server not available")
		return
	}

	var toolsToAdd []mcpserver.ServerTool
	for _, tool := range tools {
		// Apply the standard tool name prefixing
		exposedName := a.registry.nameTracker.GetExposedToolName(serverName, tool.Name)

		// Check if already registered
		if a.toolManager.isActive(exposedName) {
			continue
		}

		// Mark as active
		a.toolManager.setActive(exposedName, true)

		// Create the tool with a handler that routes through session connections
		serverTool := mcpserver.ServerTool{
			Tool: mcp.Tool{
				Name:        exposedName,
				Description: tool.Description,
				InputSchema: tool.InputSchema,
			},
			Handler: toolHandlerFactory(a, exposedName),
		}
		toolsToAdd = append(toolsToAdd, serverTool)
	}

	if len(toolsToAdd) > 0 {
		logging.Debug("Aggregator", "Registering %d session-specific tools for server %s", len(toolsToAdd), serverName)
		mcpServer.AddTools(toolsToAdd...)
	}
}

// RegisterSyntheticAuthToolSync is deprecated and does nothing.
// Per ADR-008, synthetic auth tools are no longer created. Use core_auth_login instead.
//
// TODO(cleanup): Remove this function and its call site in manager.go after confirming
// no external callers depend on it. Tracked as part of ADR-008 cleanup.
//
// Deprecated: Synthetic auth tools are no longer used. This function is kept for
// backward compatibility and will be removed in a future version.
func (a *AggregatorServer) RegisterSyntheticAuthToolSync(serverName string) {
	// ADR-008: No more synthetic auth tools
	logging.Debug("Aggregator", "RegisterSyntheticAuthToolSync is deprecated, use core_auth_login instead (server: %s)", serverName)
}

// NotifySessionPromptsChanged sends a prompts/list_changed notification to a specific session.
func (a *AggregatorServer) NotifySessionPromptsChanged(sessionID string) {
	a.mu.RLock()
	mcpServer := a.mcpServer
	a.mu.RUnlock()

	if mcpServer == nil {
		return
	}

	err := mcpServer.SendNotificationToSpecificClient(
		sessionID,
		"notifications/prompts/list_changed",
		nil,
	)
	if err != nil {
		logging.Warn("Aggregator", "Failed to send prompts/list_changed notification to session %s: %v",
			logging.TruncateSessionID(sessionID), err)
	}
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
		if _, origName, err := a.registry.ResolveToolName(tool.Name); err == nil {
			originalName = origName
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
//  2. If not found, checks if it's a core tool by name pattern
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

	// If not found in registry, check if it's a core tool by name pattern
	// This avoids the deadlock that can occur when calling createToolsFromProviders()
	// during workflow execution
	if a.isCoreToolByName(toolName) {
		logging.Debug("Aggregator", "Tool %s matches core tool pattern, calling directly", toolName)
		return a.callCoreToolDirectly(ctx, toolName, args)
	}

	logging.Debug("Aggregator", "Tool %s not found in registry or core tools", toolName)
	return nil, fmt.Errorf("tool not found: %s", toolName)
}

// isCoreToolByName checks if a tool name matches the pattern of core tools
// without needing to recreate the tool list (which can cause deadlocks)
func (a *AggregatorServer) isCoreToolByName(toolName string) bool {
	// Core tools have specific naming patterns based on their prefix
	coreToolPrefixes := []string{
		"core_workflow_",
		"core_service_",
		"core_config_",
		"core_serviceclass_",
		"core_mcpserver_",
		"core_events",
		"core_auth_", // Authentication tools (core_auth_login, core_auth_logout)
		"workflow_",  // Direct workflow execution tools
	}

	for _, prefix := range coreToolPrefixes {
		if strings.HasPrefix(toolName, prefix) {
			return true
		}
	}

	return false
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
			// Check if this is a workflow management tool or a workflow execution tool
			managementTools := []string{"workflow_list", "workflow_get", "workflow_create",
				"workflow_update", "workflow_delete", "workflow_validate", "workflow_available",
				"workflow_execution_list", "workflow_execution_get"}

			isManagementTool := false
			for _, mgmtTool := range managementTools {
				if originalToolName == mgmtTool {
					isManagementTool = true
					break
				}
			}

			if isManagementTool {
				// Use the original tool name for workflow management tools
				logging.Debug("Aggregator", "Calling workflow management tool %s directly", originalToolName)
				result, err := provider.ExecuteTool(ctx, originalToolName, args)
				if err != nil {
					return nil, err
				}
				return convertToMCPResult(result), nil
			} else {
				// This is a workflow execution tool - map workflow_ to action_
				actionToolName := strings.Replace(originalToolName, "workflow_", "action_", 1)
				logging.Debug("Aggregator", "Mapping workflow execution tool %s to action tool %s", originalToolName, actionToolName)
				result, err := provider.ExecuteTool(ctx, actionToolName, args)
				if err != nil {
					return nil, err
				}
				return convertToMCPResult(result), nil
			}
		}
		return nil, fmt.Errorf("workflow handler does not implement ToolProvider interface")

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
		return nil, fmt.Errorf("service manager does not implement ToolProvider interface")

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
		return nil, fmt.Errorf("config handler does not implement ToolProvider interface")

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
		return nil, fmt.Errorf("service class manager does not implement ToolProvider interface")

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
		return nil, fmt.Errorf("MCP server manager does not implement ToolProvider interface")

	case originalToolName == "events":
		// Event management operations
		handler := api.GetEventManager()
		if handler == nil {
			return nil, fmt.Errorf("event manager handler not available")
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			result, err := provider.ExecuteTool(ctx, originalToolName, args)
			if err != nil {
				return nil, err
			}
			return convertToMCPResult(result), nil
		}
		return nil, fmt.Errorf("event manager does not implement ToolProvider interface")

	case strings.HasPrefix(originalToolName, "auth_"):
		// Authentication operations (auth_login, auth_logout)
		authProvider := NewAuthToolProvider(a)
		result, err := authProvider.ExecuteTool(ctx, originalToolName, args)
		if err != nil {
			return nil, err
		}
		return convertToMCPResult(result), nil

	default:
		return nil, fmt.Errorf("no handler found for core tool: %s", originalToolName)
	}
}

// IsToolAvailable implements the ToolAvailabilityChecker interface.
//
// This method determines whether a specific tool is available through the aggregator,
// checking both external backend servers (via the registry) and core muster tools
// (via name pattern matching). It provides a unified way for other components to
// verify tool availability before attempting to use them.
//
// The check process:
//  1. Attempts to resolve the tool through the server registry
//  2. If not found, checks if it matches core tool naming patterns
//  3. Returns true if found in either location
//
// This method is used by:
//   - Workflow manager for validating workflow step tools
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

	// Check if it's a core tool by name pattern (avoid deadlock)
	if a.isCoreToolByName(toolName) {
		return true // Found in core tools
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
		// Execute asynchronously to avoid blocking the event publisher and to ensure
		// the publisher has completed its operation before we query it for tools.
		// The goroutine scheduling provides the necessary separation without explicit delays.
		go a.updateCapabilities()
	}
}

// handleSyntheticAuthTool is deprecated and should not be used.
// Per ADR-008, synthetic authentication tools are no longer created.
// Users should use core_auth_login with a server parameter instead.
//
// TODO(cleanup): Remove this function after confirming no external callers depend on it.
// Tracked as part of ADR-008 cleanup.
//
// Deprecated: Use core_auth_login tool instead. This function is kept for
// backward compatibility but will be removed in a future version.
//
// Args:
//   - ctx: Context for the operation
//   - serverName: Name of the server requiring authentication
//
// Returns a success message pointing users to core_auth_login.
func (a *AggregatorServer) handleSyntheticAuthTool(ctx context.Context, serverName string) (*mcp.CallToolResult, error) {
	// ADR-008: Redirect users to core_auth_login
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(fmt.Sprintf(
				"Deprecated: This authentication method is no longer supported.\n\n"+
					"Please use the core_auth_login tool instead:\n"+
					"  core_auth_login with server='%s'\n\n"+
					"This provides a unified authentication experience across all MCP servers.",
				serverName,
			)),
		},
		IsError: false,
	}, nil
}

// tryConnectWithToken attempts to establish a connection to an MCP server using an OAuth token.
// On success, it upgrades the session connection and returns a success result.
// On failure, it returns an error that the caller can use to determine next steps.
//
// This method delegates to the shared establishSessionConnection helper to avoid code duplication.
// The issuer and scope parameters are used to create a TokenProvider that can refresh
// the token when it expires (Issue #214).
func (a *AggregatorServer) tryConnectWithToken(ctx context.Context, sessionID, serverName, serverURL, issuer, scope, accessToken string) (*mcp.CallToolResult, error) {
	result, err := establishSessionConnection(ctx, a, sessionID, serverName, serverURL, issuer, scope, accessToken)
	if err != nil {
		return nil, err
	}
	return result.FormatAsMCPResult(), nil
}

// ProtectedResourceMetadata contains OAuth information discovered from
// the /.well-known/oauth-protected-resource endpoint.
type ProtectedResourceMetadata struct {
	// Issuer is the authorization server URL
	Issuer string
	// Scope is the space-separated list of required scopes
	Scope string
}

// discoverProtectedResourceMetadata fetches OAuth information from
// the server's /.well-known/oauth-protected-resource endpoint.
// This follows the MCP OAuth specification for resource metadata discovery (RFC 9728).
func discoverProtectedResourceMetadata(ctx context.Context, serverURL string) (*ProtectedResourceMetadata, error) {
	// Build the resource metadata URL
	baseURL := strings.TrimSuffix(serverURL, "/")
	// Remove /mcp suffix if present (common for MCP servers)
	baseURL = strings.TrimSuffix(baseURL, "/mcp")
	resourceMetadataURL := baseURL + "/.well-known/oauth-protected-resource"

	req, err := http.NewRequestWithContext(ctx, "GET", resourceMetadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch resource metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resource metadata returned status %d", resp.StatusCode)
	}

	// Parse the response per RFC 9728
	var metadata struct {
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse resource metadata: %w", err)
	}

	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers in resource metadata")
	}

	result := &ProtectedResourceMetadata{
		Issuer: metadata.AuthorizationServers[0],
	}

	// Join all supported scopes with space separator
	if len(metadata.ScopesSupported) > 0 {
		result.Scope = strings.Join(metadata.ScopesSupported, " ")
	}

	return result, nil
}

// discoverAuthorizationServer is a convenience wrapper that returns just the issuer.
// Deprecated: Use discoverProtectedResourceMetadata for full information including scope.
func discoverAuthorizationServer(ctx context.Context, serverURL string) (string, error) {
	metadata, err := discoverProtectedResourceMetadata(ctx, serverURL)
	if err != nil {
		return "", err
	}
	return metadata.Issuer, nil
}

// defaultSessionID is used for stdio transport which is inherently single-user.
// This constant is used to identify tokens stored for the default session.
const defaultSessionID = "default-session"

// clientSessionIDContextKey is the context key for storing client-provided session IDs.
type clientSessionIDContextKey struct{}

// getSessionIDFromContext extracts the session ID from context.
//
// Session ID precedence (first match wins):
//  1. Client-provided session ID via X-Muster-Session-ID header (enables CLI persistence)
//  2. MCP session ID from mcp-go library (per-connection, random UUID)
//  3. Default session ID for stdio transport (single-user mode)
//
// The client-provided session ID (via header) is critical for CLI tools where each
// invocation creates a new connection. Without it, MCP server tokens would be lost
// between CLI invocations because the mcp-go session ID changes on each connection.
// See ADR-004 for the design rationale.
//
// SECURITY: The client-provided session ID is trusted because:
//   - It's sent by the authenticated CLI client (aggregator auth validates the user)
//   - Token lookup still requires matching (sessionID, issuer, scope)
//   - A malicious client can only access tokens it previously stored with that session ID
func getSessionIDFromContext(ctx context.Context) string {
	// 1. Check for client-provided session ID (CLI persistence)
	if clientSessionID, ok := ctx.Value(clientSessionIDContextKey{}).(string); ok && clientSessionID != "" {
		return clientSessionID
	}

	// 2. Try to get session ID from MCP client session (set by mcp-go library)
	if session := mcpserver.ClientSessionFromContext(ctx); session != nil {
		sessionID := session.SessionID()
		if sessionID != "" {
			return sessionID
		}
	}

	// 3. Fall back to default session ID for stdio transport only.
	// This is a security limitation for stdio which is inherently single-user.
	// For HTTP transports (SSE/Streamable HTTP), the mcp-go library always provides
	// a unique session ID, so this fallback should only trigger for stdio.
	logging.Warn("OAuth", "No MCP session in context, using default session (stdio mode). "+
		"Token isolation is not enforced for stdio transport.")
	return defaultSessionID
}

// clientSessionIDMiddleware extracts the X-Muster-Session-ID header and adds it to context.
// This middleware enables CLI tools to maintain persistent session identity across invocations.
func clientSessionIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientSessionID := r.Header.Get(api.ClientSessionIDHeader)
		if clientSessionID != "" {
			logging.Debug("Aggregator", "Client provided session ID: %s", logging.TruncateSessionID(clientSessionID))
			ctx := context.WithValue(r.Context(), clientSessionIDContextKey{}, clientSessionID)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// resolveSessionTool attempts to resolve a tool name through session connections.
// This is used for OAuth-protected servers where tools are stored per-session.
//
// Returns the server name, session-specific client, original tool name, or an error if not found.
func (a *AggregatorServer) resolveSessionTool(sessionID, exposedName string) (string, MCPClient, string, error) {
	session := a.sessionRegistry.GetOrCreateSession(sessionID)

	// Iterate through all session connections to find the tool
	for serverName, conn := range session.GetAllConnections() {
		if conn.Status != StatusSessionConnected || conn.Client == nil {
			continue
		}

		// Check if this connection has the requested tool
		for _, tool := range conn.GetTools() {
			exposedToolName := a.registry.nameTracker.GetExposedToolName(serverName, tool.Name)
			if exposedToolName == exposedName {
				return serverName, conn.Client, tool.Name, nil
			}
		}
	}

	return "", nil, "", fmt.Errorf("tool not found in session connections")
}
