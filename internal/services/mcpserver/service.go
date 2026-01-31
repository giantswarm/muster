package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"muster/internal/api"
	"muster/internal/events"
	"muster/internal/mcpserver"
	"muster/internal/services"
	"muster/pkg/logging"
)

// DefaultRemoteTimeout is the default connection timeout in seconds for remote MCP servers.
// This value must be kept in sync with the kubebuilder:default annotation in MCPServerSpec.Timeout
// (see pkg/apis/muster/v1alpha1/mcpserver_types.go).
const DefaultRemoteTimeout = 30

// UnreachableThreshold is the number of consecutive failures before marking a server as unreachable.
const UnreachableThreshold = 3

// Exponential backoff configuration for unreachable servers.
const (
	// InitialBackoff is the initial retry interval after first failure (30 seconds)
	InitialBackoff = 30 * time.Second
	// MaxBackoff is the maximum retry interval (30 minutes)
	MaxBackoff = 30 * time.Minute
	// BackoffMultiplier is the factor by which backoff increases on each failure
	BackoffMultiplier = 2.0
)

// RestartGracePeriod is the pause between stop and start during a restart.
// This allows time for:
// - Subprocess cleanup and port release for stdio servers
// - Connection draining for remote HTTP/SSE servers
// - Upstream load balancers to detect the disconnect
const RestartGracePeriod = 200 * time.Millisecond

// Service implements the Service interface for MCP server management
// The MCP client now handles both process management AND MCP communication
type Service struct {
	*services.BaseService
	definition      *api.MCPServer
	client          interface{} // MCP client that manages the process AND handles MCP communication
	clientInitMutex sync.Mutex  // Protects client operations

	// Connection failure tracking for exponential backoff.
	// These fields are protected by failureMutex for thread-safe access.
	failureMutex        sync.RWMutex
	consecutiveFailures int        // Number of consecutive connection failures
	lastAttempt         *time.Time // When the last connection attempt was made (preserved after success for diagnostics)
	nextRetryAfter      *time.Time // When the next retry should be attempted (cleared on success)
}

// NewService creates a new MCP server service
func NewService(definition *api.MCPServer) (*Service, error) {
	baseService := services.NewBaseService(definition.Name, services.TypeMCPServer, []string{})

	service := &Service{
		BaseService: baseService,
		definition:  definition,
	}

	return service, nil
}

