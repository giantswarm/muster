package api

import "time"

// MCPServer represents a single MCP (Model Context Protocol) server definition and runtime state.
// It consolidates MCPServerDefinition, MCPServerInfo, and MCPServerConfig into a unified type
// that can be used for both configuration persistence (YAML) and API responses (JSON).
//
// MCP servers provide tools and capabilities to the muster system through the aggregator.
// They are configured as stdio processes or remote HTTP endpoints with their own
// specific configuration requirements and runtime characteristics.
type MCPServer struct {
	// Name is the unique identifier for this MCP server instance.
	// This name is used for registration, lookup, and management operations.
	Name string `yaml:"name" json:"name"`

	// Type specifies how this MCP server should be executed.
	// Supported values: "stdio" for local processes, "streamable-http" for HTTP-based servers, "sse" for Server-Sent Events
	Type MCPServerType `yaml:"type" json:"type"`

	// ToolPrefix is an optional prefix that will be prepended to all tool names
	// provided by this MCP server. This helps avoid naming conflicts when multiple
	// servers provide tools with similar names.
	ToolPrefix string `yaml:"toolPrefix,omitempty" json:"toolPrefix,omitempty"`

	// AutoStart determines whether this MCP server should be automatically started
	// when the muster system initializes or when dependencies become available.
	AutoStart bool `yaml:"autoStart,omitempty" json:"autoStart,omitempty"`

	// Command specifies the executable path for stdio type servers.
	// This field is required when Type is "stdio".
	Command string `yaml:"command,omitempty" json:"command,omitempty"`

	// Args specifies the command line arguments for stdio type servers.
	// This field is only available when Type is "stdio".
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`

	// URL is the endpoint where the remote MCP server can be reached
	// This field is required when Type is "streamable-http" or "sse".
	// Examples: http://mcp-server:8080/mcp, https://api.example.com/mcp
	URL string `yaml:"url,omitempty" json:"url,omitempty"`

	// Env contains environment variables to set for the MCP server.
	// For stdio servers, these are passed to the process when it is started.
	// For remote servers, these can be used for authentication or configuration.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Headers contains HTTP headers to send with requests to remote MCP servers.
	// This field is only relevant when Type is "streamable-http" or "sse".
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`

	// Auth configures authentication behavior for this MCP server.
	// This is only relevant for remote servers (streamable-http or sse).
	Auth *MCPServerAuth `yaml:"auth,omitempty" json:"auth,omitempty"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	Timeout int `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Error contains any error message from the most recent server operation.
	// This is runtime information and not persisted to YAML files.
	Error string `json:"error,omitempty" yaml:"-"`

	// Description provides a human-readable description of this MCP server's purpose.
	// This is runtime information populated from server metadata and not persisted to YAML.
	Description string `json:"description,omitempty" yaml:"-"`
}

