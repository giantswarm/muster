package api

import (
	"context"
	"errors"
	"fmt"
	"time"
)

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

	// Family declares that this MCP server is an instance of a family of
	// equivalent servers. When set, the aggregator exposes tools as
	// {musterPrefix}_{family.name}_{toolName} with a required parameter named
	// by family.instanceArg.
	Family *MCPServerFamily `yaml:"family,omitempty" json:"family,omitempty"`

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

	// Meta contains entries merged into the `params._meta` object of every
	// outbound JSON-RPC request that carries `params`. Remote types only; see
	// ValidateMetaAllowed and the v1alpha1 CRD field of the same name.
	Meta map[string]string `yaml:"meta,omitempty" json:"meta,omitempty"`

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

// MCPServerFamily groups equivalent MCP server instances under a shared
// exposed surface. Name and InstanceArg are both required when set.
type MCPServerFamily struct {
	// Name is the family identifier shared across instances.
	Name string `yaml:"name" json:"name"`

	// InstanceArg names the required parameter callers use to select which
	// family member handles the tool call.
	InstanceArg string `yaml:"instanceArg" json:"instanceArg"`
}

// MCPServerAuth configures authentication behavior for an MCP server.
//
// Muster supports two distinct authentication mechanisms:
//
//   - SSO Token Forwarding: Muster forwards its own ID token to downstream servers.
//     Enable with ForwardToken: true. Requires downstream to trust muster's client ID.
//
//   - SSO Token Exchange (RFC 8693): Muster exchanges its token for one valid on the
//     remote cluster's Dex. Enable with TokenExchange config. Requires the remote Dex
//     to have an OIDC connector configured for the local cluster's Dex.
//
// AWS SigV4 (Type: MCPServerAuthTypeSigV4) is not one of them: it signs as
// muster's own machine identity rather than relaying the caller's, so it uses
// the shared global client instead of the session-scoped machinery.
type MCPServerAuth struct {
	// Type specifies the authentication type.
	// Supported values:
	//   - "oauth": OAuth 2.0/OIDC authentication
	//   - "none": No authentication
	//   - "sigv4": AWS Signature Version 4 request signing, see SigV4
	Type string `yaml:"type,omitempty" json:"type,omitempty"`

	// ForwardToken enables SSO via Token Forwarding.
	// When true, muster forwards its own ID token (obtained when user authenticated
	// TO muster) to this downstream server. The downstream server must be configured
	// to trust muster's OAuth client ID in its TrustedAudiences configuration.
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

	// AuthorizationServer pins the OAuth issuer when the MCP server does not
	// publish RFC 9728 Protected Resource Metadata. See the v1alpha1 CRD field
	// of the same name for full semantics. When set, muster's per-server OAuth
	// login flow skips PRM probing and uses these values directly.
	AuthorizationServer *MCPServerAuthAuthorizationServer `yaml:"authorizationServer,omitempty" json:"authorizationServer,omitempty"`

	// SigV4 configures AWS Signature Version 4 request signing. Required when
	// Type is MCPServerAuthTypeSigV4, and rejected otherwise. See the v1alpha1
	// CRD field of the same name for full semantics.
	SigV4 *MCPServerSigV4 `yaml:"sigv4,omitempty" json:"sigv4,omitempty"`
}

// MCPServerAuthTypeSigV4 is the MCPServerAuth.Type value that selects AWS
// Signature Version 4 request signing.
const MCPServerAuthTypeSigV4 = "sigv4"

// MCPServerSigV4 configures AWS Signature Version 4 signing for an MCP server.
// See the v1alpha1 CRD type of the same name for full semantics.
type MCPServerSigV4 struct {
	// Region is the SigV4 signing region.
	Region string `yaml:"region" json:"region"`

	// Service is the SigV4 signing service name.
	Service string `yaml:"service,omitempty" json:"service,omitempty"`

	// RoleARN is the role assumed before signing, if any.
	RoleARN string `yaml:"roleArn,omitempty" json:"roleArn,omitempty"`
}

