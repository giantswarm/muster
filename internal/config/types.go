package config

import "strings"

// MusterConfig is the top-level configuration structure for muster.
type MusterConfig struct {
	Aggregator AggregatorConfig `yaml:"aggregator"`
	Namespace  string           `yaml:"namespace,omitempty"`  // Namespace for MCPServer and Workflow discovery
	Kubernetes bool             `yaml:"kubernetes,omitempty"` // Enable Kubernetes CRD mode (uses CRDs instead of filesystem)
	Events     bool             `yaml:"events,omitempty"`     // Enable Kubernetes event emission (alpha, disabled by default)
}

// MCPServerType defines the type of MCP server.
type MCPServerType string

const (
	MCPServerTypeStdio          MCPServerType = "stdio"
	MCPServerTypeStreamableHTTP MCPServerType = "streamable-http"
	MCPServerTypeSSE            MCPServerType = "sse"
)

const (
	// MCPTransportStreamableHTTP is the streamable HTTP transport.
	MCPTransportStreamableHTTP = "streamable-http"
	// MCPTransportSSE is the Server-Sent Events transport.
	MCPTransportSSE = "sse"
	// MCPTransportStdio is the standard I/O transport.
	MCPTransportStdio = "stdio"
)

// Use MCPServerDefinition from mcpserver package to avoid duplication

// AggregatorConfig defines the configuration for the MCP aggregator service.
type AggregatorConfig struct {
	Port         int    `yaml:"port,omitempty"`         // Port for the aggregator SSE endpoint (default: 8080)
	Host         string `yaml:"host,omitempty"`         // Host to bind to (default: localhost)
	Transport    string `yaml:"transport,omitempty"`    // Transport to use (default: streamable-http)
	MusterPrefix string `yaml:"musterPrefix,omitempty"` // Pre-prefix for all tools (default: "x")

	// OAuth contains all OAuth-related configuration with explicit mcpClient/server roles.
	// - oauth.mcpClient: muster as OAuth client/proxy for authenticating TO remote MCP servers
	// - oauth.server: muster as OAuth resource server for protecting ITSELF
	OAuth OAuthConfig `yaml:"oauth,omitempty"`

	// Admin exposes a read-only web UI for listing and managing sessions on a
	// separate HTTP listener. Disabled by default. When enabled, the listener
	// binds to AdminBindAddress:AdminPort without authentication, so it is
	// only safe when bound to a loopback address or reached via port-forward.
	Admin AdminConfig `yaml:"admin,omitempty"`
}

// AdminConfig defines the configuration for the admin web UI.
//
// The admin surface exposes session management (list, inspect, delete) on a
// dedicated HTTP listener. It does not implement authentication; rely on
// network-level controls (loopback binding, kubectl port-forward RBAC) to
// gate access.
type AdminConfig struct {
	// Enabled controls whether the admin listener is started. Default: false.
	Enabled bool `yaml:"enabled,omitempty"`

	// Port is the TCP port for the admin listener (default: 9999).
	Port int `yaml:"port,omitempty"`

	// BindAddress is the interface to bind to (default: "127.0.0.1").
	// Change this at your own risk: the admin surface has no auth.
	BindAddress string `yaml:"bindAddress,omitempty"`
}

// OAuthConfig consolidates all OAuth-related configuration with explicit mcpClient/server roles.
// This structure clearly separates the two distinct OAuth roles that muster can play:
//   - MCPClient: when muster authenticates TO remote MCP servers on behalf of users
//   - Server: when muster protects ITSELF and requires clients to authenticate
type OAuthConfig struct {
	// MCPClient configuration for remote MCP server authentication (muster as OAuth proxy).
	// When enabled, muster acts as an OAuth client proxy, handling authentication
	// flows on behalf of users without exposing tokens to the Muster Agent.
	MCPClient OAuthMCPClientConfig `yaml:"mcpClient,omitempty"`

	// Server configuration for protecting the Muster Server itself.
	// When enabled, muster acts as an OAuth Resource Server, requiring valid
	// access tokens from clients (e.g., Muster Agent) to access protected endpoints.
	Server OAuthServerConfig `yaml:"server,omitempty"`
}