// Start starts the MCP server service by creating and initializing the MCP client
// The client handles both process startup and MCP protocol initialization
//
// If the server requires OAuth authentication, this method will return an
// AuthRequiredError containing the OAuth information. The caller should handle
// this by registering the server in auth_required state with a synthetic tool.
//
// For remote servers, this method tracks consecutive connection failures and
// transitions to StateUnreachable after UnreachableThreshold failures.
func (s *Service) Start(ctx context.Context) error {
	if s.IsRunning() {
		return fmt.Errorf("service %s is already running", s.GetName())
	}

	// Record attempt time (thread-safe)
	now := time.Now()
	s.failureMutex.Lock()
	s.lastAttempt = &now
	s.failureMutex.Unlock()

	s.UpdateState(services.StateStarting, services.HealthUnknown, nil)
	s.LogInfo("Starting MCP server service")

	// Generate starting event
	s.generateEvent(events.ReasonMCPServerStarting, events.EventData{})

	// Create and initialize the MCP client (this starts the process AND establishes MCP communication)
	if err := s.createAndInitializeClient(ctx); err != nil {
		// Check if this is an auth required error - this is a special case
		// where the server exists but needs OAuth before it can connect
		if authErr, ok := err.(*mcpserver.AuthRequiredError); ok {
			// Auth errors should not count as connectivity failures
			// Use StateWaiting instead of StateStopped to prevent the event handler
			// from deregistering the server before the orchestrator can register
			// the pending auth. StateWaiting is semantically correct - the server
			// is waiting for authentication.
			s.UpdateState(services.StateWaiting, services.HealthUnknown, nil)
			s.LogInfo("MCP server requires authentication")
			// Generate auth required event
			s.generateEvent(events.ReasonMCPServerAuthRequired, events.EventData{
				Error: "authentication required",
			})
			// Return the auth error for the caller to handle
			return authErr
		}

		// Track consecutive failures for remote servers (transient errors only)
		if s.isRemoteServer() && s.isTransientConnectivityError(err) {
			s.failureMutex.Lock()
			s.consecutiveFailures++
			s.calculateNextRetryTimeLocked()
			failures := s.consecutiveFailures
			nextRetry := s.nextRetryAfter
			s.failureMutex.Unlock()

			s.LogWarn("Connection failure #%d for MCP server %s: %v (next retry after %v)",
				failures, s.GetName(), err, nextRetry)

			// Transition to unreachable state after threshold failures
			if failures >= UnreachableThreshold {
				s.UpdateState(services.StateUnreachable, services.HealthUnknown, err)
				s.generateEvent(events.ReasonMCPServerFailed, events.EventData{
					Error: fmt.Sprintf("server unreachable after %d consecutive failures: %s", failures, err.Error()),
				})
				return fmt.Errorf("server unreachable after %d consecutive failures: %w", failures, err)
			}
		}

		s.UpdateState(services.StateFailed, services.HealthUnhealthy, err)
		// Generate failure event
		s.generateEvent(events.ReasonMCPServerFailed, events.EventData{
			Error: err.Error(),
		})
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	// Success - reset consecutive failure tracking (thread-safe)
	s.failureMutex.Lock()
	s.consecutiveFailures = 0
	s.nextRetryAfter = nil
	// Note: lastAttempt is intentionally preserved for diagnostics
	s.failureMutex.Unlock()

	// Use appropriate state based on server type:
	// - Remote servers (streamable-http, sse): "connected" is more intuitive
	// - Local servers (stdio): "running" describes the process state
	if s.isRemoteServer() {
		s.UpdateState(services.StateConnected, services.HealthHealthy, nil)
		s.LogInfo("MCP server connected successfully")
	} else {
		s.UpdateState(services.StateRunning, services.HealthHealthy, nil)
		s.LogInfo("MCP server started successfully")
	}

	// Generate success event
	s.generateEvent(events.ReasonMCPServerStarted, events.EventData{})

	return nil
}

// Stop stops the MCP server service by closing the MCP client
// The client handles both MCP protocol cleanup and process termination
func (s *Service) Stop(ctx context.Context) error {
	currentState := s.GetState()

	// If already stopped, nothing to do
	if currentState == services.StateStopped {
		s.LogDebug("Service %s is already stopped", s.GetName())
		return nil
	}

	// If not running/connected and not failed, nothing to stop
	if currentState != services.StateRunning && currentState != services.StateConnected && currentState != services.StateFailed {
		s.LogDebug("Service %s is not in a stoppable state (%s), transitioning to stopped/disconnected", s.GetName(), currentState)
		if s.isRemoteServer() {
			s.UpdateState(services.StateDisconnected, services.HealthUnknown, nil)
		} else {
			s.UpdateState(services.StateStopped, services.HealthUnknown, nil)
		}
		// Generate stopped event for state transition
		s.generateEvent(events.ReasonMCPServerStopped, events.EventData{})
		return nil
	}

	s.UpdateState(services.StateStopping, s.GetHealth(), nil)
	s.LogInfo("Stopping MCP server service")

	// Close the MCP client (this stops the process AND closes MCP communication)
	if err := s.closeClient(); err != nil {
		s.LogWarn("Error during client cleanup: %v", err)
		// Still transition to stopped state for graceful shutdown
	}

	// Use appropriate state based on server type:
	// - Remote servers: "disconnected" is more intuitive
	// - Local servers: "stopped" describes the process state
	if s.isRemoteServer() {
		s.UpdateState(services.StateDisconnected, services.HealthUnknown, nil)
		s.LogInfo("MCP server disconnected successfully")
	} else {
		s.UpdateState(services.StateStopped, services.HealthUnknown, nil)
		s.LogInfo("MCP server stopped successfully")
	}

	// Generate stopped event
	s.generateEvent(events.ReasonMCPServerStopped, events.EventData{})

	return nil
}

// Restart restarts the MCP server service.
// This method performs a graceful restart by stopping the service first (if running),
// waiting a brief grace period, and then starting it again.
func (s *Service) Restart(ctx context.Context) error {
	s.LogInfo("Restarting MCP server service")

	// Generate recovery started event for restart operations
	s.generateEvent(events.ReasonMCPServerRecoveryStarted, events.EventData{})

	if s.IsRunning() {
		if err := s.Stop(ctx); err != nil {
			// Generate recovery failed event
			s.generateEvent(events.ReasonMCPServerRecoveryFailed, events.EventData{
				Error: err.Error(),
			})
			return fmt.Errorf("failed to stop service during restart: %w", err)
		}
	}

	// Grace period between stop and start (see RestartGracePeriod constant for rationale)
	time.Sleep(RestartGracePeriod)

	if err := s.Start(ctx); err != nil {
		// Generate recovery failed event
		s.generateEvent(events.ReasonMCPServerRecoveryFailed, events.EventData{
			Error: err.Error(),
		})
		return err
	}

	// Generate recovery succeeded event
	s.generateEvent(events.ReasonMCPServerRecoverySucceeded, events.EventData{})
	return nil
}

// IsRunning checks if the MCP server is running (or connected for remote servers)
func (s *Service) IsRunning() bool {
	state := s.GetState()
	return state == services.StateRunning || state == services.StateConnected
}

// IsHealthy checks if the MCP server is healthy
func (s *Service) IsHealthy() bool {
	return s.GetHealth() == services.HealthHealthy && s.IsRunning()
}

// GetServiceType returns the service type
func (s *Service) GetServiceType() string {
	return "mcpserver"
}

// GetConfiguration returns the MCP server configuration
func (s *Service) GetConfiguration() interface{} {
	return s.definition
}

// ValidateConfiguration validates the MCP server configuration
func (s *Service) ValidateConfiguration() error {
	if s.definition == nil {
		return fmt.Errorf("MCP server definition is nil")
	}

	if s.definition.Name == "" {
		return fmt.Errorf("MCP server name is required")
	}

	// Type-specific validation
	switch s.definition.Type {
	case api.MCPServerTypeStdio:
		if s.definition.Command == "" {
			return fmt.Errorf("command is required for stdio type")
		}
	case api.MCPServerTypeStreamableHTTP, api.MCPServerTypeSSE:
		if s.definition.URL == "" {
			return fmt.Errorf("url is required for streamable-http and sse types")
		}
		// Note: timeout defaults to DefaultRemoteTimeout if not specified
	default:
		return fmt.Errorf("unsupported MCP server type: %s (supported: %s, %s, %s)",
			s.definition.Type, api.MCPServerTypeStdio, api.MCPServerTypeStreamableHTTP, api.MCPServerTypeSSE)
	}

	return nil
}

// UpdateConfiguration updates the MCP server configuration
func (s *Service) UpdateConfiguration(newConfig interface{}) error {
	newDef, ok := newConfig.(*api.MCPServer)
	if !ok {
		return fmt.Errorf("invalid configuration type for MCP server")
	}

	s.definition = newDef
	return nil
}

// GetServiceData implements ServiceDataProvider
func (s *Service) GetServiceData() map[string]interface{} {
	data := map[string]interface{}{
		"name":      s.definition.Name,
		"type":      s.definition.Type,
		"state":     s.GetState(),
		"health":    s.GetHealth(),
		"autoStart": s.definition.AutoStart,
		"command":   s.definition.Command,
		"args":      s.definition.Args,
		"url":       s.definition.URL,
		"env":       s.definition.Env,
		"headers":   s.definition.Headers,
		"timeout":   s.definition.Timeout,
	}

	if s.GetLastError() != nil {
		data["error"] = s.GetLastError().Error()
	}

	// Add client to service data if available and ready
	s.clientInitMutex.Lock()
	if s.client != nil {
		data["client"] = s.client
		data["clientReady"] = true
	} else {
		data["clientReady"] = false
	}
	s.clientInitMutex.Unlock()

	// Add tool prefix for aggregator registration
	data["toolPrefix"] = s.definition.ToolPrefix

	// Add failure tracking data for unreachable server detection (thread-safe read)
	s.failureMutex.RLock()
	data["consecutiveFailures"] = s.consecutiveFailures
	if s.lastAttempt != nil {
		data["lastAttempt"] = *s.lastAttempt
	}
	if s.nextRetryAfter != nil {
		data["nextRetryAfter"] = *s.nextRetryAfter
	}
	s.failureMutex.RUnlock()

	return data
}

// CheckHealth implements HealthChecker using MCP protocol
func (s *Service) CheckHealth(ctx context.Context) (services.HealthStatus, error) {
	s.clientInitMutex.Lock()
	client := s.client
	s.clientInitMutex.Unlock()

	if client == nil {
		s.UpdateHealth(services.HealthUnhealthy)
		err := fmt.Errorf("MCP client not available")
		// Generate health check failed event
		s.generateEvent(events.ReasonMCPServerHealthCheckFailed, events.EventData{
			Error: err.Error(),
		})
		return services.HealthUnhealthy, err
	}

	// Use MCP ping to check health instead of process checking
	if pinger, ok := client.(interface{ Ping(context.Context) error }); ok {
		if err := pinger.Ping(ctx); err != nil {
			s.UpdateHealth(services.HealthUnhealthy)
			healthErr := fmt.Errorf("MCP ping failed: %w", err)
			// Generate health check failed event
			s.generateEvent(events.ReasonMCPServerHealthCheckFailed, events.EventData{
				Error: healthErr.Error(),
			})
			return services.HealthUnhealthy, healthErr
		}
	}

	s.UpdateHealth(services.HealthHealthy)
	return services.HealthHealthy, nil
}

// GetHealthCheckInterval implements HealthChecker
func (s *Service) GetHealthCheckInterval() time.Duration {
	// Fixed default health check interval since MCP servers no longer define their own health check interval
	// Health checking is managed by the service orchestrator
	return 30 * time.Second
}

// GetLogContext returns the logging context for this service
func (s *Service) GetLogContext() string {
	return fmt.Sprintf("MCPServerService-%s", s.GetName())
}

// LogInfo logs an info message with service context
func (s *Service) LogInfo(format string, args ...interface{}) {
	logging.Info(s.GetLogContext(), format, args...)
}

// LogDebug logs a debug message with service context
func (s *Service) LogDebug(format string, args ...interface{}) {
	logging.Debug(s.GetLogContext(), format, args...)
}

// LogError logs an error message with service context
func (s *Service) LogError(err error, format string, args ...interface{}) {
	logging.Error(s.GetLogContext(), err, format, args...)
}

// LogWarn logs a warning message with service context
func (s *Service) LogWarn(format string, args ...interface{}) {
	logging.Warn(s.GetLogContext(), format, args...)
}

// getRemoteInitContext creates a context with the appropriate timeout for remote MCP client initialization.
// Uses the configured timeout if set, otherwise falls back to DefaultRemoteTimeout.
func (s *Service) getRemoteInitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.definition.Timeout
	if timeout == 0 {
		timeout = DefaultRemoteTimeout
	}
	return context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
}

