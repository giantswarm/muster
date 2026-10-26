package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

func newPinTestClient(t *testing.T) *Client {
	t.Helper()
	client := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	t.Cleanup(client.Stop)
	return client
}

func endpointsOf(baseURL string) *pkgoauth.Metadata {
	return &pkgoauth.Metadata{AuthorizationEndpoint: baseURL + "/authorize", TokenEndpoint: baseURL + "/token"}
}

func TestClient_PinIssuer_PinnedMetadataDefaults(t *testing.T) {
	client := newPinTestClient(t)
	key := api.IssuerKey("org-a", unreachableIssuer)

	client.PinIssuer(key, IssuerPin{ClientID: "c"}, unreachablePinnedMetadata())

	metadata, err := client.DiscoverMetadata(t.Context(), key)
	require.NoError(t, err)
	require.Equal(t, unreachableIssuer, metadata.Issuer, "the issuer defaults to the plain issuer of the key")
	require.True(t, metadata.SupportsS256PKCE(), "a pin without code_challenge_methods_supported asserts S256")
}

// Every reader takes the endpoints and the client from one description, also
// while another description replaces it.
func TestClient_IssuerPin_EndpointsAndClientFromOneDescription(t *testing.T) {
	client := newPinTestClient(t)
	tokenEndpoints := map[string]string{"client-a": "https://a.example.com/token", "client-b": "https://b.example.com/token"}
	pin := func(clientID string) {
		client.PinIssuer(unreachableIssuer, IssuerPin{ClientID: clientID, ClientSecret: clientID + "-secret"},
			&pkgoauth.Metadata{AuthorizationEndpoint: "https://as.example.com/authorize", TokenEndpoint: tokenEndpoints[clientID]})
	}
	pin("client-a")

	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 2000 {
			pin([]string{"client-a", "client-b"}[i%2])
		}
	})
	for range 2000 {
		current := client.issuerPin(unreachableIssuer)
		metadata, err := client.metadataFor(t.Context(), unreachableIssuer, current)
		require.NoError(t, err)
		resolved := client.resolveClient(t.Context(), unreachableIssuer, current, metadata, false)
		require.Equal(t, tokenEndpoints[resolved.ClientID], metadata.TokenEndpoint)
		require.Equal(t, resolved.ClientID+"-secret", resolved.ClientSecret)
	}
	wg.Wait()
}

// startPinnedFlow starts a sign-in against issuer and returns its state as the
// callback reads it.
func startPinnedFlow(t *testing.T, client *Client, issuer string) *OAuthState {
	t.Helper()
	startURL, _, err := client.GenerateAuthURL(t.Context(), AuthChallengeParams{
		SessionID: "session-1", UserID: "user-1", ServerName: testServerName, Issuer: issuer, Scope: "repo",
	})
	require.NoError(t, err)
	parsed, err := url.Parse(startURL)
	require.NoError(t, err)
	state := client.stateStore.ValidateState(parsed.Query().Get("state"))
	require.NotNil(t, state)
	return state
}

func TestClient_ExchangeCode_RefusedWhenThePinChanged(t *testing.T) {
	tests := []struct {
		name   string
		change func(client *Client, issuer, otherAS string)
	}{
		{
			name: "another description took over",
			change: func(client *Client, issuer, otherAS string) {
				client.PinIssuer(issuer, IssuerPin{ClientID: "client-b", ClientSecret: "secret-b"}, endpointsOf(otherAS))
			},
		},
		{
			name: "the endpoints changed",
			change: func(client *Client, issuer, otherAS string) {
				client.PinIssuer(issuer, IssuerPin{ClientID: "client-a", ClientSecret: "secret-a"}, endpointsOf(otherAS))
			},
		},
		{
			name: "the pin went",
			change: func(client *Client, issuer, _ string) {
				client.UnpinIssuer(issuer)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokenForm := make(chan url.Values, 2)
			started := newAuthorizationServer(t, authorizationServerOptions{tokenForm: tokenForm})
			defer started.Close()
			other := newAuthorizationServer(t, authorizationServerOptions{tokenForm: tokenForm})
			defer other.Close()
			client := newPinTestClient(t)
			issuer := "https://as.example.com"

			client.PinIssuer(issuer, IssuerPin{ClientID: "client-a", ClientSecret: "secret-a"}, endpointsOf(started.URL))
			state := startPinnedFlow(t, client, issuer)
			tc.change(client, issuer, other.URL)

			_, err := client.ExchangeCode(t.Context(), state, "auth-code")
			require.ErrorIs(t, err, errPinChanged)
			require.Empty(t, tokenForm, "no token endpoint may receive the code or a client secret")
		})
	}
}

