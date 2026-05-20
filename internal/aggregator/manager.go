package aggregator

import (
	"context"
	"errors"
	"fmt"
	"sync"

	configPkg "github.com/giantswarm/muster/internal/config"
	internalmcp "github.com/giantswarm/muster/internal/mcpserver"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/oauth"
	"github.com/giantswarm/muster/pkg/logging"
)

// AggregatorManager owns the aggregator HTTP server and the OAuth proxy that
// authenticates muster's outbound MCP connections. It is the only component
// muster's reconciler calls to (de)register an upstream MCPServer: each dial
// flows through UpstreamProxy ("<proxy>/mcp/<server-name>", streamable-http)
// so agentgateway can apply tracing, audit, metrics, and passthrough auth
// while muster retains token exchange, family grouping, and ADR-006
// session-scoped tool filtering.
type AggregatorManager struct {
	mu     sync.RWMutex
	config AggregatorConfig

	aggregatorServer *AggregatorServer
	oauthManager     *oauth.Manager

	// reconnectLocks serialises ReconnectUpstream by name so concurrent
	// reconnects on the same MCPServer can't interleave Deregister/Register.
	reconnectLocks sync.Map


	ctx        context.Context
	cancelFunc context.CancelFunc
}

// NewAggregatorManager constructs a manager. errorCallback receives fatal
// listener errors from background goroutines and may be nil (errors will
// be logged instead).
func NewAggregatorManager(config AggregatorConfig, errorCallback func(err error)) *AggregatorManager {
	manager := &AggregatorManager{
		config: config,
	}

	manager.aggregatorServer = NewAggregatorServer(config, errorCallback)

	if config.OAuth.Enabled {
		oauthMCPClientConfig := configPkg.OAuthMCPClientConfig{
			Enabled:      config.OAuth.Enabled,
			PublicURL:    config.OAuth.PublicURL,
			ClientID:     config.OAuth.ClientID,
			CallbackPath: config.OAuth.CallbackPath,
			CAFile:       config.OAuth.CAFile,
		}

		var oauthOpts []oauth.ManagerOption
		if vClient := manager.aggregatorServer.getValkeyClient(); vClient != nil {
			keyPrefix := manager.aggregatorServer.getValkeyKeyPrefix()
			enc := manager.aggregatorServer.getValkeyEncryptor()
			logging.Info("Aggregator-Manager", "Using Valkey-backed OAuth token and state stores")
			oauthOpts = append(oauthOpts,
				oauth.WithValkeyTokenStore(oauth.NewValkeyTokenStore(vClient, oauth.DefaultTokenStoreTTL, keyPrefix, enc)),
				oauth.WithValkeyStateStore(oauth.NewValkeyStateStore(vClient, keyPrefix, enc)),
			)
		}

		manager.oauthManager = oauth.NewManager(oauthMCPClientConfig, oauthOpts...)

		if manager.oauthManager != nil {
			oauthAdapter := oauth.NewAdapter(manager.oauthManager)
			oauthAdapter.Register()
			logging.Info("Aggregator-Manager", "OAuth proxy enabled with public URL: %s", config.OAuth.PublicURL)

			manager.oauthManager.SetAuthCompletionCallback(manager.handleAuthCompletion)
		}
	}

	return manager
}

// Start brings the aggregator HTTP server up. Upstream MCPServer registration
// is reconciler-driven (see RegisterUpstream); no initial-sync or retry loop
// runs here.
func (am *AggregatorManager) Start(ctx context.Context) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.ctx, am.cancelFunc = context.WithCancel(ctx)

	if err := am.aggregatorServer.Start(am.ctx); err != nil {
		return fmt.Errorf("failed to start aggregator server: %w", err)
	}

	logging.Info("Aggregator-Manager", "Started aggregator manager on %s", am.aggregatorServer.GetEndpoint())
	return nil
}

// Stop tears the aggregator server and OAuth manager down in reverse order
// of Start. Safe to call multiple times.
func (am *AggregatorManager) Stop(ctx context.Context) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.cancelFunc != nil {
		am.cancelFunc()
	}

	if am.oauthManager != nil {
		am.oauthManager.Stop()
	}

	if am.aggregatorServer != nil {
		if err := am.aggregatorServer.Stop(ctx); err != nil {
			logging.Error("Aggregator-Manager", err, "Error stopping aggregator server")
		}
	}

	logging.Info("Aggregator-Manager", "Stopped aggregator manager")
	return nil
}