// createAndInitializeClient creates the appropriate MCP client based on the server type.
// This uses the factory pattern via NewMCPClientFromType to create the correct client.
//
// Note: This method assumes ValidateConfiguration() has already been called.
// It does not perform redundant validation checks.
//
// If the server returns a 401 during initialization, an AuthRequiredError is returned
// containing OAuth information that can be used to initiate the authentication flow.
//
// If the server uses Teleport authentication, this method obtains an HTTP client
// configured with Teleport Machine ID certificates from the Teleport handler.
func (s *Service) createAndInitializeClient(ctx context.Context) error {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()

	// Build client configuration from service definition
	// Note: Headers can be nil - the factory and client constructors handle nil maps gracefully
	config := mcpserver.MCPClientConfig{
		Command: s.definition.Command,
		Args:    s.definition.Args,
		Env:     s.definition.Env,
		URL:     s.definition.URL,
		Headers: s.definition.Headers,
	}

	// If Teleport authentication is configured, get a custom HTTP client
	if s.definition.Auth != nil && s.definition.Auth.Type == api.AuthTypeTeleport {
		httpClient, err := s.getTeleportHTTPClient(ctx)
		if err != nil {
			return fmt.Errorf("failed to get Teleport HTTP client: %w", err)
		}
		config.HTTPClient = httpClient
		s.LogDebug("Using Teleport HTTP client for %s", s.GetName())
	}

	// Use factory to create the appropriate client type
	client, err := mcpserver.NewMCPClientFromType(s.definition.Type, config)
	if err != nil {
		return fmt.Errorf("failed to create MCP client: %w", err)
	}

	s.LogDebug("Created %s MCP client for %s", s.definition.Type, s.GetName())

	// Determine timeout based on server type
	var initCtx context.Context
	var cancel context.CancelFunc
	if s.definition.Type == api.MCPServerTypeStdio {
		initCtx, cancel = context.WithTimeout(ctx, mcpserver.DefaultStdioInitTimeout)
	} else {
		initCtx, cancel = s.getRemoteInitContext(ctx)
	}
	defer cancel()

	// Initialize the client
	if err := client.Initialize(initCtx); err != nil {
		// Check if this is an authentication required error
		if authErr, ok := err.(*mcpserver.AuthRequiredError); ok {
			s.LogInfo("Server %s requires authentication (401)", s.GetName())
			// Return the auth error directly so the caller can handle it
			return authErr
		}
		return fmt.Errorf("failed to initialize %s MCP client: %w", s.definition.Type, err)
	}

	s.client = client
	s.LogDebug("%s MCP client initialized successfully for %s", s.definition.Type, s.GetName())
	return nil
}

