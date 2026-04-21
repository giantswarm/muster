package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"

	oauth "github.com/giantswarm/mcp-oauth"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/providers/dex"
	"github.com/giantswarm/mcp-oauth/providers/google"
	"github.com/giantswarm/mcp-oauth/security"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/giantswarm/mcp-oauth/storage/valkey"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

const (
	// OAuthProviderDex is the Dex OIDC provider type.
	OAuthProviderDex = "dex"
	// OAuthProviderGoogle is the Google OAuth provider type.
	OAuthProviderGoogle = "google"

	// DefaultAccessTokenTTL is the configured TTL for access tokens (30 minutes).
	// This is intentionally set to match the Dex idTokens expiry (30m) so that
	// capTokenExpiry in mcp-oauth doesn't need to cap it further. If Dex's
	// idTokens expiry is shorter than this value, capTokenExpiry will
	// automatically reduce the effective TTL to match the provider's token lifetime.
	DefaultAccessTokenTTL = 30 * time.Minute

	// DefaultRefreshTokenTTL is the server-side TTL for refresh tokens.
	// Derived from pkgoauth.DefaultSessionDuration to keep server and CLI in sync.
	// Aligned with Dex's absoluteLifetime (720h = 30 days). Note: muster uses a
	// rolling TTL (reset on each rotation), while Dex's absoluteLifetime is
	// measured from original issuance and does NOT reset.
	DefaultRefreshTokenTTL = pkgoauth.DefaultSessionDuration

	// DefaultIPRateLimit is the default rate limit for requests per IP (requests/second).
	// In Kubernetes deployments, traffic may arrive from multiple distinct IPs or be
	// NATed through an ingress, so per-IP limits should be generous enough to avoid
	// false positives while still protecting against abuse.
	DefaultIPRateLimit = 50
	// DefaultIPBurst is the default burst size for IP rate limiting.
	DefaultIPBurst = 100

	// DefaultUserRateLimit is the default rate limit for authenticated users (requests/second).
	DefaultUserRateLimit = 100
	// DefaultUserBurst is the default burst size for authenticated user rate limiting.
	DefaultUserBurst = 200

	// DefaultMaxClientsPerIP is the default maximum number of clients per IP address.
	DefaultMaxClientsPerIP = 10

	// DefaultReadHeaderTimeout is the default timeout for reading request headers.
	DefaultReadHeaderTimeout = 10 * time.Second
	// DefaultWriteTimeout is the default timeout for writing responses.
	DefaultWriteTimeout = 120 * time.Second
	// DefaultIdleTimeout is the default idle timeout for keepalive connections.
	DefaultIdleTimeout = 120 * time.Second

	// logEmailPrefixLength is the number of characters to show when logging emails.
	// Only a prefix is logged for privacy reasons.
	logEmailPrefixLength = 8
)

var (
	// dexOAuthScopes are the OAuth scopes requested when using Dex OIDC provider.
	dexOAuthScopes = []string{"openid", "profile", "email", "groups", "offline_access"}

	// googleOAuthScopes are the OAuth scopes requested when using Google OAuth provider.
	googleOAuthScopes = []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
	}
)

// buildDexScopes constructs the OAuth scopes to request from Dex.
// It starts with the base dexOAuthScopes and appends cross-client audience scopes
// for any required audiences from MCPServers with forwardToken: true.
//
// For example, if requiredAudiences contains ["dex-k8s-authenticator"], the returned
// scopes will include "audience:server:client_id:dex-k8s-authenticator" which tells
// Dex to issue tokens that are also valid for the Kubernetes authenticator client.
//
// Uses dex.FormatAudienceScopes() from mcp-oauth for security-validated formatting.
// Invalid audiences are logged and skipped (does not fail startup).
func buildDexScopes(requiredAudiences []string) []string {
	scopes := make([]string, len(dexOAuthScopes))
	copy(scopes, dexOAuthScopes)

	if len(requiredAudiences) == 0 {
		return scopes
	}

	// Use mcp-oauth's security-validated audience scope formatting.
	// This prevents scope injection attacks from malformed audience strings.
	audienceScopes, err := dex.FormatAudienceScopes(requiredAudiences)
	if err != nil {
		// Log the error but don't fail startup - audiences come from MCPServer CRDs
		// which should already be validated, but we handle errors gracefully.
		logging.Warn("OAuth", "Failed to format audience scopes: %v (check MCPServer requiredAudiences values)", err)
		return scopes
	}

	return append(scopes, audienceScopes...)
}

