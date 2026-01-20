package oauth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenExchanger(t *testing.T) {
	t.Run("creates exchanger with default options", func(t *testing.T) {
		exchanger := NewTokenExchanger()
		require.NotNil(t, exchanger)
		assert.NotNil(t, exchanger.client)
		assert.NotNil(t, exchanger.cache)
		assert.False(t, exchanger.allowPrivateIP)
	})

	t.Run("creates exchanger with custom options", func(t *testing.T) {
		exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{
			AllowPrivateIP:  true,
			CacheMaxEntries: 1000,
		})
		require.NotNil(t, exchanger)
		assert.True(t, exchanger.allowPrivateIP)
	})
}

func TestTokenExchanger_Exchange_Validation(t *testing.T) {
	exchanger := NewTokenExchanger()

	t.Run("returns error for nil request", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exchange request is nil")
	})

	t.Run("returns error for nil config", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token exchange config is nil")
	})

	t.Run("returns error when not enabled", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &TokenExchangeConfig{
				Enabled: false,
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token exchange is not enabled")
	})

	t.Run("returns error for missing subject token", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "subject token is required")
	})

	t.Run("returns error for missing dex token endpoint", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dex token endpoint is required")
	})

	t.Run("returns error for missing connector ID", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "",
			},
			SubjectToken: "test-token",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connector ID is required")
	})

	t.Run("returns error for missing user ID", func(t *testing.T) {
		_, err := exchanger.Exchange(context.Background(), &ExchangeRequest{
			Config: &TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.example.com/token",
				ConnectorID:      "local-dex",
			},
			SubjectToken: "test-token",
			UserID:       "",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user ID is required")
	})
}

func TestTokenExchanger_Cache(t *testing.T) {
	exchanger := NewTokenExchanger()

	t.Run("cache operations work correctly", func(t *testing.T) {
		// Cache stats should start empty
		stats := exchanger.GetCacheStats()
		assert.Equal(t, 0, stats.CurrentEntries)

		// Clear all should not panic on empty cache
		exchanger.ClearAllCache()
		assert.Equal(t, 0, exchanger.cache.Size())

		// Clear specific key should not panic
		exchanger.ClearCache("https://dex.example.com/token", "connector", "user123")
	})

	t.Run("cleanup removes nothing when cache is empty", func(t *testing.T) {
		removed := exchanger.Cleanup()
		assert.Equal(t, 0, removed)
	})
}

func TestTokenExchangeConfig(t *testing.T) {
	t.Run("config struct holds all fields", func(t *testing.T) {
		config := TokenExchangeConfig{
			Enabled:          true,
			DexTokenEndpoint: "https://dex.remote.example.com/token",
			ConnectorID:      "cluster-a-dex",
			Scopes:           "openid profile email groups",
		}

		assert.True(t, config.Enabled)
		assert.Equal(t, "https://dex.remote.example.com/token", config.DexTokenEndpoint)
		assert.Equal(t, "cluster-a-dex", config.ConnectorID)
		assert.Equal(t, "openid profile email groups", config.Scopes)
	})
}
