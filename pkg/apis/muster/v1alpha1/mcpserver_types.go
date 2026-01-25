package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MCPServerSpec defines the desired state of MCPServer
type MCPServerSpec struct {
	// Type specifies how this MCP server should be executed.
	// Supported values: "stdio" for local processes, "streamable-http" for HTTP-based servers, "sse" for Server-Sent Events
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=stdio;streamable-http;sse
	Type string `json:"type" yaml:"type"`

	// ToolPrefix is an optional prefix that will be prepended to all tool names
	// provided by this MCP server. This helps avoid naming conflicts when multiple
	// servers provide tools with similar names.
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_-]*$"
	ToolPrefix string `json:"toolPrefix,omitempty" yaml:"toolPrefix,omitempty"`

	// Description provides a human-readable description of this MCP server's purpose.
	// +kubebuilder:validation:MaxLength=500
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// AutoStart determines whether this MCP server should be automatically started
	// when the muster system initializes or when dependencies become available.
	// +kubebuilder:default=false
	AutoStart bool `json:"autoStart,omitempty" yaml:"autoStart,omitempty"`

	// Command specifies the executable path for stdio type servers.
	// This field is required when Type is "stdio".
	Command string `json:"command,omitempty" yaml:"command,omitempty"`

	// Args specifies the command line arguments for stdio type servers.
	// This field is only available when Type is "stdio".
	Args []string `json:"args,omitempty" yaml:"args,omitempty"`

	// URL is the endpoint where the remote MCP server can be reached
	// This field is required when Type is "streamable-http" or "sse".
	// Examples: http://mcp-server:8080/mcp, https://api.example.com/mcp
	// +kubebuilder:validation:Pattern=`^https?://[^\s/$.?#].[^\s]*$`
	URL string `json:"url,omitempty" yaml:"url,omitempty"`

	// Env contains environment variables to set for the MCP server.
	// For stdio servers, these are passed to the process when it is started.
	// For remote servers, these can be used for authentication or configuration.
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`

	// Headers contains HTTP headers to send with requests to remote MCP servers.
	// This field is only relevant when Type is "streamable-http" or "sse".
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`

	// Auth configures authentication behavior for this MCP server.
	// This is only relevant for remote servers (streamable-http or sse).
	Auth *MCPServerAuth `json:"auth,omitempty" yaml:"auth,omitempty"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	Timeout int `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

// MCPServerAuth configures authentication behavior for an MCP server.
// This enables Single Sign-On (SSO) via token forwarding between muster and
// downstream MCP servers that share the same Identity Provider, or Teleport
// authentication for private installations.
type MCPServerAuth struct {
	// Type specifies the authentication type.
	// Supported values:
	//   - "oauth": OAuth 2.0/OIDC authentication
	//   - "teleport": Teleport Application Access with Machine ID certificates
	//   - "none": No authentication
	// +kubebuilder:validation:Enum=oauth;teleport;none
	// +kubebuilder:default=none
	Type string `json:"type,omitempty" yaml:"type,omitempty"`

	// ForwardToken enables ID token forwarding for SSO.
	// When true, muster forwards the user's ID token to this server instead of
	// triggering a separate OAuth flow. The downstream server must be configured
	// to trust muster's client ID in its TrustedAudiences.
	// +kubebuilder:default=false
	ForwardToken bool `json:"forwardToken,omitempty" yaml:"forwardToken,omitempty"`

	// FallbackToOwnAuth enables fallback to server-specific OAuth flow.
	// When true and token forwarding fails (e.g., 401 response despite forwarded token),
	// muster will trigger a separate OAuth flow for this server.
	// When false, token forwarding failures result in an error.
	// +kubebuilder:default=true
	FallbackToOwnAuth bool `json:"fallbackToOwnAuth,omitempty" yaml:"fallbackToOwnAuth,omitempty"`

	// SSO controls Single Sign-On token reuse for this server.
	// When true (default), tokens from other servers using the same OAuth issuer
	// can be reused to authenticate to this server without re-authenticating.
	// When false, this server always requires its own authentication flow,
	// even if a token exists for the same issuer. Use this when you want to
	// use different accounts for servers that share the same OAuth provider.
	// +kubebuilder:default=true
	SSO *bool `json:"sso,omitempty" yaml:"sso,omitempty"`

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
	TokenExchange *TokenExchangeConfig `json:"tokenExchange,omitempty" yaml:"tokenExchange,omitempty"`

	// Teleport configures Teleport authentication for accessing private installations.
	// This is only used when Type is "teleport".
	//
	// When configured, muster uses Teleport Machine ID certificates to establish
	// mutual TLS connections to MCP servers accessible via Teleport Application Access.
	Teleport *TeleportAuthConfig `json:"teleport,omitempty" yaml:"teleport,omitempty"`
}

