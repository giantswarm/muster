package oauth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/giantswarm/mcp-oauth/providers/oidc"
)

// TokenExchanger performs RFC 8693 OAuth 2.0 Token Exchange for cross-cluster SSO.
// It enables users authenticated to muster (Cluster A) to access MCP servers on
// remote clusters (Cluster B) by exchanging their local token for a token valid
// on the remote cluster's Identity Provider.
//
// This is different from token forwarding (ForwardToken), which forwards muster's
// ID token directly. Token exchange is useful when:
//   - Remote clusters have separate Dex instances
//   - The remote Dex is configured with an OIDC connector pointing to muster's Dex
//   - You need a token issued by the remote cluster's IdP
//
// Thread-safe: Yes, the underlying TokenExchangeClient is thread-safe.
type TokenExchanger struct {
	client *oidc.TokenExchangeClient
	cache  *oidc.TokenExchangeCache
	logger *slog.Logger

	// allowPrivateIP allows token endpoints on private IP addresses.
	// This is useful for internal/VPN deployments.
	allowPrivateIP bool
}

// DefaultOIDCScopes is the default set of scopes requested for OIDC token exchange.
// These scopes provide identity (openid), user profile info (profile, email),
// and group membership (groups) for RBAC decisions.
const DefaultOIDCScopes = "openid profile email groups"

// TokenExchangerOptions configures the TokenExchanger.
type TokenExchangerOptions struct {
	// Logger for debug/info messages (nil uses default logger).
	Logger *slog.Logger

	// AllowPrivateIP allows token endpoints to resolve to private IP addresses.
	// WARNING: Reduces SSRF protection. Only enable for internal/VPN deployments.
	AllowPrivateIP bool

	// CacheMaxEntries is the maximum number of cached tokens (0 = default: 10000).
	CacheMaxEntries int

	// HTTPClient is the HTTP client to use for token exchange requests.
	// If nil, an appropriate client is created based on AllowPrivateIP setting.
	// Use this to configure custom TLS settings (e.g., for self-signed certs).
	HTTPClient *http.Client
}

// NewTokenExchangerWithOptions creates a new TokenExchanger with custom options.
func NewTokenExchangerWithOptions(opts TokenExchangerOptions) *TokenExchanger {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	client := oidc.NewTokenExchangeClientWithOptions(oidc.TokenExchangeClientOptions{
		Logger:         logger,
		AllowPrivateIP: opts.AllowPrivateIP,
		HTTPClient:     opts.HTTPClient,
	})

	maxEntries := opts.CacheMaxEntries
	if maxEntries <= 0 {
		maxEntries = oidc.DefaultCacheMaxEntries
	}
	cache := oidc.NewTokenExchangeCacheWithMaxEntries(maxEntries)

	if opts.AllowPrivateIP {
		logging.Warn("TokenExchange", "Token exchanger created with AllowPrivateIP=true, SSRF protection is reduced")
	}

	return &TokenExchanger{
		client:         client,
		cache:          cache,
		logger:         logger,
		allowPrivateIP: opts.AllowPrivateIP,
	}
}

// ExchangeRequest contains the parameters for a token exchange operation.
type ExchangeRequest struct {
	// Config is the token exchange configuration for the target cluster.
	// Uses the API type directly to avoid duplication (DRY principle).
	Config *api.TokenExchangeConfig

	// SubjectToken is the local token to exchange (ID token or access token).
	SubjectToken string

	// SubjectTokenType specifies whether SubjectToken is an ID token or access token.
	// Use oidc.TokenTypeIDToken or oidc.TokenTypeAccessToken.
	// Defaults to TokenTypeIDToken if not specified.
	SubjectTokenType string

	// UserID is extracted from the validated subject token's "sub" claim.
	// CRITICAL: This must come from validated JWT claims, not user input.
	// Used for cache key generation.
	UserID string
}

// ExchangeResult contains the result of a successful token exchange.
type ExchangeResult struct {
	// AccessToken is the exchanged token valid on the remote cluster.
	AccessToken string

	// IssuedTokenType is the type of the issued token.
	IssuedTokenType string

	// FromCache indicates whether the token was served from cache.
	FromCache bool
}