// GetServiceData reports current capability counts and configuration for
// monitoring dashboards. Server connectivity counts come from the
// aggregator's own registry (which RegisterUpstream populates) rather than
// the orchestrator service registry.
func (am *AggregatorManager) GetServiceData() map[string]interface{} {
	am.mu.RLock()
	defer am.mu.RUnlock()

	data := map[string]interface{}{
		"port": am.config.Port,
		"host": am.config.Host,
		"yolo": am.config.Yolo,
	}

	if am.aggregatorServer != nil {
		data["endpoint"] = am.aggregatorServer.GetEndpoint()

		tools := am.aggregatorServer.GetTools()
		resources := am.aggregatorServer.GetResources()
		prompts := am.aggregatorServer.GetPrompts()

		data["tools"] = len(tools)
		data["resources"] = len(resources)
		data["prompts"] = len(prompts)

		toolsWithStatus := am.aggregatorServer.GetToolsWithStatus()
		data["tools_with_status"] = toolsWithStatus

		blockedCount := 0
		for _, t := range toolsWithStatus {
			if t.Blocked {
				blockedCount++
			}
		}
		data["blocked_tools"] = blockedCount

		registered := am.aggregatorServer.GetRegistry().GetAllServers()
		connected := 0
		for _, info := range registered {
			if info.IsConnected() {
				connected++
			}
		}
		data["servers_total"] = len(registered)
		data["servers_connected"] = connected
	}

	return data
}

// RegisterUpstream opens the federated streamable-http connection through
// UpstreamProxy for the named MCPServer and inserts it into the aggregator
// registry. On a 401 with WWW-Authenticate the upstream is registered in
// pending-auth state so the synthetic auth tool can drive the OAuth flow.
// Idempotent: a second call for an already-registered name returns nil.
func (am *AggregatorManager) RegisterUpstream(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("aggregator: RegisterUpstream requires a server name")
	}
	if am.config.UpstreamProxy == "" {
		return fmt.Errorf("aggregator: UpstreamProxy not configured")
	}

	am.mu.RLock()
	server := am.aggregatorServer
	am.mu.RUnlock()

	if server == nil {
		return fmt.Errorf("aggregator: server not initialized")
	}

	if _, exists := server.GetRegistry().GetServerInfo(name); exists {
		return nil
	}

	mcpServerMgr := api.GetMCPServerManager()
	if mcpServerMgr == nil {
		return fmt.Errorf("aggregator: MCPServerManager not registered")
	}
	info, err := mcpServerMgr.GetMCPServer(name)
	if err != nil {
		return fmt.Errorf("lookup MCPServer %q: %w", name, err)
	}
	if info == nil {
		return fmt.Errorf("aggregator: MCPServer %q not found", name)
	}

	dialURL := proxyURLFor(am.config.UpstreamProxy, name)
	headers := map[string]string{}
	for k, v := range info.Headers {
		headers[k] = v
	}

	client := internalmcp.NewStreamableHTTPClientWithHeaders(dialURL, headers)
	if err := initializeUpstream(ctx, client, readinessURLFor(am.config.AgentgatewayReadinessPort)); err != nil {
		var authErr *internalmcp.AuthRequiredError
		if errors.As(err, &authErr) {
			_ = client.Close()
			authInfo := &AuthInfo{
				Issuer:              authErr.AuthInfo.Issuer,
				Scope:               authErr.AuthInfo.Scope,
				ResourceMetadataURL: authErr.AuthInfo.ResourceMetadataURL,
			}
			if regErr := am.registerServerPendingAuthForUpstream(name, info, authInfo); regErr != nil {
				return fmt.Errorf("register pending-auth %q: %w", name, regErr)
			}
			logging.Info("Aggregator-Manager", "Registered MCPServer %s in pending-auth state via upstream proxy", name)
			return nil
		}
		_ = client.Close()
		return fmt.Errorf("initialize upstream %q at %s: %w", name, dialURL, err)
	}

	registration := ServerRegistration{
		Name:       name,
		ToolPrefix: info.ToolPrefix,
		Family:     info.Family,
	}
	if err := server.RegisterServer(ctx, registration, client); err != nil {
		_ = client.Close()
		return fmt.Errorf("register upstream %q: %w", name, err)
	}

	logging.Info("Aggregator-Manager", "Registered MCPServer %s via upstream proxy at %s", name, dialURL)
	return nil
}

