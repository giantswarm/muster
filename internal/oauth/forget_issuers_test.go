package oauth

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// ForgetTenantIssuers removes, on both storage backends, everything filed
// under a namespace's issuer keys -- the tokens of every session, the
// people's subject grants, the client registrations and the flows in progress -- and
// nothing of another namespace or of muster's own.
func TestClient_ForgetTenantIssuers(t *testing.T) {
	const (
		issuer = "https://idp.example.com"
		orgB   = "ns:org-b:" + issuer
		orgB2  = "ns:org-b:https://other.example.com"
		orgA   = "ns:org-a:" + issuer
	)
	backends := map[string]func(t *testing.T) []ClientOption{
		"memory": func(*testing.T) []ClientOption { return nil },
		"valkey": func(t *testing.T) []ClientOption {
			srv := miniredis.RunT(t)
			vc, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
			require.NoError(t, err)
			t.Cleanup(vc.Close)
			return []ClientOption{
				WithTokenStorer(NewValkeyTokenStore(vc, time.Hour, "muster:", nil)),
				WithStateStorer(NewValkeyStateStore(vc, "muster:", nil)),
				WithClientCredentialStorer(NewValkeyClientCredentialStore(vc, "muster:", nil)),
			}
		},
	}
	tests := []struct {
		name       string
		issuerKeys []string
		forgotten  []string
	}{
		{name: "the whole namespace", forgotten: []string{orgB, orgB2}},
		{name: "one issuer key", issuerKeys: []string{orgB}, forgotten: []string{orgB}},
		{name: "a key of another namespace is not the namespace's", issuerKeys: []string{orgA}},
	}
	for backend, options := range backends {
		for _, tc := range tests {
			t.Run(backend+": "+tc.name, func(t *testing.T) {
				c := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid", options(t)...)
				t.Cleanup(c.Stop)
				all := []string{orgB, orgB2, orgA, issuer}
				states := make(map[string]string)
				for _, key := range all {
					// Subject-scoped: each sign-in is the person's grant as well.
					c.PinIssuer(key, IssuerPin{SubjectScoped: true}, nil)
					for _, user := range []string{"alice", "bob"} {
						c.StoreToken(user+"-1", user, &pkgoauth.Token{AccessToken: "at", Issuer: key, ExpiresAt: time.Now().Add(time.Hour)})
						c.StoreToken(user+"-2", user, &pkgoauth.Token{AccessToken: "at", Issuer: key, ExpiresAt: time.Now().Add(time.Hour)})
					}
					c.credStore.Store(key, &pkgoauth.ClientCredentials{ClientID: "dcr-" + key})
					encoded, err := c.stateStore.GenerateState(StateParams{SessionID: "alice-1", UserID: "alice", ServerName: "s", Issuer: key}, nil)
					require.NoError(t, err)
					states[key] = encoded
				}

				require.NoError(t, c.ForgetTenantIssuers(context.Background(), "org-b", tc.issuerKeys))

				gone := make(map[string]bool)
				for _, key := range tc.forgotten {
					gone[key] = true
				}
				for _, key := range all {
					for _, session := range []string{"alice-1", "alice-2", "bob-1", "bob-2", subjectSessionID("alice"), subjectSessionID("bob")} {
						assert.Equal(t, !gone[key], c.tokenStore.GetByIssuer(session, key) != nil, "token of %s in %s", key, session)
					}
					assert.Equal(t, !gone[key], c.credStore.Get(key) != nil, "registration of %s", key)
					assert.Equal(t, !gone[key], c.stateStore.ValidateState(states[key]) != nil, "flow of %s", key)
				}
			})
		}
	}
}

// muster's own namespace has no namespace: nothing is forgotten for it.
func TestClient_ForgetTenantIssuers_MustersNamespace(t *testing.T) {
	c := NewClient("https://muster.example.com/.well-known/oauth-client.json", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	t.Cleanup(c.Stop)
	c.StoreToken("s", "alice", &pkgoauth.Token{AccessToken: "at", Issuer: "https://idp.example.com", ExpiresAt: time.Now().Add(time.Hour)})

	require.NoError(t, c.ForgetTenantIssuers(context.Background(), "", nil))
	assert.NotNil(t, c.tokenStore.GetByIssuer("s", "https://idp.example.com"))
}