// validateExchangeRequest validates the exchange request and returns an error if invalid.
// This is used by both Exchange and ExchangeWithClient to ensure consistent validation.
func validateExchangeRequest(req *ExchangeRequest) error {
	if req == nil {
		return fmt.Errorf("exchange request is nil")
	}
	if req.Config == nil {
		return fmt.Errorf("token exchange config is nil")
	}
	if !req.Config.Enabled {
		return fmt.Errorf("token exchange is not enabled")
	}
	if req.SubjectToken == "" {
		return fmt.Errorf("subject token is required")
	}
	if req.Config.DexTokenEndpoint == "" {
		return fmt.Errorf("dex token endpoint is required")
	}
	// Security: Enforce HTTPS for token endpoints
	// This prevents token leakage over insecure connections and MITM attacks.
	// Note: The underlying mcp-oauth library also enforces HTTPS, so this is
	// a defense-in-depth check that provides a clearer error message.
	if !strings.HasPrefix(req.Config.DexTokenEndpoint, "https://") {
		return fmt.Errorf("dex token endpoint must use HTTPS (got: %s)", req.Config.DexTokenEndpoint)
	}
	// Security: Enforce HTTPS for expectedIssuer when explicitly set.
	// This is defense-in-depth: the CRD has schema validation, but we also
	// validate in code to protect against bypasses (direct API, config files,
	// older CRD versions without validation).
	if req.Config.ExpectedIssuer != "" && !strings.HasPrefix(req.Config.ExpectedIssuer, "https://") {
		return fmt.Errorf("expected issuer must use HTTPS (got: %s)", req.Config.ExpectedIssuer)
	}
	if req.Config.ConnectorID == "" {
		return fmt.Errorf("connector ID is required")
	}
	if req.UserID == "" {
		return fmt.Errorf("user ID is required for cache key generation")
	}
	return nil
}

// getExchangeDefaults returns the token type and scopes with defaults applied.
func getExchangeDefaults(req *ExchangeRequest) (tokenType, scopes string) {
	tokenType = req.SubjectTokenType
	if tokenType == "" {
		tokenType = oidc.TokenTypeIDToken
	}
	scopes = req.Config.Scopes
	if scopes == "" {
		scopes = DefaultOIDCScopes
	}
	return tokenType, scopes
}

