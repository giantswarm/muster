package api

import (
	"context"
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

	// HasValidToken checks if a valid cached token exists for the endpoint.
	HasValidToken(endpoint string) bool

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

	// RefreshToken forces a token refresh for the endpoint.
	// Returns an error if no token exists or refresh fails.
	RefreshToken(ctx context.Context, endpoint string) error

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

// SetAuthHandlerForTesting sets the auth handler for testing purposes.
// This function bypasses normal registration and should only be used in test code
// to provide mock implementations for unit testing.
//
// Args:
//   - h: AuthHandler implementation for testing
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Testing: This function is intended for test use only and should not be called in production code.
//
// Example:
//
//	mockHandler := &testutils.MockAuthHandler{}
//	api.SetAuthHandlerForTesting(mockHandler)
//	defer api.SetAuthHandlerForTesting(nil) // cleanup
func SetAuthHandlerForTesting(h AuthHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	authHandler = h
}
