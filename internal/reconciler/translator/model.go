package translator

// AuthnType is the authentication strategy applied at the gateway for a route.
type AuthnType string

const (
	// AuthnTypeNone disables authentication at the gateway.
	AuthnTypeNone AuthnType = "none"
	// AuthnTypeOAuth applies OAuth/OIDC token validation and optional forwarding.
	AuthnTypeOAuth AuthnType = "oauth"
	// AuthnTypeTeleport applies Teleport Application Access mutual TLS.
	AuthnTypeTeleport AuthnType = "teleport"
)

// Model is the translator's output: the full agentgateway configuration for a
// single MCPServer in a form that is independent of the eventual serialization.
type Model struct {
	// Backends are the upstream targets agentgateway routes traffic to.
	Backends []Backend
	// Routes map gateway paths to backends and policies.
	Routes []Route
	// Policies declare authentication and authorization for routes.
	Policies []Policy
}

// Backend is the gateway-side description of one MCPServer upstream. It is a
// tagged union: exactly one of StreamableHTTP, SSE, Stdio is non-nil and
// Transform guarantees this invariant. Emitters branch on which field is set.
type Backend struct {
	// Name is the stable identifier shared with the owning Route and Policy.
	Name string
	// StreamableHTTP, when non-nil, indicates an MCP-over-StreamableHTTP upstream.
	StreamableHTTP *HTTPTarget
	// SSE, when non-nil, indicates an MCP-over-SSE upstream.
	SSE *HTTPTarget
	// Stdio, when non-nil, indicates an stdio MCP child that agentgateway
	// spawns itself per agw's `mcp.targets[].stdio` schema.
	Stdio *StdioTarget
}

// HTTPTarget is the host/port/path triple shared by the StreamableHTTP and SSE
// transports.
type HTTPTarget struct {
	// Host is the DNS name or IP of the upstream.
	Host string
	// Port is the TCP port of the upstream.
	Port int
	// Path is the URL path agentgateway forwards requests to on the upstream.
	Path string
}

// StdioTarget describes a stdio MCP child that agentgateway spawns directly.
// Maps to agw's LocalMcpTarget.stdio schema: cmd, args, env (clear_env is
// always false today and not modeled).
type StdioTarget struct {
	// Command is the executable path for the stdio child.
	Command string
	// Args is the command-line argument list for the stdio child.
	Args []string
	// Env is the environment passed to the stdio child.
	Env map[string]string
}

// Route attaches a gateway path-match to a Backend under a Policy.
type Route struct {
	// Name is the stable identifier shared with the matching Backend and Policy.
	Name string
	// PathMatch is the gateway-side path prefix that matches this route.
	PathMatch string
	// BackendRef is the Name of the Backend this route forwards to.
	BackendRef string
	// PolicyRef is the Name of the Policy applied to this route.
	PolicyRef string
}

// Policy declares the gateway-side authentication contract for a Route.
type Policy struct {
	// Name is the stable identifier shared with the matching Route.
	Name string
	// Authn captures the authentication strategy and its parameters.
	Authn AuthnConfig
}

// AuthnConfig is the gateway-side view of MCPServerSpec.Auth.
type AuthnConfig struct {
	// Type selects the authentication strategy.
	Type AuthnType
	// ForwardToken, when true, forwards the caller's bearer token to the
	// upstream instead of triggering a per-backend OAuth flow.
	ForwardToken bool
	// RequiredAudiences are audiences the forwarded token must contain.
	RequiredAudiences []string
	// TokenExchange, when non-nil, configures RFC 8693 token exchange to
	// mint a token valid on the upstream's Identity Provider.
	TokenExchange *TokenExchangeAuthn
	// Teleport, when non-nil, configures Teleport Application Access mTLS.
	Teleport *TeleportAuthn
	// AuthorizationServer, when non-nil, pins the OAuth authorization
	// server for backends that do not publish RFC 9728 metadata.
	AuthorizationServer *AuthorizationServer
}

// TokenExchangeAuthn is the gateway-side view of TokenExchangeConfig.
type TokenExchangeAuthn struct {
	// Enabled toggles token exchange.
	Enabled bool
	// DexTokenEndpoint is the URL of the remote IdP's token endpoint.
	DexTokenEndpoint string
	// ExpectedIssuer is the issuer claim the exchanged token must carry.
	ExpectedIssuer string
	// ConnectorID is the OIDC connector ID on the remote IdP.
	ConnectorID string
	// Scopes is the space-separated OAuth scope string requested for the
	// exchanged token.
	Scopes string
	// ClientCredentialsSecretName references a Kubernetes Secret carrying
	// client credentials for the exchange call. Empty when no client
	// authentication is needed.
	ClientCredentialsSecretName string
	// ClientCredentialsSecretNamespace is the namespace of the credentials
	// Secret. Empty defers to the MCPServer's namespace at emit time.
	ClientCredentialsSecretNamespace string
}

// TeleportAuthn is the gateway-side view of TeleportAuthConfig.
type TeleportAuthn struct {
	// IdentitySecretName is the name of the Kubernetes Secret holding tbot
	// identity files.
	IdentitySecretName string
	// IdentitySecretNamespace is the namespace of the identity Secret.
	IdentitySecretNamespace string
	// IdentityDir is the on-disk directory of tbot identity files used in
	// filesystem mode.
	IdentityDir string
	// AppName is the Teleport application name used for routing.
	AppName string
}

// AuthorizationServer pins the OAuth authorization server for a backend.
type AuthorizationServer struct {
	// Issuer is the OAuth 2.0 / OIDC issuer URL.
	Issuer string
	// Scopes is the OAuth scope parameter value (RFC 6749 §3.3 wire format).
	Scopes string
}
