package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

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

	// DefaultRefreshTokenTTL is the default TTL for refresh tokens (30 days).
	// This is aligned with Dex's absoluteLifetime (720h = 30 days) to avoid a
	// mismatch where the muster refresh token appears valid but the underlying
	// Dex refresh token has already expired due to its hard absolute cap.
	// Note: muster issues a rolling TTL (reset on each rotation), while Dex's
	// absoluteLifetime is measured from the original issuance and does NOT reset.
	// Proper refresh token capping (analogous to capTokenExpiry) requires
	// changes in the mcp-oauth library.
	DefaultRefreshTokenTTL = 30 * 24 * time.Hour

	// DefaultIPRateLimit is the default rate limit for requests per IP (requests/second).
	DefaultIPRateLimit = 10
	// DefaultIPBurst is the default burst size for IP rate limiting.
	DefaultIPBurst = 20

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

	// DefaultSessionTrackerTTL is how long session entries are kept before cleanup.
	// Sessions inactive for longer than this will be removed to prevent memory leaks.
	DefaultSessionTrackerTTL = 24 * time.Hour

	// DefaultSessionTrackerCleanupInterval is how often the session tracker cleanup runs.
	DefaultSessionTrackerCleanupInterval = 1 * time.Hour
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

// sessionTrackerEntry holds the token hash and last access time for a session.
// This is used to track when sessions can be cleaned up.
type sessionTrackerEntry struct {
	tokenHash  string
	lastAccess time.Time
}

// OAuthHTTPServer wraps an MCP HTTP handler with OAuth 2.1 authentication.
// It provides both OAuth server functionality (authorization, token issuance)
// and resource server protection (token validation middleware).
type OAuthHTTPServer struct {
	config       config.OAuthServerConfig
	oauthServer  *oauth.Server
	oauthHandler *oauth.Handler
	tokenStore   storage.TokenStore
	httpServer   *http.Server
	mcpHandler   http.Handler
	debug        bool

	// sessionInitTracker tracks which sessions have had their init callback called.
	// This prevents calling the proactive SSO logic on every request.
	// The value is a sessionTrackerEntry containing the token hash and last access time.
	// When the token changes (user re-authenticates), proactive SSO is triggered again.
	sessionInitTracker sync.Map // map[string]sessionTrackerEntry

	// stopCleanup is closed to signal the cleanup goroutine to stop.
	stopCleanup chan struct{}
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
		stopCleanup:  make(chan struct{}),
	}

	// Start background cleanup goroutine for session tracker
	go server.runSessionTrackerCleanup()

	return server, nil
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
// Additionally, on the first request for a new session, this middleware triggers
// the session initialization callback (if registered). This enables proactive SSO:
// when a user authenticates to muster, SSO-enabled servers are automatically
// connected using muster's ID token.
func (s *OAuthHTTPServer) createAccessTokenInjectorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

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

		// Retrieve the user's stored OAuth token for SSO forwarding.
		token, err := s.tokenStore.GetToken(ctx, userInfo.Email)
		if err != nil {
			// Log at WARN level to help diagnose SSO issues
			logging.Warn("OAuth", "SSO: Failed to get token from store for email=%s: %v",
				truncateEmail(userInfo.Email), err)
			next.ServeHTTP(w, r)
			return
		}
		if token == nil {
			// This is a critical path for SSO - log at WARN to help diagnose
			logging.Warn("OAuth", "SSO: No token stored for email=%s (SSO forwarding will not work)",
				truncateEmail(userInfo.Email))
			next.ServeHTTP(w, r)
			return
		}

		// Extract the ID token for downstream OIDC authentication.
		// The ID token is required for SSO forwarding to downstream MCP servers.
		idToken := GetIDToken(token)
		if idToken == "" {
			// This is a critical path for SSO - log at WARN to help diagnose
			logging.Warn("OAuth", "SSO: No ID token in stored token for email=%s (has access_token=%v, has refresh_token=%v). SSO forwarding will not work. Check if upstream IdP returns id_token.",
				truncateEmail(userInfo.Email), token.AccessToken != "", token.RefreshToken != "")
			next.ServeHTTP(w, r)
			return
		}

		// Inject the ID token into context for downstream SSO use
		ctx = ContextWithAccessToken(ctx, idToken)

		// Also inject the upstream access token for refresh detection.
		// The access token changes on every refresh, while the ID token is preserved.
		// By tracking the access token, we can detect both:
		// - Re-authentication (new ID token from IdP)
		// - Token refresh (new access token, preserved ID token)
		ctx = ContextWithUpstreamAccessToken(ctx, token.AccessToken)
		r = r.WithContext(ctx)

		if s.debug {
			logging.Debug("OAuth", "SSO: ID token available for forwarding (email=%s)", truncateEmail(userInfo.Email))
		}

		// Trigger session initialization callback on first request for this session,
		// or when the token has changed (re-authentication or refresh).
		// This enables proactive SSO: after muster auth login or token refresh,
		// SSO-enabled servers are automatically connected using muster's ID token.
		s.triggerSessionInitIfNeeded(ctx, r)

		next.ServeHTTP(w, r)
	})
}