// MCPServerAuthAuthorizationServer pins the OAuth authorization server for an
// MCP server when RFC 9728 PRM discovery is unavailable.
type MCPServerAuthAuthorizationServer struct {
	// Issuer is the OAuth 2.0 / OIDC issuer URL.
	// Normalized form: HTTPS, no trailing slash, no fragment, no query.
	Issuer string `yaml:"issuer" json:"issuer"`

	// Scopes is the OAuth scope parameter value (RFC 6749 §3.3 wire format:
	// space-separated scope tokens).
	Scopes string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
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
	// This may differ from the issuer URL when access goes through a proxy.
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

// ErrStdioNotAllowedInKubernetesMode reports a "stdio" MCPServer submitted to a
// muster running in Kubernetes mode. A stdio server is started as a subprocess
// of the muster process, so accepting one in a deployed aggregator would turn
// "may write MCPServer resources" into "may execute code in the muster pod as
// muster's ServiceAccount" (issue #1067). The capability is removed rather than
// narrowed to a command allowlist: the pod image ships muster, not a fleet of
// MCP server binaries, and in-cluster servers are reached over HTTP.
var ErrStdioNotAllowedInKubernetesMode = errors.New(
	`type "stdio" is not supported in Kubernetes mode: a stdio server is started as a subprocess of the muster pod. ` +
		`Run the MCP server as its own workload and register it with type "streamable-http" or "sse". ` +
		`stdio stays available when muster runs as a local CLI (filesystem mode)`)

// ValidateStdioAllowed reports whether a server type may run in the current
// mode, returning ErrStdioNotAllowedInKubernetesMode for stdio in Kubernetes
// mode and nil otherwise.
//
// Callers pass the mode they already hold — client.IsKubernetesMode() for the
// tool handlers and reconciler, the flag plumbed down from it for the
// orchestrator and the service — so admission, reconciliation, and process
// start share one definition of the policy instead of three.
func ValidateStdioAllowed(serverType string, kubernetesMode bool) error {
	if kubernetesMode && serverType == string(MCPServerTypeStdio) {
		return ErrStdioNotAllowedInKubernetesMode
	}
	return nil
}

// ToMCPServer converts the API/reconciler view of a server into the
// service-layer configuration struct.
//
// One definition on purpose. The orchestrator's registration path and the
// reconciler's update path both need this conversion, they cannot import each
// other, and while each kept its own copy a new spec field reached the service
// through one path and was silently dropped by the other.
func (i MCPServerInfo) ToMCPServer() *MCPServer {
	return &MCPServer{
		Name:        i.Name,
		Type:        MCPServerType(i.Type),
		Description: i.Description,
		ToolPrefix:  i.ToolPrefix,
		Family:      i.Family,
		AutoStart:   i.AutoStart,
		Command:     i.Command,
		Args:        i.Args,
		URL:         i.URL,
		Env:         i.Env,
		Headers:     i.Headers,
		Meta:        i.Meta,
		Timeout:     i.Timeout,
		Auth:        i.Auth,
	}
}

// CanAuthenticateInteractively reports whether a 401 from a server with this
// auth configuration can be resolved by a user completing a login flow.
//
// It is false for a machine identity: the credential is muster's own, so there
// is no user to send to an authorization server and no pending-auth state to
// reach. A 401 from such a server is an ordinary connect failure — the region,
// the assumed role or its policy is wrong — and every consumer of the
// auth-required signal has to agree on that, or the CR reports "Auth Required"
// while the aggregator refuses to register it.
//
// A nil receiver means no auth is configured, which is the OAuth-capable
// default: a 401 from an unconfigured server is what triggers discovery.
func (a *MCPServerAuth) CanAuthenticateInteractively() bool {
	if a == nil {
		return true
	}
	return a.Type != MCPServerAuthTypeSigV4
}

// ValidateMetaAllowed reports whether spec.meta is usable with the given
// server type.
//
// Injection happens in an HTTP round tripper, so a stdio server — which speaks
// over a pipe and has no round tripper — would accept the map and drop it.
// Rejected rather than ignored, because a dropped entry fails nowhere: the
// AWS-hosted backend falls back to its own region and returns a correct-looking
// answer about the wrong one. That silence is the whole reason the field is
// checked here rather than left to the connect attempt.
//
// The CRD states the same rule in CEL, which only runs in Kubernetes mode.
// Keep the two in step, as ValidateSigV4 and ValidateStdioAllowed do.
func ValidateMetaAllowed(serverType string, meta map[string]string) error {
	if len(meta) == 0 {
		return nil
	}
	if serverType == string(MCPServerTypeStdio) {
		return fmt.Errorf("meta is only allowed when type is %q or %q, not %q: the entries are merged into params._meta by an HTTP transport, which a stdio server does not have",
			MCPServerTypeStreamableHTTP, MCPServerTypeSSE, serverType)
	}
	return nil
}

// ValidateSigV4 reports whether an auth configuration that selects SigV4 is
// usable with the given server type, and returns nil for every other auth type.
//
// The CRD states the same rules in CEL, but CEL only runs in Kubernetes mode.
// This function is the definition that also holds in filesystem mode, so
// admission and client construction share it instead of restating it — the same
// arrangement ValidateStdioAllowed uses. Keep the two in step: a rule that
// lives only in CEL is a rule a filesystem-mode muster does not enforce.
func ValidateSigV4(serverType string, auth *MCPServerAuth) error {
	if auth == nil {
		return nil
	}
	if auth.Type != MCPServerAuthTypeSigV4 {
		if auth.SigV4 != nil {
			return fmt.Errorf("auth.sigv4 is only allowed when auth.type is %q", MCPServerAuthTypeSigV4)
		}
		return nil
	}
	// Signing needs a body and a single request-response exchange, which is what
	// streamable-http gives. SSE would leave the credential unused rather than
	// fail, so refuse it here instead of connecting unsigned.
	if serverType != string(MCPServerTypeStreamableHTTP) {
		return fmt.Errorf("auth.type %q is only allowed when type is %q, not %q",
			MCPServerAuthTypeSigV4, MCPServerTypeStreamableHTTP, serverType)
	}
	// A missing region cannot be defaulted: the endpoint checks the signature's
	// credential scope, so a guess would surface as a signature error rather
	// than a configuration error.
	if auth.SigV4 == nil || auth.SigV4.Region == "" {
		return fmt.Errorf("auth.sigv4.region is required when auth.type is %q", MCPServerAuthTypeSigV4)
	}
	// Rejected rather than ignored. Both relay the caller's identity, which a
	// machine identity does not have, so accepting them would leave a spec that
	// says the caller's token is forwarded and a wire that signs as muster.
	if auth.ForwardToken {
		return fmt.Errorf("auth.forwardToken does not apply when auth.type is %q: the request signs as muster's own machine identity, not the caller's", MCPServerAuthTypeSigV4)
	}
	if auth.TokenExchange != nil {
		return fmt.Errorf("auth.tokenExchange does not apply when auth.type is %q: the request signs as muster's own machine identity, not the caller's", MCPServerAuthTypeSigV4)
	}
	// The CRD reaches the same conclusion by a different route — its
	// authorizationServer rule requires type "oauth", which the sigv4 pairing
	// rule excludes — but that reasoning lives only in CEL. State it here so
	// filesystem mode rejects it too instead of silently ignoring the block.
	if auth.AuthorizationServer != nil {
		return fmt.Errorf("auth.authorizationServer does not apply when auth.type is %q: there is no authorization server in a machine identity flow", MCPServerAuthTypeSigV4)
	}
	return nil
}

// RegisteredByAnnotation records the authenticated subject that registered an
// MCPServer. Stamped server-side on create so attribution cannot be spoofed or
// omitted by clients (issue #1021). The key is shared with the developer portal.
const RegisteredByAnnotation = "ui.giantswarm.io/registered-by"

// RegisteredByEmailAnnotation records the email claim of the authenticated
// identity that registered an MCPServer, when the token carried one. Display
// metadata only — RegisteredByAnnotation holds the stable identifier
// (issue #1048). The key is shared with the developer portal.
const RegisteredByEmailAnnotation = "ui.giantswarm.io/registered-by-email"

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

	// Suspended declares the desired lifecycle state of this server's service.
	// When true, the reconciler stops the service and refuses to start it
	// until the field is set back to false (issue #1055).
	Suspended bool `json:"suspended,omitempty"`

	// RestartRequestedAt requests a one-shot restart of this server's service.
	// The reconciler acts only when it differs from LastRestartedAt.
	RestartRequestedAt *time.Time `json:"restartRequestedAt,omitempty"`

	// LastRestartedAt mirrors the RestartRequestedAt value most recently
	// processed by the reconciler (from the CR status).
	LastRestartedAt *time.Time `json:"lastRestartedAt,omitempty"`

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

	// Meta contains entries merged into the `params._meta` object of every
	// outbound JSON-RPC request that carries `params`.
	Meta map[string]string `json:"meta,omitempty"`

	// Auth configures authentication behavior for this MCP server.
	Auth *MCPServerAuth `json:"auth,omitempty"`

	// Timeout specifies the connection timeout for remote operations (in seconds)
	Timeout int `json:"timeout,omitempty"`

	// ToolPrefix is an optional prefix for tool names.
	ToolPrefix string `json:"toolPrefix,omitempty"`

	// Family declares that this MCP server is an instance of a family of
	// equivalent servers, sharing exposed tool names with siblings.
	Family *MCPServerFamily `json:"family,omitempty"`

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

	// ProtocolVersion is the MCP revision this backend answered with during the
	// handshake. It can be older than ClientProtocolVersion: a backend that
	// supports only an earlier revision answers with that one. Empty when the
	// server has no connected client.
	ProtocolVersion string `json:"protocolVersion,omitempty"`

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

	// RegisteredBy is the authenticated subject that registered this server,
	// read from the RegisteredByAnnotation. Empty when the server was created
	// without an authenticated context (e.g. GitOps-applied CRs).
	RegisteredBy string `json:"registeredBy,omitempty"`

	// RegisteredByEmail is the email claim of the identity that registered
	// this server, read from the RegisteredByEmailAnnotation. Display metadata
	// only — RegisteredBy is the stable identifier. Empty when the token
	// carried no email claim (e.g. Kubernetes ServiceAccount identities).
	RegisteredByEmail string `json:"registeredByEmail,omitempty"`
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
	// An error means the set could not be read. That is not the same as an
	// empty set, and a caller must not treat it as one.
	//
	// Returns:
	//   - []MCPServerInfo: Slice of server information (empty if no servers exist)
	//   - error: nil on success, or an error if the servers could not be listed
	ListMCPServers(ctx context.Context) ([]MCPServerInfo, error)

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

// MCPServerLifecycleAsCaller is the optional capability the registered
// MCPServer manager gains when writes-as-caller is enabled (issue #1057):
// lifecycle actions on MCPServer-backed services become CR spec writes
// performed with the caller's own identity — authorized by k8s RBAC and
// audited by the apiserver — instead of orchestrator mutations with muster's
// privilege. handled=false means the caller must fall back to the imperative
// path (flag off, or the name is a non-MCPServer service such as muster's own
// aggregator); it is always accompanied by a nil result and a nil error.
//
// The implementing adapter pins this interface at compile time so a signature
// drift cannot silently fail the type assertion and reopen the privileged
// imperative path.
type MCPServerLifecycleAsCaller interface {
	StartMCPServerAsCaller(ctx context.Context, name string) (result *CallToolResult, handled bool, err error)
	StopMCPServerAsCaller(ctx context.Context, name string) (result *CallToolResult, handled bool, err error)
	RestartMCPServerAsCaller(ctx context.Context, name string) (result *CallToolResult, handled bool, err error)
}
