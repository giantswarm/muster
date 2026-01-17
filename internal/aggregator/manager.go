package aggregator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"muster/internal/api"
	configPkg "muster/internal/config"
	"muster/internal/mcpserver"
	"muster/internal/oauth"
	"muster/pkg/logging"
)

// This file contains aggregator manager logic that coordinates between
// the aggregator server and event handling to provide automatic MCP
// server registration based on health status.

// AggregatorManager provides a high-level interface for managing the aggregator server
// and coordinating automatic MCP server registration based on service health status.
//
// The manager combines the aggregator server with event handling to provide:
//   - Automatic registration of healthy MCP servers
//   - Event-driven updates when service states change
//   - Periodic retry mechanisms for failed registrations
//   - Centralized lifecycle management
//   - OAuth proxy for remote MCP server authentication
//
// It acts as the primary entry point for the aggregator functionality and
// integrates with the muster service architecture through the central API pattern.
type AggregatorManager struct {
	mu     sync.RWMutex
	config AggregatorConfig

	// External dependencies - accessed through the central API pattern
	orchestratorAPI api.OrchestratorAPI
	serviceRegistry api.ServiceRegistryHandler

	// Internal components
	aggregatorServer *AggregatorServer // The core MCP server that exposes aggregated capabilities
	eventHandler     *EventHandler     // Handles service state change events
	oauthManager     *oauth.Manager    // OAuth proxy for remote MCP server authentication

	// Lifecycle management
	ctx        context.Context    // Context for coordinating shutdown
	cancelFunc context.CancelFunc // Function to cancel the context
}

// NewAggregatorManager creates a new aggregator manager with the specified configuration.
//
// The manager requires access to the orchestrator API for receiving service state events
// and the service registry for querying service information. These dependencies are
// provided through the central API pattern to maintain loose coupling.
//
// Args:
//   - config: Configuration for the aggregator server behavior
//   - orchestratorAPI: Interface for receiving service lifecycle events
//   - serviceRegistry: Interface for querying service information
//
// Returns a configured but not yet started aggregator manager.
func NewAggregatorManager(config AggregatorConfig, orchestratorAPI api.OrchestratorAPI, serviceRegistry api.ServiceRegistryHandler, errorCallback func(err error)) *AggregatorManager {
	manager := &AggregatorManager{
		config:          config,
		orchestratorAPI: orchestratorAPI,
		serviceRegistry: serviceRegistry,
	}

	// Create the aggregator server with the provided configuration
	manager.aggregatorServer = NewAggregatorServer(config, errorCallback)

	// Initialize OAuth manager if enabled
	if config.OAuth.Enabled {
		oauthConfig := configPkg.OAuthConfig{
			Enabled:      config.OAuth.Enabled,
			PublicURL:    config.OAuth.PublicURL,
			ClientID:     config.OAuth.ClientID,
			CallbackPath: config.OAuth.CallbackPath,
		}
		manager.oauthManager = oauth.NewManager(oauthConfig)

		if manager.oauthManager != nil {
			// Register OAuth handler with the API layer
			oauthAdapter := oauth.NewAdapter(manager.oauthManager)
			oauthAdapter.Register()
			logging.Info("Aggregator-Manager", "OAuth proxy enabled with public URL: %s", config.OAuth.PublicURL)

			// Register the auth completion callback to establish session connections
			// after browser OAuth completes
			manager.oauthManager.SetAuthCompletionCallback(manager.handleAuthCompletion)
		}
	}

	return manager
}