// MCPServerAuth configures authentication behavior for an MCP server.
//
// Muster supports four distinct authentication mechanisms:
//
//   - SSO Token Reuse: Tokens are shared between servers with the same OAuth issuer.
//     This is the default behavior. Disable per-server with SSO: false.
//
//   - SSO Token Forwarding: Muster forwards its own ID token to downstream servers.
//     Enable with ForwardToken: true. Requires downstream to trust muster's client ID.
//
//   - SSO Token Exchange (RFC 8693): Muster exchanges its token for one valid on the
//     remote cluster's Dex. Enable with TokenExchange config. Requires the remote Dex
//     to have an OIDC connector configured for the local cluster's Dex.
//
//   - Teleport Authentication: Muster uses Teleport Machine ID certificates to access
//     private installations via Teleport Application Access. Enable with Type: "teleport"
//     and configure Teleport settings.
type MCPServerAuth struct {
	// Type specifies the authentication type.
	// Supported values:
	//   - "oauth": OAuth 2.0/OIDC authentication
	//   - "teleport": Teleport Application Access with Machine ID certificates
	//   - "none": No authentication
	Type string `yaml:"type,omitempty" json:"type,omitempty"`

	// ForwardToken enables SSO via Token Forwarding.
	// When true, muster forwards its own ID token (obtained when user authenticated
	// TO muster) to this downstream server. The downstream server must be configured
	// to trust muster's OAuth client ID in its TrustedAudiences configuration.
	//
	// This is different from SSO Token Reuse (controlled by the SSO field below),
	// which shares tokens between servers that happen to use the same OAuth issuer.
	//
	// Use ForwardToken when:
	//   - Muster itself is OAuth-protected (oauth_server enabled)
	//   - The downstream server trusts muster as an identity relay
	//   - You want users to authenticate once to muster for all downstream access
	ForwardToken bool `yaml:"forwardToken,omitempty" json:"forwardToken,omitempty"`

	// RequiredAudiences specifies additional audience(s) that the SSO token should contain.
	// This is used with both Token Forwarding and Token Exchange SSO methods.
	//
	// When the downstream server requires tokens with specific audiences (e.g., Kubernetes
	// OIDC authentication), specify them here:
	//   requiredAudiences:
	//     - "dex-k8s-authenticator"
	//
	// For Token Forwarding (forwardToken: true):
	//   - At session initialization, muster collects all requiredAudiences from MCPServers
	//   - These are requested from muster's IdP using cross-client scopes
	//   - The resulting multi-audience ID token is forwarded to downstream servers
	//
	// For Token Exchange (tokenExchange.enabled: true):
	//   - The audiences are appended as cross-client scopes to the token exchange request
	//   - The remote IdP issues a token containing the requested audiences
	RequiredAudiences []string `yaml:"requiredAudiences,omitempty" json:"requiredAudiences,omitempty"`

	// FallbackToOwnAuth enables graceful degradation when token forwarding or exchange fails.
	// When true and ForwardToken or TokenExchange is enabled but fails (e.g., downstream returns 401),
	// muster will trigger a separate OAuth flow for this server.
	// When false, token forwarding/exchange failures result in an error requiring intervention.
	FallbackToOwnAuth bool `yaml:"fallbackToOwnAuth,omitempty" json:"fallbackToOwnAuth,omitempty"`

	// SSO controls SSO via Token Reuse for this server.
	// When true (default), tokens from other servers using the same OAuth issuer
	// can be reused to authenticate to this server without re-authenticating.
	// When false, this server always requires its own authentication flow,
	// even if a token exists for the same issuer.
	//
	// Use SSO: false when you need different accounts for servers that share
	// the same OAuth provider (e.g., personal vs work GitHub accounts).
	//
	// This is different from ForwardToken (Token Forwarding), which forwards
	// muster's identity rather than sharing tokens between peer servers.
	SSO *bool `yaml:"sso,omitempty" json:"sso,omitempty"`

	// TokenExchange enables SSO via RFC 8693 Token Exchange for cross-cluster SSO.
	// When configured, muster exchanges its local token for a token valid on the
	// remote cluster's Identity Provider (e.g., Dex).
	//
	// Use TokenExchange when:
	//   - The remote cluster has its own Dex instance
	//   - The remote Dex is configured with an OIDC connector for muster's Dex
	//   - You need a token issued by the remote cluster's IdP (not just forwarded)
	//
	// Token exchange takes precedence over ForwardToken if both are configured.
	TokenExchange *TokenExchangeConfig `yaml:"tokenExchange,omitempty" json:"tokenExchange,omitempty"`

	// Teleport configures Teleport authentication for accessing private installations.
	// This is only used when Type is "teleport".
	//
	// When configured, muster uses Teleport Machine ID certificates to establish
	// mutual TLS connections to MCP servers accessible via Teleport Application Access.
	//
	// The Teleport identity files (tls.crt, tls.key, ca.crt) are typically:
	//   - In Kubernetes: Mounted from a Secret managed by tbot
	//   - In filesystem mode: Read directly from the tbot output directory
	Teleport *TeleportAuth `yaml:"teleport,omitempty" json:"teleport,omitempty"`
}

