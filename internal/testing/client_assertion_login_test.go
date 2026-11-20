package testing

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/testing/mock"
)

func TestFailedMusterLoginPreservesCurrentAssertionCredential(t *testing.T) {
	for _, method := range []string{"none", "private_key_jwt"} {
		t.Run(method, func(t *testing.T) {
			var registrationRequests atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/oauth/register" {
					registrationRequests.Add(1)
				}
				response.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(endpoint.Close)
			currentKey, err := newAssertionKey()
			require.NoError(t, err)
			instance := &MusterInstance{
				ID: "login", Endpoint: endpoint.URL + "/mcp",
				MockOAuthServers:    map[string]*MockOAuthServerInfo{"provider": {UseAsMusterOAuthServer: true}},
				MusterOAuthClientID: "current-client", MusterOAuthAccessToken: "current-access", MusterOAuthRefreshToken: "current-refresh", MusterOAuthAssertionKey: currentKey,
			}
			manager := &musterInstanceManager{mockOAuthServers: map[string]map[string]*mock.OAuthServer{
				instance.ID: {"provider": mock.NewOAuthServer(mock.OAuthServerConfig{ClientID: "upstream-client"})},
			}}
			handler := NewTestToolsHandler(manager, instance, false, NewSilentLogger(false, false))
			_, err = handler.handleMusterAuthLogin(t.Context(), map[string]interface{}{"token_endpoint_auth_method": method})
			require.ErrorContains(t, err, "client registration failed")
			require.EqualValues(t, 1, registrationRequests.Load())
			require.True(t, instance.MusterOAuthAssertionKey == currentKey, "failed login must preserve the signing credential for the current refresh grant")
			require.Equal(t, "current-client", instance.MusterOAuthClientID)
			require.Equal(t, "current-access", instance.MusterOAuthAccessToken)
			require.Equal(t, "current-refresh", instance.MusterOAuthRefreshToken)
		})
	}
}
