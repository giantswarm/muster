package api

import (
	"context"
	"errors"
	"time"
)

// authHandler stores the registered AuthHandler implementation.
var authHandler AuthHandler

// AuthHandler provides OAuth authentication for CLI and agent clients.
// This interface abstracts authentication operations, enabling consistent
// auth handling across all CLI commands while maintaining testability.
//
// Following the project's service locator pattern, this interface is defined
// in the API layer and implemented by adapters in consuming packages.
type AuthHandler interface {
	// CheckAuthRequired probes the endpoint to determine if OAuth is required.
	// Returns true if 401 was received and OAuth flow should be initiated.
	CheckAuthRequired(ctx context.Context, endpoint string) (bool, error)

	// HasCredentials reports whether usable credentials exist for the
	// endpoint: either a non-expired access token or an expired token with
	// a refresh token that the mcp-go transport can use for automatic
	// refresh.
	HasCredentials(endpoint string) bool

	// GetBearerToken returns a valid Bearer token for the endpoint.
	// Returns an error if not authenticated.
	GetBearerToken(endpoint string) (string, error)

	// Login initiates the OAuth flow for the given endpoint.
	// Opens browser and waits for callback completion.
	Login(ctx context.Context, endpoint string) error

	// LoginWithIssuer initiates the OAuth flow for the given endpoint with a known issuer.
	// This is used when the issuer URL is already known (e.g., from a WWW-Authenticate header).
	LoginWithIssuer(ctx context.Context, endpoint, issuerURL string) error

	// Logout clears stored tokens for the endpoint.
	Logout(endpoint string) error

	// LogoutAll clears all stored tokens.
	LogoutAll() error

	// GetStatus returns authentication status for all known endpoints.
	GetStatus() []AuthStatus

	// GetStatusForEndpoint returns authentication status for a specific endpoint.
	GetStatusForEndpoint(endpoint string) *AuthStatus

	// InvalidateCache removes any cached state for the given endpoint.
	// This forces the next status or token lookup to read fresh data from
	// the persistent store. Call this after an external mechanism (e.g.
	// mcp-go's transport) may have refreshed a token outside of this handler.
	InvalidateCache(endpoint string)

	// Close cleans up any resources held by the auth handler.
	Close() error
}

// AuthStatus represents authentication state for a single endpoint.
type AuthStatus struct {
	// Endpoint is the URL of the authenticated endpoint.
	Endpoint string

	// Authenticated indicates whether there is a valid token.
	Authenticated bool

	// ExpiresAt is when the current token expires.
	ExpiresAt time.Time

	// IssuerURL is the OAuth issuer that issued this token.
	IssuerURL string

	// Subject is the authenticated user's subject (sub) claim from the token.
	// This is typically a unique user identifier.
	Subject string

	// Email is the authenticated user's email address (if available in the token).
	Email string

	// HasRefreshToken indicates whether a refresh token is available for this endpoint.
	// If false, the token cannot be refreshed and will require re-authentication when it expires.
	HasRefreshToken bool

	// RefreshExpiresAt is the estimated time when the refresh token (session) expires.
	// This represents the muster-side refresh token expiry, calculated from the token's
	// creation time plus the configured refresh token TTL. The actual session may end
	// earlier if the upstream provider (e.g., Dex) has a shorter absolute lifetime.
	RefreshExpiresAt time.Time

	// Error is non-empty if the auth check failed.
	Error string
}

// RegisterAuthHandler registers the auth handler implementation.
// This handler provides OAuth authentication for CLI commands and agent clients.
//
// The registration is thread-safe and should be called during system initialization.
// Only one auth handler can be registered at a time; subsequent registrations
// will replace the previous handler.
//
// Args:
//   - h: AuthHandler implementation that manages OAuth operations
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := cli.NewAuthAdapter()
//	api.RegisterAuthHandler(adapter)
func RegisterAuthHandler(h AuthHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	authHandler = h
}

// SwapAuthHandler atomically installs h and returns the handler it displaced.
//
// This is the primitive tests should use to isolate themselves from the
// process-global registry. RegisterAuthHandler(nil) clobbers rather than
// restores: a test that resets to nil destroys whatever the enclosing test or
// package had registered. Capture the previous value instead:
//
//	prev := api.SwapAuthHandler(mock)
//	t.Cleanup(func() { api.SwapAuthHandler(prev) })
//
// Args:
//   - h: AuthHandler to install; nil clears the registration
//
// Returns:
//   - AuthHandler: the handler that was registered before this call, or nil
//
// Thread-safe: Yes, protected by handlerMutex.
func SwapAuthHandler(h AuthHandler) AuthHandler {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	prev := authHandler
	authHandler = h
	return prev
}

// UnregisterAuthHandler clears the registration if and only if h is the
// currently registered handler, and reports whether it did.
//
// The compare and the clear happen under one write lock. A plain
// GetAuthHandler-then-RegisterAuthHandler(nil) pair would be two separate
// acquisitions, letting a concurrent registration slip in between and be
// erased by a handler that is no longer current.
//
// Args:
//   - h: the handler expected to be registered
//
// Returns:
//   - bool: true if h was registered and the registration was cleared
//
// Thread-safe: Yes, protected by handlerMutex.
func UnregisterAuthHandler(h AuthHandler) bool {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	if authHandler != h {
		return false
	}
	authHandler = nil
	return true
}

// GetOrRegisterAuthHandler returns the registered auth handler, constructing
// and registering one via newHandler if none is present yet.
//
// The check, the construction, and the publish all happen under a single write
// lock, so concurrent callers run newHandler exactly once and every caller
// observes the same handler. Spelling this as separate GetAuthHandler /
// RegisterAuthHandler calls leaves the composite operation racy even though
// each individual call is synchronized: racing callers each build their own
// handler, the last publish wins, and the losers are orphaned without Close().
//
// IMPORTANT: newHandler runs while handlerMutex is held, and that mutex guards
// every handler registry in this package. newHandler must therefore not call
// any api.GetXxx or api.RegisterXxx function -- doing so deadlocks, because
// sync.RWMutex is not reentrant. Keep the factory to constructing the handler.
//
// Args:
//   - newHandler: factory invoked only when no handler is registered
//
// Returns:
//   - AuthHandler: the registered handler, never nil on success
//   - error: the factory's error, or an error if the factory returned nil
//
// Thread-safe: Yes, protected by handlerMutex.
func GetOrRegisterAuthHandler(newHandler func() (AuthHandler, error)) (AuthHandler, error) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()

	if authHandler != nil {
		return authHandler, nil
	}

	h, err := newHandler()
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, errors.New("auth handler factory returned nil")
	}

	authHandler = h
	return h, nil
}

// GetAuthHandler returns the registered auth handler.
// This provides access to OAuth authentication functionality.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - AuthHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	handler := api.GetAuthHandler()
//	if handler == nil {
//	    return fmt.Errorf("auth handler not available")
//	}
//	if err := handler.Login(ctx, endpoint); err != nil {
//	    return fmt.Errorf("login failed: %w", err)
//	}
func GetAuthHandler() AuthHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return authHandler
}