// closeClient closes the MCP client, which also terminates the process
func (s *Service) closeClient() error {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()

	if s.client == nil {
		return nil
	}

	// Close the client - this handles both MCP protocol cleanup and process termination
	if closer, ok := s.client.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			s.LogWarn("Error closing MCP client: %v", err)
			return err
		} else {
			s.LogDebug("MCP client closed successfully")
		}
	}

	s.client = nil
	return nil
}

// GetMCPClient returns the MCP client for this service (used by aggregator)
// This is now much simpler since the client IS the process manager
func (s *Service) GetMCPClient() interface{} {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()
	return s.client
}

// IsClientReady returns whether the MCP client is initialized and ready
func (s *Service) IsClientReady() bool {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()
	return s.client != nil
}

// generateEvent creates a Kubernetes event for this MCPServer service
func (s *Service) generateEvent(reason events.EventReason, data events.EventData) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		logging.Debug(s.GetLogContext(), "Event manager not available, skipping event generation")
		return
	}

	// Create an object reference for the MCPServer CRD
	// MCPServer lifecycle events should be associated with the MCPServer CRD resource
	objectRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      s.GetName(),
		Namespace: "default", // TODO: Make configurable or derive from service configuration
	}

	// Populate service-specific data
	data.Name = s.GetName()
	if data.Namespace == "" {
		data.Namespace = "default"
	}

	err := eventManager.CreateEvent(context.Background(), objectRef, string(reason), "", string(events.EventTypeNormal))
	if err != nil {
		logging.Debug(s.GetLogContext(), "Failed to generate event %s: %v", string(reason), err)
	} else {
		logging.Debug(s.GetLogContext(), "Generated event %s for MCPServer service", string(reason))
	}
}

