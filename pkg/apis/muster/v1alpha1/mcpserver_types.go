package v1alpha1

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IssuerURL is a normalized OAuth/OIDC issuer URL.
// Per RFC 8414 §2: HTTPS, no trailing slash, no fragment, no query string.
//
// Accepted: https://issuer.example.com
//
//	https://issuer.example.com:8443
//	https://issuer.example.com/tenant
//	https://login.microsoftonline.com/<tenant-uuid>/v2.0
//
// Rejected: https://issuer.example.com/  (trailing slash)
//
//	https://issuer.example.com#frag  (fragment)
//	https://issuer.example.com?x=1  (query)
//	http://issuer.example.com  (non-HTTPS)
//
// +kubebuilder:validation:Pattern=`^https://[^/?#]+(/[^?#]*[^/?#])?$`
type IssuerURL string

// Normalize returns the issuer URL with any trailing slash removed.
// RFC 8414 §2 forbids trailing slashes; mirrors pkg/oauth/client.go's
// canonical form so allowlist or override comparisons match downstream
// AS metadata `issuer` values.
func (u IssuerURL) Normalize() string {
	return strings.TrimSuffix(string(u), "/")
}

// MCPServerSpec defines the desired state of MCPServer
type MCPServerSpec struct {
	// Type specifies how this MCP server should be executed.
	// Supported values: "stdio" for local processes, "streamable-http" for HTTP-based servers, "sse" for Server-Sent Events.
	//
	// "stdio" is CLI-only. A stdio server is started as a subprocess of the muster
	// process, so a muster running in Kubernetes mode rejects it: the tool handlers
	// fail the call, and a CR applied directly through the API server is reconciled
	// to state Failed instead of spawning a process in the muster pod. Run the MCP
	// server as its own workload and use "streamable-http" or "sse" instead.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=stdio;streamable-http;sse
	Type string `json:"type" yaml:"type"`

	// ToolPrefix is an optional prefix that will be prepended to all tool names
	// provided by this MCP server. This helps avoid naming conflicts when multiple
	// servers provide tools with similar names.
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_-]*$"
	ToolPrefix string `json:"toolPrefix,omitempty" yaml:"toolPrefix,omitempty"`

	// Family declares that this MCP server is an instance of a family of
	// equivalent servers (for example, multiple kubernetes MCP servers pointed
	// at different clusters). When set, the aggregator exposes tools from all
	// servers in the same family under a single name
	// ({musterPrefix}_{family.name}_{toolName}) with a required parameter
	// (named by family.instanceArg) that selects which instance handles the
	// call. The parameter is always required even for single-instance families
	// so skills written against the family name remain stable as instances are
	// added or removed. When unset, the legacy per-server prefixing applies
	// ({musterPrefix}_{toolPrefix-or-name}_{toolName}).
	Family *MCPServerFamily `json:"family,omitempty" yaml:"family,omitempty"`

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

	// Meta contains entries merged into the `params._meta` object of every
	// outbound JSON-RPC request that carries `params`. It carries call-scoped
	// configuration to a backend that reads it from the MCP metadata field
	// instead of from tool arguments.
	//
	// An entry already present in a request's `_meta` wins, so a caller can
	// still override per call. Requests without `params` are left untouched.
	//
	// The injection happens in an HTTP round tripper shared by every remote
	// client, so it applies to `streamable-http` and `sse` alike, with or
	// without auth. For a SigV4 server the round tripper sits in front of the
	// signer, because the body has to be rewritten before it is signed.
	//
	// A stdio server has no HTTP transport, so the CEL rule above rejects the
	// field there rather than accepting and dropping it.
	//
	// The AWS-hosted MCP server is the motivating case: it takes the region it
	// operates in from `params._meta.AWS_REGION`, and its tools declare no
	// region argument, so without this a call fails with NoRegionError. That
	// operating region is a different value from Auth.SigV4.Region, which is
	// the signing region — the two default to the same string but resolve
	// independently, endpoint-first for signing and config-first for operating.
	Meta map[string]string `json:"meta,omitempty" yaml:"meta,omitempty"`

	// Auth configures authentication behavior for this MCP server.
	// This is only relevant for remote servers (streamable-http or sse).
	Auth *MCPServerAuth `json:"auth,omitempty" yaml:"auth,omitempty"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	Timeout int `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// Suspended declares the desired lifecycle state of this server's service.
	// When true, the reconciler stops the service (and refuses to start it)
	// until the field is set back to false. This is the CR-driven replacement
	// for the imperative start/stop tools: writing it is authorized and
	// audited by the apiserver like any other spec mutation (issue #1055).
	// +kubebuilder:default=false
	Suspended bool `json:"suspended,omitempty" yaml:"suspended,omitempty"`

	// RestartRequestedAt requests a one-shot restart of this server's service.
	// Restart is an action, not a steady state, so it cannot be a boolean: the
	// caller writes a timestamp, which doubles as the audit record of when the
	// restart was requested. The reconciler restarts the service only when this
	// differs from status.lastRestartedAt, then mirrors the processed value
	// into status — repeated reconciles of an already-processed request are
	// no-ops (issue #1055).
	RestartRequestedAt *metav1.Time `json:"restartRequestedAt,omitempty" yaml:"restartRequestedAt,omitempty"`
}

