package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/events"
	"github.com/giantswarm/muster/internal/mcpserver"
	"github.com/giantswarm/muster/internal/services"
	"github.com/giantswarm/muster/pkg/logging"
)

// DefaultRemoteTimeout is the default connection timeout in seconds for remote MCP servers.
// This value must be kept in sync with the kubebuilder:default annotation in MCPServerSpec.Timeout
// (see pkg/apis/muster/v1alpha1/mcpserver_types.go).
const DefaultRemoteTimeout = 30

// UnreachableThreshold is the number of consecutive failures before marking a server as unreachable.
const UnreachableThreshold = 3

// BackoffMultiplier is the factor by which the reconnect backoff grows on each
// consecutive failure.
const BackoffMultiplier = 2.0

// InitialBackoff is the initial retry interval after the first connection
// failure. Overridable via MUSTER_MCPSERVER_INITIAL_BACKOFF (a Go duration,
// e.g. "1s") so the integration test harness can recover from transient
// connect failures without waiting out production backoff.
var InitialBackoff = durationFromEnv("MUSTER_MCPSERVER_INITIAL_BACKOFF", 30*time.Second)

// MaxBackoff caps the reconnect backoff of a remote server. The schedule
// doubles from InitialBackoff on every consecutive failure and stops growing
// here, so once the upstream heals the server is retried within this bound
// plus the orchestrator's retry tick, however long the outage lasted. The
// former 30 minute cap let a 504 window of a few minutes hide a recovery for
// another 4.5, then 8.5, then 16.5 minutes (issue #1163). Overridable via
// MUSTER_MCPSERVER_MAX_BACKOFF (a Go duration); a value below InitialBackoff
// makes every retry wait exactly that long.
var MaxBackoff = durationFromEnv("MUSTER_MCPSERVER_MAX_BACKOFF", 2*time.Minute)

// httpStatusPattern finds the HTTP status code in the error text of a failed
// initialize. The transports report it as "request failed with status 504"
// (streamable-http), "unexpected status code: 504" (SSE) or "server returned
// 401 Unauthorized" (muster's auth detection); none of them exposes it as a
// typed error, so the text is the only place it survives to.
var httpStatusPattern = regexp.MustCompile(`(?i)\b(?:status(?: code)?:?|returned)\s+([1-5]\d{2})\b`)

// durationFromEnv reads a Go duration from the named environment variable,
// falling back to def when unset or unparsable.
func durationFromEnv(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

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
	// retryBackoff is the wait the current nextRetryAfter was computed from,
	// kept so events and status can name the schedule exactly.
	retryBackoff time.Duration
	// lastFailureHTTPStatus is the HTTP status the endpoint answered the most
	// recent failed attempt with; 0 when the attempt got no HTTP response at
	// all (connection refused, DNS, timeout). Distinguishes an upstream that
	// answers 504 from an endpoint nothing listens on (issue #1163).
	lastFailureHTTPStatus int

	// onAuthRequired runs synchronously before the StateAuthRequired transition.
	// Immutable after construction; set via WithAuthRequiredHook.
	onAuthRequired func(definition *api.MCPServer, authErr *mcpserver.AuthRequiredError)

	// kubernetesMode mirrors the MusterClient's mode. It exists so the layer
	// that actually spawns the subprocess can refuse a stdio definition
	// (issue #1067) instead of trusting the gates upstream of it. Immutable
	// after construction; set via WithKubernetesMode.
	kubernetesMode bool

	// healthEventMutex guards healthEventUnhealthy, which gates emission of
	// MCPServerHealthCheckFailed to the healthy->unhealthy transition so the
	// 30s health-check loop does not re-emit the same event every poll.
	healthEventMutex     sync.Mutex
	healthEventUnhealthy bool
}

// Option configures a Service at construction time.
type Option func(*Service)