// OAuthMCPClientConfig defines the OAuth client/proxy configuration for remote MCP server authentication.
// When enabled, the Muster Server acts as an OAuth client proxy, handling authentication
// flows on behalf of users without exposing tokens to the Muster Agent.
type OAuthMCPClientConfig struct {
	// Enabled controls whether OAuth MCP client/proxy functionality is active.
	// When false, remote MCP servers requiring auth will return errors.
	Enabled bool `yaml:"enabled,omitempty"`

	// PublicURL is the publicly accessible URL of the Muster Server.
	// This is used to construct OAuth callback URLs (e.g., https://muster.example.com).
	// Required when OAuth MCP client is enabled.
	PublicURL string `yaml:"publicUrl,omitempty"`

	// ClientID is the OAuth client identifier.
	// This should be the URL of the Client ID Metadata Document (CIMD).
	// If not set and PublicURL is set, the ClientID will be auto-derived as
	// {PublicURL}/.well-known/oauth-client.json and muster will serve the CIMD itself.
	ClientID string `yaml:"clientId,omitempty"`

	// CallbackPath is the path for the OAuth proxy callback endpoint (default: "/oauth/proxy/callback").
	// This is used when muster authenticates with remote MCP servers.
	// NOTE: This MUST be different from the OAuth server callback (/oauth/callback) to avoid conflicts.
	CallbackPath string `yaml:"callbackPath,omitempty"`

	// CIMD contains Client ID Metadata Document configuration.
	// Muster can serve its own CIMD when acting as an OAuth client for MCP servers.
	CIMD OAuthCIMDConfig `yaml:"cimd,omitempty"`

	// ExtraCAFile mirrors the process-level --extra-ca-file flag for the
	// OAuth/token-exchange layer's internal-deployment heuristic. When set,
	// the token-exchange HTTP client allows resolution to private IP ranges
	// (e.g. in-cluster Dex via .svc.cluster.local).
	// Not part of any user-facing config; populated by the serve command.
	ExtraCAFile string `yaml:"-"`
}

// OAuthCIMDConfig contains Client ID Metadata Document configuration.
type OAuthCIMDConfig struct {
	// Path is the path for serving the Client ID Metadata Document (default: "/.well-known/oauth-client.json").
	// Muster will serve the CIMD at this path when OAuth MCP client is enabled and PublicURL is set.
	Path string `yaml:"path,omitempty"`

	// Scopes is the OAuth scopes to advertise in the self-hosted CIMD.
	// This determines what API scopes downstream MCP servers can use when muster
	// forwards tokens via SSO. Default: "openid profile email offline_access".
	// Operators can add additional scopes (e.g., Google API scopes) as needed.
	// Format: space-separated list of scope strings.
	Scopes string `yaml:"scopes,omitempty"`
}

// GetEffectiveClientID returns the effective OAuth client ID.
// If ClientID is explicitly set, it is returned as-is.
// If ClientID is empty but PublicURL is set, returns the self-hosted CIMD URL.
// Otherwise, returns empty string (OAuth proxy requires publicUrl to function).
func (c *OAuthMCPClientConfig) GetEffectiveClientID() string {
	if c.ClientID != "" {
		return c.ClientID
	}

	// Auto-derive from PublicURL if set
	if c.PublicURL != "" {
		cimdPath := c.CIMD.Path
		if cimdPath == "" {
			cimdPath = DefaultOAuthCIMDPath
		}
		return strings.TrimSuffix(c.PublicURL, "/") + cimdPath
	}

	// No publicUrl means OAuth proxy won't work - return empty
	return ""
}

// ShouldServeCIMD returns true if muster should serve its own CIMD.
// This is the case when OAuth MCP client is enabled, PublicURL is set, and ClientID
// is either empty or matches the auto-derived CIMD URL.
func (c *OAuthMCPClientConfig) ShouldServeCIMD() bool {
	if !c.Enabled || c.PublicURL == "" {
		return false
	}

	// If ClientID is not set, we should serve our own CIMD
	if c.ClientID == "" {
		return true
	}

	// If ClientID matches what we would auto-generate, serve our own CIMD
	cimdPath := c.CIMD.Path
	if cimdPath == "" {
		cimdPath = DefaultOAuthCIMDPath
	}
	autoClientID := strings.TrimSuffix(c.PublicURL, "/") + cimdPath
	return c.ClientID == autoClientID
}

// GetCIMDPath returns the path for serving the CIMD.
func (c *OAuthMCPClientConfig) GetCIMDPath() string {
	if c.CIMD.Path != "" {
		return c.CIMD.Path
	}
	return DefaultOAuthCIMDPath
}