// Exchange exchanges a local token for a token valid on a remote cluster.
// The token is cached to reduce the number of exchange requests.
//
// Args:
//   - ctx: Context for cancellation and timeouts
//   - req: Exchange request parameters
//
// Returns the exchanged token or an error if exchange fails.
func (e *TokenExchanger) Exchange(ctx context.Context, req *ExchangeRequest) (*ExchangeResult, error) {
	if err := validateExchangeRequest(req); err != nil {
		return nil, err
	}

	tokenType, scopes := getExchangeDefaults(req)

	// Check cache first
	cacheKey := oidc.GenerateCacheKey(req.Config.DexTokenEndpoint, req.Config.ConnectorID, req.UserID)
	if cached := e.cache.Get(cacheKey); cached != nil {
		logging.Debug("TokenExchange", "Cache hit for user=%s endpoint=%s",
			logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint)
		return &ExchangeResult{
			AccessToken:     cached.AccessToken,
			IssuedTokenType: cached.IssuedTokenType,
			FromCache:       true,
		}, nil
	}

	// Perform the exchange
	logging.Debug("TokenExchange", "Exchanging token for user=%s endpoint=%s connector=%s",
		logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint, req.Config.ConnectorID)

	// Build the token exchange request with client credentials if available
	exchangeReq := oidc.TokenExchangeRequest{
		TokenEndpoint:      req.Config.DexTokenEndpoint,
		SubjectToken:       req.SubjectToken,
		SubjectTokenType:   tokenType,
		ConnectorID:        req.Config.ConnectorID,
		Scope:              scopes,
		RequestedTokenType: oidc.TokenTypeAccessToken,
		ClientID:           req.Config.ClientID,
		ClientSecret:       req.Config.ClientSecret,
	}

	// Log whether client credentials are being used (without revealing them)
	if req.Config.ClientID != "" {
		logging.Debug("TokenExchange", "Using client credentials for token exchange (client_id=%s)",
			req.Config.ClientID)
	}

	resp, err := e.client.Exchange(ctx, exchangeReq)
	if err != nil {
		logging.Warn("TokenExchange", "Token exchange failed for user=%s endpoint=%s: %v",
			logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint, err)
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Validate the issuer claim of the exchanged token (defense-in-depth for proxied access)
	expectedIssuer := GetExpectedIssuer(req.Config)
	if expectedIssuer != "" {
		if err := validateTokenIssuer(resp.AccessToken, expectedIssuer); err != nil {
			logging.Warn("TokenExchange", "Issuer validation failed for user=%s endpoint=%s: %v",
				logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint, err)
			return nil, fmt.Errorf("issuer validation failed: %w", err)
		}
		logging.Debug("TokenExchange", "Issuer validation passed for user=%s (expected=%s)",
			logging.TruncateSessionID(req.UserID), expectedIssuer)
	}

	// Cache the result
	if resp.ExpiresIn > 0 {
		e.cache.Set(cacheKey, resp.AccessToken, resp.IssuedTokenType, resp.ExpiresIn)
		logging.Debug("TokenExchange", "Cached exchanged token for user=%s (expires in %ds)",
			logging.TruncateSessionID(req.UserID), resp.ExpiresIn)
	}

	logging.Info("TokenExchange", "Successfully exchanged token for user=%s endpoint=%s",
		logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint)

	return &ExchangeResult{
		AccessToken:     resp.AccessToken,
		IssuedTokenType: resp.IssuedTokenType,
		FromCache:       false,
	}, nil
}

// ExchangeWithClient exchanges a local token for a token valid on a remote cluster using
// a custom HTTP client. This is used when the token exchange endpoint is accessed via
// Teleport Application Access, which requires mutual TLS authentication.
//
// The httpClient parameter should be configured with the appropriate TLS certificates
// (e.g., Teleport Machine ID certificates). If nil, uses the default exchanger client.
//
// Args:
//   - ctx: Context for cancellation and timeouts
//   - req: Exchange request parameters
//   - httpClient: Custom HTTP client with Teleport TLS certificates (or nil for default)
//
// Returns the exchanged token or an error if exchange fails.
func (e *TokenExchanger) ExchangeWithClient(ctx context.Context, req *ExchangeRequest, httpClient *http.Client) (*ExchangeResult, error) {
	// If no custom client provided, use the default Exchange method
	if httpClient == nil {
		return e.Exchange(ctx, req)
	}

	// Validate request using shared validation logic (DRY)
	if err := validateExchangeRequest(req); err != nil {
		return nil, err
	}

	tokenType, scopes := getExchangeDefaults(req)

	// Check cache first (same cache key as normal exchange)
	cacheKey := oidc.GenerateCacheKey(req.Config.DexTokenEndpoint, req.Config.ConnectorID, req.UserID)
	if cached := e.cache.Get(cacheKey); cached != nil {
		logging.Debug("TokenExchange", "Cache hit for user=%s endpoint=%s (with custom client)",
			logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint)
		return &ExchangeResult{
			AccessToken:     cached.AccessToken,
			IssuedTokenType: cached.IssuedTokenType,
			FromCache:       true,
		}, nil
	}

	// Create a temporary TokenExchangeClient with the custom HTTP client
	// This is efficient because:
	// 1. Cache hit above means we rarely reach this code path
	// 2. Creating the client is cheap (just wraps the HTTP client)
	tempClient := oidc.NewTokenExchangeClientWithOptions(oidc.TokenExchangeClientOptions{
		Logger:         e.logger,
		AllowPrivateIP: e.allowPrivateIP,
		HTTPClient:     httpClient,
	})

	logging.Debug("TokenExchange", "Exchanging token for user=%s endpoint=%s connector=%s (with custom client)",
		logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint, req.Config.ConnectorID)

	// Build the token exchange request with client credentials if available
	exchangeReq := oidc.TokenExchangeRequest{
		TokenEndpoint:      req.Config.DexTokenEndpoint,
		SubjectToken:       req.SubjectToken,
		SubjectTokenType:   tokenType,
		ConnectorID:        req.Config.ConnectorID,
		Scope:              scopes,
		RequestedTokenType: oidc.TokenTypeAccessToken,
		ClientID:           req.Config.ClientID,
		ClientSecret:       req.Config.ClientSecret,
	}

	// Log whether client credentials are being used (without revealing them)
	if req.Config.ClientID != "" {
		logging.Debug("TokenExchange", "Using client credentials for token exchange (client_id=%s, with custom client)",
			req.Config.ClientID)
	}

	// Perform the exchange with the custom client
	resp, err := tempClient.Exchange(ctx, exchangeReq)
	if err != nil {
		logging.Warn("TokenExchange", "Token exchange failed for user=%s endpoint=%s (with custom client): %v",
			logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint, err)
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Validate the issuer claim of the exchanged token
	expectedIssuer := GetExpectedIssuer(req.Config)
	if expectedIssuer != "" {
		if err := validateTokenIssuer(resp.AccessToken, expectedIssuer); err != nil {
			logging.Warn("TokenExchange", "Issuer validation failed for user=%s endpoint=%s (with custom client): %v",
				logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint, err)
			return nil, fmt.Errorf("issuer validation failed: %w", err)
		}
		logging.Debug("TokenExchange", "Issuer validation passed for user=%s (expected=%s, with custom client)",
			logging.TruncateSessionID(req.UserID), expectedIssuer)
	}

	// Cache the result (same cache as normal exchange)
	if resp.ExpiresIn > 0 {
		e.cache.Set(cacheKey, resp.AccessToken, resp.IssuedTokenType, resp.ExpiresIn)
		logging.Debug("TokenExchange", "Cached exchanged token for user=%s (expires in %ds, with custom client)",
			logging.TruncateSessionID(req.UserID), resp.ExpiresIn)
	}

	logging.Info("TokenExchange", "Successfully exchanged token for user=%s endpoint=%s (with custom client)",
		logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint)

	return &ExchangeResult{
		AccessToken:     resp.AccessToken,
		IssuedTokenType: resp.IssuedTokenType,
		FromCache:       false,
	}, nil
}

// ClearCache removes a cached token for the given parameters.
// This is useful when a cached token is rejected by the remote server.
func (e *TokenExchanger) ClearCache(tokenEndpoint, connectorID, userID string) {
	cacheKey := oidc.GenerateCacheKey(tokenEndpoint, connectorID, userID)
	e.cache.Delete(cacheKey)
	logging.Debug("TokenExchange", "Cleared cache for user=%s endpoint=%s",
		logging.TruncateSessionID(userID), tokenEndpoint)
}

// ClearAllCache removes all cached tokens.
func (e *TokenExchanger) ClearAllCache() {
	e.cache.Clear()
	logging.Debug("TokenExchange", "Cleared all cached tokens")
}

// GetCacheStats returns statistics about the token exchange cache.
func (e *TokenExchanger) GetCacheStats() oidc.TokenExchangeCacheStats {
	return e.cache.GetStats()
}

// Cleanup removes expired tokens from the cache.
// This should be called periodically for long-running services.
func (e *TokenExchanger) Cleanup() int {
	removed := e.cache.Cleanup()
	if removed > 0 {
		logging.Debug("TokenExchange", "Cleaned up %d expired cached tokens", removed)
	}
	return removed
}

// GetExpectedIssuer returns the expected issuer URL for token validation.
// If ExpectedIssuer is explicitly set in the config, it is used directly.
// Otherwise, the issuer is derived from DexTokenEndpoint (backward compatible).
//
// This separation is important for proxied access scenarios:
//   - DexTokenEndpoint may go through a proxy (e.g., https://dex-cluster.proxy.example.com/token)
//   - ExpectedIssuer is the actual Dex issuer (e.g., https://dex.cluster-b.example.com)
func GetExpectedIssuer(config *api.TokenExchangeConfig) string {
	if config == nil {
		return ""
	}
	// Use explicit ExpectedIssuer if set
	if config.ExpectedIssuer != "" {
		return config.ExpectedIssuer
	}
	// Fall back to deriving from DexTokenEndpoint (backward compatible)
	return deriveIssuerFromTokenEndpoint(config.DexTokenEndpoint)
}

// deriveIssuerFromTokenEndpoint derives an issuer URL from a token endpoint URL.
// This is used for backward compatibility when ExpectedIssuer is not set.
// It strips the /token suffix and any trailing slashes.
//
// Examples:
//   - https://dex.example.com/token -> https://dex.example.com
//   - https://dex.example.com/dex/token -> https://dex.example.com/dex
func deriveIssuerFromTokenEndpoint(tokenEndpoint string) string {
	if tokenEndpoint == "" {
		return ""
	}
	// Parse the URL to handle it correctly
	u, err := url.Parse(tokenEndpoint)
	if err != nil {
		// If parsing fails, fall back to simple string manipulation
		issuer := strings.TrimSuffix(tokenEndpoint, "/token")
		issuer = strings.TrimSuffix(issuer, "/")
		return issuer
	}
	// Remove /token suffix from path
	u.Path = strings.TrimSuffix(u.Path, "/token")
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}

// validateTokenIssuer validates that the exchanged token has the expected issuer claim.
// This is important for security when access goes through a proxy, to ensure the token
// was actually issued by the expected Dex instance.
//
// Args:
//   - token: The JWT token to validate
//   - expectedIssuer: The expected issuer URL
//
// Returns nil if validation passes, or an error describing the mismatch.
// If the token is not a JWT (e.g., opaque token), validation is skipped.
func validateTokenIssuer(token, expectedIssuer string) error {
	if token == "" {
		return fmt.Errorf("token is empty")
	}
	if expectedIssuer == "" {
		// No expected issuer configured, skip validation
		return nil
	}

	// Check if the token looks like a JWT (has 3 dot-separated parts)
	// If not, skip issuer validation as it's an opaque token
	if !isJWTToken(token) {
		logging.Debug("TokenExchange", "Token is not a JWT, skipping issuer validation")
		return nil
	}

	// Extract the issuer claim from the token
	actualIssuer, err := extractIssuerFromToken(token)
	if err != nil {
		// If we can't extract the issuer, log and skip validation
		// This handles edge cases where the token looks like a JWT but isn't
		logging.Debug("TokenExchange", "Could not extract issuer from token: %v, skipping validation", err)
		return nil
	}

	// Normalize both URLs for comparison (remove trailing slashes)
	normalizedExpected := strings.TrimSuffix(expectedIssuer, "/")
	normalizedActual := strings.TrimSuffix(actualIssuer, "/")

	// Use constant-time comparison to prevent timing attacks.
	// While timing attacks on issuer validation are unlikely to be practical,
	// this provides consistency with other security-sensitive comparisons
	// in the codebase (e.g., audience matching in mcp-oauth).
	if subtle.ConstantTimeCompare([]byte(normalizedActual), []byte(normalizedExpected)) != 1 {
		// Provide actionable guidance in the error message
		return fmt.Errorf("token issuer mismatch: expected %q, got %q. "+
			"Hint: If accessing Dex via a proxy (e.g., Teleport), set 'expectedIssuer' to the actual Dex issuer URL (%q)",
			normalizedExpected, normalizedActual, normalizedActual)
	}

	return nil
}

// isJWTToken checks if a token appears to be a JWT (has 3 dot-separated parts).
// This is a quick heuristic check, not a full JWT validation.
func isJWTToken(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3
}

// extractIssuerFromToken extracts the issuer (iss claim) from a JWT token.
// This parses the token payload without cryptographic verification.
//
// SECURITY NOTE:
//   - The token is assumed to come from a trusted token exchange endpoint.
//   - Full signature verification is the responsibility of the downstream server.
//   - This is a defense-in-depth check for proxied access scenarios.
func extractIssuerFromToken(token string) (string, error) {
	// JWT format: header.payload.signature
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid JWT format: expected at least 2 parts")
	}

	// Decode the payload using RawURLEncoding (handles missing padding automatically)
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard base64 as fallback for non-standard implementations
		decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			return "", fmt.Errorf("failed to decode payload: %w", err)
		}
	}

	// Parse the claims
	var claims struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return "", fmt.Errorf("failed to parse claims: %w", err)
	}

	if claims.Iss == "" {
		return "", fmt.Errorf("iss claim not found in token")
	}

	return claims.Iss, nil
}