// MCPServerFamily groups equivalent MCP server instances under a shared
// exposed surface. When MCPServerSpec.Family is set, the aggregator emits a
// single family-scoped tool per backend tool name with a required parameter
// (named by InstanceArg) that selects which instance handles the call.
type MCPServerFamily struct {
	// Name is the family identifier. Servers sharing the same Name expose
	// their tools as {musterPrefix}_{Name}_{toolName}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_-]*$"
	Name string `json:"name" yaml:"name"`

	// InstanceArg names the required parameter callers use to select which
	// family member handles the tool call (for example "management_cluster",
	// "country", "model"). All servers declaring the same family.name must
	// agree on InstanceArg; if they diverge, the aggregator falls back to
	// per-server prefixing for the entire family and logs a warning.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_]*$"
	InstanceArg string `json:"instanceArg" yaml:"instanceArg"`
}

// MCPServerAuth configures authentication behavior for an MCP server.
// This enables Single Sign-On (SSO) via token forwarding between muster and
// downstream MCP servers that share the same Identity Provider.
// +kubebuilder:validation:XValidation:rule="!has(self.authorizationServer) || self.type == 'oauth'",message="authorizationServer is only valid when type is oauth"
// +kubebuilder:validation:XValidation:rule="!(has(self.forwardToken) && self.forwardToken == true && has(self.authorizationServer))",message="forwardToken bypasses per-backend OAuth; set one or the other, not both"
// +kubebuilder:validation:XValidation:rule="!(has(self.tokenExchange) && has(self.tokenExchange.enabled) && self.tokenExchange.enabled == true && has(self.authorizationServer))",message="tokenExchange has its own issuer/endpoint config; set one or the other, not both"
// +kubebuilder:validation:XValidation:rule="has(self.sigv4) == (self.type == 'sigv4')",message="type 'sigv4' requires the sigv4 block, and the sigv4 block requires type 'sigv4'"
// +kubebuilder:validation:XValidation:rule="!has(self.sigv4) || (self.forwardToken == false && !has(self.tokenExchange))",message="sigv4 signs as muster's own machine identity, so forwardToken and tokenExchange do not apply"
type MCPServerAuth struct {
	// Type specifies the authentication type.
	// Supported values:
	//   - "oauth": OAuth 2.0/OIDC authentication
	//   - "none": No authentication
	//   - "sigv4": AWS Signature Version 4, signed with muster's own machine
	//     identity. See SigV4.
	// +kubebuilder:validation:Enum=oauth;none;sigv4
	// +kubebuilder:default=none
	Type string `json:"type,omitempty" yaml:"type,omitempty"`

	// ForwardToken enables ID token forwarding for SSO.
	// When true, muster forwards the session's upstream dex ID token (or, for
	// sessions established by a trusted-issuer bearer, that IdP-issued bearer)
	// to this server byte-identical, instead of triggering a separate OAuth
	// flow. Every forwarded token is issued by the IdP (dex); muster is not
	// an identity provider, never signs tokens, and no backend is ever
	// configured to trust a muster JWKS. The downstream server must trust the
	// IdP's issuer/JWKS (e.g. muster's client ID in its TrustedAudiences for
	// forwarded dex ID tokens).
	//
	// The forwarded token is not audience-scoped to this server: the same
	// token is accepted by every forwardToken backend, so all forwardToken
	// backends must be equally trusted. A token's nested act claim (minted
	// by the IdP, e.g. via exchange at dex) carries the delegation chain for
	// backend authorization decisions.
	// +kubebuilder:default=false
	ForwardToken bool `json:"forwardToken,omitempty" yaml:"forwardToken,omitempty"`

	// RequiredAudiences specifies additional audience(s) that the forwarded ID token
	// should contain. When ForwardToken is true, muster will request these audiences
	// from the upstream IdP (e.g., Dex) using cross-client scopes.
	//
	// This is used when the downstream server requires tokens with specific audiences,
	// for example when forwarding tokens to Kubernetes for OIDC authentication:
	//   requiredAudiences:
	//     - "dex-k8s-authenticator"
	//
	// At user authentication, muster collects all requiredAudiences from MCPServers
	// with forwardToken: true and requests them all from the IdP.
	RequiredAudiences []string `json:"requiredAudiences,omitempty" yaml:"requiredAudiences,omitempty"`

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

	// AuthorizationServer is an opt-out for backends that don't publish RFC 9728
	// Protected Resource Metadata. When set, muster's per-server OAuth login flow
	// (core_auth_login) skips PRM probing and uses these values directly. muster
	// logs each override use at INFO so non-compliance is observable.
	//
	// This override does NOT bypass mcp-go's connect-time PRM probe; backends
	// without RFC 9728 metadata still reconcile to "Auth Required" on first
	// connect, then transition to "Connected" after `muster auth login`.
	//
	// Setting AuthorizationServer does NOT change the RFC 8707 `resource`
	// parameter — that remains driven by the MCP server URL.
	//
	// AuthorizationServer is mutually exclusive with ForwardToken: true and
	// TokenExchange.Enabled: true. The CRD admission rules above reject any
	// CR that combines them. Only valid when Type is "oauth".
	//
	// Use case: Atlassian Remote MCP and similar backends that publish RFC 8414
	// metadata at their resource origin instead of via RFC 9728.
	AuthorizationServer *MCPServerAuthAuthorizationServer `json:"authorizationServer,omitempty" yaml:"authorizationServer,omitempty"`

	// SigV4 configures AWS Signature Version 4 request signing. Required when
	// Type is "sigv4", and rejected otherwise.
	//
	// Unlike the OAuth mechanisms above, this is a machine identity: every
	// request signs as muster itself, not as the calling user, so it never
	// touches the session-scoped client machinery. Only valid together with
	// spec.type "streamable-http".
	//
	// ForwardToken and TokenExchange are rejected alongside it by the rule
	// above. AuthorizationServer needs no rule of its own: it already requires
	// Type "oauth", which the sigv4 pairing rule excludes.
	SigV4 *MCPServerSigV4 `json:"sigv4,omitempty" yaml:"sigv4,omitempty"`
}

