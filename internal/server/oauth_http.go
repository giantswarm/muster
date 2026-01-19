package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
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

	"muster/internal/api"
	"muster/internal/config"
	"muster/pkg/logging"
)

const (
	// OAuthProviderDex is the Dex OIDC provider type.
	OAuthProviderDex = "dex"
	// OAuthProviderGoogle is the Google OAuth provider type.
	OAuthProviderGoogle = "google"

	// DefaultRefreshTokenTTL is the default TTL for refresh tokens (90 days).
	DefaultRefreshTokenTTL = 90 * 24 * time.Hour

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
	sessionInitTracker sync.Map // map[string]bool
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

	return &OAuthHTTPServer{
		config:       cfg,
		oauthServer:  oauthServer,
		oauthHandler: oauthHandler,
		tokenStore:   tokenStore,
		mcpHandler:   mcpHandler,
		debug:        debug,
	}, nil
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

		// Retrieve the user's stored OAuth token
		token, err := s.tokenStore.GetToken(ctx, userInfo.Email)
		if err != nil {
			if s.debug {
				logging.Debug("OAuth", "Failed to get token from store: %v", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		if token == nil {
			if s.debug {
				logging.Debug("OAuth", "No token stored for user")
			}
			next.ServeHTTP(w, r)
			return
		}

		// Extract the ID token for downstream OIDC authentication
		idToken := GetIDToken(token)
		if idToken == "" {
			if s.debug {
				logging.Debug("OAuth", "No ID token in stored token")
			}
			next.ServeHTTP(w, r)
			return
		}

		// Inject the token into context for downstream use
		ctx = ContextWithAccessToken(ctx, idToken)
		r = r.WithContext(ctx)

		if s.debug {
			logging.Debug("OAuth", "Injected access token for user (email hash: %s...)", hashEmail(userInfo.Email))
		}

		// Trigger session initialization callback on first request for this session.
		// This enables proactive SSO: after muster auth login, SSO-enabled servers
		// are automatically connected using muster's ID token.
		s.triggerSessionInitIfNeeded(ctx)

		next.ServeHTTP(w, r)
	})
}

// triggerSessionInitIfNeeded triggers the session initialization callback if this
// is the first authenticated request for the session. This enables proactive SSO
// connections to be established after muster auth login.
func (s *OAuthHTTPServer) triggerSessionInitIfNeeded(ctx context.Context) {
	// Get session ID from context using the shared api package type
	sessionID, ok := api.GetClientSessionIDFromContext(ctx)
	if !ok || sessionID == "" {
		return
	}

	// Check if we've already initialized this session
	if _, exists := s.sessionInitTracker.LoadOrStore(sessionID, true); exists {
		// Already initialized
		return
	}

	// Get the session init callback
	callback := api.GetSessionInitCallback()
	if callback == nil {
		return
	}

	// Extract values we need to preserve for the background goroutine.
	// We must NOT pass the original ctx to the goroutine because it will be
	// canceled when the HTTP request completes, potentially before the callback finishes.
	// Instead, we create a new background context and copy the necessary values.
	idToken, _ := GetAccessTokenFromContext(ctx)

	// Trigger the callback asynchronously to not block the request.
	// Use a background context with the necessary values copied over.
	go func() {
		logging.Info("OAuth", "Triggering proactive SSO for new session %s", logging.TruncateSessionID(sessionID))

		// Create a background context with the ID token for SSO forwarding.
		// This context won't be canceled when the HTTP request completes.
		bgCtx := context.Background()
		if idToken != "" {
			bgCtx = ContextWithAccessToken(bgCtx, idToken)
		}
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
		scopes := make([]string, len(dexOAuthScopes))
		copy(scopes, dexOAuthScopes)

		// Add cross-client audience if configured
		if cfg.Dex.KubernetesAuthenticatorClientID != "" {
			audienceScope := "audience:server:client_id:" + cfg.Dex.KubernetesAuthenticatorClientID
			scopes = append(scopes, audienceScope)
			logger.Info("Requesting cross-client audience for Kubernetes API authentication",
				"kubernetesAuthenticatorClientID", cfg.Dex.KubernetesAuthenticatorClientID)
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

	// Create server configuration
	serverConfig := &oauthserver.Config{
		Issuer:                           cfg.BaseURL,
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

// hashEmail returns a truncated prefix of the email for logging purposes.
// Only the first logEmailPrefixLength characters are shown for privacy.
func hashEmail(email string) string {
	if len(email) > logEmailPrefixLength {
		return email[:logEmailPrefixLength]
	}
	return email
}