// UpstreamServerState reports the aggregator's view of an upstream MCPServer.
// Absent (never registered or already deregistered) is the zero value; a
// pending-auth registration returns AuthRequired; a connected client returns
// Connected.
func (am *AggregatorManager) UpstreamServerState(name string) api.UpstreamServerState {
	return am.upstreamServerStateLocked(name, "")
}

// UpstreamServerStateForSession reports the aggregator's view of an upstream
// MCPServer as seen by a specific session. For pending-auth servers it
// promotes the state to Connected when the session has an active pooled
// connection — necessary so core_service_list reflects post-SSO state, since
// the global registry stays in pending-auth (per-session connections live in
// the connection pool, not the global registry).
func (am *AggregatorManager) UpstreamServerStateForSession(ctx context.Context, name string) api.UpstreamServerState {
	sessionID := api.GetSessionIDFromContext(ctx)
	return am.upstreamServerStateLocked(name, sessionID)
}

func (am *AggregatorManager) upstreamServerStateLocked(name, sessionID string) api.UpstreamServerState {
	am.mu.RLock()
	server := am.aggregatorServer
	am.mu.RUnlock()
	if server == nil {
		return api.UpstreamServerAbsent
	}
	info, exists := server.GetRegistry().GetServerInfo(name)
	if !exists {
		return api.UpstreamServerAbsent
	}
	if info.RequiresSessionAuth() {
		if sessionID != "" && server.HasPooledConnection(sessionID, name) {
			return api.UpstreamServerConnected
		}
		return api.UpstreamServerAuthRequired
	}
	if info.IsConnected() {
		return api.UpstreamServerConnected
	}
	return api.UpstreamServerAbsent
}

// ReconnectUpstream atomically deregisters and re-registers an upstream
// MCPServer under a per-name lock so concurrent reconnects on the same
// name serialise and never interleave their Deregister/Register pair.
func (am *AggregatorManager) ReconnectUpstream(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("aggregator: ReconnectUpstream requires a server name")
	}
	lockAny, _ := am.reconnectLocks.LoadOrStore(name, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	if err := am.DeregisterUpstream(ctx, name); err != nil {
		return err
	}
	return am.RegisterUpstream(ctx, name)
}

// DeregisterUpstream removes a previously registered MCPServer and closes
// its client. Returns nil when no registration exists.
func (am *AggregatorManager) DeregisterUpstream(_ context.Context, name string) error {
	am.mu.RLock()
	server := am.aggregatorServer
	am.mu.RUnlock()

	if server == nil {
		return nil
	}
	if _, exists := server.GetRegistry().GetServerInfo(name); !exists {
		return nil
	}
	if err := server.DeregisterServer(name); err != nil {
		return fmt.Errorf("deregister upstream %q: %w", name, err)
	}
	logging.Info("Aggregator-Manager", "Deregistered MCPServer %s", name)
	return nil
}

// RegisterServerPendingAuth registers a server that requires OAuth with no
// extra auth config; preserved for the api.AggregatorHandler interface.
func (am *AggregatorManager) RegisterServerPendingAuth(serverName, url, toolPrefix string, authInfo *AuthInfo) error {
	return am.RegisterServerPendingAuthWithConfig(serverName, url, toolPrefix, authInfo, nil)
}

// RegisterServerPendingAuthWithConfig registers a server requiring OAuth and
// stores its auth configuration so synthetic auth tools can drive forwarding
// or RFC 8693 exchange after browser auth completes. url is the upstream's
// own URL (used in the synthetic tool's user-facing description); the dial
// URL is computed from UpstreamProxy at session-connection time.
func (am *AggregatorManager) RegisterServerPendingAuthWithConfig(serverName, url, toolPrefix string, authInfo *AuthInfo, authConfig *api.MCPServerAuth) error {
	return am.registerServerPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{
			Name:       serverName,
			ToolPrefix: toolPrefix,
		},
		URL:        url,
		AuthInfo:   authInfo,
		AuthConfig: authConfig,
	})
}