// GetCIMDScopes returns the OAuth scopes to advertise in the CIMD.
// If not set, returns the default scopes: "openid profile email offline_access".
func (c *OAuthMCPClientConfig) GetCIMDScopes() string {
	if c.CIMD.Scopes != "" {
		return c.CIMD.Scopes
	}
	return DefaultOAuthCIMDScopes
}

// GetRedirectURI returns the full redirect URI for OAuth proxy callbacks.
// This is where remote MCP server IdPs will redirect after authentication.
func (c *OAuthMCPClientConfig) GetRedirectURI() string {
	callbackPath := c.CallbackPath
	if callbackPath == "" {
		callbackPath = DefaultOAuthProxyCallbackPath
	}
	return strings.TrimSuffix(c.PublicURL, "/") + callbackPath
}

// OAuthServerConfig defines the OAuth server configuration for protecting the Muster Server.
// When enabled, the Muster Server acts as an OAuth Resource Server, requiring valid
// access tokens from clients (e.g., Muster Agent) to access protected endpoints.
// This implements ADR 005 (OAuth Protection for Muster Server).
type OAuthServerConfig struct {
	// Enabled controls whether OAuth server protection is active.
	// When true, all MCP endpoints require valid OAuth tokens.
	Enabled bool `yaml:"enabled,omitempty"`

	// BaseURL is the publicly accessible base URL of the Muster Server.
	// This is used as the OAuth issuer URL (e.g., https://muster.example.com).
	// Required when OAuth server is enabled.
	BaseURL string `yaml:"baseUrl,omitempty"`

	// Provider specifies the OAuth provider to use: "dex" or "google".
	// Default: "dex"
	Provider string `yaml:"provider,omitempty"`

	// Dex configuration (used when Provider is "dex")
	Dex DexConfig `yaml:"dex,omitempty"`

	// Google configuration (used when Provider is "google")
	Google GoogleConfig `yaml:"google,omitempty"`

	// Storage configuration for OAuth tokens and client registrations.
	Storage OAuthStorageConfig `yaml:"storage,omitempty"`

	// RegistrationToken is the token required for dynamic client registration.
	// Required if AllowPublicClientRegistration is false.
	// For production, use RegistrationTokenFile instead to avoid secrets in config files.
	RegistrationToken string `yaml:"registrationToken,omitempty"`

	// RegistrationTokenFile is the path to a file containing the registration token.
	// This is the recommended way to provide secrets in production deployments.
	RegistrationTokenFile string `yaml:"registrationTokenFile,omitempty"`

	// AllowPublicClientRegistration allows unauthenticated dynamic client registration.
	// WARNING: This can lead to DoS attacks. Default: false.
	AllowPublicClientRegistration bool `yaml:"allowPublicClientRegistration,omitempty"`

	// EncryptionKey is the AES-256 key for encrypting tokens at rest (32 bytes, base64-encoded).
	// Required for production deployments.
	// For production, use EncryptionKeyFile instead to avoid secrets in config files.
	EncryptionKey string `yaml:"encryptionKey,omitempty"`

	// EncryptionKeyFile is the path to a file containing the encryption key.
	// This is the recommended way to provide secrets in production deployments.
	EncryptionKeyFile string `yaml:"encryptionKeyFile,omitempty"`

	// TrustedPublicRegistrationSchemes lists URI schemes allowed for unauthenticated
	// client registration. Enables Cursor/VSCode without registration tokens.
	// Example: ["cursor", "vscode"]
	TrustedPublicRegistrationSchemes []string `yaml:"trustedPublicRegistrationSchemes,omitempty"`

	// TrustedPublicRegistrationRedirectURIs lists fully-qualified HTTPS redirect URIs
	// allowed to register without a RegistrationAccessToken. Matching is exact after
	// RFC 3986 normalization. Enables SaaS MCP clients that cannot send a DCR token.
	// Example: ["https://claude.ai/api/mcp/auth_callback"]
	TrustedPublicRegistrationRedirectURIs []string `yaml:"trustedPublicRegistrationRedirectURIs,omitempty"`

	// EnableCIMD enables Client ID Metadata Documents per MCP 2025-11-25 spec.
	// Default: true
	EnableCIMD bool `yaml:"enableCIMD,omitempty"`

	// AllowLocalhostRedirectURIs allows http://localhost and http://127.0.0.1 redirect URIs.
	// Required for native apps (like muster agent) per RFC 8252 Section 7.3.
	// Default: true (native app support enabled by default)
	AllowLocalhostRedirectURIs bool `yaml:"allowLocalhostRedirectURIs,omitempty"`

	// SessionDuration is the maximum session duration before re-authentication
	// is required. This sets the server-side refresh token TTL.
	// Default: 720h (30 days), aligned with Dex's absoluteLifetime.
	// Format: Go duration string (e.g., "720h", "30d" is NOT valid, use hours).
	SessionDuration string `yaml:"sessionDuration,omitempty"`

	// AllowedOrigins is a comma-separated list of allowed CORS origins.
	AllowedOrigins string `yaml:"allowedOrigins,omitempty"`

	// EnableHSTS enables HSTS header (for reverse proxy scenarios).
	EnableHSTS bool `yaml:"enableHSTS,omitempty"`

	// TLSCertFile is the path to the TLS certificate file (PEM format).
	// If both TLSCertFile and TLSKeyFile are provided, the server will use HTTPS.
	TLSCertFile string `yaml:"tlsCertFile,omitempty"`

	// TLSKeyFile is the path to the TLS private key file (PEM format).
	TLSKeyFile string `yaml:"tlsKeyFile,omitempty"`

	// TrustedAudiences lists additional OAuth client IDs (audiences) whose
	// JWT ID tokens are accepted directly as bearer tokens, without requiring
	// the client to complete muster's own OAuth flow first. This enables
	// authenticating muster with tokens generated directly against Dex (e.g.
	// by dex-k8s-authenticator or another Dex client), as long as the token's
	// `aud` claim matches one of these values and its signature validates
	// against the provider's JWKS.
	//
	// SECURITY: only list client IDs you fully trust. Any JWT signed by the
	// configured OIDC provider and carrying one of these audiences is treated
	// as a valid muster bearer token.
	TrustedAudiences []string `yaml:"trustedAudiences,omitempty"`

	// TrustedIssuers registers external OIDC issuers for RFC 8693 token exchange.
	// Tokens are accepted as subject_tokens of type id_token, access_token, or jwt.
	TrustedIssuers []TrustedIssuerConfig `yaml:"trustedIssuers,omitempty"`

	// TrustedProxyCIDRs lists CIDRs from which X-Forwarded-Proto and
	// X-Forwarded-Host headers are trusted for DPoP htu URL reconstruction.
	// Required when muster runs behind a reverse proxy that terminates TLS.
	TrustedProxyCIDRs []string `yaml:"trustedProxyCIDRs,omitempty"`

	// EnableJWTMode issues signed RFC 9068 JWTs as access tokens instead of
	// opaque random strings. Required when downstream services (e.g. agentgateway)
	// need to validate tokens locally without calling the introspection endpoint.
	// JWTSigningKeyFile must be set when this is true.
	EnableJWTMode bool `yaml:"enableJWTMode,omitempty"`

	// JWTSigningKeyFile is the path to a PEM-encoded private key used to sign
	// access tokens in JWT mode. Supported formats: EC PRIVATE KEY (P-256, ES256),
	// RSA PRIVATE KEY / PRIVATE KEY (PKCS#8, RS256). kid is derived from the
	// RFC 7638 JWK thumbprint of the public key. Required when EnableJWTMode is true.
	JWTSigningKeyFile string `yaml:"jwtSigningKeyFile,omitempty"`

	// ResourceIdentifier is the canonical URI that identifies this muster instance
	// as an RFC 8707 resource server. When set, access tokens carry this value in
	// their aud claim and tokens bound to a different resource are rejected, preventing
	// replay across resource servers sharing the same IdP.
	// If empty the library defaults to BaseURL (the issuer URL).
	// Example: "https://muster.example.com/mcp"
	ResourceIdentifier string `yaml:"resourceIdentifier,omitempty"`

	// TokenExchangeBroker exposes muster's RFC 8693 token exchange to external
	// confidential clients: a broker client POSTs a token-exchange request with
	// an `audience` parameter to /oauth/token and receives a token minted by
	// the audience's downstream Dex (not a muster-issued JWT). Subject tokens
	// are validated against TrustedIssuers; the per-client allowlist below
	// gates which audiences each client may request.
	TokenExchangeBroker TokenExchangeBrokerConfig `yaml:"tokenExchangeBroker,omitempty"`
}

