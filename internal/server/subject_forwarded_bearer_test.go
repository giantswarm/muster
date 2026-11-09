package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
)

func TestSubjectEndpointValidatesForwardedBearer(t *testing.T) {
	dexServer := startSigningDex(t)
	otherIssuer := startSigningDex(t)
	server, err := NewOAuthHTTPServer(config.OAuthServerConfig{
		Enabled:          true,
		BaseURL:          "https://muster.example.com",
		Provider:         OAuthProviderDex,
		ExtraCAFile:      dexServer.caFile,
		TrustedAudiences: []string{"agent-platform"},
		TrustedIssuers: []config.TrustedIssuerConfig{{
			Issuer:             dexServer.issuer,
			JwksURL:            dexServer.issuer + "/keys",
			AllowedAudiences:   []string{"kagent"},
			AcceptedTypHeaders: []string{""},
			AllowPrivateIPJWKS: true,
			Delegation:         &config.TrustedIssuerDelegationConfig{ActorIssuers: []string{"https://idp.ate-system.svc"}},
		}},
		Dex: config.DexConfig{
			IssuerURL:          dexServer.issuer,
			ClientID:           "muster",
			ClientSecret:       "secret",
			AllowPrivateIPOIDC: true,
		},
	}, http.NotFoundHandler(), false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.WithoutCancel(t.Context()))) })

	expires := time.Now().Add(10 * time.Minute)
	for _, testCase := range []struct {
		name       string
		bearer     string
		statusCode int
	}{
		{"trusted audience", dexServer.idToken(t, "agent-platform", expires), http.StatusNoContent},
		{"trusted issuer", dexServer.idToken(t, "kagent", expires), http.StatusNoContent},
		{"wrong audience", dexServer.idToken(t, "other-client", expires), http.StatusUnauthorized},
		{"expired", dexServer.idToken(t, "agent-platform", time.Now().Add(-10*time.Minute)), http.StatusUnauthorized},
		{"wrong issuer", otherIssuer.idToken(t, "agent-platform", expires), http.StatusUnauthorized},
		{"wrong signature", otherIssuer.idToken(t, "agent-platform", expires, map[string]any{"iss": dexServer.issuer}), http.StatusUnauthorized},
		{"missing bearer", "", http.StatusUnauthorized},
		{"unknown opaque bearer", "unrecognized-access-token", http.StatusUnauthorized},
		{"delegated trusted audience", dexServer.idToken(t, "agent-platform", expires, map[string]any{"act": map[string]any{"sub": "agent:review"}}), http.StatusUnauthorized},
		{"delegated trusted issuer", dexServer.idToken(t, "kagent", expires, map[string]any{"act": map[string]any{"iss": "https://idp.ate-system.svc", "sub": "actor/kagent/session-1"}}), http.StatusForbidden},
		{"DPoP without proof", dexServer.idToken(t, "agent-platform", expires, map[string]any{"cnf": map[string]any{"jkt": "0ZcOCORZNYy-DWpqq30jZyJGHTN0d2HglBV3uiguA4I"}}), http.StatusUnauthorized},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			handler := server.ValidateTokenWithSubject(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				called = true
				require.NotEmpty(t, api.GetSubjectFromContext(request.Context()))
				require.NotEmpty(t, api.GetSessionIDFromContext(request.Context()))
				identity := api.SignInIdentityFromContext(request.Context())
				require.Equal(t, dexServer.issuer, identity.Issuer)
				require.Equal(t, "CiQwOGE4Njg0Yi1kYjg4LTRiNzMtOTBhOS0zY2QxNjYxZjU0NjYSBWxvY2Fs", identity.Subject)
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/session-bindings", nil)
			if testCase.bearer != "" {
				request.Header.Set("Authorization", "Bearer "+testCase.bearer)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			require.Equal(t, testCase.statusCode, response.Code, response.Body.String())
			require.Equal(t, testCase.statusCode == http.StatusNoContent, called)
		})
	}
}
