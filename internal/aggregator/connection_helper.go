package aggregator

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	internalmcp "github.com/giantswarm/muster/internal/mcpserver"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/events"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
	"github.com/giantswarm/muster/internal/server"
	"github.com/giantswarm/muster/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// errMissingSession is the user-facing message returned when a tool call
// lacks session/subject context. Shared by requireSessionContext and
// requireSessionContextResult so the wording stays in sync.
const errMissingSession = "Error: authentication context missing — no active session"

// requireSessionContext extracts sessionID and subject from ctx,
// returning an error that names serverName when either is missing.
func requireSessionContext(ctx context.Context, serverName string) (sessionID, sub string, err error) {
	sessionID = getSessionIDFromContext(ctx)
	sub = getUserSubjectFromContext(ctx)
	if sessionID == "" || sub == "" {
		return "", "", fmt.Errorf("no session context available for server %s", serverName)
	}
	return sessionID, sub, nil
}

// requireSessionContextResult is like requireSessionContext but returns an
// api.CallToolResult error suitable for direct return from tool handlers.
func requireSessionContextResult(ctx context.Context) (sessionID, sub string, errResult *api.CallToolResult) {
	sessionID = getSessionIDFromContext(ctx)
	sub = getUserSubjectFromContext(ctx)
	if sessionID == "" || sub == "" {
		return "", "", &api.CallToolResult{
			Content: []interface{}{errMissingSession},
			IsError: true,
		}
	}
	return sessionID, sub, nil
}

// ConnectionResult contains the result of establishing a session connection.
// This is returned by establishConnection and used by callers to format
// their specific result types (api.CallToolResult or mcp.CallToolResult).
//
// The Client field holds the live, initialized MCP client. Ownership is
// transferred to the caller, who must either pool or close it.
type ConnectionResult struct {
	// ServerName is the name of the server that was connected
	ServerName string
	// ToolCount is the number of tools available from the server
	ToolCount int
	// ResourceCount is the number of resources available from the server
	ResourceCount int
	// PromptCount is the number of prompts available from the server
	PromptCount int
	// Client is the live MCP client. The caller owns its lifecycle and must
	// either pool it for reuse or close it when done.
	Client MCPClient
	// TokenExpiry records when the client's bearer token expires. Zero means
	// no expiry tracking (e.g., token forwarding clients). Callers should pass
	// this to SessionConnectionPool.PutWithExpiry for proactive refresh.
	TokenExpiry time.Time
	// ExchangedToken is the RFC 8693 exchanged bearer this client sends
	// downstream. Populated only by the token-exchange path — forward-token
	// and DynamicAuth connections leave it empty. Callers should pass it to
	// SessionConnectionPool.SetExchangedToken so the admin UI can surface it.
	ExchangedToken string
}

// establishConnection creates a connection to an MCP server and populates
// the CapabilityStore. This is the shared implementation used by both:
//   - AuthToolProvider.tryConnectWithToken (core_auth_login tool)
//   - AggregatorServer.tryConnectWithToken (OAuth browser callback, manager.go)
//
// This method:
//  1. Creates the appropriate client (DynamicAuthClient or static headers)
//  2. Initializes the connection and fetches capabilities
//  3. Populates the CapabilityStore and registers tools
//  4. Broadcasts tool change notifications
//
// Both sessionID and sub are extracted from the context. The sessionID is used
// as the cache key for per-login-session isolation, while sub is used for user
// identity operations (notifications).
//
// Args:
//   - ctx: Context for the operation (must contain sessionID and sub)
//   - a: The aggregator server instance
//   - serverName: Name of the MCP server
//   - serverURL: URL of the MCP server
//   - issuer: OAuth issuer URL (empty for non-OAuth servers)
//   - scope: OAuth scope (empty for non-OAuth servers)
//   - accessToken: The access token to use for authentication
//
// Returns the connection result or an error if connection failed.
func establishConnection(
	ctx context.Context,
	a *AggregatorServer,
	serverName, serverURL, issuer, scope, accessToken string,
) (*ConnectionResult, error) {
	sessionID, sub, err := requireSessionContext(ctx, serverName)
	if err != nil {
		return nil, err
	}

	oauthHandler := api.GetOAuthHandler()

	var client internalmcp.MCPClient
	if oauthHandler != nil && oauthHandler.IsEnabled() && issuer != "" {
		tokenStore := internalmcp.NewMusterTokenStore(sessionID, sub, issuer, oauthHandler)
		client = internalmcp.NewDynamicAuthClient(serverURL, tokenStore, scope)
		logging.Debug("Connection", "Using DynamicAuthClient for session %s, server %s (issuer=%s)",
			logging.TruncateIdentifier(sessionID), serverName, issuer)
	} else {
		headers := map[string]string{
			pkgoauth.HeaderAuthorization: pkgoauth.SchemeBearer + " " + accessToken,
		}
		client = internalmcp.NewStreamableHTTPClientWithHeaders(serverURL, headers)
		logging.Debug("Connection", "Using static auth headers for session %s, server %s",
			logging.TruncateIdentifier(sessionID), serverName)
	}

	// Try to initialize the client
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to initialize connection: %w", err)
	}

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverName, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverName, err)
		prompts = nil
	}

	// Populate the CapabilityStore keyed by session ID for per-login isolation
	if a.capabilityStore != nil {
		if err := a.capabilityStore.Set(ctx, sessionID, serverName, &oauthstore.Capabilities{
			Tools: tools, Resources: resources, Prompts: prompts,
		}); err != nil {
			logging.Warn("Connection", "Failed to store capabilities for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
		}
	}

	if a.authStore != nil {
		if err := a.authStore.MarkAuthenticated(ctx, sessionID, serverName); err != nil {
			logging.Warn("Connection", "Failed to mark auth for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverName, err)
		}
	}

	// Sync service state to Connected now that authentication succeeded
	notifyMCPServerConnected(serverName, "authentication")

	logging.Info("Connection", "User %s connected to %s with %d tools, %d resources, %d prompts",
		logging.TruncateIdentifier(sub), serverName, len(tools), len(resources), len(prompts))

	return &ConnectionResult{
		ServerName:    serverName,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
		Client:        client,
	}, nil
}

// FormatAsAPIResult formats the connection result as an api.CallToolResult.
// Used by AuthToolProvider.tryConnectWithToken.
func (r *ConnectionResult) FormatAsAPIResult() *api.CallToolResult {
	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf(
			api.AuthMsgSuccessfullyConnected+" to '%s'!\n\n"+
				"Available capabilities:\n"+
				"- Tools: %d\n"+
				"- Resources: %d\n"+
				"- Prompts: %d\n\n"+
				"You can now use the tools from this server.",
			r.ServerName, r.ToolCount, r.ResourceCount, r.PromptCount,
		)},
		IsError: false,
	}
}

