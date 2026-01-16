package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTokenProviderFunc(t *testing.T) {
	t.Run("implements TokenProvider", func(t *testing.T) {
		var _ TokenProvider = TokenProviderFunc(func(ctx context.Context) string { return "" })
	})

	t.Run("returns token from function", func(t *testing.T) {
		provider := TokenProviderFunc(func(ctx context.Context) string {
			return "test-token"
		})

		token := provider.GetAccessToken(context.Background())
		assert.Equal(t, "test-token", token)
	})

	t.Run("can use context values", func(t *testing.T) {
		type contextKey string
		const tokenKey contextKey = "token"

		provider := TokenProviderFunc(func(ctx context.Context) string {
			if v, ok := ctx.Value(tokenKey).(string); ok {
				return v
			}
			return ""
		})

		ctx := context.WithValue(context.Background(), tokenKey, "context-token")
		token := provider.GetAccessToken(ctx)
		assert.Equal(t, "context-token", token)
	})
}

func TestStaticTokenProvider(t *testing.T) {
	t.Run("returns static token", func(t *testing.T) {
		provider := StaticTokenProvider("static-access-token")

		token := provider.GetAccessToken(context.Background())
		assert.Equal(t, "static-access-token", token)
	})

	t.Run("returns empty string for empty token", func(t *testing.T) {
		provider := StaticTokenProvider("")

		token := provider.GetAccessToken(context.Background())
		assert.Empty(t, token)
	})

	t.Run("ignores context", func(t *testing.T) {
		provider := StaticTokenProvider("fixed-token")

		// Even with cancelled context, should return the token
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		token := provider.GetAccessToken(ctx)
		assert.Equal(t, "fixed-token", token)
	})
}

func TestTokenProviderToHeaderFunc(t *testing.T) {
	t.Run("converts non-empty token to Authorization header", func(t *testing.T) {
		provider := StaticTokenProvider("my-access-token")
		headerFunc := tokenProviderToHeaderFunc(provider)

		headers := headerFunc(context.Background())
		assert.NotNil(t, headers)
		assert.Equal(t, "Bearer my-access-token", headers["Authorization"])
	})

	t.Run("returns nil for empty token", func(t *testing.T) {
		provider := StaticTokenProvider("")
		headerFunc := tokenProviderToHeaderFunc(provider)

		headers := headerFunc(context.Background())
		assert.Nil(t, headers)
	})

	t.Run("dynamic provider can change token", func(t *testing.T) {
		callCount := 0
		provider := TokenProviderFunc(func(ctx context.Context) string {
			callCount++
			return "token-" + string(rune('0'+callCount))
		})
		headerFunc := tokenProviderToHeaderFunc(provider)

		// First call
		headers1 := headerFunc(context.Background())
		assert.Equal(t, "Bearer token-1", headers1["Authorization"])

		// Second call should get new token
		headers2 := headerFunc(context.Background())
		assert.Equal(t, "Bearer token-2", headers2["Authorization"])
	})
}