// TokenExchangeConfig configures RFC 8693 Token Exchange for cross-cluster SSO.
// This enables muster to exchange its local token for a token valid on a remote
// cluster's Identity Provider (typically Dex).
//
// The remote Dex must be configured with an OIDC connector that trusts the local
// cluster's Dex. For example:
//
//	# On remote cluster's Dex (cluster-b)
//	connectors:
//	- type: oidc
//	  id: cluster-a-dex
//	  name: "Cluster A"
//	  config:
//	    issuer: https://dex.cluster-a.example.com
//	    getUserInfo: true
//	    insecureEnableGroups: true
type TokenExchangeConfig struct {
	// Enabled determines whether token exchange should be attempted.
	// Default: false
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// DexTokenEndpoint is the URL used to connect to the remote cluster's Dex token endpoint.
	// This may differ from the issuer URL when access goes through a proxy (e.g., Teleport).
	// Required when Enabled is true.
	// Example: https://dex.cluster-b.example.com/token (direct)
	// Example: https://dex-cluster.proxy.example.com/token (via proxy)
	DexTokenEndpoint string `yaml:"dexTokenEndpoint,omitempty" json:"dexTokenEndpoint,omitempty"`

	// ExpectedIssuer is the expected issuer URL in the exchanged token's "iss" claim.
	// This should match the remote Dex's configured issuer URL.
	// When access goes through a proxy, this differs from DexTokenEndpoint.
	// If not specified, the issuer is derived from DexTokenEndpoint (backward compatible).
	// Example: https://dex.cluster-b.example.com
	ExpectedIssuer string `yaml:"expectedIssuer,omitempty" json:"expectedIssuer,omitempty"`

	// ConnectorID is the ID of the OIDC connector on the remote Dex that
	// trusts the local cluster's Dex.
	// Required when Enabled is true.
	// Example: "cluster-a-dex"
	ConnectorID string `yaml:"connectorId,omitempty" json:"connectorId,omitempty"`

	// Scopes are the scopes to request for the exchanged token.
	// Default: "openid profile email groups"
	Scopes string `yaml:"scopes,omitempty" json:"scopes,omitempty"`

	// ClientCredentialsSecretRef references a Kubernetes Secret containing
	// client credentials for authenticating with the remote Dex's token endpoint.
	// This is required when the remote Dex requires client authentication for
	// token exchange (RFC 8693).
	ClientCredentialsSecretRef *ClientCredentialsSecretRef `yaml:"clientCredentialsSecretRef,omitempty" json:"clientCredentialsSecretRef,omitempty"`

	// ClientID is the resolved client ID from the secret (populated at runtime).
	// This field is not persisted and is populated when loading credentials.
	ClientID string `yaml:"-" json:"-"`

	// ClientSecret is the resolved client secret from the secret (populated at runtime).
	// This field is not persisted and is populated when loading credentials.
	ClientSecret string `yaml:"-" json:"-"`
}