// FormatAsMCPResult formats the connection result as an mcp.CallToolResult.
// Used by AggregatorServer.tryConnectWithToken.
func (r *ConnectionResult) FormatAsMCPResult() *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(fmt.Sprintf(
				api.AuthMsgSuccessfullyConnected+" to %s!\n\n"+
					"Available capabilities:\n"+
					"- Tools: %d\n"+
					"- Resources: %d\n"+
					"- Prompts: %d\n\n"+
					"You can now use the tools from this server.",
				r.ServerName, r.ToolCount, r.ResourceCount, r.PromptCount,
			)),
		},
		IsError: false,
	}
}

// getIDTokenForForwarding retrieves an ID token for SSO token forwarding from available sources.
//
// Token sources are checked in priority order:
//  1. Request context - contains the ID token when called from within an HTTP request handler.
//     Injected by createAccessTokenInjectorMiddleware from the Valkey token store.
//  2. OAuth manager's token store - contains the token populated by SetSessionCreationHandler
//     and SetTokenRefreshHandler, looked up by (sessionID, musterIssuer). This is the primary
//     source for background closures (headerFunc) that run outside the HTTP request lifecycle
//     with context.Background().
//  3. If the store yields no valid token and refresher is non-nil, an in-process upstream
//     provider refresh is attempted via refresher(ctx, sessionID). On success, TokenRefreshHandler
//     fires synchronously, the proxy store is updated, and the store is re-read.
//
// The context token takes priority because it's the freshest, directly from the current request.
//
// Args:
//   - ctx: Request context that may contain an injected ID token
//   - sessionID: The session ID (token family ID) for token store lookups
//   - musterIssuer: The issuer URL to look up in the OAuth proxy store
//   - refresher: Optional callback to refresh the upstream session (may be nil)
//
// Returns the ID token string, or empty string if no token is available.
func getIDTokenForForwarding(ctx context.Context, sessionID, musterIssuer string, refresher func(context.Context, string) error) string {
	if idToken, ok := server.GetIDTokenFromContext(ctx); ok && idToken != "" {
		logging.Debug("Connection", "Found ID token in request context for session %s",
			logging.TruncateIdentifier(sessionID))
		return idToken
	}

	oauthHandler := api.GetOAuthHandler()
	if oauthHandler != nil && oauthHandler.IsEnabled() && musterIssuer != "" {
		fullToken := oauthHandler.GetFullTokenByIssuer(sessionID, musterIssuer)
		if fullToken != nil && fullToken.IDToken != "" {
			logging.Debug("Connection", "Found ID token in OAuth proxy store for session %s, issuer %s",
				logging.TruncateIdentifier(sessionID), musterIssuer)
			return fullToken.IDToken
		}

		// No valid token in the store (expired or never set). Attempt an in-process
		// upstream refresh so that TokenRefreshHandler fires and repopulates the store.
		if refresher != nil {
			if err := refresher(ctx, sessionID); err != nil {
				logging.Debug("Connection", "Session refresh failed for %s: %v",
					logging.TruncateIdentifier(sessionID), err)
			} else {
				fullToken = oauthHandler.GetFullTokenByIssuer(sessionID, musterIssuer)
				if fullToken != nil && fullToken.IDToken != "" {
					logging.Info("Connection", "Recovered ID token via session refresh for session %s",
						logging.TruncateIdentifier(sessionID))
					return fullToken.IDToken
				}
			}
		}
	}

	logging.Debug("Connection", "No ID token found for session %s",
		logging.TruncateIdentifier(sessionID))
	return ""
}