// Start initializes and starts the aggregator manager.
//
// This method performs the following initialization sequence:
//  1. Starts the underlying aggregator server
//  2. Validates that required APIs are available
//  3. Performs initial sync of healthy MCP servers
//  4. Sets up event handling for automatic updates
//  5. Starts periodic retry mechanism for failed registrations
//
// The method is idempotent - calling it multiple times has no additional effect.
// Returns an error if any component fails to start.
func (am *AggregatorManager) Start(ctx context.Context) error {

	// Validate that required APIs are available
	if am.orchestratorAPI == nil {
		return fmt.Errorf("required APIs not available")
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	// Create cancellable context for coordinating shutdown
	am.ctx, am.cancelFunc = context.WithCancel(ctx)

	// Start the aggregator server first
	if err := am.aggregatorServer.Start(am.ctx); err != nil {
		return fmt.Errorf("failed to start aggregator server: %w", err)
	}

	// Perform initial synchronization: Register all healthy running MCP servers
	if err := am.registerHealthyMCPServers(am.ctx); err != nil {
		logging.Warn("Aggregator-Manager", "Error during initial MCP server registration: %v", err)
		// Continue anyway - the event handler will handle future registrations
	}

	// Create event handler with callbacks for registration/deregistration
	am.eventHandler = NewEventHandler(
		am.orchestratorAPI,
		am.registerSingleServer,
		am.deregisterSingleServer,
		am.isServerAuthRequired,
	)

	// Start the event handler for automatic updates
	if err := am.eventHandler.Start(am.ctx); err != nil {
		// Stop the aggregator server if event handler fails
		am.aggregatorServer.Stop(am.ctx)
		return fmt.Errorf("failed to start event handler: %w", err)
	}

	// Start periodic retry mechanism for failed registrations
	go am.retryFailedRegistrations(am.ctx)

	logging.Info("Aggregator-Manager", "Started aggregator manager on %s", am.aggregatorServer.GetEndpoint())
	return nil
}

// Stop gracefully shuts down the aggregator manager.
//
// This method stops all components in reverse order of startup:
//  1. Cancels the context to signal shutdown to all goroutines
//  2. Stops the event handler
//  3. Stops the OAuth manager
//  4. Stops the aggregator server
//  5. Waits for all background operations to complete
//
// The method is idempotent and can be called multiple times safely.
func (am *AggregatorManager) Stop(ctx context.Context) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Cancel context to signal shutdown to all components
	if am.cancelFunc != nil {
		am.cancelFunc()
	}

	// Stop event handler first to prevent new registrations
	if am.eventHandler != nil {
		if err := am.eventHandler.Stop(); err != nil {
			logging.Error("Aggregator-Manager", err, "Error stopping event handler")
		}
	}

	// Stop OAuth manager
	if am.oauthManager != nil {
		am.oauthManager.Stop()
	}

	// Stop aggregator server and wait for graceful shutdown
	if am.aggregatorServer != nil {
		if err := am.aggregatorServer.Stop(ctx); err != nil {
			logging.Error("Aggregator-Manager", err, "Error stopping aggregator server")
		}
	}

	logging.Info("Aggregator-Manager", "Stopped aggregator manager")
	return nil
}

// GetServiceData returns comprehensive service monitoring data.
//
// This method provides detailed information about the aggregator's current state,
// including configuration, connection status, tool/resource/prompt counts,
// and server statistics. The data is suitable for monitoring dashboards
// and health checks.
//
// Returns a map containing various metrics and status information.
func (am *AggregatorManager) GetServiceData() map[string]interface{} {
	am.mu.RLock()
	defer am.mu.RUnlock()

	data := map[string]interface{}{
		"port": am.config.Port,
		"host": am.config.Host,
		"yolo": am.config.Yolo,
	}

	// Add aggregator server metrics if available
	if am.aggregatorServer != nil {
		data["endpoint"] = am.aggregatorServer.GetEndpoint()

		// Get capability counts
		tools := am.aggregatorServer.GetTools()
		resources := am.aggregatorServer.GetResources()
		prompts := am.aggregatorServer.GetPrompts()

		data["tools"] = len(tools)
		data["resources"] = len(resources)
		data["prompts"] = len(prompts)

		// Get detailed tool status information
		toolsWithStatus := am.aggregatorServer.GetToolsWithStatus()
		data["tools_with_status"] = toolsWithStatus

		// Count blocked tools for security monitoring
		blockedCount := 0
		for _, t := range toolsWithStatus {
			if t.Blocked {
				blockedCount++
			}
		}
		data["blocked_tools"] = blockedCount

		// Calculate server connectivity statistics
		totalServers := 0
		connectedServers := 0

		if am.serviceRegistry != nil {
			// Get all MCP services from the registry
			allServices := am.serviceRegistry.GetByType(api.TypeMCPServer)
			totalServers = len(allServices)

			// Count healthy running services (these have ready clients)
			for _, service := range allServices {
				if service.GetState() == api.StateRunning && service.GetHealth() == api.HealthHealthy {
					connectedServers++
				}
			}
		}

		data["servers_total"] = totalServers
		data["servers_connected"] = connectedServers
	}

	// Add event handler status
	if am.eventHandler != nil {
		data["event_handler_running"] = am.eventHandler.IsRunning()
	}

	return data
}