// MCPServerSigV4 configures AWS Signature Version 4 signing for an MCP server.
//
// Everything here is AWS-specific because SigV4 is AWS. MCPServerAuth.Type is
// an enum, so another provider's signing scheme gets its own sub-struct rather
// than sharing these fields.
type MCPServerSigV4 struct {
	// Region is the SigV4 signing region. It must match the region in URL,
	// because the signature's credential scope is checked against the
	// endpoint. This is not the region the backend operates in — that is
	// MCPServerSpec.Meta.
	// +kubebuilder:validation:Required
	Region string `json:"region" yaml:"region"`

	// Service is the SigV4 signing service name. It defaults to the first
	// hostname label of URL, which is how AWS's own clients derive it:
	// aws-mcp.eu-central-1.api.aws signs as service "aws-mcp".
	Service string `json:"service,omitempty" yaml:"service,omitempty"`

	// RoleARN, when set, is assumed from the pod's base credentials before
	// signing. Leave empty to sign as the pod's own identity.
	RoleARN string `json:"roleArn,omitempty" yaml:"roleArn,omitempty"`
}

// MCPServerAuthAuthorizationServer pins the OAuth authorization server for an
// MCP server when RFC 9728 PRM discovery is unavailable.
//
// Three optional additions turn the pin into a full description of an
// authorization server muster cannot discover or register with on its own,
// GitHub being the prompting case: it publishes no RFC 8414 metadata, accepts
// neither Client ID Metadata Documents nor RFC 7591 registration, and issues
// user tokens that belong to the person rather than to one login session.
//
// +kubebuilder:validation:XValidation:rule="has(self.authorizationEndpoint) == has(self.tokenEndpoint)",message="authorizationEndpoint and tokenEndpoint must be set together"
type MCPServerAuthAuthorizationServer struct {
	// Issuer is the OAuth 2.0 / OIDC issuer URL.
	// muster fetches AS metadata via the existing OAuth client, which performs
	// RFC 8414 / OIDC discovery against this issuer -- unless
	// AuthorizationEndpoint and TokenEndpoint are set, in which case the issuer
	// is only the identity the stored grants are filed under.
	// +kubebuilder:validation:Required
	Issuer IssuerURL `json:"issuer" yaml:"issuer"`

	// AuthorizationEndpoint is the authorization server's authorization
	// endpoint. Set it together with TokenEndpoint for an authorization server
	// that publishes no RFC 8414 / OIDC discovery document (GitHub:
	// https://github.com/login/oauth/authorize). muster then performs no
	// discovery for this issuer and assumes S256 PKCE support, which the
	// operator asserts by pinning the endpoints.
	AuthorizationEndpoint IssuerURL `json:"authorizationEndpoint,omitempty" yaml:"authorizationEndpoint,omitempty"`

	// TokenEndpoint is the authorization server's token endpoint (GitHub:
	// https://github.com/login/oauth/access_token). See AuthorizationEndpoint.
	TokenEndpoint IssuerURL `json:"tokenEndpoint,omitempty" yaml:"tokenEndpoint,omitempty"`

	// ClientCredentialsSecretRef references a Kubernetes Secret holding a
	// client registered with this authorization server out of band (a GitHub
	// App or OAuth App). When set, muster identifies itself with these
	// credentials instead of its Client ID Metadata Document or a dynamic
	// registration -- the only option against an authorization server that
	// supports neither. The secret carries `client-id` and `client-secret`
	// (key names configurable in the reference).
	ClientCredentialsSecretRef *ClientCredentialsSecretRef `json:"clientCredentialsSecretRef,omitempty" yaml:"clientCredentialsSecretRef,omitempty"`

	// GrantScope decides whom a token obtained from this authorization server
	// belongs to. `session` (the default) keeps today's behavior: the token is
	// bound to the login session that completed the browser flow, so every new
	// session signs in again. `subject` files the token under the user's
	// identity as well, so any later session of the same person -- another
	// client, a re-login, a front-end whose bearer rotates -- reuses the grant
	// without a new consent, until the person signs out of this server
	// (`core_auth_logout`) or everywhere. Use `subject` for external accounts
	// the person owns (GitHub), never for tokens that carry session-specific
	// authority.
	// +kubebuilder:validation:Enum=session;subject
	GrantScope string `json:"grantScope,omitempty" yaml:"grantScope,omitempty"`

	// Scopes is the OAuth scope parameter value (RFC 6749 §3.3 wire format:
	// space-separated scope tokens). Matches existing TokenExchangeConfig.Scopes.
	// +optional
	Scopes string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
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
	// This may differ from the issuer URL when access goes through a proxy.
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

	// ClientCredentialsSecretRef references a Kubernetes Secret containing
	// client credentials for authenticating with the remote Dex's token endpoint.
	// This is required when the remote Dex requires client authentication for
	// token exchange (RFC 8693).
	//
	// The secret should contain:
	//   - client-id: The OAuth client ID registered on the remote Dex
	//   - client-secret: The OAuth client secret for authentication
	//
	// Example secret:
	//
	//	apiVersion: v1
	//	kind: Secret
	//	metadata:
	//	  name: grizzly-token-exchange-credentials
	//	  namespace: muster
	//	type: Opaque
	//	stringData:
	//	  client-id: muster-token-exchange
	//	  client-secret: <secret-value>
	ClientCredentialsSecretRef *ClientCredentialsSecretRef `json:"clientCredentialsSecretRef,omitempty" yaml:"clientCredentialsSecretRef,omitempty"`
}

