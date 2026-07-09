package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	oauth "github.com/giantswarm/mcp-oauth"
	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/providers/dex"
	"github.com/giantswarm/mcp-oauth/providers/google"
	"github.com/giantswarm/mcp-oauth/security"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/giantswarm/mcp-oauth/storage/valkey"
	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
	"github.com/giantswarm/muster/pkg/tlsutil"
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

	// DefaultSecurityEventRate bounds security-event log emission (events/second
	// per keyed event), keeping a malformed-token attack from flooding the audit
	// pipeline. Mirrors mcp-oauth's production-example default (1, 5).
	DefaultSecurityEventRate = 1
	// DefaultSecurityEventBurst is the burst size for security-event log emission.
	DefaultSecurityEventBurst = 5

	// DefaultMetadataFetchRate is the per-domain rate for CIMD outbound metadata
	// fetches (requests/second). Prevents a misbehaving or malicious client from
	// triggering unbounded outbound HTTP requests to arbitrary domains.
	DefaultMetadataFetchRate = 1
	// DefaultMetadataFetchBurst is the burst size for CIMD metadata fetch rate limiting.
	DefaultMetadataFetchBurst = 3

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
	config           config.OAuthServerConfig
	oauthServer      *oauth.Server
	oauthHandler     *oauthhandler.Handler
	tokenStore       storage.TokenStore
	httpServer       *http.Server
	mcpHandler       http.Handler
	debug            bool
	onAuthenticated  func(ctx context.Context, sessionID string)
	dpopValkeyClient valkeygo.Client // non-nil only when DPoP uses Valkey-backed replay cache
}

// NewOAuthHTTPServer creates a new OAuth-enabled HTTP server that wraps
// the provided MCP handler with authentication protection. Caller-provided
// mcp-oauth options (e.g. token-family lifecycle handlers) are forwarded to
// the underlying OAuth server.
func NewOAuthHTTPServer(cfg config.OAuthServerConfig, mcpHandler http.Handler, debug bool, opts ...oauth.ServerOption) (*OAuthHTTPServer, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("OAuth server is not enabled")
	}

	if err := validateHTTPSRequirement(cfg.BaseURL); err != nil {
		return nil, err
	}

	oauthServer, tokenStore, dpopClient, err := createOAuthServer(cfg, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth server: %w", err)
	}

	oauthHandler := oauthhandler.New(oauthServer, oauthServer.Logger)

	server := &OAuthHTTPServer{
		config:           cfg,
		oauthServer:      oauthServer,
		oauthHandler:     oauthHandler,
		tokenStore:       tokenStore,
		mcpHandler:       mcpHandler,
		debug:            debug,
		dpopValkeyClient: dpopClient,
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

	return s.withAgentExchangeAudience(mux)
}

// grantTypeTokenExchange is the RFC 8693 token-exchange grant.
const grantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"

// maxTokenExchangeBody bounds the request body the audience-injection middleware
// reads. Token-exchange requests are a handful of form fields; this only guards
// against an unbounded read.
const maxTokenExchangeBody = 1 << 20