// OAuthHTTPServer wraps an MCP HTTP handler with OAuth 2.1 authentication.
// It provides both OAuth server functionality (authorization, token issuance)
// and resource server protection (token validation middleware).
type OAuthHTTPServer struct {
	config          config.OAuthServerConfig
	oauthServer     *oauth.Server
	oauthHandler    *oauth.Handler
	tokenStore      storage.TokenStore
	httpServer      *http.Server
	mcpHandler      http.Handler
	debug           bool
	onAuthenticated func(ctx context.Context, sessionID string)
}

// NewOAuthHTTPServer creates a new OAuth-enabled HTTP server that wraps
// the provided MCP handler with authentication protection.
func NewOAuthHTTPServer(cfg config.OAuthServerConfig, mcpHandler http.Handler, debug bool) (*OAuthHTTPServer, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("OAuth server is not enabled")
	}

	// Validate HTTPS requirement for OAuth 2.1 compliance
	if err := validateHTTPSRequirement(cfg.BaseURL); err != nil {
		return nil, err
	}

	oauthServer, tokenStore, err := createOAuthServer(cfg, debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth server: %w", err)
	}

	oauthHandler := oauth.NewHandler(oauthServer, oauthServer.Logger)

	server := &OAuthHTTPServer{
		config:       cfg,
		oauthServer:  oauthServer,
		oauthHandler: oauthHandler,
		tokenStore:   tokenStore,
		mcpHandler:   mcpHandler,
		debug:        debug,
	}

	return server, nil
}

// SetOnAuthenticated registers a callback that fires on every authenticated
// MCP request after the session ID has been extracted. The aggregator uses
// this to trigger on-demand SSO connections from the HTTP middleware rather
// than from individual MCP operations.
func (s *OAuthHTTPServer) SetOnAuthenticated(fn func(ctx context.Context, sessionID string)) {
	s.onAuthenticated = fn
}

// CreateMux creates an HTTP mux that routes to both OAuth and MCP handlers.
// The MCP endpoints are protected by the OAuth ValidateToken middleware.
func (s *OAuthHTTPServer) CreateMux() http.Handler {
	mux := http.NewServeMux()

	// Health check endpoint for Kubernetes probes (unauthenticated)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Setup OAuth 2.1 endpoints
	s.setupOAuthRoutes(mux)

	// Setup OAuth proxy endpoints (for downstream auth to remote MCP servers)
	s.setupOAuthProxyRoutes(mux)

	// Setup MCP endpoint with OAuth protection
	s.setupMCPRoutes(mux)

	return mux
}

// setupOAuthRoutes registers OAuth 2.1 endpoints on the mux.
func (s *OAuthHTTPServer) setupOAuthRoutes(mux *http.ServeMux) {
	// Protected Resource Metadata endpoint (RFC 9728)
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.oauthHandler.ServeProtectedResourceMetadata)

	// Authorization Server Metadata endpoint (RFC 8414)
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.oauthHandler.ServeAuthorizationServerMetadata)

	// Dynamic Client Registration endpoint (RFC 7591)
	mux.HandleFunc("/oauth/register", s.oauthHandler.ServeClientRegistration)

	// OAuth Authorization endpoint
	mux.HandleFunc("/oauth/authorize", s.oauthHandler.ServeAuthorization)

	// OAuth Token endpoint
	mux.HandleFunc("/oauth/token", s.oauthHandler.ServeToken)

	// OAuth Callback endpoint (from provider)
	mux.HandleFunc("/oauth/callback", s.oauthHandler.ServeCallback)

	// Token Revocation endpoint (RFC 7009)
	mux.HandleFunc("/oauth/revoke", s.oauthHandler.ServeTokenRevocation)

	// Token Introspection endpoint (RFC 7662)
	mux.HandleFunc("/oauth/introspect", s.oauthHandler.ServeTokenIntrospection)

	logging.Info("OAuth", "Registered OAuth 2.1 endpoints")
}