// A callback whose description changed since the flow started is refused as
// such, before its iss is judged against the new description, and no token
// endpoint receives the code.
func TestHandler_HandleCallback_RefusedWhenThePinChanged(t *testing.T) {
	tokenForm := make(chan url.Values, 2)
	started := newAuthorizationServer(t, authorizationServerOptions{tokenForm: tokenForm})
	defer started.Close()
	other := newAuthorizationServer(t, authorizationServerOptions{tokenForm: tokenForm})
	defer other.Close()
	client := newPinTestClient(t)
	issuer := "https://as.example.com"

	client.PinIssuer(issuer, IssuerPin{ClientID: "client-a", ClientSecret: "secret-a"}, endpointsOf(started.URL))
	encodedState := storeCallbackState(t, client, issuer)
	client.PinIssuer(issuer, IssuerPin{ClientID: "client-b", ClientSecret: "secret-b", ExpectedIssuer: other.URL}, endpointsOf(other.URL))

	query := url.Values{"code": {"auth-code"}, "state": {encodedState}, "iss": {started.URL}}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oauth/proxy/callback?"+query.Encode(), nil)
	recorder := httptest.NewRecorder()
	NewHandler(client).HandleCallback(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), summaryNotCompleted)
	require.NotContains(t, recorder.Body.String(), summaryUnverifiedResponse)
	require.Empty(t, tokenForm, "no token endpoint may receive the code or a client secret")
}

func TestClient_ExchangeCode_RotatedSecretKeepsTheFlow(t *testing.T) {
	tokenForm := make(chan url.Values, 1)
	as := newAuthorizationServer(t, authorizationServerOptions{tokenForm: tokenForm})
	defer as.Close()
	client := newPinTestClient(t)
	issuer := "https://as.example.com"

	client.PinIssuer(issuer, IssuerPin{ClientID: "client-a", ClientSecret: "old"}, endpointsOf(as.URL))
	state := startPinnedFlow(t, client, issuer)
	client.PinIssuer(issuer, IssuerPin{ClientID: "client-a", ClientSecret: "new"}, endpointsOf(as.URL))

	_, err := client.ExchangeCode(t.Context(), state, "auth-code")
	require.NoError(t, err)
	form := <-tokenForm
	require.Equal(t, "client-a", form.Get("client_id"))
	require.Equal(t, "new", form.Get("client_secret"))
}

func TestStateStores_KeepScopeAndPinDigest(t *testing.T) {
	stores := map[string]func(t *testing.T) StateStorer{
		"memory": func(*testing.T) StateStorer { return NewStateStore() },
		"valkey": func(t *testing.T) StateStorer {
			vc, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{miniredis.RunT(t).Addr()}, DisableCache: true})
			require.NoError(t, err)
			t.Cleanup(vc.Close)
			return NewValkeyStateStore(vc, "muster:", nil)
		},
	}
	for name, newStore := range stores {
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			defer store.Stop()

			encoded, err := store.GenerateState(StateParams{
				SessionID: "session-1", Issuer: "https://as.example.com", Scope: "repo", CodeVerifier: "verifier", PinDigest: "digest",
			}, nil)
			require.NoError(t, err)

			state := store.ValidateState(encoded)
			require.NotNil(t, state)
			require.Equal(t, "repo", state.Scope)
			require.Equal(t, "digest", state.PinDigest)
		})
	}
}
