package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	oauth "github.com/giantswarm/mcp-oauth"
	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	"github.com/giantswarm/mcp-oauth/providers/oidc"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// delegatedUserInfo is what mcp-oauth puts in context for a verified
// trusted-issuer token carrying act: ID is the raw sub, whatever subjectClaim
// maps the session subject to elsewhere.
func delegatedUserInfo() *providers.UserInfo {
	return &providers.UserInfo{
		ID:           "CgdwZXJzb24xEgNkZXg",
		Email:        "person@example.com",
		Issuer:       "https://dex.example.com",
		TokenSource:  providers.TokenSourceTrustedIssuer,
		ActorIssuer:  "https://idp.ate-system.svc",
		ActorSubject: "actor/kagent/session-1",
		ActorChain:   []oidc.ActorClaim{{Issuer: "https://idp.ate-system.svc", Subject: "actor/kagent/session-1"}},
	}
}

func serveInjector(t *testing.T, bearer string, userInfo *providers.UserInfo) (*httptest.ResponseRecorder, context.Context) {
	t.Helper()
	s := &OAuthHTTPServer{config: config.OAuthServerConfig{
		BaseURL: "https://muster.example.com",
		TrustedIssuers: []config.TrustedIssuerConfig{
			{Issuer: "https://dex.example.com", Delegation: &config.TrustedIssuerDelegationConfig{ActorIssuers: []string{"https://idp.ate-system.svc"}}},
			{Issuer: "https://obo.example.com"},
		},
	}}
	var captured context.Context
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { captured = r.Context() })
	r := requestWithBearer(bearer)
	ctx := oauthhandler.ContextWithUserInfo(r.Context(), userInfo)
	ctx = oauthhandler.ContextWithSessionID(ctx, "ext-delegated")
	rec := httptest.NewRecorder()
	s.createAccessTokenInjectorMiddleware(next).ServeHTTP(rec, r.WithContext(ctx))
	return rec, captured
}

func TestServeDelegated(t *testing.T) {
	handler := &capturingOAuthHandler{enabled: true}
	api.RegisterOAuthHandler(handler)
	defer api.RegisterOAuthHandler(nil)

	t.Run("served as the person with the token's toolset", func(t *testing.T) {
		bearer := fakeJWT(t, map[string]interface{}{"sub": "CgdwZXJzb24xEgNkZXg", "scope": "openid email preset:read-only tool:x_k8s_get", "act": map[string]any{"sub": "actor/kagent/session-1"}})
		userInfo := delegatedUserInfo()
		userInfo.Scopes = []string{"openid", "email", "preset:read-only", "tool:x_k8s_get"}
		rec, ctx := serveInjector(t, bearer, userInfo)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, ctx)

		require.Equal(t, "CgdwZXJzb24xEgNkZXg", api.GetSubjectFromContext(ctx), "the raw sub, never an email mapping")
		require.Equal(t, "ext-delegated", api.GetSessionIDFromContext(ctx))
		require.Equal(t, api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "CgdwZXJzb24xEgNkZXg"}, api.SignInIdentityFromContext(ctx))

		bound, present, err := toolset.BoundFromContext(ctx)
		require.NoError(t, err)
		require.True(t, present)
		require.Equal(t, []string{"preset:read-only", "tool:x_k8s_get"}, bound.Raw)

		caller, ok := api.DelegatedCallerFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, bearer, caller.Token)
		require.Equal(t, []string{"actor/kagent/session-1"}, caller.ActorChain)

		require.Empty(t, GetBearerTokenFromContext(ctx), "the delegated token is never put where backends get it forwarded")
		_, hasIDToken := GetIDTokenFromContext(ctx)
		require.False(t, hasIDToken)
		require.Empty(t, handler.stored, "the delegated token is never filed as a login")
	})

	for name, scope := range map[string]string{
		"no scope claim":     "",
		"no selector":        "openid email",
		"reserved selector":  "toolset:shared",
		"malformed selector": "tool:a,b",
	} {
		t.Run(name+" is refused with insufficient_scope", func(t *testing.T) {
			claims := map[string]interface{}{"sub": "CgdwZXJzb24xEgNkZXg"}
			if scope != "" {
				claims["scope"] = scope
			}
			userInfo := delegatedUserInfo()
			userInfo.Scopes = strings.Fields(scope)
			rec, ctx := serveInjector(t, fakeJWT(t, claims), userInfo)
			require.Nil(t, ctx, "next is not called")
			require.Equal(t, http.StatusForbidden, rec.Code)
			challenge := rec.Header().Get("WWW-Authenticate")
			require.Contains(t, challenge, `Bearer error="insufficient_scope"`)
			require.Contains(t, challenge, `resource_metadata="https://muster.example.com/.well-known/oauth-protected-resource"`)
		})
	}

	t.Run("act outside a trusted issuer is refused", func(t *testing.T) {
		userInfo := delegatedUserInfo()
		userInfo.TokenSource = providers.TokenSourceSSO
		rec, ctx := serveInjector(t, fakeJWT(t, map[string]interface{}{"sub": "x", "scope": "preset:read-only"}), userInfo)
		require.Nil(t, ctx)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.Contains(t, rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`)
	})

	// An on-behalf-of token (act) from an issuer without delegation is served
	// as before: its bearer is forwarded to forwardToken servers.
	t.Run("act from an issuer without delegation keeps the forwarding path", func(t *testing.T) {
		defer stubAcceptForwardedIDToken(func(context.Context, string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
			return nil, oauth.ErrTrustedAudienceMismatch
		})()
		defer stubAcceptTrustedIssuerToken(func(context.Context, string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
			return nil, oauthserver.ErrIssuerNotTrusted
		})()
		userInfo := delegatedUserInfo()
		userInfo.Issuer = "https://obo.example.com"
		userInfo.Email = ""
		bearer := fakeJWT(t, map[string]interface{}{"sub": "alice", "act": map[string]any{"sub": "agent:sre-agent"}})
		rec, ctx := serveInjector(t, bearer, userInfo)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, ctx)
		require.Equal(t, bearer, GetBearerTokenFromContext(ctx))
		_, delegated := api.DelegatedCallerFromContext(ctx)
		require.False(t, delegated)
	})

	t.Run("a person's own token keeps today's path", func(t *testing.T) {
		defer stubAcceptForwardedIDToken(func(context.Context, string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
			return nil, oauth.ErrTrustedAudienceMismatch
		})()
		defer stubAcceptTrustedIssuerToken(func(context.Context, string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
			return nil, oauthserver.ErrIssuerNotTrusted
		})()
		bearer := fakeJWT(t, map[string]interface{}{"sub": "CgdwZXJzb24xEgNkZXg"})
		_, ctx := serveInjector(t, bearer, &providers.UserInfo{ID: "CgdwZXJzb24xEgNkZXg"})
		require.NotNil(t, ctx)
		require.Equal(t, bearer, GetBearerTokenFromContext(ctx))
		_, delegated := api.DelegatedCallerFromContext(ctx)
		require.False(t, delegated)
	})
}