// EstablishConnectionWithTokenForwarding attempts to establish a connection
// using ID token forwarding for SSO. This is used when an MCPServer has forwardToken: true.
//
// The function:
//  1. Gets the user's ID token from muster's OAuth session
//  2. Forwards it to the downstream MCP server
//  3. If successful, populates the CapabilityStore and registers tools
//
// Both sessionID and sub are extracted from ctx (set by OAuth middleware).
//
// Args:
//   - ctx: Context for the operation (must contain sessionID and sub)
//   - a: The aggregator server instance
//   - serverInfo: The server info containing URL and auth config
//   - musterIssuer: The issuer URL of muster's OAuth provider (used to get the ID token)
//
// Returns:
//   - *ConnectionResult: The connection result if successful
//   - error: The error if connection failed
func EstablishConnectionWithTokenForwarding(
	ctx context.Context,
	a *AggregatorServer,
	serverInfo *ServerInfo,
	musterIssuer string,
) (*ConnectionResult, error) {
	sessionID, sub, err := requireSessionContext(ctx, serverInfo.Name)
	if err != nil {
		return nil, err
	}

	if a.authStore != nil {
		authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverInfo.Name)
		if authenticated {
			logging.Debug("Connection", "Session %s already authenticated to %s, skipping token forwarding",
				logging.TruncateIdentifier(sessionID), serverInfo.Name)
			return &ConnectionResult{ServerName: serverInfo.Name}, nil
		}
	}

	// The onStaleToken callback evicts the pooled connection and revokes the
	// auth entry when the token cannot be resolved after repeated attempts.
	// This stops mcp-go's infinite retry loop with expired tokens.
	onStaleToken := func() {
		if a.connPool != nil {
			a.connPool.Evict(sessionID, serverInfo.Name)
			logging.Info("Connection", "Evicted stale SSO connection for session %s to %s",
				logging.TruncateIdentifier(sessionID), serverInfo.Name)
		}
		if a.authStore != nil {
			if err := a.authStore.Revoke(context.Background(), sessionID, serverInfo.Name); err != nil {
				logging.Warn("Connection", "Failed to revoke auth for session %s server %s after stale token eviction: %v",
					logging.TruncateIdentifier(sessionID), serverInfo.Name, err)
			}
		}
	}
	client, forwardedToken, err := a.newTokenForwardingClient(ctx, sessionID, sub, musterIssuer, serverInfo, onStaleToken)
	if err != nil {
		return nil, err
	}

	logging.Info("Connection", "Attempting ID token forwarding for user %s to server %s",
		logging.TruncateIdentifier(sub), serverInfo.Name)

	// Try to initialize the client with the forwarded token
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()

		// A rejection here is indistinguishable from other transport failures
		// (mcp-go surfaces the 401 as a generic initialize error), so the
		// issuer diagnostic is attached to every connect failure.
		logging.Warn("Connection", "ID token forwarding failed for user %s to server %s: %v (%s)",
			logging.TruncateIdentifier(sub), serverInfo.Name, err, forwardedTokenDiagnostic(forwardedToken))

		// Emit event for token forwarding failure
		emitTokenForwardingEvent(serverInfo.Name, serverInfo.GetNamespace(), false, err.Error())

		return nil, fmt.Errorf("ID token forwarding failed: %w", err)
	}

	// Token forwarding succeeded - emit success event
	logging.Info("Connection", "ID token forwarding succeeded for user %s to server %s",
		logging.TruncateIdentifier(sub), serverInfo.Name)
	emitTokenForwardingEvent(serverInfo.Name, serverInfo.GetNamespace(), true, "")

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to list tools after token forwarding: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)
		prompts = nil
	}

	// Populate the CapabilityStore keyed by session ID for per-login isolation
	if a.capabilityStore != nil {
		if err := a.capabilityStore.Set(ctx, sessionID, serverInfo.Name, &oauthstore.Capabilities{
			Tools: tools, Resources: resources, Prompts: prompts,
		}); err != nil {
			logging.Warn("Connection", "Failed to store capabilities for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverInfo.Name, err)
		}
	}

	if a.authStore != nil {
		if err := a.authStore.MarkAuthenticated(ctx, sessionID, serverInfo.Name); err != nil {
			logging.Warn("Connection", "Failed to mark auth for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverInfo.Name, err)
		}
	}

	// Sync service state to Connected now that SSO succeeded
	notifyMCPServerConnected(serverInfo.Name, "SSO token forwarding")

	logging.Info("Connection", "User %s connected to %s via SSO token forwarding with %d tools",
		logging.TruncateIdentifier(sub), serverInfo.Name, len(tools))

	return &ConnectionResult{
		ServerName:    serverInfo.Name,
		ToolCount:     len(tools),
		ResourceCount: len(resources),
		PromptCount:   len(prompts),
		Client:        client,
	}, nil
}

// emitTokenForwardingEvent records token forwarding outcomes. Successful
// forwarding fires on every session connecting to every SSO server, so it is
// demoted to a debug log (the highest-volume Normal-event noise on a
// multi-user instance). Only failures are surfaced as Warning Kubernetes
// events, where they carry actionable signal.
func emitTokenForwardingEvent(serverName, namespace string, success bool, errorMsg string) {
	if success {
		logging.Debug("Connection", "ID token forwarded for SSO authentication to MCPServer %s", serverName)
		return
	}

	eventManager := api.GetEventManager()
	if eventManager == nil {
		return
	}

	// Log when namespace is missing - this indicates a configuration issue
	if namespace == "" {
		logging.Warn("Connection", "No namespace set for server %s event, defaulting to %q - check MCPServer configuration", serverName, metav1.NamespaceDefault)
		namespace = metav1.NamespaceDefault
	}

	objRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      serverName,
		Namespace: namespace,
	}

	_ = eventManager.CreateEventWithData(context.Background(), objRef, string(events.ReasonMCPServerTokenForwardingFailed), api.EventData{
		Error: errorMsg,
	})
}

// ShouldUseTokenForwarding checks if token forwarding should be used for a server.
// Token forwarding is enabled when:
//   - AuthConfig.ForwardToken is true (forwardToken implies OAuth-based auth)
//   - OR: AuthConfig.Type is "oauth" and ForwardToken is true
//
// Setting forwardToken: true implicitly enables OAuth authentication since
// token forwarding only makes sense in an OAuth context.
func ShouldUseTokenForwarding(serverInfo *ServerInfo) bool {
	if serverInfo == nil || serverInfo.AuthConfig == nil {
		return false
	}
	// ForwardToken implies OAuth authentication - no need to check Type explicitly
	return serverInfo.AuthConfig.ForwardToken
}

// ShouldUseTokenExchange checks if RFC 8693 token exchange should be used for a server.
// Token exchange is enabled when:
//   - AuthConfig.TokenExchange is not nil
//   - AuthConfig.TokenExchange.Enabled is true
//   - Required fields (DexTokenEndpoint, ConnectorID) are set
//
// Token exchange takes precedence over token forwarding if both are configured.
func ShouldUseTokenExchange(serverInfo *ServerInfo) bool {
	if serverInfo == nil || serverInfo.AuthConfig == nil || serverInfo.AuthConfig.TokenExchange == nil {
		return false
	}
	config := serverInfo.AuthConfig.TokenExchange
	return config.Enabled && config.DexTokenEndpoint != "" && config.ConnectorID != ""
}

