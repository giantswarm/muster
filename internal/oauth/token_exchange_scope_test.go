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

func TestTokenExchangerEffectiveScopes(t *testing.T) {
	for _, test := range []struct {
		name, responseScope, expectedScope string
		cached                             bool
	}{
		{name: "omitted scope", expectedScope: "openid groups", cached: true},
		{name: "unchanged scope", responseScope: "openid groups", expectedScope: "openid groups", cached: true},
		{name: "narrower scope", responseScope: "openid", expectedScope: "openid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			downstream := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				require.NoError(t, request.ParseForm())
				require.Equal(t, "openid groups", request.PostForm.Get("scope"))
				response.Header().Set("Content-Type", "application/json")
				body := map[string]any{
					"access_token": "downstream-token", "issued_token_type": "urn:ietf:params:oauth:token-type:id_token",
					"token_type": "N_A", "expires_in": 1800,
				}
				if test.responseScope != "" {
					body["scope"] = test.responseScope
				}
				require.NoError(t, json.NewEncoder(response).Encode(body))
			}))
			t.Cleanup(downstream.Close)
			exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{HTTPClient: downstream.Client(), AllowPrivateIP: true})
			request := &ExchangeRequest{
				Config: &api.TokenExchangeConfig{
					Enabled: true, DexTokenEndpoint: downstream.URL, ConnectorID: "dex", Scopes: "openid groups",
				},
				SubjectToken: "subject", UserID: "person",
			}
			result, err := exchanger.Exchange(t.Context(), request)
			require.NoError(t, err)
			require.Equal(t, test.expectedScope, result.Scope)
			require.False(t, result.FromCache)
			result, err = exchanger.Exchange(t.Context(), request)
			require.NoError(t, err)
			require.Equal(t, test.expectedScope, result.Scope)
			require.Equal(t, test.cached, result.FromCache)
			expectedRequests := int32(2)
			if test.cached {
				expectedRequests = 1
			}
			require.Equal(t, expectedRequests, requests.Load())
		})
	}
}
