package mcpserver

import (
	"context"
)

// TokenProvider is an interface for dynamically providing OAuth access tokens.
// Implementations should return the current valid access token, potentially
// refreshing it if needed. This enables automatic token refresh without
// recreating MCP client connections.
//
// This interface is a key part of the dynamic token injection pattern (Issue #214):
// - Instead of creating clients with static Authorization headers
// - Clients use a TokenProvider that's called on each HTTP request
// - The TokenProvider can return refreshed tokens transparently
type TokenProvider interface {
	// GetAccessToken returns the current access token for the given context.
	// Returns an empty string if no token is available.
	// The implementation may refresh the token if it's expiring.
	GetAccessToken(ctx context.Context) string
}

// TokenProviderFunc is a function type that implements TokenProvider.
// This allows using simple functions as TokenProviders.
type TokenProviderFunc func(ctx context.Context) string

// GetAccessToken implements TokenProvider.
func (f TokenProviderFunc) GetAccessToken(ctx context.Context) string {
	return f(ctx)
}

// StaticTokenProvider returns a TokenProvider that always returns the same token.
// This is useful for testing or when token refresh is not needed.
func StaticTokenProvider(token string) TokenProvider {
	return TokenProviderFunc(func(_ context.Context) string {
		return token
	})
}

// tokenProviderToHeaderFunc converts a TokenProvider to the mcp-go HTTPHeaderFunc format.
// This adapter allows using our TokenProvider interface with the mcp-go library's
// dynamic header injection capabilities.
func tokenProviderToHeaderFunc(provider TokenProvider) func(context.Context) map[string]string {
	return func(ctx context.Context) map[string]string {
		token := provider.GetAccessToken(ctx)
		if token == "" {
			return nil
		}
		return map[string]string{
			"Authorization": "Bearer " + token,
		}
	}
}