// EstablishConnectionWithTokenExchange attempts to establish a connection
// using RFC 8693 Token Exchange for cross-cluster SSO. This is used when an MCPServer has
// tokenExchange configured.
//
// The function:
//  1. Gets the user's ID token from muster's OAuth session
//  2. Extracts the user ID (sub claim) from the token
//  3. Exchanges it for a token valid on the remote cluster's Dex
//  4. If successful, populates the CapabilityStore and registers tools
//
// Both sessionID and sub are extracted from ctx (set by OAuth middleware).
//
// Args:
//   - ctx: Context for the operation (must contain sessionID and sub)
//   - a: The aggregator server instance
//   - serverInfo: The server info containing URL and auth config
//   - musterIssuer: The issuer URL of muster's OAuth provider (used to get the ID token)
//
// Returns:
//   - *ConnectionResult: The connection result if successful
//   - error: The error if connection failed

func EstablishConnectionWithTokenExchange(
	ctx context.Context,
	a *AggregatorServer,
	serverInfo *ServerInfo,
	musterIssuer string,
) (*ConnectionResult, error) {
	if serverInfo == nil || serverInfo.AuthConfig == nil || serverInfo.AuthConfig.TokenExchange == nil {
		return nil, fmt.Errorf("invalid server configuration for token exchange")
	}

	sessionID, sub, err := requireSessionContext(ctx, serverInfo.Name)
	if err != nil {
		return nil, err
	}
	if a.authStore != nil {
		if authenticated, _ := a.authStore.IsAuthenticated(ctx, sessionID, serverInfo.Name); authenticated {
			logging.Debug("Connection", "Session %s already authenticated to %s, skipping token exchange",
				logging.TruncateIdentifier(sessionID), serverInfo.Name)
			return &ConnectionResult{ServerName: serverInfo.Name}, nil
		}
	}

	// Get the OAuth handler for token exchange
	oauthHandler := api.GetOAuthHandler()
	if oauthHandler == nil || !oauthHandler.IsEnabled() {
		return nil, fmt.Errorf("OAuth handler not available for token exchange")
	}

	// Get ID token from multiple sources (in priority order):
	// 1. Request context (for tokens from muster's OAuth server protection)
	// 2. OAuth proxy token store (for tokens from muster's own OAuth session)
	idToken := getIDTokenForForwarding(ctx, sessionID, musterIssuer, a.sessionRefresher())
	if idToken == "" {
		logging.Debug("Connection", "No ID token available for user %s",
			logging.TruncateIdentifier(sub))
		return nil, fmt.Errorf("no ID token available for token exchange")
	}

	// Validate ID token is not expired before exchanging
	if expired, expErr := pkgoauth.IsExpired(idToken); expired {
		logging.Warn("Connection", "ID token expired for user %s, cannot exchange for %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, expErr)
		return nil, fmt.Errorf("ID token has expired, needs refresh before exchange")
	}

	// Extract user ID from the token for cache key generation
	userID, err := pkgoauth.Subject(idToken)
	if err != nil || userID == "" {
		logging.Warn("Connection", "Failed to extract user ID from token for user %s: %v",
			logging.TruncateIdentifier(sub), err)
		return nil, fmt.Errorf("failed to extract user ID from token: %w", err)
	}

	logging.Info("Connection", "Attempting token exchange for user %s to server %s",
		logging.TruncateIdentifier(sub), serverInfo.Name)

	// Load client credentials from secret if configured.
	var clientID, clientSecret string
	if serverInfo.AuthConfig.TokenExchange.ClientCredentialsSecretRef != nil {
		credentials, err := loadTokenExchangeCredentials(ctx, serverInfo)
		if err != nil {
			logging.Error("Connection", err, "Failed to load token exchange credentials for %s", serverInfo.Name)
			return nil, fmt.Errorf("failed to load client credentials: %w", err)
		}
		clientID, clientSecret = credentials.ClientID, credentials.ClientSecret
		logging.Debug("Connection", "Loaded client credentials for token exchange to %s (client_id=%s)",
			serverInfo.Name, credentials.ClientID)
	}

	// Stamp the runtime state onto a value copy; never mutate
	// serverInfo.AuthConfig.TokenExchange in place, it is shared with the registry
	// definition (see api.TokenExchangeConfig.WithResolvedRuntime).
	exchangeConfig, err := serverInfo.AuthConfig.TokenExchange.WithResolvedRuntime(
		clientID,
		clientSecret,
		serverInfo.AuthConfig.RequiredAudiences,
	)
	if err != nil {
		// Log the error but continue without the audiences - they should already be
		// validated at CRD admission, but handle gracefully if not.
		logging.Warn("Connection", "Failed to format audience scopes for %s: %v (continuing without audiences)",
			serverInfo.Name, err)
	} else if len(serverInfo.AuthConfig.RequiredAudiences) > 0 {
		logging.Debug("Connection", "Added %d required audiences to token exchange scopes for %s",
			len(serverInfo.AuthConfig.RequiredAudiences), serverInfo.Name)
	}

	// Perform the token exchange.
	exchangedToken, err := oauthHandler.ExchangeTokenForRemoteCluster(
		ctx,
		idToken,
		userID,
		&exchangeConfig.TokenExchangeConfig,
	)
	if err != nil {
		logging.Warn("Connection", "Token exchange failed for user %s to server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)

		// Emit event for token exchange failure
		emitTokenExchangeEvent(serverInfo.Name, serverInfo.GetNamespace(), false, err.Error())

		// Audit log for failed token exchange (compliance/security monitoring)
		logging.Audit(logging.AuditEvent{
			Action:  "token_exchange",
			Outcome: "failure",
			Subject: logging.TruncateIdentifier(sub),
			UserID:  logging.TruncateIdentifier(userID),
			Target:  serverInfo.Name,
			Details: fmt.Sprintf("endpoint=%s connector=%s", serverInfo.AuthConfig.TokenExchange.DexTokenEndpoint, serverInfo.AuthConfig.TokenExchange.ConnectorID),
			Error:   err.Error(),
		})

		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Token exchange succeeded - emit success event and audit log
	logging.Info("Connection", "Token exchange succeeded for user %s to server %s",
		logging.TruncateIdentifier(sub), serverInfo.Name)
	emitTokenExchangeEvent(serverInfo.Name, serverInfo.GetNamespace(), true, "")

	// Audit log for successful token exchange (compliance/security monitoring)
	logging.Audit(logging.AuditEvent{
		Action:  "token_exchange",
		Outcome: "success",
		Subject: logging.TruncateIdentifier(sub),
		UserID:  logging.TruncateIdentifier(userID),
		Target:  serverInfo.Name,
		Details: fmt.Sprintf("endpoint=%s connector=%s", serverInfo.AuthConfig.TokenExchange.DexTokenEndpoint, serverInfo.AuthConfig.TokenExchange.ConnectorID),
	})

	// Extract the exchanged token's expiry for proactive pool refresh.
	// If the token is near expiry, getOrCreateClientForToolCall will
	// proactively evict the pooled client and re-exchange before the
	// downstream server returns 401.
	tokenExpiry, err := pkgoauth.Expiry(exchangedToken)
	if err != nil {
		logging.Debug("TokenExchange", "Could not extract expiry from exchanged token, refreshing on the fallback interval: %v", err)
	}

	// The exchanged access token has a fixed lifetime (Dex default 30m). The
	// persistent connection carries mcp-go's continuous listener, which outlives
	// that lifetime and never traverses the tool-call retry path, so the token
	// must be refreshed on the connection itself. reexchange mints a fresh token
	// from the latest subject ID token; onStaleToken evicts the connection when
	// the subject can no longer be refreshed, so the listener stops instead of
	// looping an expired token and the state settles to Auth Required.
	reexchange, onStaleToken := a.makeTokenExchangeRefreshClosures(
		serverInfo.Name, sessionID, userID, musterIssuer, oauthHandler, &exchangeConfig,
	)

	headerFunc := makeTokenExchangeHeaderFunc(serverInfo.Name, exchangedToken, tokenExpiry, reexchange, onStaleToken)

	client := internalmcp.NewStreamableHTTPClientWithHeaderFunc(serverInfo.URL, headerFunc)

	// Try to initialize the client with the exchanged token
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()

		logging.Warn("Connection", "Connection with exchanged token failed for user %s to server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)

		return nil, fmt.Errorf("connection with exchanged token failed: %w", err)
	}

	// Fetch tools from the server
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to list tools after token exchange: %w", err)
	}

	// Fetch resources and prompts (optional - some servers may not support them)
	resources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list resources for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)
		resources = nil
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Connection", "Failed to list prompts for user %s, server %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, err)
		prompts = nil
	}

	// Populate the CapabilityStore keyed by session ID for per-login isolation
	if a.capabilityStore != nil {
		if storeErr := a.capabilityStore.Set(ctx, sessionID, serverInfo.Name, &oauthstore.Capabilities{
			Tools: tools, Resources: resources, Prompts: prompts,
		}); storeErr != nil {
			logging.Warn("Connection", "Failed to store capabilities for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverInfo.Name, storeErr)
		}
	}

	if a.authStore != nil {
		if err := a.authStore.MarkAuthenticated(ctx, sessionID, serverInfo.Name); err != nil {
			logging.Warn("Connection", "Failed to mark auth for %s/%s: %v",
				logging.TruncateIdentifier(sessionID), serverInfo.Name, err)
		}
	}

	// Sync service state to Connected now that token exchange succeeded
	notifyMCPServerConnected(serverInfo.Name, "RFC 8693 token exchange")

	logging.Info("Connection", "User %s connected to %s via RFC 8693 token exchange with %d tools",
		logging.TruncateIdentifier(sub), serverInfo.Name, len(tools))

	return &ConnectionResult{
		ServerName:     serverInfo.Name,
		ToolCount:      len(tools),
		ResourceCount:  len(resources),
		PromptCount:    len(prompts),
		Client:         client,
		TokenExpiry:    tokenExpiry,
		ExchangedToken: exchangedToken,
	}, nil
}