// registerHealthyMCPServers performs initial synchronization by registering all
// currently healthy and running MCP servers.
//
// This method is called during startup to ensure that existing healthy servers
// are immediately available through the aggregator. It only registers servers
// that are both running and healthy, as this guarantees that their MCP clients
// are ready for use.
//
// Returns an error if the service registry is unavailable, but continues
// processing even if individual server registrations fail.
func (am *AggregatorManager) registerHealthyMCPServers(ctx context.Context) error {
	if am.serviceRegistry == nil {
		return fmt.Errorf("service registry not available")
	}

	// Get all MCP services from the registry
	mcpServices := am.serviceRegistry.GetByType(api.TypeMCPServer)

	registeredCount := 0
	for _, service := range mcpServices {
		// Only register servers that are running AND healthy (client is guaranteed ready)
		if service.GetState() != api.StateRunning || service.GetHealth() != api.HealthHealthy {
			continue
		}

		// Attempt to register the healthy server
		if err := am.registerSingleServer(ctx, service.GetName()); err != nil {
			logging.Warn("Aggregator-Manager", "Failed to register healthy MCP server %s: %v",
				service.GetName(), err)
			// Continue with other servers
		} else {
			registeredCount++
		}
	}

	if registeredCount > 0 {
		logging.Info("Aggregator-Manager", "Initial sync completed: registered %d healthy MCP servers", registeredCount)
	}

	return nil
}

// registerSingleServer registers a single MCP server with the aggregator.
//
// This method is called when a server becomes healthy and running. Since the
// service architecture guarantees that running+healthy services have ready
// MCP clients, this method can safely extract and use the client immediately.
//
// If the server returns a 401 during initialization, the method will register
// the server in auth_required state with a synthetic authentication tool.
//
// Args:
//   - ctx: Context for the registration operation
//   - serverName: Unique name of the server to register
//
// Returns an error if the server cannot be found, has no client, or
// registration with the aggregator fails.
func (am *AggregatorManager) registerSingleServer(ctx context.Context, serverName string) error {
	// Get the service from registry
	service, exists := am.serviceRegistry.Get(serverName)
	if !exists {
		return fmt.Errorf("service %s not found", serverName)
	}

	// Get service data - this contains the MCP client and configuration
	serviceData := service.GetServiceData()
	if serviceData == nil {
		return fmt.Errorf("no service data available for %s", serverName)
	}

	// Extract tool prefix from service configuration
	toolPrefix, _ := serviceData["toolPrefix"].(string)

	// Get MCP client from service data - this is the authoritative source
	clientInterface, exists := serviceData["client"]
	if !exists || clientInterface == nil {
		return fmt.Errorf("no MCP client available for %s (service state inconsistent)", serverName)
	}

	mcpClient, ok := clientInterface.(MCPClient)
	if !ok {
		return fmt.Errorf("invalid MCP client type for %s", serverName)
	}

	// Register with the aggregator
	if err := am.aggregatorServer.RegisterServer(ctx, serverName, mcpClient, toolPrefix); err != nil {
		return fmt.Errorf("failed to register server: %w", err)
	}

	logging.Info("Aggregator-Manager", "Successfully registered MCP server %s with prefix %s", serverName, toolPrefix)
	return nil
}