// TeleportAuthConfig configures Teleport authentication for an MCP server.
// This enables access to MCP servers on private installations via Teleport
// Application Access using Machine ID certificates managed by tbot.
type TeleportAuthConfig struct {
	// IdentitySecretName is the name of the Kubernetes Secret containing
	// tbot identity files. The secret should contain: tls.crt, tls.key, ca.crt
	// Required when running in Kubernetes mode.
	// Example: tbot-identity-output
	IdentitySecretName string `json:"identitySecretName,omitempty" yaml:"identitySecretName,omitempty"`

	// IdentitySecretNamespace is the Kubernetes namespace where the identity
	// secret is located. Defaults to the MCPServer's namespace if not specified.
	IdentitySecretNamespace string `json:"identitySecretNamespace,omitempty" yaml:"identitySecretNamespace,omitempty"`

	// IdentityDir is the directory containing Teleport identity files.
	// Used in filesystem mode when certificates are mounted directly.
	// Example: /var/run/tbot/identity
	IdentityDir string `json:"identityDir,omitempty" yaml:"identityDir,omitempty"`

	// AppName is the Teleport application name for routing.
	// This is used to identify which Teleport-protected application to connect to.
	// Example: mcp-kubernetes
	AppName string `json:"appName,omitempty" yaml:"appName,omitempty"`
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
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// DexTokenEndpoint is the URL used to connect to the remote cluster's Dex token endpoint.
	// This may differ from the issuer URL when access goes through a proxy (e.g., Teleport).
	// Required when Enabled is true.
	// Example: https://dex.cluster-b.example.com/token (direct)
	// Example: https://dex-cluster.proxy.example.com/token (via proxy)
	// +kubebuilder:validation:Pattern=`^https://[^\s/$.?#].[^\s]*$`
	DexTokenEndpoint string `json:"dexTokenEndpoint,omitempty" yaml:"dexTokenEndpoint,omitempty"`

	// ExpectedIssuer is the expected issuer URL in the exchanged token's "iss" claim.
	// This should match the remote Dex's configured issuer URL.
	// When access goes through a proxy, this differs from DexTokenEndpoint.
	// If not specified, the issuer is derived from DexTokenEndpoint (backward compatible).
	// Example: https://dex.cluster-b.example.com
	// +kubebuilder:validation:Pattern=`^https://[^\s/$.?#].[^\s]*$`
	ExpectedIssuer string `json:"expectedIssuer,omitempty" yaml:"expectedIssuer,omitempty"`

	// ConnectorID is the ID of the OIDC connector on the remote Dex that
	// trusts the local cluster's Dex.
	// Required when Enabled is true.
	// Example: "cluster-a-dex"
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_-]*$"
	ConnectorID string `json:"connectorId,omitempty" yaml:"connectorId,omitempty"`

	// Scopes are the scopes to request for the exchanged token.
	// +kubebuilder:default="openid profile email groups"
	Scopes string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

// MCPServerStateValue represents the high-level infrastructure state of an MCPServer.
// This is independent of user session state (authentication, per-user connection status).
//
// Status values reflect infrastructure availability with context-appropriate terminology:
//
// For stdio (local process) servers:
//   - Running: Process is running and responding
//   - Starting: Process is being started
//   - Stopped: Process is not running (initial state or explicitly stopped)
//   - Failed: Process crashed or cannot be started
//
// For remote (streamable-http, sse) servers:
//   - Connected: TCP connection established (may still require auth)
//   - Connecting: Attempting to establish connection
//   - Disconnected: Not connected (initial state or connection closed)
//   - Failed: Endpoint unreachable (network error, DNS failure, etc.)
type MCPServerStateValue string