// emitTokenExchangeEvent records token exchange outcomes. Like token
// forwarding, successful exchange fires per-session per-server and is demoted
// to a debug log; only failures are surfaced as Warning Kubernetes events.
func emitTokenExchangeEvent(serverName, namespace string, success bool, errorMsg string) {
	if success {
		logging.Debug("Connection", "Token exchanged for cross-cluster SSO to MCPServer %s", serverName)
		return
	}

	eventManager := api.GetEventManager()
	if eventManager == nil {
		return
	}

	// Log when namespace is missing - this indicates a configuration issue
	if namespace == "" {
		logging.Warn("Connection", "No namespace set for server %s event, defaulting to %q - check MCPServer configuration", serverName, metav1.NamespaceDefault)
		namespace = metav1.NamespaceDefault
	}

	objRef := api.ObjectReference{
		Kind:      "MCPServer",
		Name:      serverName,
		Namespace: namespace,
	}

	_ = eventManager.CreateEventWithData(context.Background(), objRef, string(events.ReasonMCPServerTokenExchangeFailed), api.EventData{
		Error: errorMsg,
	})
}

// tokenExchangeRefreshMargin is the time before expiry at which a pooled
// token-exchange client triggers a background re-exchange. Dex access tokens
// typically live for 30 minutes, so a 5-minute margin gives ample time for
// the background goroutine to complete the exchange + client initialization
// without blocking the user's request.
const tokenExchangeRefreshMargin = 5 * time.Minute

// tokenReexchangeTimeout bounds the subject-token resolution and Dex round-trip
// performed by the token-exchange header closure. It must stay well under
// tokenExchangeRefreshMargin so a slow or hung Dex surfaces as a failure (which
// advances the eviction counter) long before the current token actually expires.
const tokenReexchangeTimeout = 30 * time.Second

// tokenExchangeFallbackRefreshInterval is how long an exchanged token is assumed
// valid when it carries no parseable exp. A zero expiry must not latch proactive
// refresh off for the life of the connection: the listener would then loop an
// expired token again, which is the exact failure the refresh exists to prevent.
// Refreshing blind on this interval keeps the token fresh without a Dex round-trip
// on every header call.
const tokenExchangeFallbackRefreshInterval = 5 * time.Minute