// RegisterServerPendingAuth registers a server that requires authentication.
// This creates a placeholder with a synthetic auth tool that users can call
// to initiate the OAuth flow.
//
// Args:
//   - serverName: Unique name of the server
//   - url: The server endpoint URL
//   - toolPrefix: Server-specific prefix for tools
//   - authInfo: OAuth information from the 401 response
//
// Returns an error if registration fails.
func (am *AggregatorManager) RegisterServerPendingAuth(serverName, url, toolPrefix string, authInfo *AuthInfo) error {
	return am.RegisterServerPendingAuthWithConfig(serverName, url, toolPrefix, authInfo, nil)
}

// RegisterServerPendingAuthWithConfig registers a server that requires authentication with auth config.
// This is an extended version that also accepts auth config for SSO token forwarding.
//
// Args:
//   - serverName: Unique name of the server
//   - url: The server endpoint URL
//   - toolPrefix: Server-specific prefix for tools
//   - authInfo: OAuth information from the 401 response
//   - authConfig: Auth configuration for token forwarding (may be nil)
//
// Returns an error if registration fails.
func (am *AggregatorManager) RegisterServerPendingAuthWithConfig(serverName, url, toolPrefix string, authInfo *AuthInfo, authConfig *api.MCPServerAuth) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.aggregatorServer == nil {
		return fmt.Errorf("aggregator server not available")
	}

	if err := am.aggregatorServer.GetRegistry().RegisterPendingAuthWithConfig(serverName, url, toolPrefix, authInfo, authConfig); err != nil {
		return err
	}

	// Per ADR-008: No synthetic auth tools are created.
	// Users authenticate via core_auth_login tool with server parameter.

	return nil
}

// UpgradeServerAfterAuth upgrades a pending auth server to connected status
// after successful OAuth authentication. This is called when the OAuth callback
// is received and a token is available.
//
// Note: This method upgrades the global server state. For session-scoped tool
// visibility, use UpgradeSessionConnection instead.
//
// Args:
//   - ctx: Context for the operation
//   - serverName: Name of the server to upgrade
//   - client: The newly authenticated MCP client
//
// Returns an error if upgrade fails.
func (am *AggregatorManager) UpgradeServerAfterAuth(ctx context.Context, serverName string, client MCPClient) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.aggregatorServer == nil {
		return fmt.Errorf("aggregator server not available")
	}

	err := am.aggregatorServer.GetRegistry().UpgradeToConnected(ctx, serverName, client)
	if err != nil {
		return err
	}

	// Trigger capability update to register the real tools
	am.aggregatorServer.UpdateCapabilities()

	logging.Info("Aggregator-Manager", "Server %s upgraded to connected after OAuth authentication", serverName)
	return nil
}

