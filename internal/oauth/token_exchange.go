package oauth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"muster/internal/api"
	"muster/pkg/logging"

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

// NewTokenExchanger creates a new TokenExchanger with default options.
func NewTokenExchanger() *TokenExchanger {
	return NewTokenExchangerWithOptions(TokenExchangerOptions{})
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

// Exchange exchanges a local token for a token valid on a remote cluster.
// The token is cached to reduce the number of exchange requests.
//
// Args:
//   - ctx: Context for cancellation and timeouts
//   - req: Exchange request parameters
//
// Returns the exchanged token or an error if exchange fails.
func (e *TokenExchanger) Exchange(ctx context.Context, req *ExchangeRequest) (*ExchangeResult, error) {
	if req == nil {
		return nil, fmt.Errorf("exchange request is nil")
	}
	if req.Config == nil {
		return nil, fmt.Errorf("token exchange config is nil")
	}
	if !req.Config.Enabled {
		return nil, fmt.Errorf("token exchange is not enabled")
	}
	if req.SubjectToken == "" {
		return nil, fmt.Errorf("subject token is required")
	}
	if req.Config.DexTokenEndpoint == "" {
		return nil, fmt.Errorf("dex token endpoint is required")
	}
	// Security: Enforce HTTPS for token endpoints
	// This prevents token leakage over insecure connections and MITM attacks.
	// Note: The underlying mcp-oauth library also enforces HTTPS, so this is
	// a defense-in-depth check that provides a clearer error message.
	if !strings.HasPrefix(req.Config.DexTokenEndpoint, "https://") {
		return nil, fmt.Errorf("dex token endpoint must use HTTPS (got: %s)", req.Config.DexTokenEndpoint)
	}
	if req.Config.ConnectorID == "" {
		return nil, fmt.Errorf("connector ID is required")
	}
	if req.UserID == "" {
		return nil, fmt.Errorf("user ID is required for cache key generation")
	}

	// Default token type to ID token
	tokenType := req.SubjectTokenType
	if tokenType == "" {
		tokenType = oidc.TokenTypeIDToken
	}

	// Default scopes if not specified
	scopes := req.Config.Scopes
	if scopes == "" {
		scopes = DefaultOIDCScopes
	}

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

	resp, err := e.client.Exchange(ctx, oidc.TokenExchangeRequest{
		TokenEndpoint:      req.Config.DexTokenEndpoint,
		SubjectToken:       req.SubjectToken,
		SubjectTokenType:   tokenType,
		ConnectorID:        req.Config.ConnectorID,
		Scope:              scopes,
		RequestedTokenType: oidc.TokenTypeAccessToken,
	})
	if err != nil {
		logging.Warn("TokenExchange", "Token exchange failed for user=%s endpoint=%s: %v",
			logging.TruncateSessionID(req.UserID), req.Config.DexTokenEndpoint, err)
		return nil, fmt.Errorf("token exchange failed: %w", err)
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