// ClientCredentialsSecretRef references a Kubernetes Secret containing
// OAuth client credentials for token exchange authentication.
type ClientCredentialsSecretRef struct {
	// Name is the name of the Kubernetes Secret.
	// Required.
	Name string `yaml:"name" json:"name"`

	// Namespace is the Kubernetes namespace where the secret is located.
	// If not specified, defaults to the MCPServer's namespace.
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`

	// ClientIDKey is the key in the secret data that contains the client ID.
	// Defaults to "client-id" if not specified.
	ClientIDKey string `yaml:"clientIdKey,omitempty" json:"clientIdKey,omitempty"`

	// ClientSecretKey is the key in the secret data that contains the client secret.
	// Defaults to "client-secret" if not specified.
	ClientSecretKey string `yaml:"clientSecretKey,omitempty" json:"clientSecretKey,omitempty"`
}

// MCPServerType defines the execution model for an MCP server.
// This determines how the server process is managed and what configuration
// options are available for server deployment.
type MCPServerType string

const (
	// MCPServerTypeStdio indicates that the MCP server should be run as a local process.
	// Stdio servers are started using the configured command and arguments,
	// with communication typically happening over stdin/stdout.
	MCPServerTypeStdio MCPServerType = "stdio"

	// MCPServerTypeStreamableHTTP indicates that the MCP server should be accessed via HTTP.
	// StreamableHTTP servers are accessed via HTTP/HTTPS endpoints with streaming support.
	MCPServerTypeStreamableHTTP MCPServerType = "streamable-http"

	// MCPServerTypeSSE indicates that the MCP server should be accessed via Server-Sent Events.
	// SSE servers are accessed via HTTP/HTTPS endpoints using Server-Sent Events for communication.
	MCPServerTypeSSE MCPServerType = "sse"
)

// IsRemote returns true if the server type is a remote (HTTP-based) server.
// Remote servers use connected/disconnected states rather than running/stopped.
func (t MCPServerType) IsRemote() bool {
	return t == MCPServerTypeStreamableHTTP || t == MCPServerTypeSSE
}

// MCPServerInfo contains consolidated MCP server information for API responses.
// This type is used when returning server information through the API, providing
// a flattened view of server configuration and runtime state that is convenient
// for clients and user interfaces.
type MCPServerInfo struct {
	// Name is the unique identifier for this MCP server instance.
	Name string `json:"name"`

	// Type indicates the execution model for this server (stdio, streamable-http, or sse).
	Type string `json:"type"`

	// Description provides a human-readable description of the server's purpose and capabilities.
	Description string `json:"description,omitempty"`

	// AutoStart determines whether this MCP server should be automatically started
	AutoStart bool `json:"autoStart,omitempty"`

	// Command specifies the executable path for stdio type servers.
	Command string `json:"command,omitempty"`

	// Args specifies the command line arguments for stdio type servers.
	Args []string `json:"args,omitempty"`

	// URL is the endpoint where the remote MCP server can be reached
	URL string `json:"url,omitempty"`

	// Env contains environment variables to set for the MCP server.
	Env map[string]string `json:"env,omitempty"`

	// Headers contains HTTP headers to send with requests to remote MCP servers.
	Headers map[string]string `json:"headers,omitempty"`

	// Auth configures authentication behavior for this MCP server.
	Auth *MCPServerAuth `json:"auth,omitempty"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	Timeout int `json:"timeout,omitempty"`

	// ToolPrefix is an optional prefix for tool names.
	ToolPrefix string `json:"toolPrefix,omitempty"`

	// Error contains any error message from recent server operations.
	// This field is populated if the server is in an error state.
	Error string `json:"error,omitempty"`

	// State represents the high-level infrastructure state of the MCP server.
	// This is the primary status indicator.
	// Possible values for stdio servers: Running, Starting, Stopped, Failed
	// Possible values for remote servers: Connected, Connecting, Disconnected, Failed
	// Note: State reflects infrastructure availability only. Per-user session state
	// (auth status, connection status) is tracked in the Session Registry.
	State string `json:"state,omitempty"`

	// StatusMessage provides a user-friendly, actionable message about the server's status.
	// This field is populated based on the server's state and error information.
	// Examples:
	//   - "Server is running normally"
	//   - "Authentication required - run: muster auth login --server <name>"
	//   - "Cannot reach server - check network connectivity"
	//   - "Certificate error - verify TLS configuration"
	StatusMessage string `json:"statusMessage,omitempty"`

	// ConsecutiveFailures tracks the number of consecutive connection failures.
	// Used for exponential backoff and to identify unreachable servers.
	ConsecutiveFailures int `json:"consecutiveFailures,omitempty"`

	// LastAttempt indicates when the last connection attempt was made.
	LastAttempt *time.Time `json:"lastAttempt,omitempty"`

	// NextRetryAfter indicates the earliest time when the next retry should be attempted.
	NextRetryAfter *time.Time `json:"nextRetryAfter,omitempty"`

	// SessionStatus represents the per-user session connection status.
	// This is only populated when the request includes a session context.
	// Possible values: connected, disconnected, pending_auth, failed
	// Empty if no session context is available.
	SessionStatus string `json:"sessionStatus,omitempty"`

	// SessionAuth represents the per-user authentication status for this server.
	// This is only populated when the request includes a session context.
	// Possible values: authenticated, auth_required, token_expired, unknown
	// Empty if no session context is available or auth is not required.
	SessionAuth string `json:"sessionAuth,omitempty"`

	// ToolsCount is the number of tools available from this server for the current session.
	// This is session-specific as OAuth-protected servers may expose different tools
	// based on user permissions.
	ToolsCount int `json:"toolsCount,omitempty"`

	// ConnectedAt indicates when the current session connected to this server.
	// Only populated if there is an active session connection.
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
}

// MCPServerManagerHandler defines the interface for MCP server management operations.
// This interface provides the core functionality for managing MCP server lifecycle,
// configuration, and tool availability. It also implements the ToolProvider interface
// to expose MCP server management capabilities as tools that can be called through
// the aggregator.
type MCPServerManagerHandler interface {
	// ListMCPServers returns information about all registered MCP servers.
	// This includes both configuration and runtime state information for each server.
	//
	// Returns:
	//   - []MCPServerInfo: Slice of server information (empty if no servers exist)
	ListMCPServers() []MCPServerInfo

	// GetMCPServer retrieves detailed information about a specific MCP server.
	// This includes both configuration and runtime state for the requested server.
	//
	// Args:
	//   - name: The unique name of the MCP server to retrieve
	//
	// Returns:
	//   - *MCPServerInfo: Server information, or nil if server not found
	//   - error: nil on success, or an error if the server could not be retrieved
	GetMCPServer(name string) (*MCPServerInfo, error)

	// ToolProvider interface for exposing MCP server management tools.
	// This allows MCP server operations to be performed through the aggregator
	// tool system, enabling programmatic and user-driven server management.
	ToolProvider
}