// setupOAuthProxyRoutes registers endpoints for the OAuth proxy (for downstream auth).
// This includes:
//   - The OAuth proxy callback for authenticating with remote MCP servers (at /oauth/proxy/callback)
//   - The self-hosted CIMD for remote servers to fetch our client metadata
//
// Note: The proxy callback path (/oauth/proxy/callback) is different from the OAuth server
// callback (/oauth/callback) to avoid route conflicts. They handle different OAuth flows:
//   - /oauth/callback: Cursor authenticating TO muster (OAuth server flow)
//   - /oauth/proxy/callback: Muster authenticating WITH remote servers (OAuth proxy flow)
func (s *OAuthHTTPServer) setupOAuthProxyRoutes(mux *http.ServeMux) {
	oauthProxyHandler := api.GetOAuthHandler()
	if oauthProxyHandler == nil || !oauthProxyHandler.IsEnabled() {
		return
	}

	// Mount the OAuth proxy callback handler for remote server authentication
	callbackPath := oauthProxyHandler.GetCallbackPath()
	if callbackPath != "" {
		mux.Handle(callbackPath, oauthProxyHandler.GetHTTPHandler())
		logging.Info("OAuth", "Mounted OAuth proxy callback at %s", callbackPath)
	}

	// Mount the self-hosted CIMD if enabled
	// This allows remote MCP servers to fetch muster's client metadata
	if oauthProxyHandler.ShouldServeCIMD() {
		cimdPath := oauthProxyHandler.GetCIMDPath()
		cimdHandler := oauthProxyHandler.GetCIMDHandler()
		if cimdPath != "" && cimdHandler != nil {
			mux.HandleFunc(cimdPath, cimdHandler)
			logging.Info("OAuth", "Mounted self-hosted CIMD at %s", cimdPath)
		}
	}
}

// setupMCPRoutes registers MCP endpoints with OAuth protection.
func (s *OAuthHTTPServer) setupMCPRoutes(mux *http.ServeMux) {
	// Create middleware to inject access token into context for downstream use
	accessTokenInjector := s.createAccessTokenInjectorMiddleware(s.mcpHandler)

	// Wrap MCP endpoint with OAuth middleware (ValidateToken validates and adds user info)
	mux.Handle("/mcp", s.oauthHandler.ValidateToken(accessTokenInjector))
	mux.Handle("/sse", s.oauthHandler.ValidateToken(accessTokenInjector))
	mux.Handle("/message", s.oauthHandler.ValidateToken(accessTokenInjector))

	logging.Info("OAuth", "Protected MCP endpoints with OAuth middleware")
}