// TokenExchangeBrokerConfig configures brokered RFC 8693 token exchange
// (muster as a shared token broker for external clients).
type TokenExchangeBrokerConfig struct {
	// ClientAudiences maps an authenticated confidential broker client ID to
	// the audiences it may request. Requests for audiences outside the
	// client's list are rejected with invalid_target. Maps to mcp-oauth's
	// Config.TokenExchangeClientAudiences.
	ClientAudiences map[string][]string `yaml:"clientAudiences,omitempty"`

	// Targets maps an RFC 8693 audience name (e.g. a management cluster name)
	// to the downstream Dex exchange target.
	Targets map[string]BrokerTargetConfig `yaml:"targets,omitempty"`

	// AllowPrivateIP allows downstream token endpoints to resolve to private
	// or loopback IP addresses. WARNING: reduces SSRF protection; only enable
	// for internal/VPN deployments where the target Dex is reachable via a
	// private address.
	AllowPrivateIP bool `yaml:"allowPrivateIP,omitempty"`

	// DefaultSecretNamespace is the namespace used for target credential
	// secret refs that do not set an explicit namespace. Populated from the
	// muster namespace by the serve command; not user-facing config.
	DefaultSecretNamespace string `yaml:"-"`
}

// Enabled reports whether brokered token exchange is configured.
func (c TokenExchangeBrokerConfig) Enabled() bool {
	return len(c.Targets) > 0
}

