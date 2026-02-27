package oauth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// DefaultExpiryMargin is the default margin when checking token expiry.
// This accounts for clock skew and network latency.
const DefaultExpiryMargin = 30 * time.Second

// DefaultTokenStorageDir is the default directory for storing OAuth tokens,
// relative to the user's home directory. This follows XDG conventions.
// This constant is shared across all OAuth implementations for consistency.
const DefaultTokenStorageDir = ".config/muster/tokens"

// DefaultTokenDir returns the absolute path to the default token storage
// directory (~/.config/muster/tokens). It does not create the directory;
// callers that need it to exist should call os.MkdirAll themselves.
func DefaultTokenDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, DefaultTokenStorageDir), nil
}

// DefaultSessionDuration is the expected maximum session duration before
// re-authentication is required. This should match the server-side
// RefreshTokenTTL and is used by the CLI to estimate session expiry from
// the stored token's CreatedAt timestamp.
// Aligned with Dex's absoluteLifetime (720h = 30 days).
const DefaultSessionDuration = 30 * 24 * time.Hour

// NormalizeServerURL normalizes a server URL by stripping transport-specific
// path suffixes (/mcp, /sse) and trailing slashes to get the base server URL.
// This ensures consistent token storage and OAuth metadata discovery regardless
// of which endpoint path is used when connecting.
//
// This function is shared across all OAuth implementations for consistency.
func NormalizeServerURL(serverURL string) string {
	serverURL = strings.TrimSuffix(serverURL, "/")
	serverURL = strings.TrimSuffix(serverURL, "/mcp")
	serverURL = strings.TrimSuffix(serverURL, "/sse")
	return serverURL
}

// IDTokenClaims holds the identity claims extracted from JWT ID tokens.
// This is used to display user identity information (subject, email) from
// OAuth authentication without requiring full JWT validation.
type IDTokenClaims struct {
	// Subject is the unique user identifier (sub claim).
	Subject string `json:"sub"`
	// Email is the user's email address (email claim).
	Email string `json:"email"`
}

// Token represents an OAuth access token with associated metadata.
type Token struct {
	// AccessToken is the bearer token used for authorization.
	AccessToken string `json:"access_token"`

	// TokenType is typically "Bearer".
	TokenType string `json:"token_type,omitempty"`

	// RefreshToken is used to obtain new access tokens (optional).
	RefreshToken string `json:"refresh_token,omitempty"`

	// ExpiresIn is the token lifetime in seconds (from token response).
	ExpiresIn int `json:"expires_in,omitempty"`

	// ExpiresAt is the calculated expiration timestamp.
	ExpiresAt time.Time `json:"expires_at,omitempty"`

	// Scope is the granted scope(s), space-separated.
	Scope string `json:"scope,omitempty"`

	// Issuer is the token issuer (Identity Provider URL).
	Issuer string `json:"issuer,omitempty"`

	// IDToken is the OIDC ID token (if available).
	IDToken string `json:"id_token,omitempty"`
}

// IsExpired checks if the token has expired.
// Returns true if the token is expired or will expire within the given margin.
func (t *Token) IsExpired() bool {
	return t.IsExpiredWithMargin(DefaultExpiryMargin)
}

// IsExpiredWithMargin checks if the token has expired or will expire within the margin.
func (t *Token) IsExpiredWithMargin(margin time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false // Tokens without expiration don't expire
	}
	return time.Now().Add(margin).After(t.ExpiresAt)
}