// createAccessTokenInjectorMiddleware creates middleware that injects the user's
// OAuth access token into the request context. This token can then be used
// for downstream authentication (e.g., to remote MCP servers).
//
// After extracting credentials, the middleware fires the onAuthenticated callback
// (if set) to trigger on-demand SSO connections for any uncached SSO servers.
func (s *OAuthHTTPServer) createAccessTokenInjectorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if s.debug {
			if bearer := extractBearerToken(r); bearer != "" {
				logging.Debug("OAuth", "Incoming bearer token on %s: %s", r.URL.Path, bearer)
			}
		}

		// Get user info from context (set by ValidateToken middleware)
		userInfo, ok := oauth.UserInfoFromContext(ctx)
		if !ok || userInfo == nil {
			if s.debug {
				logging.Debug("OAuth", "No user info in context, proceeding without token injection")
			}
			next.ServeHTTP(w, r)
			return
		}

		if userInfo.Email == "" {
			if s.debug {
				logging.Debug("OAuth", "User info has no email, proceeding without token injection")
			}
			next.ServeHTTP(w, r)
			return
		}

		// Always propagate session ID early so onAuthenticated callbacks can
		// detect sessions with broken refresh chains even when the ID token
		// is missing.
		if sessionID, ok := oauth.SessionIDFromContext(ctx); ok {
			ctx = api.WithSessionID(ctx, sessionID)
		}

		// Look up the upstream provider token by the muster access token.
		// After a token refresh, only the access-token-keyed entry in the
		// token store has the fresh upstream token (with a valid ID token);
		// the email-keyed entry that was used before is never updated by
		// mcp-oauth's RefreshAccessToken and becomes stale.
		token := s.getProviderToken(ctx, r)
		if token == nil {
			// The bearer token was accepted by ValidateToken but muster's token
			// store has no entry for it. This happens when a client presents a
			// JWT issued directly by the upstream IdP (e.g. a Dex ID token) and
			// mcp-oauth accepted it via the TrustedAudiences path. Treat the
			// bearer token itself as the ID token and synthesize a stable
			// session so downstream SSO flows can run.
			if s.injectExternalIDToken(w, r, ctx, next) {
				return
			}

			logging.Warn("OAuth", "SSO: No token stored for email=%s (SSO forwarding will not work)",
				truncateEmail(userInfo.Email))
			r = r.WithContext(ctx)
			s.fireOnAuthenticated(ctx)
			next.ServeHTTP(w, r)
			return
		}

		// Extract the ID token for downstream OIDC authentication.
		idToken := GetIDToken(token)
		if idToken == "" {
			logging.Warn("OAuth", "SSO: No ID token in stored token for email=%s (has access_token=%v, has refresh_token=%v). SSO forwarding will not work. Check if upstream IdP returns id_token.",
				truncateEmail(userInfo.Email), token.AccessToken != "", token.RefreshToken != "")
			r = r.WithContext(ctx)
			s.fireOnAuthenticated(ctx)
			next.ServeHTTP(w, r)
			return
		}

		// Inject the ID token into context for downstream SSO use
		ctx = ContextWithIDToken(ctx, idToken)

		// Extract and inject the authenticated user's subject (sub claim) for user identity.
		if subject := extractSubjectFromIDToken(idToken); subject != "" {
			ctx = api.WithSubject(ctx, subject)
		}

		r = r.WithContext(ctx)
		s.fireOnAuthenticated(ctx)

		if s.debug {
			logging.Debug("OAuth", "SSO: ID token available for forwarding (email=%s)", truncateEmail(userInfo.Email))
		}

		next.ServeHTTP(w, r)
	})
}

// musterIssuer returns the OAuth issuer URL used as the key when storing
// tokens in the OAuth proxy token store. This MUST match the issuer the
// aggregator uses for lookups (see aggregator.getMusterIssuer): for Dex,
// that's the upstream Dex issuer URL; for other providers, it's muster's
// own base URL.
//
// Returns empty string when no issuer can be determined.
func (s *OAuthHTTPServer) musterIssuer() string {
	if s.config.Provider == OAuthProviderDex && s.config.Dex.IssuerURL != "" {
		return s.config.Dex.IssuerURL
	}
	return s.config.BaseURL
}

// acceptForwardedIDToken is a test seam around (*oauth.Server).AcceptForwardedIDToken.
// Tests stub this to avoid needing a real JWKS/provider setup; production code
// leaves it at its default.
var acceptForwardedIDToken = func(s *oauth.Server, ctx context.Context, bearerToken string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
	return s.AcceptForwardedIDToken(ctx, bearerToken)
}