// BrokerTargetConfig describes one downstream Dex target of the token broker.
type BrokerTargetConfig struct {
	// DexTokenEndpoint is the downstream Dex token endpoint URL (HTTPS).
	// Example: https://dex.cluster-b.example.com/token
	DexTokenEndpoint string `yaml:"dexTokenEndpoint"`

	// ExpectedIssuer is the expected iss claim of the exchanged token. If
	// empty, it is derived from DexTokenEndpoint (strip /token suffix).
	ExpectedIssuer string `yaml:"expectedIssuer,omitempty"`

	// ConnectorID is the downstream Dex OIDC connector that trusts the
	// subject token's issuer.
	ConnectorID string `yaml:"connectorId"`

	// Scopes is the space-separated scope set requested downstream. Defaults
	// to "openid profile email groups". For Kubernetes-bound audiences this
	// must include the Dex cross-client scope for the apiserver's client,
	// e.g. "audience:server:client_id:dex-k8s-authenticator" — without it
	// the exchanged token's aud is the exchange client only, which mcp-*
	// servers accept but kube-apiserver rejects.
	Scopes string `yaml:"scopes,omitempty"`

	// ClientCredentialsSecretRef references the Kubernetes Secret holding the
	// downstream exchange client credentials (same secrets the per-MCPServer
	// tokenExchange config uses; no duplication).
	ClientCredentialsSecretRef *BrokerSecretRefConfig `yaml:"clientCredentialsSecretRef,omitempty"`
}

// BrokerSecretRefConfig references a Kubernetes Secret with OAuth client
// credentials. Mirrors api.ClientCredentialsSecretRef (kept separate to avoid
// an import cycle between config and api).
type BrokerSecretRefConfig struct {
	// Name is the secret name. Required.
	Name string `yaml:"name"`
	// Namespace defaults to the broker's DefaultSecretNamespace (the muster
	// namespace) when empty.
	Namespace string `yaml:"namespace,omitempty"`
	// ClientIDKey defaults to "client-id".
	ClientIDKey string `yaml:"clientIdKey,omitempty"`
	// ClientSecretKey defaults to "client-secret".
	ClientSecretKey string `yaml:"clientSecretKey,omitempty"`
}

// TrustedIssuerConfig mirrors server.TrustedIssuer.
type TrustedIssuerConfig struct {
	// Issuer is the expected iss claim value.
	Issuer string `yaml:"issuer,omitempty"`
	// JwksURL is the JWKS endpoint. Independent of Issuer.
	JwksURL string `yaml:"jwksUrl,omitempty"`
	// AllowedAudiences lists accepted aud values. Empty accepts any audience.
	AllowedAudiences []string `yaml:"allowedAudiences,omitempty"`
	// AllowedScopes caps scopes for tokens from this issuer. Nil means no restriction.
	AllowedScopes []string `yaml:"allowedScopes,omitempty"`
	// AllowedClaims requires each named claim to match its pattern. Keys are JWT
	// claim names; values are exact strings or globs ('*' spans any chars incl. '/',
	// '?' one char). Absent or non-string claims are rejected. Empty means no
	// restriction. Use to express K8s SA trust via sub, e.g.
	// "system:serviceaccount:<namespace>:*".
	AllowedClaims map[string]string `yaml:"allowedClaims,omitempty"`
	// AllowPrivateIPJWKS allows the JwksURL to resolve to a private or loopback
	// address. Required for in-cluster Kubernetes SA trust where the JWKS endpoint
	// is https://kubernetes.default.svc/openid/v1/jwks. Emits a startup warning
	// when set (mcp-oauth dev-override flag). Default: false.
	AllowPrivateIPJWKS bool `yaml:"allowPrivateIPJWKS,omitempty"`
	// AcceptedTypHeaders lists the JWT typ header values accepted for Bearer
	// tokens from this issuer. Empty keeps the RFC 9068 default ("at+jwt").
	// Kubernetes ServiceAccount tokens carry no typ header; use [""] to
	// accept them.
	AcceptedTypHeaders []string `yaml:"acceptedTypHeaders,omitempty"`
}