// SetExpiresAtFromExpiresIn calculates and sets ExpiresAt from ExpiresIn.
func (t *Token) SetExpiresAtFromExpiresIn() {
	if t.ExpiresIn > 0 && t.ExpiresAt.IsZero() {
		t.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
}

// Scopes returns the scope as a slice of individual scopes.
func (t *Token) Scopes() []string {
	if t.Scope == "" {
		return nil
	}
	return strings.Fields(t.Scope)
}

// ToOAuth2Token converts the Token to an oauth2.Token for compatibility with golang.org/x/oauth2.
func (t *Token) ToOAuth2Token() *oauth2.Token {
	token := &oauth2.Token{
		AccessToken:  t.AccessToken,
		TokenType:    t.TokenType,
		RefreshToken: t.RefreshToken,
		Expiry:       t.ExpiresAt,
	}

	// Add ID token to extra data if available
	if t.IDToken != "" {
		token = token.WithExtra(map[string]interface{}{
			"id_token": t.IDToken,
		})
	}

	return token
}

// Metadata represents OAuth 2.0 Authorization Server Metadata as defined in RFC 8414.
type Metadata struct {
	// Issuer is the authorization server's issuer identifier.
	Issuer string `json:"issuer"`

	// AuthorizationEndpoint is the URL of the authorization endpoint.
	AuthorizationEndpoint string `json:"authorization_endpoint"`

	// TokenEndpoint is the URL of the token endpoint.
	TokenEndpoint string `json:"token_endpoint"`

	// UserinfoEndpoint is the URL of the userinfo endpoint (OIDC).
	UserinfoEndpoint string `json:"userinfo_endpoint,omitempty"`

	// JwksURI is the URL of the JSON Web Key Set.
	JwksURI string `json:"jwks_uri,omitempty"`

	// RegistrationEndpoint is the URL for dynamic client registration.
	RegistrationEndpoint string `json:"registration_endpoint,omitempty"`

	// ScopesSupported lists the OAuth 2.0 scope values supported.
	ScopesSupported []string `json:"scopes_supported,omitempty"`

	// ResponseTypesSupported lists the response_type values supported.
	ResponseTypesSupported []string `json:"response_types_supported,omitempty"`

	// GrantTypesSupported lists the grant types supported.
	GrantTypesSupported []string `json:"grant_types_supported,omitempty"`

	// TokenEndpointAuthMethodsSupported lists the client authentication methods.
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`

	// CodeChallengeMethodsSupported lists the PKCE code challenge methods.
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
}

// SupportsPKCE returns true if the server supports S256 PKCE.
func (m *Metadata) SupportsPKCE() bool {
	for _, method := range m.CodeChallengeMethodsSupported {
		if method == "S256" {
			return true
		}
	}
	// If not specified, assume S256 is supported (OAuth 2.1 requirement)
	return len(m.CodeChallengeMethodsSupported) == 0
}

// AuthChallenge represents parsed information from a WWW-Authenticate header.
// This contains the OAuth server metadata needed to initiate the auth flow.
type AuthChallenge struct {
	// Scheme is the authentication scheme (typically "Bearer" for OAuth 2.0).
	Scheme string

	// Realm is the protection realm (often the authorization server name or URL).
	Realm string

	// Issuer is the OAuth/OIDC issuer URL.
	// This may be derived from the Realm if it's a URL.
	Issuer string

	// ResourceMetadataURL is the URL to the protected resource metadata.
	// This follows RFC 9728 for OAuth 2.0 Protected Resource Metadata.
	ResourceMetadataURL string

	// Scope is the space-separated list of required OAuth scopes.
	Scope string

	// Error is the error code from the WWW-Authenticate header (if any).
	Error string

	// ErrorDescription is a human-readable error description (if any).
	ErrorDescription string
}

// IsOAuthChallenge returns true if this represents an OAuth authentication challenge.
func (c *AuthChallenge) IsOAuthChallenge() bool {
	if c == nil {
		return false
	}
	// Must be Bearer scheme
	if !strings.EqualFold(c.Scheme, "Bearer") {
		return false
	}
	// Should have a realm (issuer) or resource_metadata URL
	return c.Realm != "" || c.ResourceMetadataURL != "" || c.Issuer != ""
}

// GetIssuer returns the OAuth issuer URL.
// It prefers the explicit Issuer field, falls back to Realm if it's a URL.
func (c *AuthChallenge) GetIssuer() string {
	if c == nil {
		return ""
	}
	if c.Issuer != "" {
		return c.Issuer
	}
	// The realm is often the issuer URL
	if strings.HasPrefix(c.Realm, "http://") || strings.HasPrefix(c.Realm, "https://") {
		return c.Realm
	}
	return ""
}

// PKCEChallenge represents a PKCE (Proof Key for Code Exchange) challenge.
// PKCE is required for OAuth 2.1 public clients to prevent authorization code interception.
type PKCEChallenge struct {
	// CodeVerifier is the cryptographically random string (32-96 bytes, base64url-encoded).
	// This is kept secret and never transmitted to the authorization server.
	CodeVerifier string

	// CodeChallenge is the SHA256 hash of the verifier (base64url-encoded).
	// This is sent in the authorization request.
	CodeChallenge string

	// CodeChallengeMethod is always "S256" for security (plain is not allowed in OAuth 2.1).
	CodeChallengeMethod string
}

// ClientMetadata represents OAuth 2.0 Client Metadata as defined in RFC 7591.
// Used for Client ID Metadata Documents (CIMD) in MCP OAuth.
type ClientMetadata struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
	PolicyURI               string   `json:"policy_uri,omitempty"`
	TermsOfServiceURI       string   `json:"tos_uri,omitempty"`
	SoftwareID              string   `json:"software_id,omitempty"`
	SoftwareVersion         string   `json:"software_version,omitempty"`
}

// Server status constants for use in ServerAuthStatus.Status field.
// These are the primary status values visible to users and AI assistants.
const (
	// ServerStatusConnected indicates the server is connected and operational.
	// Tools from this server are available for use.
	ServerStatusConnected = "connected"

	// ServerStatusAuthRequired indicates the server requires OAuth authentication.
	// This is an important status that requires user action: run 'muster auth login --server <name>'.
	// The server is reachable but needs authentication before tools become available.
	//
	// This status is more informative than a generic "Disconnected" because it tells
	// users exactly what action is needed to restore connectivity.
	ServerStatusAuthRequired = "auth_required"

	// ServerStatusDisconnected indicates the server is disconnected.
	// The connection was previously established but is no longer active.
	ServerStatusDisconnected = "disconnected"

	// ServerStatusError indicates the server encountered an error.
	// Check the error field for details about what went wrong.
	ServerStatusError = "error"

	// ServerStatusUnreachable indicates the server endpoint cannot be reached.
	// This is distinct from auth_required - unreachable means network/connectivity failure,
	// not an authentication issue. Users should not be prompted to authenticate
	// for unreachable servers.
	//
	// Related constants:
	// - api.StateUnreachable (internal/api/service.go)
	// - aggregator.StatusUnreachable (internal/aggregator/types.go)
	ServerStatusUnreachable = "unreachable"

	// ServerStatusFailed indicates a session-level failure (e.g., connection dropped,
	// unexpected error during communication). This is distinct from infrastructure
	// failures (tracked in MCPServer Phase) and auth failures (tracked in AuthStatus).
	ServerStatusFailed = "failed"
)

// Display constants for user-facing output.
// These are formatted strings suitable for CLI prompts and status displays.
const (
	// DisplayAuthRequired is the formatted string shown in CLI prompts when servers require authentication.
	// This is displayed prominently in uppercase because it requires user action (running 'auth login').
	// Example prompt: "muster staging [AUTH REQUIRED] > "
	DisplayAuthRequired = "[AUTH REQUIRED]"
)

// AuthStatusResponse is the structured response from the auth://status MCP resource.
// It provides the AI with complete information about which servers need authentication.
// This type is shared between the aggregator (producer) and agent (consumer).
type AuthStatusResponse struct {
	Servers []ServerAuthStatus `json:"servers"`
}

// ServerAuthStatus represents the authentication status of a single MCP server.
// The Issuer field enables SSO detection - servers with the same issuer can share auth.
//
// SSO in muster has two mechanisms:
//   - Token Forwarding: When TokenForwardingEnabled is true, muster forwards its own
//     ID token to the downstream server (requires forwardToken: true in MCPServer config).
//   - Token Exchange: When TokenExchangeEnabled is true, muster exchanges its token
//     for one valid on the remote cluster's IdP (for cross-cluster SSO).
type ServerAuthStatus struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // "connected", "auth_required", "disconnected", "error"
	Issuer   string `json:"issuer,omitempty"`
	Scope    string `json:"scope,omitempty"`
	AuthTool string `json:"auth_tool,omitempty"` // Always "core_auth_login" per ADR-008
	Error    string `json:"error,omitempty"`

	// TokenForwardingEnabled indicates this server uses SSO via ID token forwarding.
	// When true, muster forwards its own ID token (from muster's OAuth server protection)
	// to this downstream server, rather than requiring a separate OAuth flow.
	TokenForwardingEnabled bool `json:"token_forwarding_enabled,omitempty"`

	// TokenExchangeEnabled indicates this server uses SSO via RFC 8693 Token Exchange.
	// When true, muster exchanges its local token for one valid on the remote cluster's
	// Identity Provider (e.g., Dex). This enables cross-cluster SSO when clusters have
	// separate Dex instances. Token exchange takes precedence over token forwarding.
	TokenExchangeEnabled bool `json:"token_exchange_enabled,omitempty"`

	// SSOAttemptFailed indicates that SSO authentication was attempted but failed.
	// This occurs when token forwarding is enabled but the downstream server
	// rejected the forwarded token (e.g., audience mismatch, token expired).
	// When true, the status will be "auth_required" and users should check
	// server trust configuration.
	SSOAttemptFailed bool `json:"sso_attempt_failed,omitempty"`
}

// AuthRequiredInfo contains information about a server requiring authentication.
// This is a simplified view used by the agent to build human-readable notifications.
//
// Per ADR-008, AuthTool is always "core_auth_login" - callers can use this tool
// with the Server field as the argument to authenticate.
type AuthRequiredInfo struct {
	Server   string `json:"server"`
	Issuer   string `json:"issuer"`
	Scope    string `json:"scope,omitempty"`
	AuthTool string `json:"auth_tool"` // Always "core_auth_login" per ADR-008
}