// injectExternalIDToken handles the case where the bearer token was validated
// by mcp-oauth but muster's token store has no entry for it — i.e. a JWT
// issued directly by the upstream OIDC provider (typically a Dex ID token
// accepted via TrustedAudiences). The bearer token IS the ID token, so we
// delegate JWT parsing, JWKS signature verification, issuer/audience checks
// and session-ID derivation to mcp-oauth's AcceptForwardedIDToken, then
// inject the verified subject and ID token into the request context and
// mirror the token into the OAuth proxy store so downstream SSO forwarding
// can resolve it later.
//
// Returns true if the request was handled as an external token (in which
// case next.ServeHTTP has already been called and the caller must return);
// returns false when AcceptForwardedIDToken rejects the token (audience
// mismatch, signature failure, not a JWT, etc.) and the caller should fall
// back to the existing "no token stored" path.
func (s *OAuthHTTPServer) injectExternalIDToken(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	next http.Handler,
) bool {
	bearerToken := extractBearerToken(r)
	if bearerToken == "" {
		return false
	}

	acceptance, err := acceptForwardedIDToken(s.oauthServer, ctx, bearerToken)
	if err != nil {
		if s.debug {
			if errors.Is(err, oauth.ErrTrustedAudienceMismatch) {
				logging.Debug("OAuth", "SSO: forwarded ID token audience mismatch, falling back: %v", err)
			} else {
				logging.Debug("OAuth", "SSO: forwarded ID token rejected, falling back: %v", err)
			}
		}
		return false
	}

	ctx = api.WithSessionID(ctx, acceptance.SessionID)
	ctx = api.WithSubject(ctx, acceptance.Subject)
	ctx = ContextWithIDToken(ctx, bearerToken)

	// Mirror the ID token into the OAuth proxy store keyed by (sessionID,
	// musterIssuer). getIDTokenForForwarding (in the aggregator) looks here
	// for background header-func closures that run without the request
	// context. The key MUST match the issuer the aggregator computes —
	// mirror its resolution logic here. mcp-oauth's AcceptForwardedIDToken
	// intentionally does NOT mirror into TokenStore, so this keying is
	// muster's own responsibility.
	if issuer := s.musterIssuer(); issuer != "" {
		if oh := api.GetOAuthHandler(); oh != nil && oh.IsEnabled() {
			oh.StoreToken(acceptance.SessionID, acceptance.Subject, issuer, &api.OAuthToken{IDToken: bearerToken})
		}
	}

	if s.debug {
		var email string
		if acceptance.UserInfo != nil {
			email = acceptance.UserInfo.Email
		}
		logging.Debug("OAuth", "SSO: accepted externally-issued ID token (email=%s, session=%s)",
			truncateEmail(email), logging.TruncateIdentifier(acceptance.SessionID))
	}

	r = r.WithContext(ctx)
	s.fireOnAuthenticated(ctx)
	next.ServeHTTP(w, r)
	return true
}

// fireOnAuthenticated calls the onAuthenticated callback if set and a session
// ID is available in the context. Extracted to avoid duplication across the
// multiple early-return paths in createAccessTokenInjectorMiddleware.
func (s *OAuthHTTPServer) fireOnAuthenticated(ctx context.Context) {
	if s.onAuthenticated != nil {
		if sessionID := api.GetSessionIDFromContext(ctx); sessionID != "" {
			s.onAuthenticated(ctx, sessionID)
		}
	}
}

// GetOAuthServer returns the underlying OAuth server for testing or direct access.
func (s *OAuthHTTPServer) GetOAuthServer() *oauth.Server {
	return s.oauthServer
}

// GetOAuthHandler returns the OAuth handler for testing or direct access.
func (s *OAuthHTTPServer) GetOAuthHandler() *oauth.Handler {
	return s.oauthHandler
}