// headerFuncWarnInterval is the minimum interval between WARN-level log messages
// when the headerFunc closure fails to resolve an ID token. Between warnings,
// failures are logged at DEBUG to avoid flooding logs from stale sessions.
const headerFuncWarnInterval = 1 * time.Minute

// maxConsecutiveTokenFailures is the number of consecutive token resolution
// failures in the headerFunc before the stale connection is signaled for
// eviction. On the token-forwarding path resolution is a local
// store lookup, so at mcp-go's 1-second retry interval 3 failures ≈ 3 seconds
// before the connection is proactively closed. On the token-exchange path each
// attempt performs a Dex round-trip bounded by tokenReexchangeTimeout, so the
// worst case is longer (up to 3×tokenReexchangeTimeout against a hung Dex);
// the connection still settles cleanly, just not within a few seconds.
const maxConsecutiveTokenFailures = 3

// makeTokenExchangeHeaderFunc builds the header function for a token-exchange
// connection. The exchanged token is refreshed in place: once the current token
// enters the tokenExchangeRefreshMargin window before its expiry, the next call
// re-exchanges a fresh one via reexchange and caches it. On re-exchange failure
// the previous token is kept as a fallback; after maxConsecutiveTokenFailures
// consecutive failures onStaleToken is invoked once (asynchronously) to evict the
// connection so mcp-go's retry loop stops. The counter resets on a successful
// re-exchange, allowing re-eviction if refresh fails again later.
//
// reexchange must be safe to call without a request context; it resolves the
// latest subject token itself and must bound its own network work so it cannot
// block indefinitely. A zero expiry (token carries no parseable exp) is treated
// as tokenExchangeFallbackRefreshInterval from now, so refresh keeps firing
// blind rather than latching off and re-stranding the listener.
//
// mcp-go invokes the returned closure concurrently on a single connection: the
// continuous-listening GET runs in its own goroutine (listenForever) while tool
// calls invoke it from their own goroutines, and both reach headerFunc via
// sendHTTP. The mutex is therefore required for correctness, not just defensive;
// it also serialises the bounded reexchange so a single refresh serves any
// concurrent caller (single-flight) rather than each firing its own Dex round-trip.
func makeTokenExchangeHeaderFunc(
	serverName, initialToken string,
	expiry time.Time,
	reexchange func() (string, time.Time, error),
	onStaleToken func(),
) func(context.Context) map[string]string {
	var mu sync.Mutex
	token := initialToken
	if expiry.IsZero() {
		expiry = time.Now().Add(tokenExchangeRefreshMargin + tokenExchangeFallbackRefreshInterval)
	}
	var consecutiveFailures int
	var staleEvicted bool
	var lastWarnTime time.Time

	return func(_ context.Context) map[string]string {
		mu.Lock()
		defer mu.Unlock()

		if !expiry.IsZero() && time.Now().After(expiry.Add(-tokenExchangeRefreshMargin)) {
			newToken, newExpiry, err := reexchange()
			if err != nil {
				consecutiveFailures++
				if time.Since(lastWarnTime) >= headerFuncWarnInterval {
					logging.Warn("Connection", "Token re-exchange failed for %s (%d/%d consecutive), reusing current token: %v",
						serverName, consecutiveFailures, maxConsecutiveTokenFailures, err)
					lastWarnTime = time.Now()
				}
				if consecutiveFailures >= maxConsecutiveTokenFailures && !staleEvicted && onStaleToken != nil {
					staleEvicted = true
					go func() {
						defer func() {
							if r := recover(); r != nil {
								logging.Error("Connection", fmt.Errorf("panic in onStaleToken: %v", r),
									"onStaleToken callback panicked for %s", serverName)
							}
						}()
						onStaleToken()
					}()
				}
			} else {
				token = newToken
				if newExpiry.IsZero() {
					expiry = time.Now().Add(tokenExchangeRefreshMargin + tokenExchangeFallbackRefreshInterval)
				} else {
					expiry = newExpiry
				}
				consecutiveFailures = 0
				staleEvicted = false
			}
		}
		return map[string]string{pkgoauth.HeaderAuthorization: pkgoauth.SchemeBearer + " " + token}
	}
}

// makeTokenExchangeRefreshClosures builds the reexchange and onStaleToken
// callbacks that back a token-exchange connection's self-refreshing header
// func. reexchange resolves the latest subject ID token and mints a fresh
// backend token from it, bounded by tokenReexchangeTimeout. onStaleToken evicts
// the pooled connection and revokes stored auth so the listener stops once the
// subject can no longer be refreshed. Requiring api.ResolvedTokenExchangeConfig
// guarantees the config carries the resolved client credentials and audience
// scopes, not the shared spec-only registry definition.
func (a *AggregatorServer) makeTokenExchangeRefreshClosures(
	serverName, sessionID, fallbackUserID, musterIssuer string,
	oauthHandler api.OAuthHandler,
	exchangeConfig *api.ResolvedTokenExchangeConfig,
) (func() (string, time.Time, error), func()) {
	reexchange := func() (string, time.Time, error) {
		ctx, cancel := context.WithTimeout(context.Background(), tokenReexchangeTimeout)
		defer cancel()

		freshID := getIDTokenForForwarding(ctx, sessionID, musterIssuer, a.sessionRefresher())
		if freshID == "" {
			return "", time.Time{}, fmt.Errorf("no subject ID token available for re-exchange")
		}
		if expired, _ := pkgoauth.IsExpired(freshID); expired {
			return "", time.Time{}, fmt.Errorf("subject ID token expired")
		}
		freshUserID, subErr := pkgoauth.Subject(freshID)
		if subErr != nil || freshUserID == "" {
			freshUserID = fallbackUserID
		}
		newToken, exErr := oauthHandler.ExchangeTokenForRemoteCluster(ctx, freshID, freshUserID, &exchangeConfig.TokenExchangeConfig)
		if exErr != nil {
			return "", time.Time{}, exErr
		}
		newExpiry, expErr := pkgoauth.Expiry(newToken)
		if expErr != nil {
			logging.Debug("TokenExchange", "Could not extract expiry from re-exchanged token for %s, refreshing on the fallback interval: %v",
				serverName, expErr)
		}
		return newToken, newExpiry, nil
	}

	onStaleToken := func() {
		if a.connPool != nil {
			a.connPool.Evict(sessionID, serverName)
		}
		if a.authStore != nil {
			if revokeErr := a.authStore.Revoke(context.Background(), sessionID, serverName); revokeErr != nil {
				logging.Warn("Connection", "Failed to revoke token-exchange auth for %s/%s: %v",
					logging.TruncateIdentifier(sessionID), serverName, revokeErr)
			}
		}
	}

	return reexchange, onStaleToken
}