// UpgradeSessionConnection upgrades a session's connection to an OAuth server
// after successful authentication. This creates a session-specific MCP client
// with the user's token and fetches their available tools.
//
// This implements session-scoped tool visibility as described in ADR-006.
//
// Args:
//   - ctx: Context for the operation
//   - sessionID: The session to upgrade
//   - serverName: The server the session authenticated with
//   - token: The OAuth access token (wrapped in RedactedToken for safety)
//
// Returns an error if the upgrade fails.
func (am *AggregatorManager) UpgradeSessionConnection(ctx context.Context, sessionID, serverName string, token oauth.RedactedToken) error {
	am.mu.RLock()
	if am.aggregatorServer == nil {
		am.mu.RUnlock()
		return fmt.Errorf("aggregator server not available")
	}
	aggregatorServer := am.aggregatorServer
	am.mu.RUnlock()

	// Validate token is not empty
	if token.IsEmpty() {
		return fmt.Errorf("token cannot be empty")
	}

	// Get server info to determine connection type and URL
	serverInfo, exists := aggregatorServer.GetRegistry().GetServerInfo(serverName)
	if !exists {
		return fmt.Errorf("server %s not found", serverName)
	}

	if serverInfo.Status != StatusAuthRequired {
		return fmt.Errorf("server %s is not in auth_required state", serverName)
	}

	// Create an authenticated MCP client with the user's token
	// Note: token.Value() is only called here to construct the header
	headers := map[string]string{
		"Authorization": "Bearer " + token.Value(),
	}

	// Determine the client type based on server URL
	// For now, we assume streamable-http for remote OAuth servers
	client := newAuthenticatedClient(serverInfo.URL, headers)

	// Initialize the client
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize authenticated client: %w", err)
	}

	// Fetch tools from the server for this user
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return fmt.Errorf("failed to list tools: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Aggregator-Manager", "Failed to list resources for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverName, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Aggregator-Manager", "Failed to list prompts for session %s, server %s: %v",
			logging.TruncateSessionID(sessionID), serverName, err)
		prompts = nil
	}

	// Get session registry and upgrade the connection
	sessionRegistry := aggregatorServer.GetSessionRegistry()
	session := sessionRegistry.GetOrCreateSession(sessionID)

	// Create the session connection
	conn := &SessionConnection{
		ServerName:  serverName,
		Status:      StatusSessionConnected,
		Client:      client,
		ConnectedAt: time.Now(),
	}
	conn.UpdateTools(tools)
	conn.UpdateResources(resources)
	conn.UpdatePrompts(prompts)

	session.SetConnection(serverName, conn)

	// Send targeted notification to the session that their tools have changed
	aggregatorServer.NotifySessionToolsChanged(sessionID)

	logging.Info("Aggregator-Manager", "Session %s connected to %s with %d tools, %d resources, %d prompts",
		logging.TruncateSessionID(sessionID), serverName, len(tools), len(resources), len(prompts))

	return nil
}

// newAuthenticatedClient creates an MCP client with OAuth token authentication.
func newAuthenticatedClient(url string, headers map[string]string) MCPClient {
	// Use streamable HTTP by default for remote OAuth servers
	return mcpserver.NewStreamableHTTPClientWithHeaders(url, headers)
}

// isServerAuthRequired checks if a server is currently in auth_required state.
// This is used by the event handler to avoid deregistering servers that are
// waiting for OAuth authentication.
func (am *AggregatorManager) isServerAuthRequired(serverName string) bool {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if am.aggregatorServer == nil {
		return false
	}

	info, exists := am.aggregatorServer.GetRegistry().GetServerInfo(serverName)
	if !exists {
		return false
	}

	return info.Status == StatusAuthRequired
}

// handleAuthCompletion is called after successful OAuth browser authentication.
// It establishes the session connection to the MCP server using the new token.
//
// This callback is registered with the OAuth manager and called from the OAuth
// callback handler after a user successfully authenticates in the browser.
func (am *AggregatorManager) handleAuthCompletion(ctx context.Context, sessionID, serverName, accessToken string) error {
	am.mu.RLock()
	aggregatorServer := am.aggregatorServer
	am.mu.RUnlock()

	if aggregatorServer == nil {
		return fmt.Errorf("aggregator server not available")
	}

	// Get the server URL from the registry
	serverInfo, exists := aggregatorServer.GetRegistry().GetServerInfo(serverName)
	if !exists {
		return fmt.Errorf("server %s not found", serverName)
	}

	// Get issuer and scope from AuthInfo for dynamic token refresh
	var issuer, scope string
	if serverInfo.AuthInfo != nil {
		issuer = serverInfo.AuthInfo.Issuer
		scope = serverInfo.AuthInfo.Scope
	}

	logging.Info("Aggregator-Manager", "OAuth callback completing - establishing session connection for session=%s server=%s",
		logging.TruncateSessionID(sessionID), serverName)

	// Use the aggregator server's tryConnectWithToken to establish the connection
	// Pass issuer and scope to enable dynamic token refresh
	result, err := aggregatorServer.tryConnectWithToken(ctx, sessionID, serverName, serverInfo.URL, issuer, scope, accessToken)
	if err != nil {
		return fmt.Errorf("failed to establish session connection: %w", err)
	}

	// Log success (result contains success message)
	if result != nil && len(result.Content) > 0 {
		logging.Debug("Aggregator-Manager", "Session connection established successfully")
	}

	return nil
}

