package oauth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/mcp-oauth/providers/oidc"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

// grantReleasingHandler is an api.OAuthHandler test double that also
// implements api.GrantReleaser: it records the lookups it served and answers
// from a fixed map of grants keyed by "<sub>|<issuer>".
type grantReleasingHandler struct {
	api.OAuthHandler
	enabled bool
	grants  map[string]*api.OAuthToken
	lookups []string
}

func (h *grantReleasingHandler) IsEnabled() bool { return h.enabled }

func (h *grantReleasingHandler) SubjectGrantForRelease(_ context.Context, userID, issuer string) *api.OAuthToken {
	h.lookups = append(h.lookups, userID+"|"+issuer)
	return h.grants[userID+"|"+issuer]
}

func withOAuthHandler(t *testing.T, h api.OAuthHandler) {
	t.Helper()
	prev := api.GetOAuthHandler()
	api.RegisterOAuthHandler(h)
	t.Cleanup(func() { api.RegisterOAuthHandler(prev) })
}

func grantBroker() *BrokerExchanger {
	return newTestBroker(config.TokenExchangeBrokerConfig{
		Targets: map[string]config.BrokerTargetConfig{
			"github": {GrantIssuer: "https://github.com/login/oauth/"},
		},
	}, &http.Client{})
}

func dexIdentity(rawSub, mappedSubject string) *oauthserver.SubjectIdentity {
	return &oauthserver.SubjectIdentity{
		Subject: mappedSubject,
		Issuer:  "https://dex.example.com",
		Claims:  &oidc.IDTokenClaims{Claims: jwt.Claims{Subject: rawSub, Issuer: "https://dex.example.com"}},
	}
}

func TestBrokerExchanger_GrantTarget_ReleasesTheGrant(t *testing.T) {
	expires := time.Now().Add(7 * time.Hour).Truncate(time.Second)
	handler := &grantReleasingHandler{enabled: true, grants: map[string]*api.OAuthToken{
		"CgUzMjQ4OBIRZ2lhbnRzd2FybS1naXRodWI|https://github.com/login/oauth": {
			AccessToken: "ghu_live", RefreshToken: "ghr_secret", ExpiresAt: expires, Scope: "repo read:org",
		},
	}}
	withOAuthHandler(t, handler)

	result, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "github",
		ClientID:     "devportal",
		Subject:      dexIdentity("CgUzMjQ4OBIRZ2lhbnRzd2FybS1naXRodWI", "timo@giantswarm.io"),
		SubjectToken: "dex-id-token",
	})
	require.NoError(t, err)
	assert.Equal(t, "ghu_live", result.AccessToken)
	assert.Equal(t, oauthserver.SubjectTokenTypeAccessToken, result.IssuedTokenType)
	assert.True(t, result.ExpiresAt.Equal(expires), "expiry is the grant's own")
	assert.Equal(t, "repo read:org", result.Scope)

	// The grant was looked up by the token's raw sub, not by the
	// trustedIssuers subjectClaim mapping, and under the normalised issuer.
	assert.Equal(t, []string{"CgUzMjQ4OBIRZ2lhbnRzd2FybS1naXRodWI|https://github.com/login/oauth"}, handler.lookups)
}

func TestBrokerExchanger_GrantTarget_NoGrantIsInvalidTarget(t *testing.T) {
	handler := &grantReleasingHandler{enabled: true, grants: map[string]*api.OAuthToken{}}
	withOAuthHandler(t, handler)

	_, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "github",
		ClientID:     "devportal",
		Subject:      dexIdentity("someone-else", "someone@giantswarm.io"),
		SubjectToken: "dex-id-token",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, oauthserver.ErrInvalidTarget), "a missing grant is invalid_target, got: %v", err)
	assert.Contains(t, err.Error(), "no_grant")
}

func TestBrokerExchanger_GrantTarget_FallsBackToMappedSubjectWithoutClaims(t *testing.T) {
	handler := &grantReleasingHandler{enabled: true, grants: map[string]*api.OAuthToken{
		"alice|https://github.com/login/oauth": {AccessToken: "ghu_alice"},
	}}
	withOAuthHandler(t, handler)

	result, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience: "github",
		Subject:  &oauthserver.SubjectIdentity{Subject: "alice"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ghu_alice", result.AccessToken)
}

func TestBrokerExchanger_GrantTarget_RequiresTheOAuthProxy(t *testing.T) {
	withOAuthHandler(t, &grantReleasingHandler{enabled: false})

	_, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience: "github",
		Subject:  dexIdentity("alice", "alice"),
	})
	require.Error(t, err)
	assert.False(t, errors.Is(err, oauthserver.ErrInvalidTarget), "a disabled proxy is a server fault, not invalid_target")
}

func TestBrokerTargetConfig_IsGrantTarget(t *testing.T) {
	assert.True(t, config.BrokerTargetConfig{GrantIssuer: "https://github.com/login/oauth"}.IsGrantTarget())
	assert.False(t, config.BrokerTargetConfig{DexTokenEndpoint: "https://dex.example.com/token"}.IsGrantTarget())
}