// newTokenForwardingClient resolves the session's forwardable token and builds
// the streamable-HTTP client whose header func re-resolves it on every request.
// Both the connect-time discovery path and the pool-miss tool-call path build
// their client here so token resolution cannot diverge between them.
//
// Resolution matches the header func: the validated inbound bearer first, so
// the connect-time check exercises the token the connection will actually
// forward, then the OAuth-store ID token (with an in-process upstream refresh)
// for sessions without a forwardable bearer (opaque-token human sessions). An
// expired or absent token is an error; the caller surfaces it to the session.
//
// The resolved token is returned alongside the client so connect-failure paths
// can attribute a backend rejection to the token's issuer (see
// forwardedTokenDiagnostic).
func (a *AggregatorServer) newTokenForwardingClient(
	ctx context.Context,
	sessionID, sub, musterIssuer string,
	serverInfo *ServerInfo,
	onStaleToken func(),
) (*internalmcp.StreamableHTTPClient, string, error) {
	refresher := a.sessionRefresher()
	token := forwardableBearer(ctx)
	if token == "" {
		token = getIDTokenForForwarding(ctx, sessionID, musterIssuer, refresher)
	}
	if token == "" {
		logging.Debug("Connection", "No forwardable token available for user %s",
			logging.TruncateIdentifier(sub))
		return nil, "", fmt.Errorf("no token available for forwarding to %s", serverInfo.Name)
	}

	if expired, expErr := pkgoauth.IsExpired(token); expired {
		logging.Warn("Connection", "Token expired for user %s, cannot forward to %s: %v",
			logging.TruncateIdentifier(sub), serverInfo.Name, expErr)
		return nil, "", fmt.Errorf("token has expired for %s, re-authenticate to refresh: %w", serverInfo.Name, expErr)
	}

	headerFunc := makeTokenForwardingHeaderFunc(sessionID, musterIssuer, serverInfo.Name, token, refresher, onStaleToken)
	return internalmcp.NewStreamableHTTPClientWithHeaderFunc(serverInfo.URL, headerFunc), token, nil
}

// forwardedTokenDiagnostic identifies a forwarded token by its issuer claim
// only — never the token itself or any other claim — with a hint for the most
// common rejection cause: the backend does not trust the issuer's JWKS.
func forwardedTokenDiagnostic(token string) string {
	iss, err := pkgoauth.Issuer(token)
	if err != nil || iss == "" {
		return "forwarded token has no parseable iss claim"
	}
	return fmt.Sprintf("forwarded token iss=%s; the backend must trust this issuer's JWKS to accept forwarded tokens", iss)
}

// isForwardableToken reports whether token is a decodable JWT. An opaque
// bearer is never forwarded: a downstream backend cannot validate it, so
// opaque-token sessions resolve the stored dex ID token instead. muster
// issues only opaque access tokens (it is not an IdP and has no signing
// key), so any decodable JWT here was issued by an external trusted issuer
// (dex).
func isForwardableToken(token string) bool {
	if token == "" {
		return false
	}
	_, err := pkgoauth.Subject(token)
	return err == nil
}

// forwardableBearer returns the validated inbound bearer from ctx when it is a
// decodable JWT, or "" otherwise (see isForwardableToken).
//
// The bearer is forwarded byte-identical, not re-minted per backend, so it is
// not audience-scoped to the receiving backend: a trusted-issuer (dex) token
// carries that issuer's audience. The same token is accepted by every
// forwardToken backend that trusts its issuer, so those backends must be
// equally trusted; the token's nested act chain (minted by the IdP) is the
// backend's delegation provenance.
func forwardableBearer(ctx context.Context) string {
	bearer := server.GetBearerTokenFromContext(ctx)
	if !isForwardableToken(bearer) {
		return ""
	}
	return bearer
}