// WithAuthRequiredHook registers a function that Start calls synchronously when it
// encounters a 401, before UpdateState(StateAuthRequired) publishes the state-change
// event. Work done inside the hook (e.g. registering the server in the aggregator)
// is therefore visible to all subscribers before the event reaches them.
//
// The hook receives the service's current definition rather than a creation-time
// snapshot, so registrations reflect configuration updates applied before a restart.
func WithAuthRequiredHook(fn func(*api.MCPServer, *mcpserver.AuthRequiredError)) Option {
	return func(s *Service) {
		s.onAuthRequired = fn
	}
}

// WithKubernetesMode tells the service whether muster runs against an
// apiserver. In Kubernetes mode a stdio definition is refused rather than
// started, because its subprocess would run inside the muster pod under
// muster's ServiceAccount (issue #1067).
func WithKubernetesMode(kubernetesMode bool) Option {
	return func(s *Service) {
		s.kubernetesMode = kubernetesMode
	}
}

// NewService creates a new MCP server service
func NewService(definition *api.MCPServer, opts ...Option) (*Service, error) {
	baseService := services.NewBaseService(definition.Name, services.TypeMCPServer, []string{})

	service := &Service{
		BaseService: baseService,
		definition:  definition,
	}

	for _, opt := range opts {
		opt(service)
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
		// where the server exists but needs OAuth before it can connect.
		//
		// A machine identity is excluded: its credential is muster's own, so
		// there is no user to send to a login flow and a 401 means the signing
		// configuration or the assumed role is wrong. Classifying that here,
		// where the error is first seen, keeps the CR status, the aggregator
		// registry and the event stream from disagreeing about whether the
		// server is waiting for a user. It falls through to the failure
		// handling below instead.
		if authErr, ok := err.(*mcpserver.AuthRequiredError); ok {
			if !s.definition.Auth.CanAuthenticateInteractively() {
				s.LogWarn("Server answered 401, but auth type %q has no interactive login: "+
					"treating it as a connection failure. Check the signing region, the assumed role and its policy",
					s.definition.Auth.Type)
			} else {
				// Auth errors should not count as connectivity failures
				// Use StateAuthRequired to indicate the server IS reachable but needs authentication.
				// This maps to CRD state "Auth Required" per issue #337 - a 401 response proves
				// the server is reachable at the network level, but authentication is needed.
				s.enterAuthRequired(authErr)
				// Return the auth error for the caller to handle
				return authErr
			}
		}

		// Track consecutive failures for remote servers (transient errors only)
		if s.isRemoteServer() && s.isTransientConnectivityError(err) {
			s.failureMutex.Lock()
			s.consecutiveFailures++
			s.lastFailureHTTPStatus = httpStatusFromError(err)
			s.calculateNextRetryTimeLocked()
			failures := s.consecutiveFailures
			schedule := s.retryScheduleLocked()
			s.failureMutex.Unlock()

			s.LogWarn("Connection failure #%d for MCP server %s: %v (%s)",
				failures, s.GetName(), err, schedule)

			// Transition to unreachable state after threshold failures. The
			// event names the HTTP status and the scheduled retry so an
			// operator can tell an upstream 504 from a refused connection, and
			// see when muster looks again, without the logs (issue #1163).
			if failures >= UnreachableThreshold {
				s.UpdateState(services.StateUnreachable, services.HealthUnknown, err)
				s.generateEvent(events.ReasonMCPServerFailed, events.EventData{
					Error: fmt.Sprintf("server unreachable after %d consecutive failures (%s): %s", failures, schedule, err.Error()),
				})
				return fmt.Errorf("server unreachable after %d consecutive failures: %w", failures, err)
			}

			s.UpdateState(services.StateFailed, services.HealthUnhealthy, err)
			s.generateEvent(events.ReasonMCPServerFailed, events.EventData{
				Error: fmt.Sprintf("connection failure %d of %d before unreachable (%s): %s", failures, UnreachableThreshold, schedule, err.Error()),
			})
			return fmt.Errorf("failed to start MCP server: %w", err)
		}

		s.UpdateState(services.StateFailed, services.HealthUnhealthy, err)
		// Generate failure event
		s.generateEvent(events.ReasonMCPServerFailed, events.EventData{
			Error: err.Error(),
		})
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	// Success - reset consecutive failure tracking (thread-safe)
	s.resetFailureTracking()

	// A server reached with the caller's identity (forwardToken or
	// tokenExchange) is served per session, and the client the probe just
	// opened carries no user token. Whether the backend accepted that
	// anonymous initialize says nothing about how the server must be used:
	// during a rollover from an anonymous to an OAuth-protected release the
	// old pod still answers, and keeping this client would register the
	// server globally, hand every session the token-less client and have it
	// fail with 401 once the protected pod takes over (issue #1135). The
	// probe still proved reachability, so this is Auth Required, not Failed.
	if s.isRemoteServer() && s.definition.Auth.UsesSessionAuth() {
		return s.discardAnonymousProbe()
	}

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

// enterAuthRequired runs the auth-required hook and publishes the
// StateAuthRequired transition, in that order, so that the aggregator's
// pending-auth registration exists before any subscriber sees the event.
func (s *Service) enterAuthRequired(authErr *mcpserver.AuthRequiredError) {
	// A 401 proves the endpoint is reachable again: the reconnect schedule
	// from the outage before it must not linger in the status next to
	// "Auth Required".
	s.resetFailureTracking()
	if s.onAuthRequired != nil {
		s.onAuthRequired(s.definition, authErr)
	}
	s.UpdateState(services.StateAuthRequired, services.HealthUnknown, nil)
	s.LogInfo("MCP server requires authentication")
	// Generate auth required event
	s.generateEvent(events.ReasonMCPServerAuthRequired, events.EventData{
		Error: "authentication required",
	})
}

// resetFailureTracking clears the reconnect schedule after an attempt that
// reached the endpoint (a successful initialize or a 401). lastAttempt is
// intentionally preserved for diagnostics.
func (s *Service) resetFailureTracking() {
	s.failureMutex.Lock()
	s.consecutiveFailures = 0
	s.nextRetryAfter = nil
	s.retryBackoff = 0
	s.lastFailureHTTPStatus = 0
	s.failureMutex.Unlock()
}

// discardAnonymousProbe closes the client Start opened without a user token
// and settles a session-auth server in Auth Required, exactly as a 401 from
// the backend would have. The synthesized AuthRequiredError carries no
// challenge: per-session connections forward the caller's token to the
// server URL and need none, and core_auth_login rediscovers resource metadata
// when it is missing.
func (s *Service) discardAnonymousProbe() error {
	s.LogInfo("MCP server accepted an anonymous connection but is configured for session-level auth " +
		"(forwardToken or tokenExchange): discarding the shared client, tools are served per session")
	if err := s.closeClient(); err != nil {
		s.LogWarn("Error closing anonymous probe client: %v", err)
	}
	authErr := &mcpserver.AuthRequiredError{
		URL: s.definition.URL,
		Err: fmt.Errorf("server %s is configured for session-level authentication and is connected per session", s.GetName()),
	}
	s.enterAuthRequired(authErr)
	return authErr
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

	if err := api.ValidateSigV4(string(s.definition.Type), s.definition.Auth); err != nil {
		return err
	}

	// Type-specific validation
	switch s.definition.Type {
	case api.MCPServerTypeStdio:
		if err := api.ValidateStdioAllowed(string(s.definition.Type), s.kubernetesMode); err != nil {
			return err
		}
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

// UpdateConfiguration updates the MCP server configuration.
//
// Concurrency: must be called from the reconciler goroutine only;
// concurrent access to s.definition is not synchronized.
func (s *Service) UpdateConfiguration(newConfig interface{}) error {
	newDef, ok := newConfig.(*api.MCPServer)
	if !ok {
		return fmt.Errorf("invalid configuration type for MCP server")
	}

	s.definition = newDef
	return nil
}

// ConfigurationChanged returns true if the new configuration differs from the
// current one in a way that requires a restart. Description changes are excluded
// because they are metadata-only and do not affect runtime behavior.
//
// Concurrency: must be called from the reconciler goroutine only;
// concurrent access to s.definition is not synchronized.
func (s *Service) ConfigurationChanged(newConfig interface{}) bool {
	newDef, ok := newConfig.(*api.MCPServer)
	if !ok {
		return true
	}

	cur := s.definition

	if cur.URL != newDef.URL {
		s.LogDebug("Config change detected: url changed from %q to %q", cur.URL, newDef.URL)
		return true
	}
	if cur.Command != newDef.Command {
		s.LogDebug("Config change detected: command changed from %q to %q", cur.Command, newDef.Command)
		return true
	}
	if cur.Type != newDef.Type {
		s.LogDebug("Config change detected: type changed from %q to %q", cur.Type, newDef.Type)
		return true
	}
	// Only false->true matters: enabling auto-start on a stopped service requires
	// a restart. Disabling it on an already-running service is a no-op.
	if !cur.AutoStart && newDef.AutoStart {
		s.LogDebug("Config change detected: autoStart changed from false to true")
		return true
	}
	if !slices.Equal(cur.Args, newDef.Args) {
		s.LogDebug("Config change detected: args changed from %v to %v", cur.Args, newDef.Args)
		return true
	}
	if !maps.Equal(cur.Env, newDef.Env) {
		s.LogDebug("Config change detected: env changed")
		return true
	}
	if !maps.Equal(cur.Headers, newDef.Headers) {
		s.LogDebug("Config change detected: headers changed")
		return true
	}
	if !maps.Equal(cur.Meta, newDef.Meta) {
		s.LogDebug("Config change detected: meta changed")
		return true
	}
	if cur.Timeout != newDef.Timeout {
		s.LogDebug("Config change detected: timeout changed from %d to %d", cur.Timeout, newDef.Timeout)
		return true
	}
	if cur.ToolPrefix != newDef.ToolPrefix {
		s.LogDebug("Config change detected: toolPrefix changed from %q to %q", cur.ToolPrefix, newDef.ToolPrefix)
		return true
	}
	if !reflect.DeepEqual(cur.Family, newDef.Family) {
		s.LogDebug("Config change detected: family changed from %+v to %+v", cur.Family, newDef.Family)
		return true
	}
	if authConfigChanged(cur.Auth, newDef.Auth) {
		s.LogDebug("Config change detected: auth configuration changed")
		return true
	}

	return false
}

// authConfigChanged reports whether two auth configurations differ in a way that
// requires a restart. It compares the full auth spec but ignores the token-exchange
// client credentials (ClientID/ClientSecret), which are resolved from a Secret at
// runtime and stored back on the running service's definition. Those fields are
// absent from a definition freshly rebuilt from the CR (they are tagged json:"-"
// yaml:"-"), so a plain reflect.DeepEqual would always report a difference once
// credentials have loaded, causing the reconciler to restart the MCPServer on every
// pass (see giantswarm/giantswarm#37060).
func authConfigChanged(cur, next *api.MCPServerAuth) bool {
	if cur == nil || next == nil {
		return cur != next
	}
	return !reflect.DeepEqual(normalizeAuthForComparison(cur), normalizeAuthForComparison(next))
}

// normalizeAuthForComparison returns a copy of the auth config with runtime-only
// token-exchange credential fields cleared, so spec-derived fields can be compared
// without the resolved credentials skewing the result. The input is never mutated.
func normalizeAuthForComparison(auth *api.MCPServerAuth) *api.MCPServerAuth {
	if auth == nil {
		return nil
	}
	normalized := *auth
	if auth.TokenExchange != nil {
		te := auth.TokenExchange.SpecOnly()
		normalized.TokenExchange = &te
	}
	return &normalized
}

// GetServiceData implements ServiceDataProvider
func (s *Service) GetServiceData() map[string]interface{} {
	data := map[string]interface{}{
		"name":        s.definition.Name,
		"type":        s.definition.Type,
		"state":       s.GetState(),
		"health":      s.GetHealth(),
		"autoStart":   s.definition.AutoStart,
		"command":     s.definition.Command,
		"args":        s.definition.Args,
		"url":         s.definition.URL,
		"env":         s.definition.Env,
		"headers":     s.definition.Headers,
		"meta":        s.definition.Meta,
		"timeout":     s.definition.Timeout,
		"description": s.definition.Description,
	}

	// Include auth if configured (nil is handled as "no auth" in reconciler comparison)
	if s.definition.Auth != nil {
		data["auth"] = s.definition.Auth
	}

	if s.GetLastError() != nil {
		data["error"] = s.GetLastError().Error()
	}

	// Add client to service data if available and ready
	s.clientInitMutex.Lock()
	if s.client != nil {
		data["client"] = s.client
		data["clientReady"] = true
		// The negotiated revision can be older than the one muster asks for,
		// so it is reported per backend rather than assumed. An empty value
		// means the handshake has not completed; the key stays absent so that
		// core_service_status and core_mcpserver_get agree.
		if versioned, ok := s.client.(interface{ NegotiatedProtocolVersion() string }); ok {
			if version := versioned.NegotiatedProtocolVersion(); version != "" {
				data[api.ServiceDataProtocolVersion] = version
			}
		}
	} else {
		data["clientReady"] = false
	}
	s.clientInitMutex.Unlock()

	// Add tool prefix, family and namespace for aggregator registration. The
	// namespace is the default for the definition's Secret references; it is
	// absent in filesystem mode rather than reported as an empty string.
	data["toolPrefix"] = s.definition.ToolPrefix
	data["family"] = s.definition.Family
	if s.definition.Namespace != "" {
		data["namespace"] = s.definition.Namespace
	}

	// Add failure tracking data for unreachable server detection (thread-safe
	// read). The orchestrator's retry loop and the reconciler's status sync
	// both read these keys.
	s.failureMutex.RLock()
	data[api.ServiceDataConsecutiveFailures] = s.consecutiveFailures
	if s.lastAttempt != nil {
		data[api.ServiceDataLastAttempt] = *s.lastAttempt
	}
	if s.nextRetryAfter != nil {
		data[api.ServiceDataNextRetryAfter] = *s.nextRetryAfter
	}
	if s.lastFailureHTTPStatus != 0 {
		data[api.ServiceDataLastFailureHTTPStatus] = s.lastFailureHTTPStatus
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
		s.emitHealthCheckFailedOnce(err.Error())
		return services.HealthUnhealthy, err
	}

	// Use MCP ping to check health instead of process checking
	if pinger, ok := client.(interface{ Ping(context.Context) error }); ok {
		if err := pinger.Ping(ctx); err != nil {
			s.UpdateHealth(services.HealthUnhealthy)
			healthErr := fmt.Errorf("MCP ping failed: %w", err)
			s.emitHealthCheckFailedOnce(healthErr.Error())
			return services.HealthUnhealthy, healthErr
		}
	}

	s.UpdateHealth(services.HealthHealthy)
	// Clear the gate so the next healthy->unhealthy transition emits again.
	s.resetHealthCheckEventGate()
	return services.HealthHealthy, nil
}

// emitHealthCheckFailedOnce emits MCPServerHealthCheckFailed only on the
// transition into the unhealthy state. Subsequent failing health checks while
// already unhealthy are suppressed so the 30s poll loop does not produce an
// event every interval for a persistently-failing server (the dominant source
// of event spam that kept the feature disabled in production).
func (s *Service) emitHealthCheckFailedOnce(errMsg string) {
	s.healthEventMutex.Lock()
	already := s.healthEventUnhealthy
	s.healthEventUnhealthy = true
	s.healthEventMutex.Unlock()
	if already {
		return
	}
	s.generateEvent(events.ReasonMCPServerHealthCheckFailed, events.EventData{
		Error: errMsg,
	})
}

// resetHealthCheckEventGate clears the unhealthy gate after a healthy check so
// the next failure transition emits a fresh event.
func (s *Service) resetHealthCheckEventGate() {
	s.healthEventMutex.Lock()
	s.healthEventUnhealthy = false
	s.healthEventMutex.Unlock()
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
func (s *Service) createAndInitializeClient(ctx context.Context) error {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()

	// Refuse here and not only at admission: this is the call that spawns the
	// process, so it also covers definitions that reached the orchestrator
	// through a path with no aggregator tool call behind it (issue #1067).
	if err := api.ValidateStdioAllowed(string(s.definition.Type), s.kubernetesMode); err != nil {
		return err
	}

	// Build client configuration from service definition
	// Note: Headers can be nil - the factory and client constructors handle nil maps gracefully
	config := mcpserver.MCPClientConfig{
		Command: s.definition.Command,
		Args:    s.definition.Args,
		Env:     s.definition.Env,
		URL:     s.definition.URL,
		Headers: s.definition.Headers,
		Meta:    s.definition.Meta,
		Auth:    s.definition.Auth,
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
		// Check if this is an authentication required error. errors.As walks
		// the wrap chain so that wrappers from future mcp-go versions do not
		// re-break this detection path.
		var authErr *mcpserver.AuthRequiredError
		if errors.As(err, &authErr) {
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

	// Associate MCPServer lifecycle events with the MCPServer CRD resource in
	// the configured muster namespace so they are not orphaned in "default".
	namespace := eventManager.DefaultNamespace()
	if namespace == "" {
		namespace = "default"
	}
	objectRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      s.GetName(),
		Namespace: namespace,
	}

	err := eventManager.CreateEventWithData(context.Background(), objectRef, string(reason), data.ToAPI())
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

	// A credential that could not be resolved is retryable: the causes are all
	// external and fixable while muster keeps running (a projected token not
	// there yet, a role not deployed, an STS outage), so the server must not
	// settle into a failure that only a pod restart clears.
	if errors.Is(err, mcpserver.ErrAWSCredentialsUnavailable) {
		return true
	}

	// A 401 only reaches here for an auth type with no interactive login: the
	// interactive path returns before the failure handling. For a machine
	// identity that means the signing configuration or the credential is wrong,
	// both of which change without a restart, so keep retrying with backoff.
	var authErr *mcpserver.AuthRequiredError
	if errors.As(err, &authErr) {
		return true
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
	for i := 1; i < s.consecutiveFailures && backoffDuration < MaxBackoff; i++ {
		backoffDuration = time.Duration(float64(backoffDuration) * BackoffMultiplier)
	}
	// The cap always wins, also over an InitialBackoff configured above it.
	backoffDuration = min(backoffDuration, MaxBackoff)

	nextRetry := time.Now().Add(backoffDuration)
	s.nextRetryAfter = &nextRetry
	s.retryBackoff = backoffDuration
}

// retryScheduleLocked describes the failed attempt's HTTP outcome and the
// scheduled retry for logs and the MCPServerFailed event, e.g.
// "endpoint answered HTTP 504, next retry in 2m0s at 2026-09-05T15:57:34Z" or
// "no HTTP response, next retry in 30s at ...". MUST be called with
// failureMutex held.
func (s *Service) retryScheduleLocked() string {
	outcome := "no HTTP response"
	if s.lastFailureHTTPStatus != 0 {
		outcome = "endpoint answered HTTP " + strconv.Itoa(s.lastFailureHTTPStatus)
	}
	if s.nextRetryAfter == nil {
		return outcome
	}
	return fmt.Sprintf("%s, next retry in %s at %s", outcome,
		s.retryBackoff.Round(time.Millisecond), s.nextRetryAfter.UTC().Format(time.RFC3339))
}

// httpStatusFromError returns the HTTP status code named in err's text, or 0
// when the failure carried none (connection refused, DNS, timeout).
func httpStatusFromError(err error) int {
	if err == nil {
		return 0
	}
	match := httpStatusPattern.FindStringSubmatch(err.Error())
	if match == nil {
		return 0
	}
	status, convErr := strconv.Atoi(match[1])
	if convErr != nil {
		return 0
	}
	return status
}

// GetLastFailureHTTPStatus returns the HTTP status the endpoint answered the
// most recent failed connection attempt with, or 0 when there was no HTTP
// response or the last attempt succeeded. Thread-safe.
func (s *Service) GetLastFailureHTTPStatus() int {
	s.failureMutex.RLock()
	defer s.failureMutex.RUnlock()
	return s.lastFailureHTTPStatus
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