// triggerSessionInitIfNeeded triggers the session initialization callback if this
// is the first authenticated request for the session, or if the token has changed.
// This enables proactive SSO connections to be established after:
// - Initial muster authentication (first login)
// - Re-authentication (logout + login with new ID token)
// - Token refresh (server-side refresh gets new access token)
func (s *OAuthHTTPServer) triggerSessionInitIfNeeded(ctx context.Context, r *http.Request) {
	// Get session ID from the request header directly.
	// We can't rely on context here because clientSessionIDMiddleware runs
	// AFTER this middleware in the chain (it wraps the MCP handler, not the OAuth chain).
	sessionID := r.Header.Get(api.ClientSessionIDHeader)
	if sessionID == "" {
		logging.Debug("OAuth", "SSO: No session ID header, skipping session init")
		return
	}

	// Get the ID token from context - we need this for SSO forwarding
	idToken, _ := GetAccessTokenFromContext(ctx)
	if idToken == "" {
		logging.Debug("OAuth", "SSO: No ID token in context for session %s, skipping session init",
			logging.TruncateSessionID(sessionID))
		return
	}

	// Get the upstream access token for hash computation.
	// We use the access token (not ID token) because:
	// - Access token changes on every token refresh
	// - ID token is preserved during refresh (per OAuth spec)
	// This allows us to detect both re-authentication AND server-side token refresh.
	upstreamAccessToken, _ := GetUpstreamAccessTokenFromContext(ctx)
	if upstreamAccessToken == "" {
		// Fall back to ID token if upstream access token is not available
		upstreamAccessToken = idToken
	}

	// Compute a hash of the upstream access token to detect token changes
	currentTokenHash := hashToken(upstreamAccessToken)
	now := time.Now()

	// Check if we've already initialized this session with this token.
	// If the token has changed (re-authentication or refresh), we need to trigger SSO again.
	if existingEntry, exists := s.sessionInitTracker.Load(sessionID); exists {
		entry := existingEntry.(sessionTrackerEntry)
		if entry.tokenHash == currentTokenHash {
			// Already initialized with the same token - just update last access time
			s.sessionInitTracker.Store(sessionID, sessionTrackerEntry{
				tokenHash:  currentTokenHash,
				lastAccess: now,
			})
			logging.Debug("OAuth", "SSO: Session %s already initialized with current token, skipping",
				logging.TruncateSessionID(sessionID))
			return
		}
		// Token has changed - could be re-authentication or server-side refresh
		logging.Info("OAuth", "SSO: Token changed for session %s (re-auth or refresh), triggering SSO",
			logging.TruncateSessionID(sessionID))
	}

	// Store the current token hash with timestamp
	s.sessionInitTracker.Store(sessionID, sessionTrackerEntry{
		tokenHash:  currentTokenHash,
		lastAccess: now,
	})

	// Get the session init callback
	callback := api.GetSessionInitCallback()
	if callback == nil {
		logging.Warn("OAuth", "SSO: No session init callback registered, proactive SSO disabled")
		return
	}

	// Trigger the callback asynchronously to not block the request.
	// Use a background context with the necessary values copied over.
	go func() {
		logging.Info("OAuth", "SSO: Triggering proactive SSO for session %s (has_id_token=%v)",
			logging.TruncateSessionID(sessionID), idToken != "")

		// Create a background context with the ID token for SSO forwarding.
		// This context won't be canceled when the HTTP request completes.
		bgCtx := context.Background()
		bgCtx = ContextWithAccessToken(bgCtx, idToken)
		bgCtx = api.WithClientSessionID(bgCtx, sessionID)

		callback(bgCtx, sessionID)
	}()
}