// isRemoteServer returns true if this is a remote MCP server (streamable-http or sse)
// as opposed to a local stdio server. Remote servers are subject to network
// connectivity issues and unreachable state tracking.
func (s *Service) isRemoteServer() bool {
	return s.definition.Type.IsRemote()
}

// getTeleportHTTPClient returns an HTTP client configured with Teleport certificates.
// It uses the Teleport handler registered in the API service locator.
func (s *Service) getTeleportHTTPClient(ctx context.Context) (*http.Client, error) {
	teleportAuth := s.definition.Auth.Teleport
	if teleportAuth == nil {
		return nil, fmt.Errorf("teleport auth configured but teleport settings are missing")
	}

	// Get the Teleport handler from the API service locator
	teleportHandler := api.GetTeleportClient()
	if teleportHandler == nil {
		return nil, fmt.Errorf("teleport client handler not registered")
	}

	// Build the client configuration from the MCPServer auth settings
	clientConfig := api.TeleportClientConfig{
		IdentityDir:             teleportAuth.IdentityDir,
		IdentitySecretName:      teleportAuth.IdentitySecretName,
		IdentitySecretNamespace: teleportAuth.IdentitySecretNamespace,
		AppName:                 teleportAuth.AppName,
	}

	// Validate that exactly one identity source is specified (mutual exclusivity)
	if clientConfig.IdentityDir == "" && clientConfig.IdentitySecretName == "" {
		return nil, fmt.Errorf("teleport auth requires either identityDir or identitySecretName")
	}
	if clientConfig.IdentityDir != "" && clientConfig.IdentitySecretName != "" {
		return nil, fmt.Errorf("teleport auth: identityDir and identitySecretName are mutually exclusive")
	}

	// Get the HTTP client from the Teleport handler
	httpClient, err := teleportHandler.GetHTTPClientForConfig(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get Teleport HTTP client: %w", err)
	}

	return httpClient, nil
}