// registerServerPendingAuthForUpstream is the reconciler-driven path: it
// carries the MCPServer's declared spec.family through into the registry so
// pending-auth servers participate in family grouping once they connect.
func (am *AggregatorManager) registerServerPendingAuthForUpstream(serverName string, info *api.MCPServerInfo, authInfo *AuthInfo) error {
	return am.registerServerPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{
			Name:       serverName,
			ToolPrefix: info.ToolPrefix,
			Family:     info.Family,
		},
		URL:        info.URL,
		AuthInfo:   authInfo,
		AuthConfig: info.Auth,
	})
}

func (am *AggregatorManager) registerServerPendingAuth(registration PendingAuthRegistration) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.aggregatorServer == nil {
		return fmt.Errorf("aggregator server not available")
	}

	if err := am.aggregatorServer.GetRegistry().RegisterPendingAuthFromRegistration(registration); err != nil {
		return err
	}

	authConfig := registration.AuthConfig
	if authConfig != nil && (authConfig.ForwardToken || (authConfig.TokenExchange != nil && authConfig.TokenExchange.Enabled)) {
		am.aggregatorServer.wirePoolNotificationCallback(registration.Name)
	}

	return nil
}

// handleAuthCompletion runs after a user finishes browser-based OAuth. The
// session-establishing connection it kicks off is dialed via UpstreamProxy
// inside establishConnection (see connection_helper.go); url and issuer here
// are display-only.
func (am *AggregatorManager) handleAuthCompletion(ctx context.Context, sessionID, userID, serverName, accessToken string) error {
	am.mu.RLock()
	aggregatorServer := am.aggregatorServer
	am.mu.RUnlock()

	if aggregatorServer == nil {
		return fmt.Errorf("aggregator server not available")
	}

	serverInfo, exists := aggregatorServer.GetRegistry().GetServerInfo(serverName)
	if !exists {
		return fmt.Errorf("server %s not found", serverName)
	}

	var issuer, scope string
	if serverInfo.AuthInfo != nil {
		issuer = serverInfo.AuthInfo.Issuer
		scope = serverInfo.AuthInfo.Scope
	}

	logging.Info("Aggregator-Manager", "OAuth callback completing - establishing connection for session=%s server=%s",
		logging.TruncateIdentifier(sessionID), serverName)

	ctx = api.WithSessionID(ctx, sessionID)
	ctx = api.WithSubject(ctx, userID)

	result, err := aggregatorServer.tryConnectWithToken(ctx, serverName, serverInfo.URL, issuer, scope, accessToken)
	if err != nil {
		return fmt.Errorf("failed to establish connection: %w", err)
	}

	if result != nil && len(result.Content) > 0 {
		logging.Debug("Aggregator-Manager", "Connection established successfully")
	}

	return nil
}

// AgentgatewayListenerPort surfaces the filesystem-mode agentgateway bind
// port so the reconciler's yaml.Applier writes a matching `binds[].port`.
// Zero in cluster mode (agentgateway is deployed out-of-band).
func (am *AggregatorManager) AgentgatewayListenerPort() uint16 {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.config.AgentgatewayListenerPort
}

// AgentgatewayManagementPorts surfaces the filesystem-mode agentgateway
// admin / stats / readiness ports the yaml.Applier should embed and the
// subprocess manager should probe. Zero values mean "use the agentgateway
// defaults (15000 / 15020 / 15021)".
func (am *AggregatorManager) AgentgatewayManagementPorts() (admin, stats, readiness uint16) {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.config.AgentgatewayAdminPort, am.config.AgentgatewayStatsPort, am.config.AgentgatewayReadinessPort
}

// GetEndpoint returns the aggregator's MCP endpoint URL.
func (am *AggregatorManager) GetEndpoint() string {
	am.mu.RLock()
	defer am.mu.RUnlock()
	if am.aggregatorServer != nil {
		return am.aggregatorServer.GetEndpoint()
	}
	return ""
}

// GetAggregatorServer exposes the underlying server for advanced operations
// (test helpers, the API adapter's CallTool path).
func (am *AggregatorManager) GetAggregatorServer() *AggregatorServer {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.aggregatorServer
}