// GetOAuthServer returns the underlying OAuth server for testing or direct access.
func (s *OAuthHTTPServer) GetOAuthServer() *oauth.Server {
	return s.oauthServer
}

// GetOAuthHandler returns the OAuth handler for testing or direct access.
func (s *OAuthHTTPServer) GetOAuthHandler() *oauth.Handler {
	return s.oauthHandler
}

// GetTokenStore returns the token store for downstream OAuth passthrough.
func (s *OAuthHTTPServer) GetTokenStore() storage.TokenStore {
	return s.tokenStore
}

// Shutdown gracefully shuts down the server.
func (s *OAuthHTTPServer) Shutdown(ctx context.Context) error {
	// Stop the session tracker cleanup goroutine
	if s.stopCleanup != nil {
		close(s.stopCleanup)
	}

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

	// Create server configuration.
	// AccessTokenTTL is explicitly set rather than relying on the library default (1h).
	// The mcp-oauth library's capTokenExpiry function will further reduce this if the
	// upstream provider (e.g., Dex) issues shorter-lived tokens. For example, with
	// Dex idTokens: "30m", the effective access token TTL becomes min(30m, 30m) = 30m.
	serverConfig := &oauthserver.Config{
		Issuer:                           cfg.BaseURL,
		AccessTokenTTL:                   int64(DefaultAccessTokenTTL.Seconds()),
		RefreshTokenTTL:                  int64(DefaultRefreshTokenTTL.Seconds()),
		AllowRefreshTokenRotation:        true,
		RequirePKCE:                      true,
		AllowPKCEPlain:                   false,
		AllowPublicClientRegistration:    cfg.AllowPublicClientRegistration,
		RegistrationAccessToken:          cfg.RegistrationToken,
		MaxClientsPerIP:                  maxClientsPerIP,
		EnableClientIDMetadataDocuments:  cfg.EnableCIMD,
		TrustedPublicRegistrationSchemes: cfg.TrustedPublicRegistrationSchemes,
		AllowLocalhostRedirectURIs:       cfg.AllowLocalhostRedirectURIs,

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

// runSessionTrackerCleanup runs a background goroutine that periodically removes
// expired session entries from the session init tracker. This prevents unbounded
// memory growth in long-running servers.
func (s *OAuthHTTPServer) runSessionTrackerCleanup() {
	ticker := time.NewTicker(DefaultSessionTrackerCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCleanup:
			logging.Debug("OAuth", "Session tracker cleanup goroutine stopped")
			return
		case <-ticker.C:
			s.cleanupExpiredSessions()
		}
	}
}

// cleanupExpiredSessions removes session entries that haven't been accessed
// within the TTL period.
func (s *OAuthHTTPServer) cleanupExpiredSessions() {
	now := time.Now()
	expiredCount := 0

	s.sessionInitTracker.Range(func(key, value interface{}) bool {
		sessionID := key.(string)
		entry := value.(sessionTrackerEntry)

		if now.Sub(entry.lastAccess) > DefaultSessionTrackerTTL {
			s.sessionInitTracker.Delete(sessionID)
			expiredCount++
		}
		return true
	})

	if expiredCount > 0 {
		logging.Info("OAuth", "Cleaned up %d expired session tracker entries", expiredCount)
	}
}

// hashToken returns a short hash of the token for comparison purposes.
// This is used to detect when a user has re-authenticated with a new token.
// We use only the first 16 characters of the SHA-256 hash for efficiency.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:8]) // First 8 bytes = 16 hex chars
}