// isTransientConnectivityError checks if an error is a transient network/connectivity
// error that should count towards the unreachable threshold.
//
// Transient errors are temporary issues that may resolve with retry:
// - Connection refused (server not listening)
// - Network unreachable (routing issues)
// - DNS resolution failures
// - Timeouts
// - HTTP 5xx server errors (500-511)
//
// Configuration errors (certificates, TLS) are NOT transient and should fail
// immediately without counting towards unreachable threshold, as they won't
// resolve without user intervention.
func (s *Service) isTransientConnectivityError(err error) bool {
	if err == nil {
		return false
	}

	// Configuration errors should fail fast, not count towards unreachable
	if s.isConfigurationError(err) {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Check for net.OpError (connection refused, timeout, no route to host)
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Check for DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Transient connectivity patterns - these may resolve with retry
	transientPatterns := []string{
		"connection refused",
		"connection reset",
		"connection timed out",
		"no such host",
		"network is unreachable",
		"host is unreachable",
		"no route to host",
		"dial tcp",
		"dial unix",
		"i/o timeout",
		"eof",
		"connection closed",
	}

	for _, pattern := range transientPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	// HTTP 5xx errors are transient server errors that may resolve with retry.
	// Check numeric status codes 500-511 (covers all standard 5xx codes)
	for code := 500; code <= 511; code++ {
		if strings.Contains(errStr, fmt.Sprintf("status %d", code)) {
			return true
		}
	}

	// Also check common descriptive error messages for 5xx responses
	http5xxDescriptive := []string{
		"internal server error",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"http version not supported",
		"variant also negotiates",
	}

	for _, pattern := range http5xxDescriptive {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	// Context timeout/deadline exceeded also count as transient connectivity issues
	if strings.Contains(errStr, "context deadline exceeded") ||
		strings.Contains(errStr, "context canceled") {
		return true
	}

	return false
}

// isConfigurationError checks if an error is a configuration issue that
// requires user intervention and should NOT be retried with exponential backoff.
// These errors won't resolve on their own - the user needs to fix the config.
func (s *Service) isConfigurationError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Certificate and TLS configuration errors require user intervention
	configPatterns := []string{
		"certificate",
		"x509",
		"tls handshake",
		"certificate signed by unknown authority",
		"certificate has expired",
		"certificate is not valid",
	}

	for _, pattern := range configPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// calculateNextRetryTimeLocked calculates the next retry time using exponential backoff.
// Backoff follows: InitialBackoff * 2^(failures-1), capped at MaxBackoff.
// MUST be called with failureMutex held.
func (s *Service) calculateNextRetryTimeLocked() {
	// Calculate backoff duration: initial * 2^(failures-1)
	backoffDuration := InitialBackoff
	for i := 1; i < s.consecutiveFailures; i++ {
		backoffDuration = time.Duration(float64(backoffDuration) * BackoffMultiplier)
		if backoffDuration > MaxBackoff {
			backoffDuration = MaxBackoff
			break
		}
	}

	nextRetry := time.Now().Add(backoffDuration)
	s.nextRetryAfter = &nextRetry
}

// GetConsecutiveFailures returns the number of consecutive connection failures.
// Thread-safe.
func (s *Service) GetConsecutiveFailures() int {
	s.failureMutex.RLock()
	defer s.failureMutex.RUnlock()
	return s.consecutiveFailures
}

// GetLastAttempt returns the time of the last connection attempt.
// This value is preserved after successful connections for diagnostic purposes.
// Thread-safe.
func (s *Service) GetLastAttempt() *time.Time {
	s.failureMutex.RLock()
	defer s.failureMutex.RUnlock()
	if s.lastAttempt == nil {
		return nil
	}
	t := *s.lastAttempt
	return &t
}

// GetNextRetryAfter returns the time after which the next retry should be attempted.
// Returns nil if no retry is scheduled (either never failed or after successful connection).
// Thread-safe.
func (s *Service) GetNextRetryAfter() *time.Time {
	s.failureMutex.RLock()
	defer s.failureMutex.RUnlock()
	if s.nextRetryAfter == nil {
		return nil
	}
	t := *s.nextRetryAfter
	return &t
}

// IsUnreachable returns true if the server is in the unreachable state.
func (s *Service) IsUnreachable() bool {
	return s.GetState() == services.StateUnreachable
}