const (
	// Stdio server states (local process)

	// MCPServerStateRunning indicates a stdio server process is running.
	MCPServerStateRunning MCPServerStateValue = "Running"

	// MCPServerStateStarting indicates a stdio server process is being started.
	MCPServerStateStarting MCPServerStateValue = "Starting"

	// MCPServerStateStopped indicates a stdio server process is not running.
	MCPServerStateStopped MCPServerStateValue = "Stopped"

	// Remote server states (streamable-http, sse)

	// MCPServerStateConnected indicates a remote server is reachable.
	// TCP connection can be established (ignoring 401/403 auth responses).
	MCPServerStateConnected MCPServerStateValue = "Connected"

	// MCPServerStateConnecting indicates a connection attempt is in progress.
	MCPServerStateConnecting MCPServerStateValue = "Connecting"

	// MCPServerStateDisconnected indicates a remote server is not connected.
	MCPServerStateDisconnected MCPServerStateValue = "Disconnected"

	// Common states (both server types)

	// MCPServerStateFailed indicates infrastructure is not available.
	// For stdio: process crashed or cannot be started.
	// For http/sse: endpoint unreachable (network error, DNS failure, etc.).
	MCPServerStateFailed MCPServerStateValue = "Failed"
)

// MCPServerStatus defines the observed state of MCPServer.
//
// IMPORTANT: This status reflects infrastructure state only, NOT user session state.
// Per-user connection status and authentication state are tracked in the Session Registry
// (internal/aggregator/session_registry.go) to support multi-user environments.
//
// Infrastructure State (CRD):
//   - State: Running/Connected/Starting/Connecting/Stopped/Disconnected/Failed
//   - Conditions: Standard K8s conditions for detailed status
//
// Session State (Session Registry):
//   - ConnectionStatus: Connected, PendingAuth, Failed (per-user)
//   - AuthStatus: Authenticated, AuthRequired, TokenExpired (per-user)
//   - AvailableTools: Tools visible to this specific user
type MCPServerStatus struct {
	// State represents the high-level infrastructure state of the MCP server.
	// This is independent of user session state (authentication, connection status).
	//
	// For stdio servers: Running, Starting, Stopped, Failed
	// For remote servers: Connected, Connecting, Disconnected, Failed
	// +kubebuilder:validation:Enum=Running;Starting;Stopped;Connected;Connecting;Disconnected;Failed
	State MCPServerStateValue `json:"state,omitempty" yaml:"state,omitempty"`

	// LastError contains any error message from the most recent server operation.
	// Note: Per-user authentication errors are tracked in the Session Registry,
	// not here. This field only contains infrastructure-level errors.
	LastError string `json:"lastError,omitempty" yaml:"lastError,omitempty"`

	// LastConnected indicates when the server was last successfully connected
	LastConnected *metav1.Time `json:"lastConnected,omitempty" yaml:"lastConnected,omitempty"`

	// RestartCount tracks how many times this server has been restarted (stdio only)
	RestartCount int `json:"restartCount,omitempty" yaml:"restartCount,omitempty"`

	// ConsecutiveFailures tracks the number of consecutive connection failures.
	// This is used for exponential backoff and to identify unreachable servers.
	// Reset to 0 when a connection succeeds.
	ConsecutiveFailures int `json:"consecutiveFailures,omitempty" yaml:"consecutiveFailures,omitempty"`

	// LastAttempt indicates when the last connection attempt was made.
	// Used with ConsecutiveFailures to implement exponential backoff.
	LastAttempt *metav1.Time `json:"lastAttempt,omitempty" yaml:"lastAttempt,omitempty"`

	// NextRetryAfter indicates the earliest time when the next retry should be attempted.
	// This is calculated based on exponential backoff from ConsecutiveFailures.
	NextRetryAfter *metav1.Time `json:"nextRetryAfter,omitempty" yaml:"nextRetryAfter,omitempty"`

	// Conditions represent the latest available observations of the MCPServer's current state.
	// Standard condition types:
	//   - Ready: True if infrastructure is reachable (process running or TCP connectable)
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mcps
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="URL",type="string",JSONPath=".spec.url"
// +kubebuilder:printcolumn:name="AutoStart",type="boolean",JSONPath=".spec.autoStart"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="self.spec.type != 'stdio' || has(self.spec.command)",message="command is required when type is stdio"
// +kubebuilder:validation:XValidation:rule="self.spec.type == 'stdio' || has(self.spec.url)",message="url is required when type is streamable-http or sse"
// +kubebuilder:validation:XValidation:rule="self.spec.type == 'stdio' || !has(self.spec.args)",message="args field is only allowed when type is stdio"
// +kubebuilder:validation:XValidation:rule="self.spec.type != 'stdio' || !has(self.spec.headers)",message="headers field is only allowed when type is streamable-http or sse"

// MCPServer is the Schema for the mcpservers API
type MCPServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MCPServerSpec   `json:"spec,omitempty"`
	Status MCPServerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MCPServerList contains a list of MCPServer
type MCPServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MCPServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MCPServer{}, &MCPServerList{})
}
