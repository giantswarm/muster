package config

import "strings"

// MusterConfig is the top-level configuration structure for muster.
type MusterConfig struct {
	Aggregator AggregatorConfig `yaml:"aggregator"`
	Auth       AuthConfig       `yaml:"auth,omitempty"`       // Authentication settings for CLI
	Namespace  string           `yaml:"namespace,omitempty"`  // Namespace for MCPServer, ServiceClass and Workflow discovery
	Kubernetes bool             `yaml:"kubernetes,omitempty"` // Enable Kubernetes CRD mode (uses CRDs instead of filesystem)
}

// AuthConfig defines authentication behavior settings for the CLI.
// These settings control how the CLI handles authentication to the Muster aggregator.
type AuthConfig struct {
	// SilentRefresh controls whether silent re-authentication is enabled.
	// When true (the default), the CLI attempts OIDC prompt=none re-authentication
	// when a previous session exists, allowing seamless token refresh without
	// user interaction if the IdP session is still valid.
	// When false, the CLI always uses interactive authentication.
	SilentRefresh *bool `yaml:"silent_refresh,omitempty"`
}

// IsSilentRefreshEnabled returns whether silent refresh is enabled.
// Returns DefaultAuthSilentRefresh if SilentRefresh is nil.
func (c *AuthConfig) IsSilentRefreshEnabled() bool {
	if c.SilentRefresh == nil {
		return DefaultAuthSilentRefresh
	}
	return *c.SilentRefresh
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

	// OAuth Proxy configuration for remote MCP server authentication (client role)
	OAuth OAuthConfig `yaml:"oauth,omitempty"`

	// OAuthServer configuration for protecting the Muster Server itself (resource server role)
	OAuthServer OAuthServerConfig `yaml:"oauthServer,omitempty"`
}

// OAuthConfig defines the OAuth proxy configuration for remote MCP server authentication.
// When enabled, the Muster Server acts as an OAuth client proxy, handling authentication
// flows on behalf of users without exposing tokens to the Muster Agent.
type OAuthConfig struct {
	// PublicURL is the publicly accessible URL of the Muster Server.
	// This is used to construct OAuth callback URLs (e.g., https://muster.example.com).
	// Required when OAuth is enabled.
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

	// CIMDPath is the path for serving the Client ID Metadata Document (default: "/.well-known/oauth-client.json").
	// Muster will serve the CIMD at this path when OAuth is enabled and PublicURL is set.
	CIMDPath string `yaml:"cimdPath,omitempty"`

	// CIMDScopes is the OAuth scopes to advertise in the self-hosted CIMD.
	// This determines what API scopes downstream MCP servers can use when muster
	// forwards tokens via SSO. Default: "openid profile email offline_access".
	// Operators can add additional scopes (e.g., Google API scopes) as needed.
	// Format: space-separated list of scope strings.
	CIMDScopes string `yaml:"cimdScopes,omitempty"`

	// CAFile is the path to a CA certificate file for verifying TLS connections to OAuth servers.
	// This is useful when connecting to OAuth servers with self-signed certificates.
	CAFile string `yaml:"caFile,omitempty"`

	// Enabled controls whether OAuth proxy functionality is active.
	// When false, remote MCP servers requiring auth will return errors.
	Enabled bool `yaml:"enabled,omitempty"`
}

// GetEffectiveClientID returns the effective OAuth client ID.
// If ClientID is explicitly set, it is returned as-is.
// If ClientID is empty but PublicURL is set, returns the self-hosted CIMD URL.
// Otherwise, returns empty string (OAuth proxy requires publicUrl to function).
func (c *OAuthConfig) GetEffectiveClientID() string {
	if c.ClientID != "" {
		return c.ClientID
	}

	// Auto-derive from PublicURL if set
	if c.PublicURL != "" {
		cimdPath := c.CIMDPath
		if cimdPath == "" {
			cimdPath = DefaultOAuthCIMDPath
		}
		return strings.TrimSuffix(c.PublicURL, "/") + cimdPath
	}

	// No publicUrl means OAuth proxy won't work - return empty
	return ""
}

// ShouldServeCIMD returns true if muster should serve its own CIMD.
// This is the case when OAuth is enabled, PublicURL is set, and ClientID
// is either empty or matches the auto-derived CIMD URL.
func (c *OAuthConfig) ShouldServeCIMD() bool {
	if !c.Enabled || c.PublicURL == "" {
		return false
	}

	// If ClientID is not set, we should serve our own CIMD
	if c.ClientID == "" {
		return true
	}

	// If ClientID matches what we would auto-generate, serve our own CIMD
	cimdPath := c.CIMDPath
	if cimdPath == "" {
		cimdPath = DefaultOAuthCIMDPath
	}
	autoClientID := strings.TrimSuffix(c.PublicURL, "/") + cimdPath
	return c.ClientID == autoClientID
}

// GetCIMDPath returns the path for serving the CIMD.
func (c *OAuthConfig) GetCIMDPath() string {
	if c.CIMDPath != "" {
		return c.CIMDPath
	}
	return DefaultOAuthCIMDPath
}

// GetCIMDScopes returns the OAuth scopes to advertise in the CIMD.
// If not set, returns the default scopes: "openid profile email offline_access".
func (c *OAuthConfig) GetCIMDScopes() string {
	if c.CIMDScopes != "" {
		return c.CIMDScopes
	}
	return DefaultOAuthCIMDScopes
}

// GetRedirectURI returns the full redirect URI for OAuth proxy callbacks.
// This is where remote MCP server IdPs will redirect after authentication.
func (c *OAuthConfig) GetRedirectURI() string {
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

	// EnableCIMD enables Client ID Metadata Documents per MCP 2025-11-25 spec.
	// Default: true
	EnableCIMD bool `yaml:"enableCIMD,omitempty"`

	// AllowLocalhostRedirectURIs allows http://localhost and http://127.0.0.1 redirect URIs.
	// Required for native apps (like muster agent) per RFC 8252 Section 7.3.
	// Default: true (native app support enabled by default)
	AllowLocalhostRedirectURIs bool `yaml:"allowLocalhostRedirectURIs,omitempty"`

	// AllowedOrigins is a comma-separated list of allowed CORS origins.
	AllowedOrigins string `yaml:"allowedOrigins,omitempty"`

	// EnableHSTS enables HSTS header (for reverse proxy scenarios).
	EnableHSTS bool `yaml:"enableHSTS,omitempty"`

	// TLSCertFile is the path to the TLS certificate file (PEM format).
	// If both TLSCertFile and TLSKeyFile are provided, the server will use HTTPS.
	TLSCertFile string `yaml:"tlsCertFile,omitempty"`

	// TLSKeyFile is the path to the TLS private key file (PEM format).
	TLSKeyFile string `yaml:"tlsKeyFile,omitempty"`
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

	// CAFile is the path to a CA certificate file for Dex TLS verification.
	// Use this when Dex uses a private/internal CA.
	CAFile string `yaml:"caFile,omitempty"`
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

	// KeyPrefix is the prefix for all Valkey keys (default: "muster:").
	KeyPrefix string `yaml:"keyPrefix,omitempty"`

	// DB is the Valkey database number (default: 0).
	DB int `yaml:"db,omitempty"`
}
