package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/config"
)

// serveExchange runs a form-encoded POST through withAgentExchangeAudience and
// returns the audience the downstream handler observed after re-parsing the body.
func serveExchange(t *testing.T, defaultAudience string, form url.Values) (seenAudience string, downstreamHit bool) {
	t.Helper()
	s := &OAuthHTTPServer{config: config.OAuthServerConfig{
		TokenExchangeBroker: config.TokenExchangeBrokerConfig{DefaultAgentAudience: defaultAudience},
	}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamHit = true
		require.NoError(t, r.ParseForm())
		seenAudience = r.PostForm.Get("audience")
	})
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.withAgentExchangeAudience(next).ServeHTTP(httptest.NewRecorder(), req)
	return seenAudience, downstreamHit
}

func TestWithAgentExchangeAudience(t *testing.T) {
	obo := url.Values{
		"grant_type":    {grantTypeTokenExchange},
		"subject_token": {"user.jwt"},
		"actor_token":   {"agent.jwt"},
	}

	t.Run("injects audience for an on-behalf-of exchange with none", func(t *testing.T) {
		seen, hit := serveExchange(t, "glean", obo)
		require.True(t, hit)
		require.Equal(t, "glean", seen)
	})

	t.Run("does not override an explicit audience", func(t *testing.T) {
		form := url.Values{
			"grant_type":    {grantTypeTokenExchange},
			"subject_token": {"user.jwt"},
			"actor_token":   {"agent.jwt"},
			"audience":      {"explicit"},
		}
		seen, _ := serveExchange(t, "glean", form)
		require.Equal(t, "explicit", seen)
	})

	t.Run("does not inject without an actor token", func(t *testing.T) {
		form := url.Values{
			"grant_type":    {grantTypeTokenExchange},
			"subject_token": {"user.jwt"},
		}
		seen, _ := serveExchange(t, "glean", form)
		require.Empty(t, seen)
	})

	t.Run("no-op when DefaultAgentAudience is empty", func(t *testing.T) {
		seen, hit := serveExchange(t, "", obo)
		require.True(t, hit)
		require.Empty(t, seen)
	})
}
