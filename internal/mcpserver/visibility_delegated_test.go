package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/callerwrite"
	"github.com/giantswarm/muster/v5/internal/server"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// exchangingOAuthHandler records the token exchange the visibility handler
// asks for and answers with token or err.
type exchangingOAuthHandler struct {
	api.OAuthHandler
	token    string
	err      error
	calls    atomic.Int32
	subject  string
	cacheKey string
	config   api.TokenExchangeConfig
}

func (h *exchangingOAuthHandler) IsEnabled() bool { return true }

func (h *exchangingOAuthHandler) ExchangeTokenForRemoteCluster(_ context.Context, localToken, userID string, config *api.TokenExchangeConfig) (string, error) {
	h.calls.Add(1)
	h.subject, h.cacheKey, h.config = localToken, userID, *config
	return h.token, h.err
}

type staticDelegationExchange struct{ err error }

func (s staticDelegationExchange) DelegationExchangeConfig(api.DelegatedCaller) (api.TokenExchangeConfig, error) {
	if s.err != nil {
		return api.TokenExchangeConfig{}, s.err
	}
	return api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: "https://dex.example.com/token",
		ClientID:         "muster",
		Delegation:       &api.TokenExchangeDelegation{ActorToken: "muster-workload-token"},
	}, nil
}

// tokenFactory records the token each caller client is built with.
type tokenFactory struct{ tokens []string }

func (f *tokenFactory) factory(token string) (ctrlclient.Client, error) {
	f.tokens = append(f.tokens, token)
	scheme := runtime.NewScheme()
	utilruntime.Must(musterv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).Build(), nil
}

func useDelegationExchange(t *testing.T, oauth api.OAuthHandler, exchange api.DelegationExchange) {
	t.Helper()
	api.RegisterOAuthHandler(oauth)
	api.RegisterDelegationExchange(exchange)
	t.Cleanup(func() {
		api.RegisterOAuthHandler(nil)
		api.RegisterDelegationExchange(nil)
	})
}

func delegatedContext(ctx context.Context) context.Context {
	ctx = api.WithSessionID(ctx, "ext-delegated")
	return api.WithDelegatedCaller(ctx, api.DelegatedCaller{Token: "delegated-token", Issuer: "https://dex.example.com", Subject: "alice"})
}

func TestVisibility_DelegatedCaller(t *testing.T) {
	t.Run("lists with a token exchanged for the API server's audience", func(t *testing.T) {
		oauth := &exchangingOAuthHandler{token: testJWT(t, callerwrite.DefaultKubernetesAudience)}
		useDelegationExchange(t, oauth, staticDelegationExchange{})
		calls := &tokenFactory{}
		v := NewVisibility(nil, calls.factory, "", "muster")

		v.CallerVisibility(delegatedContext(t.Context()))

		require.Equal(t, int32(1), oauth.calls.Load())
		require.Equal(t, "delegated-token", oauth.subject)
		require.Equal(t, &api.TokenExchangeDelegation{ActorToken: "muster-workload-token", Audience: callerwrite.DefaultKubernetesAudience}, oauth.config.Delegation)
		require.Equal(t, "delegated/ext-delegated/visibility/"+callerwrite.DefaultKubernetesAudience, oauth.cacheKey, "cached per session and audience")
		require.NotEmpty(t, calls.tokens)
		for _, token := range calls.tokens {
			require.Equal(t, oauth.token, token, "the API server sees the exchanged token, never the delegated one")
		}
	})

	t.Run("an exchange failure shows no server", func(t *testing.T) {
		oauth := &exchangingOAuthHandler{err: errors.New("invalid_request: invalid actor_token")}
		useDelegationExchange(t, oauth, staticDelegationExchange{})
		calls := &tokenFactory{}
		v := NewVisibility(nil, calls.factory, "", "muster")

		view := v.CallerVisibility(delegatedContext(t.Context()))
		require.True(t, view.Restricted())
		require.False(t, view.Sees("github"))
		require.Contains(t, view.Reason, "delegated session")
		require.Empty(t, calls.tokens, "nothing reaches the API server")
	})

	t.Run("without delegation exchange settings no server is visible", func(t *testing.T) {
		useDelegationExchange(t, &exchangingOAuthHandler{}, nil)
		calls := &tokenFactory{}
		view := NewVisibility(nil, calls.factory, "", "muster").CallerVisibility(delegatedContext(t.Context()))
		require.False(t, view.Sees("github"))
		require.Contains(t, view.Reason, "not configured")
		require.Empty(t, calls.tokens)
	})

	t.Run("a person's ID token is used as before, without an exchange", func(t *testing.T) {
		oauth := &exchangingOAuthHandler{}
		useDelegationExchange(t, oauth, staticDelegationExchange{})
		calls := &tokenFactory{}
		idToken := testJWT(t, callerwrite.DefaultKubernetesAudience)

		NewVisibility(nil, calls.factory, "", "muster").CallerVisibility(server.ContextWithIDToken(t.Context(), idToken))

		require.Zero(t, oauth.calls.Load())
		require.NotEmpty(t, calls.tokens)
		require.Equal(t, idToken, calls.tokens[0])
	})
}