// deregisterSingleServer removes a single MCP server from the aggregator.
//
// This method is called when a server is no longer healthy or running.
// It cleanly removes the server from the aggregator, which will also
// remove all tools, resources, and prompts provided by that server.
//
// Args:
//   - serverName: Unique name of the server to deregister
//
// Returns an error if deregistration fails.
func (am *AggregatorManager) deregisterSingleServer(serverName string) error {
	// Deregister from the aggregator
	if err := am.aggregatorServer.DeregisterServer(serverName); err != nil {
		return fmt.Errorf("failed to deregister server: %w", err)
	}

	logging.Info("Aggregator-Manager", "Successfully deregistered MCP server %s", serverName)
	return nil
}

// GetEndpoint returns the aggregator's MCP endpoint URL.
//
// The endpoint format depends on the configured transport:
//   - SSE: http://host:port/sse
//   - Streamable HTTP: http://host:port/mcp
//   - Stdio: "stdio"
//
// Returns an empty string if the aggregator server is not available.
func (am *AggregatorManager) GetEndpoint() string {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if am.aggregatorServer != nil {
		return am.aggregatorServer.GetEndpoint()
	}

	return ""
}

// GetAggregatorServer returns the underlying aggregator server instance.
//
// This method provides access to advanced aggregator operations that are
// not exposed through the manager interface. It should be used carefully
// and primarily for testing or debugging purposes.
//
// Returns nil if the server is not initialized.
func (am *AggregatorManager) GetAggregatorServer() *AggregatorServer {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.aggregatorServer
}

// GetEventHandler returns the event handler instance.
//
// This method is primarily intended for testing and debugging purposes
// to inspect the state of the event handling system.
//
// Returns nil if the event handler is not initialized.
func (am *AggregatorManager) GetEventHandler() *EventHandler {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.eventHandler
}

// ManualRefresh manually triggers a re-synchronization of all healthy MCP servers.
//
// This method can be useful for debugging or when you need to force a refresh
// of the server registrations outside of the normal event-driven flow.
// It performs the same operation as the initial sync during startup.
//
// Args:
//   - ctx: Context for the refresh operation
//
// Returns an error if the refresh operation fails.
func (am *AggregatorManager) ManualRefresh(ctx context.Context) error {
	return am.registerHealthyMCPServers(ctx)
}

// retryFailedRegistrations runs a periodic background task that attempts to
// register services that are healthy but not yet registered with the aggregator.
//
// This mechanism provides resilience against temporary failures during
// initial registration or when services recover from unhealthy states.
// It runs until the provided context is cancelled.
//
// Args:
//   - ctx: Context for controlling the retry loop lifecycle
func (am *AggregatorManager) retryFailedRegistrations(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			am.attemptPendingRegistrations(ctx)
		}
	}
}

// attemptPendingRegistrations tries to register services that are healthy
// but not yet registered with the aggregator.
//
// This method scans all MCP services and attempts to register any that are
// running and healthy but not currently registered. It's used by the retry
// mechanism to handle temporary registration failures.
//
// Args:
//   - ctx: Context for the registration attempts
func (am *AggregatorManager) attemptPendingRegistrations(ctx context.Context) {
	if am.serviceRegistry == nil {
		return
	}

	// Get all MCP services from the registry
	mcpServices := am.serviceRegistry.GetByType(api.TypeMCPServer)

	for _, service := range mcpServices {
		// Only try services that are running and healthy
		if service.GetState() != api.StateRunning || service.GetHealth() != api.HealthHealthy {
			continue
		}

		// Check if already registered with aggregator
		if am.aggregatorServer != nil {
			if _, exists := am.aggregatorServer.GetRegistry().GetServerInfo(service.GetName()); exists {
				continue // Already registered
			}
		}

		// Attempt registration
		if err := am.registerSingleServer(ctx, service.GetName()); err != nil {
			logging.Debug("Aggregator-Manager", "Retry registration failed for %s: %v", service.GetName(), err)
		} else {
			logging.Info("Aggregator-Manager", "Successfully registered %s on retry", service.GetName())
		}
	}
}
