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

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
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

// stubSessionSubjects is an api.SessionSubjectResolver answering from a
// fixed map of subject token to session subject.
type stubSessionSubjects map[string]string

func (s stubSessionSubjects) SessionSubject(_ context.Context, bearer string) (string, error) {
	if sub, ok := s[bearer]; ok {
		return sub, nil
	}
	return "", errors.New("token not accepted")
}

func withSessionSubjects(t *testing.T, subjects stubSessionSubjects) {
	t.Helper()
	prev := api.GetSessionSubjectResolver()
	api.RegisterSessionSubjectResolver(subjects)
	t.Cleanup(func() { api.RegisterSessionSubjectResolver(prev) })
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
	withSessionSubjects(t, stubSessionSubjects{"dex-id-token": "CgUzMjQ4OBIRZ2lhbnRzd2FybS1naXRodWI"})

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

	// The grant was looked up by the token's session subject, here its raw
	// sub (a forwarded ID token), not by the subjectClaim mapping the
	// exchange validation applied, and under the normalised issuer.
	assert.Equal(t, []string{"CgUzMjQ4OBIRZ2lhbnRzd2FybS1naXRodWI|https://github.com/login/oauth"}, handler.lookups)
}

func TestBrokerExchanger_GrantTarget_NoGrantIsInvalidTarget(t *testing.T) {
	handler := &grantReleasingHandler{enabled: true, grants: map[string]*api.OAuthToken{}}
	withOAuthHandler(t, handler)
	withSessionSubjects(t, stubSessionSubjects{"dex-id-token": "someone-else"})

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

// A session served as the trustedIssuers subjectClaim mapping files its
// grants under the mapped subject, so the release looks it up there.
func TestBrokerExchanger_GrantTarget_ReleasesByTheSessionSubject(t *testing.T) {
	handler := &grantReleasingHandler{enabled: true, grants: map[string]*api.OAuthToken{
		"alice@example.com|https://github.com/login/oauth": {AccessToken: "ghu_alice"},
	}}
	withOAuthHandler(t, handler)
	withSessionSubjects(t, stubSessionSubjects{"dex-id-token": "alice@example.com"})

	result, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "github",
		Subject:      dexIdentity("CgVhbGljZRIGZ2l0aHVi", "alice@example.com"),
		SubjectToken: "dex-id-token",
	})
	require.NoError(t, err)
	assert.Equal(t, "ghu_alice", result.AccessToken)
	assert.Equal(t, []string{"alice@example.com|https://github.com/login/oauth"}, handler.lookups)
}

// A subject token muster cannot resolve to a session subject releases
// nothing.
func TestBrokerExchanger_GrantTarget_UnresolvedSubjectIsInvalidTarget(t *testing.T) {
	handler := &grantReleasingHandler{enabled: true, grants: map[string]*api.OAuthToken{}}
	withOAuthHandler(t, handler)
	withSessionSubjects(t, stubSessionSubjects{})

	_, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "github",
		Subject:      dexIdentity("alice", "alice"),
		SubjectToken: "dex-id-token",
	})
	require.ErrorIs(t, err, oauthserver.ErrInvalidTarget)
	assert.Empty(t, handler.lookups)
}

// Only subject tokens of muster's own issuer release a grant unless the
// target lists other issuers: a matching sub from another trusted issuer is
// another person.
func TestBrokerExchanger_GrantTarget_SubjectIssuers(t *testing.T) {
	grants := map[string]*api.OAuthToken{"alice|https://github.com/login/oauth": {AccessToken: "ghu_alice"}}
	otherIssuer := &oauthserver.SubjectIdentity{
		Subject: "alice",
		Issuer:  "https://dex.other.example.com",
		Claims:  &oidc.IDTokenClaims{Claims: jwt.Claims{Subject: "alice", Issuer: "https://dex.other.example.com"}},
	}

	t.Run("default is muster's own issuer", func(t *testing.T) {
		handler := &grantReleasingHandler{enabled: true, grants: grants}
		withOAuthHandler(t, handler)
		withSessionSubjects(t, stubSessionSubjects{"other-token": "alice", "own-token": "alice"})

		_, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{Audience: "github", Subject: otherIssuer, SubjectToken: "other-token"})
		require.ErrorIs(t, err, oauthserver.ErrInvalidTarget)
		assert.Contains(t, err.Error(), "subject_issuer_not_allowed")
		assert.Empty(t, handler.lookups)

		result, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{Audience: "github", Subject: dexIdentity("alice", "alice"), SubjectToken: "own-token"})
		require.NoError(t, err)
		assert.Equal(t, "ghu_alice", result.AccessToken)
	})

	t.Run("listed issuers replace the default", func(t *testing.T) {
		handler := &grantReleasingHandler{enabled: true, grants: grants}
		withOAuthHandler(t, handler)
		withSessionSubjects(t, stubSessionSubjects{"other-token": "alice", "own-token": "alice"})
		broker := newTestBroker(config.TokenExchangeBrokerConfig{Targets: map[string]config.BrokerTargetConfig{
			"github": {GrantIssuer: "https://github.com/login/oauth", SubjectIssuers: []string{"https://dex.other.example.com/"}},
		}}, &http.Client{})

		result, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{Audience: "github", Subject: otherIssuer, SubjectToken: "other-token"})
		require.NoError(t, err)
		assert.Equal(t, "ghu_alice", result.AccessToken)

		_, err = broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{Audience: "github", Subject: dexIdentity("alice", "alice"), SubjectToken: "own-token"})
		require.ErrorIs(t, err, oauthserver.ErrInvalidTarget)
	})
}

func TestBrokerExchanger_GrantTarget_RequiresTheOAuthProxy(t *testing.T) {
	withOAuthHandler(t, &grantReleasingHandler{enabled: false})
	withSessionSubjects(t, stubSessionSubjects{"dex-id-token": "alice"})

	_, err := grantBroker().Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "github",
		Subject:      dexIdentity("alice", "alice"),
		SubjectToken: "dex-id-token",
	})
	require.Error(t, err)
	assert.False(t, errors.Is(err, oauthserver.ErrInvalidTarget), "a disabled proxy is a server fault, not invalid_target")
}

func TestBrokerTargetConfig_IsGrantTarget(t *testing.T) {
	assert.True(t, config.BrokerTargetConfig{GrantIssuer: "https://github.com/login/oauth"}.IsGrantTarget())
	assert.False(t, config.BrokerTargetConfig{DexTokenEndpoint: "https://dex.example.com/token"}.IsGrantTarget())
}
