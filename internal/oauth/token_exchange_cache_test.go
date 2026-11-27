package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestTokenExchangerScopeCacheIsolationAndInvalidation(t *testing.T) {
	var requests atomic.Int32
	downstream := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if err := request.ParseForm(); err != nil {
			http.Error(response, "invalid form", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(map[string]any{
			"access_token": request.PostForm.Get("scope"), "issued_token_type": "urn:ietf:params:oauth:token-type:id_token",
			"token_type": "N_A", "expires_in": 1800,
		}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(downstream.Close)
	exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{HTTPClient: downstream.Client(), AllowPrivateIP: true})
	request := &ExchangeRequest{
		Config:       &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: downstream.URL, ConnectorID: "dex", Scopes: "openid groups"},
		SubjectToken: "subject", UserID: "person",
	}
	first, err := exchanger.Exchange(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "openid groups", first.Scope)
	request.Config.Scopes = "openid email"
	second, err := exchanger.Exchange(t.Context(), request)
	require.NoError(t, err)
	require.False(t, second.FromCache)
	require.Equal(t, "openid email", second.Scope)
	require.NotEqual(t, first.AccessToken, second.AccessToken)
	second, err = exchanger.Exchange(t.Context(), request)
	require.NoError(t, err)
	require.True(t, second.FromCache)
	require.Equal(t, "openid email", second.Scope)
	exchanger.ClearCache(request)
	second, err = exchanger.Exchange(t.Context(), request)
	require.NoError(t, err)
	require.False(t, second.FromCache)
	require.Equal(t, int32(3), requests.Load())
}
