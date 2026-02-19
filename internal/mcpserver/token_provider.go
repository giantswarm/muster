package mcpserver

import (
	"context"
)

// TokenProvider is an interface for dynamically providing OAuth access tokens.
// Implementations should return the current valid access token, potentially
// refreshing it if needed. This enables automatic token refresh without
// recreating MCP client connections.
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