// DexConfig holds configuration for the Dex OIDC provider.
type DexConfig struct {
	// IssuerURL is the Dex OIDC issuer URL.
	IssuerURL string `yaml:"issuerUrl,omitempty"`

	// ClientID is the Dex OAuth client ID.
	ClientID string `yaml:"clientId,omitempty"`

	// ClientSecret is the Dex OAuth client secret.
	// For production, use ClientSecretFile instead to avoid secrets in config files.
	ClientSecret string `yaml:"clientSecret,omitempty"`

	// ClientSecretFile is the path to a file containing the Dex OAuth client secret.
	// This is the recommended way to provide secrets in production deployments.
	// The file should contain only the secret value (no newlines at the end).
	ClientSecretFile string `yaml:"clientSecretFile,omitempty"`

	// ConnectorID is the optional Dex connector ID to bypass connector selection.
	ConnectorID string `yaml:"connectorId,omitempty"`

	// AllowPrivateIPOIDC allows the Dex issuer URL to resolve to a private or
	// loopback IP address during OIDC discovery. Required when Dex is fronted by
	// an internal-only load balancer (e.g. Azure internal LB, air-gapped clusters)
	// where the public hostname resolves to an RFC 1918 address.
	// Emits a CWE-918 startup warning when set.
	AllowPrivateIPOIDC bool `yaml:"allowPrivateIPOIDC,omitempty"`
}

// GoogleConfig holds configuration for the Google OAuth provider.
type GoogleConfig struct {
	// ClientID is the Google OAuth client ID.
	ClientID string `yaml:"clientId,omitempty"`

	// ClientSecret is the Google OAuth client secret.
	// For production, use ClientSecretFile instead to avoid secrets in config files.
	ClientSecret string `yaml:"clientSecret,omitempty"`

	// ClientSecretFile is the path to a file containing the Google OAuth client secret.
	// This is the recommended way to provide secrets in production deployments.
	ClientSecretFile string `yaml:"clientSecretFile,omitempty"`
}

// OAuthStorageConfig holds configuration for OAuth token storage backend.
type OAuthStorageConfig struct {
	// Type is the storage backend type: "memory" or "valkey" (default: "memory").
	Type string `yaml:"type,omitempty"`

	// Valkey configuration (used when Type is "valkey").
	Valkey ValkeyConfig `yaml:"valkey,omitempty"`
}

// DefaultValkeyKeyPrefix is the default prefix prepended to every Valkey key
// used by muster's backed stores when the operator does not override it via
// ValkeyConfig.KeyPrefix. Centralised here so the OAuth client/state stores
// and the aggregator session/capability stores agree on the namespace.
const DefaultValkeyKeyPrefix = "muster:"

// ValkeyConfig holds configuration for Valkey storage backend.
type ValkeyConfig struct {
	// URL is the Valkey server address (e.g., "valkey.namespace.svc:6379").
	URL string `yaml:"url,omitempty"`

	// Password is the optional password for Valkey authentication.
	// For production, use PasswordFile instead to avoid secrets in config files.
	Password string `yaml:"password,omitempty"`

	// PasswordFile is the path to a file containing the Valkey password.
	// This is the recommended way to provide secrets in production deployments.
	PasswordFile string `yaml:"passwordFile,omitempty"`

	// TLSEnabled enables TLS for Valkey connections.
	TLSEnabled bool `yaml:"tlsEnabled,omitempty"`

	// TLSServerName overrides the server name used for TLS certificate verification.
	// Use this when the Valkey server certificate CN/SAN differs from the connection address.
	TLSServerName string `yaml:"tlsServerName,omitempty"`

	// KeyPrefix is the prefix for all Valkey keys (default: "muster:").
	KeyPrefix string `yaml:"keyPrefix,omitempty"`

	// DB is the Valkey database number (default: 0).
	DB int `yaml:"db,omitempty"`
}