// ClientCredentialsSecretRef references a Kubernetes Secret containing
// OAuth client credentials for token exchange authentication.
type ClientCredentialsSecretRef struct {
	// Name is the name of the Kubernetes Secret.
	// Required.
	Name string `json:"name" yaml:"name"`

	// Namespace is the Kubernetes namespace where the secret is located.
	// If not specified, defaults to the MCPServer's namespace.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`

	// ClientIDKey is the key in the secret data that contains the client ID.
	// Defaults to "client-id" if not specified.
	// +kubebuilder:default="client-id"
	ClientIDKey string `json:"clientIdKey,omitempty" yaml:"clientIdKey,omitempty"`

	// ClientSecretKey is the key in the secret data that contains the client secret.
	// Defaults to "client-secret" if not specified.
	// +kubebuilder:default="client-secret"
	ClientSecretKey string `json:"clientSecretKey,omitempty" yaml:"clientSecretKey,omitempty"`
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
//   - Connected: TCP connection established and authenticated
//   - AuthRequired: Server is reachable but requires authentication (returned 401)
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

	// MCPServerStateConnected indicates a remote server is reachable and authenticated.
	// The server responded successfully (not 401/403).
	MCPServerStateConnected MCPServerStateValue = "Connected"

	// MCPServerStateAuthRequired indicates a remote server is reachable but requires authentication.
	// The server returned a 401 Unauthorized response, indicating it IS reachable at the
	// network level but needs OAuth authentication before it can be used.
	// Users should run `muster auth login --server <name>` to authenticate.
	MCPServerStateAuthRequired MCPServerStateValue = "Auth Required"

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
// This status reflects server-side observable state including auth requirements.
// It captures infrastructure connectivity as well as whether the server demands
// authentication (e.g. "Auth Required"). Per-user session state (which specific
// user is authenticated, token expiry, etc.) is tracked separately in the
// Session Registry (internal/aggregator/session_registry.go).
//
// Server-Side State (CRD):
//   - State: Running/Connected/Starting/Connecting/Stopped/Disconnected/Auth Required/Failed
//   - Conditions: Standard K8s conditions for detailed status
//
// Per-User Session State (Session Registry):
//   - ConnectionStatus: Connected, PendingAuth, Failed (per-user)
//   - AuthStatus: Authenticated, AuthRequired, TokenExpired (per-user)
//   - AvailableTools: Tools visible to this specific user
type MCPServerStatus struct {
	// State represents the high-level infrastructure state of the MCP server.
	// This is independent of user session state (authentication, connection status).
	//
	// For stdio servers: Running, Starting, Stopped, Failed
	// For remote servers: Connected, Auth Required, Connecting, Disconnected, Failed
	// +kubebuilder:validation:Enum=Running;Starting;Stopped;Connected;Auth Required;Connecting;Disconnected;Failed
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
	// The wait doubles with every consecutive failure, starting at 30 seconds,
	// and is capped (2 minutes by default) so a recovered upstream is noticed
	// within the cap plus the retry tick, however long the outage lasted.
	// Absent while no retry is scheduled.
	NextRetryAfter *metav1.Time `json:"nextRetryAfter,omitempty" yaml:"nextRetryAfter,omitempty"`

	// LastFailureHTTPStatus is the HTTP status code the endpoint answered the
	// most recent failed connection attempt with, for example 504 from a
	// gateway in front of the server. Absent when the attempt got no HTTP
	// response at all (connection refused, DNS failure, timeout) or the last
	// attempt succeeded. Together with LastError it tells an upstream outage
	// from an endpoint nothing listens on.
	LastFailureHTTPStatus int `json:"lastFailureHTTPStatus,omitempty" yaml:"lastFailureHTTPStatus,omitempty"`

	// LastRestartedAt mirrors the spec.restartRequestedAt value most recently
	// processed by the reconciler. A restart is performed only when the two
	// differ (spec-is-desired / status-is-observed idiom, issue #1055).
	LastRestartedAt *metav1.Time `json:"lastRestartedAt,omitempty" yaml:"lastRestartedAt,omitempty"`

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
// +kubebuilder:validation:XValidation:rule="self.spec.type != 'stdio' || !has(self.spec.meta)",message="meta field is only allowed when type is streamable-http or sse"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.auth) || !has(self.spec.auth.sigv4) || self.spec.type == 'streamable-http'",message="auth.sigv4 is only allowed when type is streamable-http"

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