// ValidateTokenWithSubject wraps the given handler with OAuth token validation
// and extracts the authenticated user's subject (sub claim) into the context.
// This is used for API endpoints that need to identify the user but don't need
// the full SSO/token-injection logic of the MCP middleware chain.
func (s *OAuthHTTPServer) ValidateTokenWithSubject(next http.Handler) http.Handler {
	return s.oauthHandler.ValidateToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		userInfo, ok := oauth.UserInfoFromContext(ctx)
		if !ok || userInfo == nil || userInfo.ID == "" {
			http.Error(w, "Unauthorized: missing user identity", http.StatusUnauthorized)
			return
		}

		ctx = api.WithSubject(ctx, userInfo.ID)

		// Propagate the session ID from mcp-oauth's ValidateToken middleware.
		if sessionID, ok := oauth.SessionIDFromContext(ctx); ok {
			ctx = api.WithSessionID(ctx, sessionID)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// GetTokenStore returns the token store for downstream OAuth passthrough.
func (s *OAuthHTTPServer) GetTokenStore() storage.TokenStore {
	return s.tokenStore
}

// Shutdown gracefully shuts down the server.
func (s *OAuthHTTPServer) Shutdown(ctx context.Context) error {
	// Shutdown OAuth server (handles rate limiters, storage cleanup, etc.)
	if s.oauthServer != nil {
		if err := s.oauthServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown OAuth server: %w", err)
		}
	}

	// Shutdown HTTP server if we started one
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// createOAuthServer creates an OAuth server using mcp-oauth library.
func createOAuthServer(cfg config.OAuthServerConfig, debug bool) (*oauth.Server, storage.TokenStore, error) {
	// Create logger with appropriate level
	var logger *slog.Logger
	if debug {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	} else {
		logger = slog.Default()
	}

	redirectURL := cfg.BaseURL + "/oauth/callback"
	var provider providers.Provider
	var err error

	switch cfg.Provider {
	case OAuthProviderDex:
		// Build scopes including any required audiences from MCPServers
		scopes := buildDexScopes(api.CollectRequiredAudiences())
		for _, scope := range scopes {
			if strings.HasPrefix(scope, dex.AudienceScopePrefix) {
				logger.Info("Requesting cross-client audience from MCPServer requiredAudiences",
					"audience", strings.TrimPrefix(scope, dex.AudienceScopePrefix))
			}
		}

		dexConfig := &dex.Config{
			IssuerURL:    cfg.Dex.IssuerURL,
			ClientID:     cfg.Dex.ClientID,
			ClientSecret: cfg.Dex.ClientSecret,
			RedirectURL:  redirectURL,
			Scopes:       scopes,
		}

		if cfg.Dex.ConnectorID != "" {
			dexConfig.ConnectorID = cfg.Dex.ConnectorID
		}

		if cfg.Dex.CAFile != "" {
			httpClient, err := createHTTPClientWithCA(cfg.Dex.CAFile)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create HTTP client with CA: %w", err)
			}
			dexConfig.HTTPClient = httpClient
			logger.Info("Using custom CA for Dex TLS verification", "caFile", cfg.Dex.CAFile)
		}

		provider, err = dex.NewProvider(dexConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create Dex provider: %w", err)
		}
		logger.Info("Using Dex OIDC provider", "issuer", cfg.Dex.IssuerURL)

	case OAuthProviderGoogle:
		provider, err = google.NewProvider(&google.Config{
			ClientID:     cfg.Google.ClientID,
			ClientSecret: cfg.Google.ClientSecret,
			RedirectURL:  redirectURL,
			Scopes:       googleOAuthScopes,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create Google provider: %w", err)
		}
		logger.Info("Using Google OAuth provider")

	default:
		return nil, nil, fmt.Errorf("unsupported OAuth provider: %s (supported: %s, %s)", cfg.Provider, OAuthProviderDex, OAuthProviderGoogle)
	}

	// Create storage backend based on configuration
	var tokenStore storage.TokenStore
	var clientStore storage.ClientStore
	var flowStore storage.FlowStore

	switch cfg.Storage.Type {
	case "valkey":
		if cfg.Storage.Valkey.URL == "" {
			return nil, nil, fmt.Errorf("valkey URL is required when using valkey storage")
		}

		valkeyConfig := valkey.Config{
			Address:   cfg.Storage.Valkey.URL,
			Password:  cfg.Storage.Valkey.Password,
			DB:        cfg.Storage.Valkey.DB,
			KeyPrefix: cfg.Storage.Valkey.KeyPrefix,
			Logger:    logger,
		}

		if cfg.Storage.Valkey.TLSEnabled {
			valkeyConfig.TLS = &tls.Config{
				MinVersion: tls.VersionTLS12,
			}
		}

		if valkeyConfig.KeyPrefix == "" {
			valkeyConfig.KeyPrefix = "muster:"
		}

		valkeyStore, err := valkey.New(valkeyConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create Valkey storage: %w", err)
		}

		// Set up encryption if key is provided
		if cfg.EncryptionKey != "" {
			keyBytes, err := base64.StdEncoding.DecodeString(cfg.EncryptionKey)
			if err != nil {
				valkeyStore.Close()
				return nil, nil, fmt.Errorf("failed to decode encryption key: %w", err)
			}
			encryptor, err := security.NewEncryptor(keyBytes)
			if err != nil {
				valkeyStore.Close()
				return nil, nil, fmt.Errorf("failed to create encryptor: %w", err)
			}
			valkeyStore.SetEncryptor(encryptor)
			logger.Info("Token encryption at rest enabled for Valkey storage (AES-256-GCM)")
		}

		tokenStore = valkeyStore
		clientStore = valkeyStore
		flowStore = valkeyStore
		logger.Info("Using Valkey storage backend", "address", cfg.Storage.Valkey.URL)

	case "memory", "":
		memStore := memory.New()
		tokenStore = memStore
		clientStore = memStore
		flowStore = memStore
		logger.Info("Using in-memory storage backend")

	default:
		return nil, nil, fmt.Errorf("unsupported OAuth storage type: %s (supported: memory, valkey)", cfg.Storage.Type)
	}

	// Set defaults
	maxClientsPerIP := DefaultMaxClientsPerIP

	refreshTokenTTL := DefaultRefreshTokenTTL
	if cfg.SessionDuration != "" {
		parsed, err := time.ParseDuration(cfg.SessionDuration)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid sessionDuration %q: %w", cfg.SessionDuration, err)
		}
		refreshTokenTTL = parsed
		logger.Info("Using custom session duration", "duration", parsed)
	}

	// Create server configuration.
	serverConfig := &oauthserver.Config{
		Issuer:                           cfg.BaseURL,
		AccessTokenTTL:                   int64(DefaultAccessTokenTTL / time.Second),
		RefreshTokenTTL:                  int64(refreshTokenTTL / time.Second),
		AllowRefreshTokenRotation:        true,
		RequirePKCE:                      true,
		AllowPKCEPlain:                   false,
		AllowPublicClientRegistration:    cfg.AllowPublicClientRegistration,
		RegistrationAccessToken:          cfg.RegistrationToken,
		MaxClientsPerIP:                  maxClientsPerIP,
		EnableClientIDMetadataDocuments:  cfg.EnableCIMD,
		TrustedPublicRegistrationSchemes: cfg.TrustedPublicRegistrationSchemes,
		AllowLocalhostRedirectURIs:       cfg.AllowLocalhostRedirectURIs,
		TrustedAudiences:                 cfg.TrustedAudiences,

		// Instrumentation
		Instrumentation: oauthserver.InstrumentationConfig{
			Enabled:         true,
			ServiceName:     "muster",
			ServiceVersion:  "1.0.0",
			MetricsExporter: "prometheus",
		},
	}

	// Create OAuth server
	oauthSrv, err := oauth.NewServer(
		provider,
		tokenStore,
		clientStore,
		flowStore,
		serverConfig,
		logger,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create OAuth server: %w", err)
	}

	// Set up encryption if key provided (for memory storage)
	if cfg.EncryptionKey != "" && cfg.Storage.Type != "valkey" {
		keyBytes, err := base64.StdEncoding.DecodeString(cfg.EncryptionKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decode encryption key: %w", err)
		}
		encryptor, err := security.NewEncryptor(keyBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create encryptor: %w", err)
		}
		oauthSrv.SetEncryptor(encryptor)
		logger.Info("Token encryption at rest enabled (AES-256-GCM)")
	}

	// Set up audit logging
	auditor := security.NewAuditor(logger, true)
	oauthSrv.SetAuditor(auditor)
	logger.Info("Security audit logging enabled")

	// Set up rate limiting
	ipRateLimiter := security.NewRateLimiter(DefaultIPRateLimit, DefaultIPBurst, logger)
	oauthSrv.SetRateLimiter(ipRateLimiter)
	logger.Info("IP-based rate limiting enabled", "rate", DefaultIPRateLimit, "burst", DefaultIPBurst)

	userRateLimiter := security.NewRateLimiter(DefaultUserRateLimit, DefaultUserBurst, logger)
	oauthSrv.SetUserRateLimiter(userRateLimiter)
	logger.Info("User-based rate limiting enabled", "rate", DefaultUserRateLimit, "burst", DefaultUserBurst)

	clientRegRL := security.NewClientRegistrationRateLimiterWithConfig(
		maxClientsPerIP,
		security.DefaultRegistrationWindow,
		security.DefaultMaxRegistrationEntries,
		logger,
	)
	oauthSrv.SetClientRegistrationRateLimiter(clientRegRL)
	logger.Info("Client registration rate limiting enabled", "maxClientsPerIP", maxClientsPerIP)

	return oauthSrv, tokenStore, nil
}

// createHTTPClientWithCA creates an HTTP client that trusts certificates signed by
// the CA in the specified file.
func createHTTPClientWithCA(caFile string) (*http.Client, error) {
	// #nosec G304 -- caFile is a configuration value from operator, not user input
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA file %s: %w", caFile, err)
	}

	caCertPool, err := x509.SystemCertPool()
	if err != nil {
		caCertPool = x509.NewCertPool()
	}

	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", caFile)
	}

	tlsConfig := &tls.Config{
		RootCAs:    caCertPool,
		MinVersion: tls.VersionTLS12,
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, nil
}