// makeTokenForwardingHeaderFunc creates the header function closure used by
// newTokenForwardingClient. Resolution order per invocation:
//
//  1. The validated inbound bearer on the request context, forwarded
//     byte-identical so the backend sees the caller's own IdP-issued token
//     including any nested act delegation chain. This serves per-request
//     forwarding for tool calls; opaque bearers are excluded (see
//     forwardableBearer).
//  2. The session's latest ID token from the OAuth store. The background
//     listen stream runs without a request context, and opaque-token-mode
//     sessions have no forwardable bearer.
//  3. fallbackToken, captured when the connection was established. Sessions
//     with no OAuth-store entry (agent OBO callers) live off 1 and 3.
//  4. When the fallback has expired, an in-process provider-only upstream
//     refresh via refresher, so sessions that still hold a refresh chain
//     (opaque-token human sessions) recover in place instead of riding into
//     eviction. The refresh is bounded by the provider's request timeout,
//     coalesced by the per-user provider-refresh single-flight, and attempted
//     only in the stale state, so sessions with no upstream refresh chain
//     never trigger it while their token is valid. See
//     oauthServer.RefreshSessionProvider for the rotation/deauth background
//     (giantswarm#37164).
//
// Failure accounting: a resolution counts as failed only when it bottoms out
// on an expired or undecodable fallback and the refresh recovered nothing,
// the stale-token state that 401-loops against the backend. A WARN is
// emitted at most once per headerFuncWarnInterval (DEBUG otherwise). When
// the failure count reaches maxConsecutiveTokenFailures the onStaleToken
// callback is invoked asynchronously to evict the pooled connection, which
// stops mcp-go's retry loop because closing the client cancels the
// transport's context. The callback fires at most once per failure streak;
// the counter resets when a usable token is resolved.
//
// mcp-go invokes the returned closure concurrently on a single connection: the
// continuous-listening GET runs in its own goroutine (listenForever) while tool
// calls invoke it from their own goroutines, and both reach headerFunc via
// sendHTTP. The mutex serialises the slow path (store lookup, refresh, warn
// rate-limiting); the failure counter is atomic so the bearer fast path never
// waits behind a store lookup or an in-flight refresh.
func makeTokenForwardingHeaderFunc(
	sessionID, musterIssuer, serverName, fallbackToken string,
	refresher func(context.Context, string) error,
	onStaleToken func(),
) func(context.Context) map[string]string {
	var mu sync.Mutex
	var lastWarnTime time.Time
	var consecutiveFailures atomic.Int64

	bearerHeader := func(token string) map[string]string {
		return map[string]string{
			pkgoauth.HeaderAuthorization: pkgoauth.SchemeBearer + " " + token,
		}
	}
	succeed := func(token, source string) map[string]string {
		if consecutiveFailures.Swap(0) > 0 {
			logging.Info("Connection", "Token resolution recovered (%s) for session %s to %s",
				source, logging.TruncateIdentifier(sessionID), serverName)
		}
		return bearerHeader(token)
	}
	// fail must be called with mu held: it serialises the warn rate-limiting.
	fail := func() map[string]string {
		failures := consecutiveFailures.Add(1)
		if time.Since(lastWarnTime) >= headerFuncWarnInterval {
			logging.Warn("Connection", "Authentication failed: no usable token for session %s to %s, using expired connection token (%d/%d consecutive failures)",
				logging.TruncateIdentifier(sessionID), serverName, failures, maxConsecutiveTokenFailures)
			lastWarnTime = time.Now()
		} else {
			logging.Debug("Connection", "Authentication failed: no usable token for session %s to %s, using expired connection token (warning suppressed, %d/%d consecutive failures)",
				logging.TruncateIdentifier(sessionID), serverName, failures, maxConsecutiveTokenFailures)
		}
		if failures == int64(maxConsecutiveTokenFailures) && onStaleToken != nil {
			logging.Warn("Connection", "Token resolution failed %d consecutive times for session %s to %s, evicting stale connection",
				failures, logging.TruncateIdentifier(sessionID), serverName)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logging.Error("Connection", fmt.Errorf("panic in onStaleToken: %v", r),
							"onStaleToken callback panicked for session %s to %s",
							logging.TruncateIdentifier(sessionID), serverName)
					}
				}()
				onStaleToken()
			}()
		}
		return bearerHeader(fallbackToken)
	}

	return func(ctx context.Context) map[string]string {
		if bearer := forwardableBearer(ctx); bearer != "" {
			return succeed(bearer, "request bearer")
		}

		mu.Lock()
		defer mu.Unlock()

		if latestToken := getIDTokenForForwarding(context.Background(), sessionID, musterIssuer, nil); latestToken != "" {
			return succeed(latestToken, "OAuth store")
		}
		if expired, _ := pkgoauth.IsExpired(fallbackToken); !expired {
			logging.Debug("Connection", "No ID token in OAuth store for session %s to %s, forwarding the connection token",
				logging.TruncateIdentifier(sessionID), serverName)
			return succeed(fallbackToken, "connection token")
		}
		if refresher != nil {
			if err := refresher(ctx, sessionID); err != nil {
				logging.Debug("Connection", "Session refresh failed for %s: %v",
					logging.TruncateIdentifier(sessionID), err)
			} else if refreshed := getIDTokenForForwarding(context.Background(), sessionID, musterIssuer, nil); refreshed != "" {
				logging.Info("Connection", "Token expired, refreshed in place for session %s to %s",
					logging.TruncateIdentifier(sessionID), serverName)
				return succeed(refreshed, "upstream refresh")
			}
		}
		return fail()
	}
}

// deferredCloseDelay is how long to wait before closing a replaced pooled
// client during background token refresh. This gives any in-flight request
// using the old client time to complete before the underlying connection is
// torn down.
const deferredCloseDelay = 60 * time.Second

// notifyMCPServerConnected updates the MCPServer service state to Connected after
// successful authentication. This syncs the session-level connection success to
// the service-level state, ensuring that `muster list mcpserver` shows the correct
// connected state.
//
// This is a best-effort operation - failures are logged at warn level but don't
// fail the connection flow.
func notifyMCPServerConnected(serverName, authMethod string) {
	if err := api.UpdateMCPServerState(serverName, api.StateConnected, api.HealthHealthy, nil); err != nil {
		logging.Warn("Connection", "Failed to update MCPServer %s state after %s: %v",
			serverName, authMethod, err)
	}
}

// loadTokenExchangeCredentials loads OAuth client credentials from a Kubernetes secret
// for token exchange authentication with remote Dex instances.
//
// Args:
//   - ctx: Context for Kubernetes API calls
//   - serverInfo: The MCP server info containing the token exchange configuration
//
// Returns:
//   - *api.ClientCredentials: The loaded credentials
//   - error: Error if credentials could not be loaded
func loadTokenExchangeCredentials(ctx context.Context, serverInfo *ServerInfo) (*api.ClientCredentials, error) {
	if serverInfo.AuthConfig == nil ||
		serverInfo.AuthConfig.TokenExchange == nil ||
		serverInfo.AuthConfig.TokenExchange.ClientCredentialsSecretRef == nil {
		return nil, fmt.Errorf("no client credentials secret reference configured")
	}

	handler := api.GetSecretCredentialsHandler()
	if handler == nil {
		return nil, fmt.Errorf("secret credentials handler not registered")
	}

	// Use the server's namespace as the default for the secret
	defaultNamespace := serverInfo.GetNamespace()
	if defaultNamespace == "" {
		defaultNamespace = metav1.NamespaceDefault
	}

	return handler.LoadClientCredentials(ctx, serverInfo.AuthConfig.TokenExchange.ClientCredentialsSecretRef, defaultNamespace)
}