func jwtWithExpiry(t *testing.T, aud string, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"sub": "alice", "iss": "https://dex.example.com", "aud": aud, "exp": exp.Unix()})
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// Each exchange of a delegated caller's token for the API server is audited
// like a backend's, without the token; an exchanged token is reused for the
// session until it expires, so a reuse exchanges and audits nothing.
func TestVisibility_DelegatedExchangeIsAudited(t *testing.T) {
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &buf)
	caller := api.DelegatedCaller{Token: "delegated-token", Issuer: "https://dex.example.com", Subject: "alice", ActorChain: []string{"actor/kagent/a"}}
	ctx := api.WithDelegatedCaller(api.WithSessionID(t.Context(), "ext-delegated"), caller)

	t.Run("success, then reuse until expiry", func(t *testing.T) {
		buf.Reset()
		now := time.Unix(1_000_000, 0)
		oauth := &exchangingOAuthHandler{token: jwtWithExpiry(t, callerwrite.DefaultKubernetesAudience, now.Add(10*time.Minute))}
		useDelegationExchange(t, oauth, staticDelegationExchange{})
		calls := &tokenFactory{}
		v := NewVisibility(nil, calls.factory, "", "muster")
		v.now = func() time.Time { return now }

		v.CallerVisibility(ctx)
		line := buf.String()
		require.Contains(t, line, "action=delegated_token_exchange outcome=success")
		require.Contains(t, line, "target="+callerwrite.DefaultKubernetesAudience)
		require.Contains(t, line, "actors=[actor/kagent/a]")
		require.NotContains(t, line, "delegated-token")
		require.NotContains(t, line, oauth.token)

		buf.Reset()
		now = now.Add(visibilityTTL + time.Second)
		v.CallerVisibility(ctx)
		require.Equal(t, int32(1), oauth.calls.Load(), "the exchanged token is reused")
		require.NotContains(t, buf.String(), "delegated_token_exchange")

		now = now.Add(10 * time.Minute)
		v.CallerVisibility(ctx)
		require.Equal(t, int32(2), oauth.calls.Load(), "an expired token is exchanged again")
	})

	t.Run("failure", func(t *testing.T) {
		buf.Reset()
		useDelegationExchange(t, &exchangingOAuthHandler{err: errors.New("invalid_request: invalid actor_token")}, staticDelegationExchange{})
		NewVisibility(nil, (&tokenFactory{}).factory, "", "muster").CallerVisibility(ctx)
		line := buf.String()
		require.Contains(t, line, "action=delegated_token_exchange outcome=failure")
		require.Contains(t, line, "invalid actor_token")
		require.NotContains(t, line, "delegated-token")
	})
}