// validateHTTPSRequirement ensures OAuth 2.1 HTTPS compliance.
// Allows HTTP only for loopback addresses (localhost, 127.0.0.1, ::1).
func validateHTTPSRequirement(baseURL string) error {
	if baseURL == "" {
		return fmt.Errorf("base URL cannot be empty")
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}

	if u.Scheme == "http" {
		host := u.Hostname()
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return fmt.Errorf("OAuth 2.1 requires HTTPS for production (got: %s). Use HTTPS or localhost for development", baseURL)
		}
	} else if u.Scheme != "https" {
		return fmt.Errorf("invalid URL scheme: %s. Must be http (localhost only) or https", u.Scheme)
	}

	return nil
}

// truncateEmail returns a truncated prefix of the email for logging purposes.
// Only the first logEmailPrefixLength characters are shown for privacy.
func truncateEmail(email string) string {
	if len(email) > logEmailPrefixLength {
		return email[:logEmailPrefixLength]
	}
	return email
}

// getProviderToken retrieves the upstream provider token for SSO forwarding.
//
// It looks up the token by the muster access token (bearer token from the
// Authorization header). The mcp-oauth token store maps muster access tokens
// to upstream provider tokens. This is the only lookup that returns fresh
// tokens after a refresh -- mcp-oauth's RefreshAccessToken stores the new
// upstream provider token under the new access token key but does NOT update
// the email-keyed entry that was used previously.
func (s *OAuthHTTPServer) getProviderToken(ctx context.Context, r *http.Request) *oauth2.Token {
	bearerToken := extractBearerToken(r)
	if bearerToken == "" {
		return nil
	}
	token, err := s.tokenStore.GetToken(ctx, bearerToken)
	if err != nil {
		logging.Warn("OAuth", "SSO: Failed to get provider token from store: %v", err)
		return nil
	}
	return token
}

// extractBearerToken extracts the bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(auth) >= len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		return auth[len(prefix):]
	}
	return ""
}

// extractSubjectFromIDToken extracts the subject (sub) claim from a JWT ID token.
// This is used for session identity binding. Returns empty string if extraction fails.
//
// SECURITY: This function does NOT verify the JWT signature. It MUST only be called
// after the ValidateToken middleware has cryptographically verified the token.
// The middleware chain in createOAuthProtectedMux guarantees this ordering.
func extractSubjectFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	return claims.Sub
}