// withAgentExchangeAudience is a TEMPORARY WORKAROUND (giantswarm/muster#965).
//
// The glean apiserver trusts Dex only (aud=dex-k8s-authenticator). kagent's STS
// exchange presents subject+actor but passes a nil audience, so muster self-issues
// aud=<resourceIdentifier>, which the apiserver cannot validate — forcing
// mcp-kubernetes to impersonate. Injecting the configured broker audience here
// routes the exchange to the matching Targets entry, so muster mints a Dex-signed
// dex-k8s-authenticator token that mcp-kubernetes passes through natively (no
// impersonation). Once kagent-dev/kagent#2106 (Go) + #2107 (Python) add
// KAGENT_STS_AUDIENCE, kagent scopes the token itself and this middleware plus
// TokenExchangeBrokerConfig.DefaultAgentAudience must be deleted.
//
// The injection is scoped to on-behalf-of exchanges (an actor_token is present)
// that carry no audience of their own, so a caller that requests an explicit
// audience is never overridden.
func (s *OAuthHTTPServer) withAgentExchangeAudience(next http.Handler) http.Handler {
	audience := s.config.TokenExchangeBroker.DefaultAgentAudience
	if audience == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isTokenExchangePost(r) {
			raw, err := io.ReadAll(io.LimitReader(r.Body, maxTokenExchangeBody))
			_ = r.Body.Close()
			if err == nil {
				body := raw
				if values, parseErr := url.ParseQuery(string(raw)); parseErr == nil &&
					values.Get("grant_type") == grantTypeTokenExchange &&
					values.Get("actor_token") != "" &&
					values.Get("audience") == "" {
					values.Set("audience", audience)
					body = []byte(values.Encode())
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				r.ContentLength = int64(len(body))
				r.Header.Set("Content-Length", strconv.Itoa(len(body)))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isTokenExchangePost reports whether r is a form-encoded POST to the token endpoint.
func isTokenExchangePost(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/oauth/token" &&
		strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
}

// setupOAuthRoutes registers OAuth 2.1 endpoints on the mux.
//
// Delegates to mcp-oauth's bundle helper, which registers the flow endpoints
// (/oauth/authorize, /oauth/callback, /oauth/token, /oauth/revoke,
// /oauth/register, /oauth/introspect — gated on EnableIntrospectionEndpoint),
// the Protected Resource Metadata routes (RFC 9728) for both the root and
// the /mcp sub-path, and the Authorization Server Metadata routes (RFC 8414
// + OpenID Connect Discovery).
func (s *OAuthHTTPServer) setupOAuthRoutes(mux *http.ServeMux) {
	s.oauthHandler.RegisterOAuthRoutes(mux, oauthhandler.OAuthRoutesOptions{
		MCPPath:         "/mcp",
		IncludeMetadata: true,
	})

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

		// Surface the raw inbound bearer for downstream token forwarding: the
		// aggregator forwards this validated token to backends. It is
		// request-scoped and read per call by the forwarding header func, so
		// inject it before any branch returns and rebind r.
		if bearer := extractBearerToken(r); bearer != "" {
			ctx = ContextWithBearerToken(ctx, bearer)
		}
		r = r.WithContext(ctx)

		// Get user info from context (set by ValidateToken middleware)
		userInfo, ok := oauthhandler.UserInfoFromContext(ctx)
		if !ok || userInfo == nil {
			if s.debug {
				logging.Debug("OAuth", "No user info in context, proceeding without token injection")
			}
			next.ServeHTTP(w, r)
			return
		}

		// Always propagate session ID early so onAuthenticated callbacks can
		// detect sessions with broken refresh chains even when the ID token
		// is missing.
		if sessionID, ok := oauthhandler.SessionIDFromContext(ctx); ok {
			ctx = api.WithSessionID(ctx, sessionID)
			r = r.WithContext(ctx)
		}

		if userInfo.Email == "" {
			// An emailless identity can still be a valid forwarded ID token
			// (e.g. a Kubernetes ServiceAccount identity pre-exchanged at Dex —
			// SA tokens carry a `sub` but no email). The TrustedAudiences
			// forwarded-token path authenticates on `sub`, not email, so try
			// it before giving up. Falls through unchanged when the bearer is
			// not an acceptable forwarded ID token.
			if s.injectExternalIDToken(w, r, ctx, next) {
				return
			}
			if s.debug {
				logging.Debug("OAuth", "User info has no email, proceeding without token injection")
			}
			// The session is authenticated even without an email (e.g. an agent
			// presenting a muster-issued OBO token): fire the callback so its
			// session-scoped backends are established with the validated bearer.
			s.fireOnAuthenticated(ctx)
			next.ServeHTTP(w, r)
			return
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

// acceptTrustedIssuerToken is a test seam around (*oauth.Server).AcceptTrustedIssuerToken.
// Used as a fallback when acceptForwardedIDToken returns ErrTrustedAudienceMismatch.
var acceptTrustedIssuerToken = func(s *oauth.Server, ctx context.Context, bearerToken string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
	return s.AcceptTrustedIssuerToken(ctx, bearerToken)
}

// injectExternalIDToken handles the case where the bearer token was validated
// by mcp-oauth but muster's token store has no entry for it. Two paths are tried:
//
//  1. TrustedAudiences (AcceptForwardedIDToken): a Dex ID token forwarded
//     cross-client whose aud is listed in TrustedAudiences.
//  2. TrustedIssuers (AcceptTrustedIssuerToken): a raw JWT from an external OIDC
//     issuer (e.g. a Kubernetes ServiceAccount projected token) whose aud is
//     muster's own resource identifier. AcceptForwardedIDToken returns
//     ErrTrustedAudienceMismatch for these; the TrustedIssuers path is tried
//     before giving up.
//
// On success the bearer is injected as the ID token, the verified subject and a
// deterministic session ID land in context, and the token is mirrored into the
// OAuth proxy store so downstream SSO forwarding can resolve it later.
//
// Returns true when the request was handled (next.ServeHTTP already called);
// returns false when both paths reject the token and the caller should fall
// through to the existing "no token stored" path.
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
		if errors.Is(err, oauth.ErrTrustedAudienceMismatch) {
			// The bearer is not a TrustedAudiences token. Try the TrustedIssuers
			// path — e.g. a raw Kubernetes SA projected token whose aud is
			// muster's own resource identifier.
			if s.debug {
				logging.Debug("OAuth", "SSO: forwarded ID token audience mismatch, trying TrustedIssuers path")
			}
			acceptance, err = acceptTrustedIssuerToken(s.oauthServer, ctx, bearerToken)
		}
		if err != nil {
			if s.debug {
				logging.Debug("OAuth", "SSO: external token rejected, falling back: %v", err)
			}
			return false
		}
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
			exp, err := pkgoauth.Expiry(bearerToken)
			if err != nil {
				logging.Warn("OAuth",
					"SSO: refusing to mirror forwarded ID token without parseable JWT exp (session=%s, issuer=%s): %v; re-auth required",
					logging.TruncateIdentifier(acceptance.SessionID), issuer, err)
			} else {
				oh.StoreToken(acceptance.SessionID, acceptance.Subject, issuer, &api.OAuthToken{
					IDToken:   bearerToken,
					ExpiresAt: exp,
				})
			}
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

// RefreshSession forces an in-process upstream provider token refresh for the given
// token family. Delegates to the underlying mcp-oauth Server.RefreshSession so that
// TokenRefreshHandler fires and the SSO proxy store is updated before the caller
// re-reads the ID token.
func (s *OAuthHTTPServer) RefreshSession(ctx context.Context, familyID string) error {
	_, err := s.oauthServer.RefreshSession(ctx, familyID)
	return err
}

// GetOAuthHandler returns the OAuth handler for testing or direct access.
func (s *OAuthHTTPServer) GetOAuthHandler() *oauthhandler.Handler {
	return s.oauthHandler
}

// ValidateTokenWithSubject wraps the given handler with OAuth token validation
// and extracts the authenticated user's subject (sub claim) into the context.
// This is used for API endpoints that need to identify the user but don't need
// the full SSO/token-injection logic of the MCP middleware chain.
func (s *OAuthHTTPServer) ValidateTokenWithSubject(next http.Handler) http.Handler {
	return s.oauthHandler.ValidateToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		userInfo, ok := oauthhandler.UserInfoFromContext(ctx)
		if !ok || userInfo == nil || userInfo.ID == "" {
			http.Error(w, "Unauthorized: missing user identity", http.StatusUnauthorized)
			return
		}

		ctx = api.WithSubject(ctx, userInfo.ID)

		// Propagate the session ID from mcp-oauth's ValidateToken middleware.
		if sessionID, ok := oauthhandler.SessionIDFromContext(ctx); ok {
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

	// Close the DPoP replay-cache Valkey client if one was created.
	if s.dpopValkeyClient != nil {
		s.dpopValkeyClient.Close()
	}

	// Shutdown HTTP server if we started one
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// createOAuthServer creates an OAuth server using mcp-oauth library.
// The returned valkeygo.Client is non-nil only when a Valkey-backed DPoP replay
// cache is created; the caller must call Close() on it when done.
func createOAuthServer(cfg config.OAuthServerConfig, opts []oauth.ServerOption) (*oauth.Server, storage.TokenStore, valkeygo.Client, error) {
	if cfg.EnableJWTMode && cfg.JWTSigningKeyFile == "" {
		return nil, nil, nil, fmt.Errorf("enableJWTMode requires jwtSigningKeyFile to be set")
	}

	logger := slog.Default()

	// mcp-oauth v1+ no longer reads a CA installed on http.DefaultTransport for
	// its permissive JWKS / OIDC-discovery clients (private-IP trusted issuers,
	// forwarded-token validation, internal-CA Dex), so build the operator's CA
	// pool once and hand it to each of those clients explicitly. nil (no
	// --extra-ca-file) keeps system-pool verification.
	var caPool *x509.CertPool
	if cfg.ExtraCAFile != "" {
		var poolErr error
		caPool, poolErr = tlsutil.LoadCAPool(cfg.ExtraCAFile)
		if poolErr != nil {
			return nil, nil, nil, fmt.Errorf("load extra CA file for OAuth server: %w", poolErr)
		}
	}

	redirectURL := cfg.BaseURL + "/oauth/callback"
	var provider providers.Provider
	var err error

	switch cfg.Provider {
	case OAuthProviderDex:
		// Build scopes including any required audiences from MCPServers
		scopes := buildDexScopes(api.CollectRequiredAudiences())
		for _, scope := range scopes {
			if audience, ok := strings.CutPrefix(scope, dex.AudienceScopePrefix); ok {
				logger.Info("Requesting cross-client audience from MCPServer requiredAudiences",
					"audience", audience)
			}
		}

		dexConfig := &dex.Config{
			IssuerURL:      cfg.Dex.IssuerURL,
			ClientID:       cfg.Dex.ClientID,
			ClientSecret:   cfg.Dex.ClientSecret,
			RedirectURL:    redirectURL,
			Scopes:         scopes,
			AllowPrivateIP: cfg.Dex.AllowPrivateIPOIDC,
			// Verify an internal-CA Dex during OIDC discovery / token calls
			// against the operator's extra CA (only consulted when
			// AllowPrivateIP is set and no explicit HTTPClient is provided).
			RootCAs: caPool,
		}

		if cfg.Dex.ConnectorID != "" {
			dexConfig.ConnectorID = cfg.Dex.ConnectorID
		}

		provider, err = dex.NewProvider(dexConfig)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to create Dex provider: %w", err)
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
			return nil, nil, nil, fmt.Errorf("failed to create Google provider: %w", err)
		}
		logger.Info("Using Google OAuth provider")

	default:
		return nil, nil, nil, fmt.Errorf("unsupported OAuth provider: %s (supported: %s, %s)", cfg.Provider, OAuthProviderDex, OAuthProviderGoogle)
	}

	// Create storage backend based on configuration. Both memory.Store and
	// valkey.Store satisfy storage.Combined (TokenStore + ClientStore + FlowStore),
	// so a single handle is enough.
	var combinedStore storage.Combined

	switch cfg.Storage.Type {
	case storage.BackendValkey:
		if cfg.Storage.Valkey.URL == "" {
			return nil, nil, nil, fmt.Errorf("valkey URL is required when using valkey storage")
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

		var valkeyOpts []valkey.Option
		if cfg.EncryptionKey != "" {
			keyBytes, err := security.DecodeKey(cfg.EncryptionKey)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to decode encryption key: %w", err)
			}
			encryptor, err := security.NewEncryptor(keyBytes)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to create encryptor: %w", err)
			}
			valkeyOpts = append(valkeyOpts, valkey.WithEncryptor(encryptor))
			logger.Info("Token encryption at rest enabled for Valkey storage (AES-256-GCM)")
		}

		valkeyStore, err := valkey.New(valkeyConfig, valkeyOpts...)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to create Valkey storage: %w", err)
		}

		combinedStore = valkeyStore
		logger.Info("Using Valkey storage backend", "address", cfg.Storage.Valkey.URL)

	case storage.BackendMemory, "":
		var memOpts []memory.Option
		if cfg.EncryptionKey != "" {
			keyBytes, err := security.DecodeKey(cfg.EncryptionKey)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to decode encryption key: %w", err)
			}
			encryptor, err := security.NewEncryptor(keyBytes)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to create encryptor: %w", err)
			}
			memOpts = append(memOpts, memory.WithEncryptor(encryptor))
			logger.Info("Token encryption at rest enabled for in-memory storage (AES-256-GCM)")
		}
		combinedStore = memory.New(memOpts...)
		logger.Info("Using in-memory storage backend")

	default:
		return nil, nil, nil, fmt.Errorf("unsupported OAuth storage type: %s (supported: %s, %s)", cfg.Storage.Type, storage.BackendMemory, storage.BackendValkey)
	}

	refreshTokenTTL := DefaultRefreshTokenTTL
	if cfg.SessionDuration != "" {
		parsed, err := time.ParseDuration(cfg.SessionDuration)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid sessionDuration %q: %w", cfg.SessionDuration, err)
		}
		refreshTokenTTL = parsed
		logger.Info("Using custom session duration", "duration", parsed)
	}

	serverConfig := newOAuthServerConfig(cfg, refreshTokenTTL)
	// Verify the forwarded-ID-token (TrustedAudiences) JWKS endpoint against the
	// operator's extra CA when the issuer is private-IP. nil keeps system-pool.
	serverConfig.JWKSRootCAs = caPool

	if cfg.EnableJWTMode {
		key, kid, alg, err := loadSigningKey(cfg.JWTSigningKeyFile)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("JWT mode enabled but signing key unavailable: %w", err)
		}
		serverConfig.AccessTokenSigningKey = key
		serverConfig.AccessTokenSigningKeyID = kid
		serverConfig.AccessTokenSigningAlgorithm = alg
		logger.Info("JWT mode enabled", "alg", alg, "kid", kid)
	}

	builtOpts, err := buildOAuthServerOptions(cfg, logger, caPool)
	if err != nil {
		return nil, nil, nil, err
	}

	// DPoP replay cache is created here (not inside buildOAuthServerOptions) so
	// that the Valkey client it may create is owned by this function and returned
	// to the caller for proper lifecycle management.
	dpopCache, dpopClient, err := newDPoPReplayCache(cfg.Storage)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create DPoP replay cache: %w", err)
	}
	builtOpts = append(builtOpts, oauthserver.WithDPoPReplayCache(dpopCache))
	builtOpts = append(builtOpts, opts...)

	oauthSrv, err := oauth.NewServerWithCombined(provider, combinedStore, serverConfig, logger, builtOpts...)
	if err != nil {
		if dpopClient != nil {
			dpopClient.Close()
		}
		return nil, nil, nil, fmt.Errorf("failed to create OAuth server: %w", err)
	}

	logEnabledOAuthOptions(logger)

	// Declaratively (re)seed confidential broker clients from mounted secrets so
	// that a wiped client store self-heals. Best-effort; never fails startup.
	seedBrokerClients(context.Background(), oauthSrv, cfg.TokenExchangeBroker, logger)

	return oauthSrv, combinedStore, dpopClient, nil
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
	return stripBearerScheme(r.Header.Get("Authorization"))
}

// stripBearerScheme returns the token portion of a "Bearer <token>" header
// value, or "" when the value is empty or carries no Bearer scheme.
func stripBearerScheme(value string) string {
	if value == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(value) >= len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
		return value[len(prefix):]
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
