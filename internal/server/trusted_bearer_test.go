package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestTrustedIssuerDoesNotExposeInboundCredentials(t *testing.T) {
	for _, email := range []string{"", "person@example.com"} {
		t.Run(email, func(t *testing.T) {
			handler := &capturingOAuthHandler{enabled: true}
			api.RegisterOAuthHandler(handler)
			t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
			defer stubAcceptTrustedIssuerToken(func(_ context.Context, bearer string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
				return acceptanceFor(bearer, "canonical-person", email), nil
			})()
			notifications := 0
			s := &OAuthHTTPServer{onAuthenticated: func(context.Context, string) { notifications++ }}
			var captured context.Context
			next := http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) { captured = request.Context() })
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
			request.Header.Set("Authorization", "Bearer resource-access-token")
			info := &providers.UserInfo{ID: "raw-subject", Email: email, Issuer: "https://issuer.example", TokenSource: providers.TokenSourceTrustedIssuer}
			ctx := oauthhandler.ContextWithUserInfo(request.Context(), info)
			ctx = ContextWithCallerTokens(ctx, CallerTokens{IDToken: "inherited-id-token", Bearer: "inherited-bearer"})
			response := httptest.NewRecorder()
			s.createAccessTokenInjectorMiddleware(next).ServeHTTP(response, request.WithContext(ctx))
			require.NotNil(t, captured)
			require.Equal(t, "canonical-person", api.GetSubjectFromContext(captured))
			require.Equal(t, "raw-subject", api.SignInIdentityFromContext(captured).Subject)
			require.Equal(t, CallerTokens{Bearer: "resource-access-token"}, CallerTokensFromContext(captured))
			require.Empty(t, handler.stored)
			require.Equal(t, 1, notifications)
		})
	}
}

func TestTrustedIssuerAcceptanceFailure(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "invalid token", err: errors.New("invalid identity"), status: http.StatusUnauthorized},
		{name: "provider unavailable", err: oauthserver.ErrProviderUnavailable, status: http.StatusServiceUnavailable},
		{name: "storage unavailable", err: oauthserver.ErrStorageUnavailable, status: http.StatusServiceUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			defer stubAcceptTrustedIssuerToken(func(context.Context, string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
				return nil, testCase.err
			})()
			server := &OAuthHTTPServer{onAuthenticated: func(context.Context, string) { t.Error("unexpected authenticated callback") }}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
			request.Header.Set("Authorization", "Bearer resource-access-token")
			info := &providers.UserInfo{ID: "person", TokenSource: providers.TokenSourceTrustedIssuer}
			request = request.WithContext(oauthhandler.ContextWithUserInfo(request.Context(), info))
			response := httptest.NewRecorder()
			server.createAccessTokenInjectorMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("unexpected resource handler")
			})).ServeHTTP(response, request)
			require.Equal(t, testCase.status, response.Code)
			if testCase.status == http.StatusUnauthorized {
				require.Equal(t, `Bearer error="invalid_token"`, response.Header().Get("WWW-Authenticate"))
				require.Empty(t, response.Header().Get("Retry-After"))
			} else {
				require.Empty(t, response.Header().Get("WWW-Authenticate"))
				require.Equal(t, "1", response.Header().Get("Retry-After"))
			}
			require.NotContains(t, response.Body.String(), testCase.err.Error())
		})
	}
}
