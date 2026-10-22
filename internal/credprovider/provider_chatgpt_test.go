package credprovider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/giantswarm/muster/v5/pkg/proto/credproviderpb"
)

// A person's ChatGPT login is a provider App grant like GitHub's: its access
// token goes to the egress gateway only for a Session bound to the person
// with chatgpt in its binding, and its refresh token never leaves muster.
func TestFetchSecretReleasesChatGPTOnlyToABoundSession(t *testing.T) {
	const (
		chatgptIssuer = "https://auth.openai.com"
		chatgptAPI    = "ate-secret://muster.giantswarm.io/chatgpt/api"
		bobActor      = "spiffe://substrate-actor.local/actor/kagent/session-bob"
		githubOnly    = "spiffe://substrate-actor.local/actor/kagent/session-github-only"
		unbound       = "spiffe://substrate-actor.local/actor/kagent/session-unbound"
	)
	grants := fakeGrants{
		"alice": {
			chatgptIssuer: {AccessToken: "chatgpt-access-alice", RefreshToken: "chatgpt-refresh-alice", ExpiresAt: time.Now().Add(time.Hour)},
			testIssuer:    {AccessToken: "ghu_alice"},
		},
		"bob": {testIssuer: {AccessToken: "ghu_bob"}},
	}
	store := NewMemoryStore()
	p, err := NewProvider(Options{
		GatewayIdentity: testGateway,
		Releasable:      map[string]string{"github": testIssuer, "chatgpt": chatgptIssuer},
		Store:           store,
		Grants:          grants.source,
		Sessions:        testSessions().lookup,
	})
	require.NoError(t, err)
	pki := newTestPKI(t)
	gateway, _ := pki.leaf(t, "gateway", "", testGateway)
	client := serveTLS(t, p, pki, gateway)
	ctx := context.Background()
	require.NoError(t, store.Put(ctx, testActor, Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{"github", "chatgpt"}}, time.Hour))
	require.NoError(t, store.Put(ctx, bobActor, Binding{Subject: "bob", Session: "kagent/s2", Providers: []string{"github", "chatgpt"}}, time.Hour))
	require.NoError(t, store.Put(ctx, githubOnly, Binding{Subject: "alice", Session: "kagent/s3", Providers: []string{"github"}}, time.Hour))

	resp, err := client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: chatgptAPI, ActorSpiffeId: testActor})
	require.NoError(t, err)
	assert.Equal(t, "chatgpt-access-alice", string(resp.GetOpaqueBytes()), "the bound person's access token, for \"Bearer \"")

	for name, tc := range map[string]struct {
		actor string
		code  codes.Code
	}{
		"a Session of a person who did not connect ChatGPT": {bobActor, codes.NotFound},
		"a Session whose binding lacks chatgpt":             {githubOnly, codes.PermissionDenied},
		"an unbound Session":                                {unbound, codes.PermissionDenied},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: chatgptAPI, ActorSpiffeId: tc.actor})
			require.Error(t, err)
			assert.Equal(t, tc.code, status.Code(err), err.Error())
		})
	}

	t.Run("a disconnect stops the next release", func(t *testing.T) {
		grants["alice"][chatgptIssuer] = nil
		_, err := client.FetchSecret(ctx, &credproviderpb.FetchSecretRequest{Uri: chatgptAPI, ActorSpiffeId: testActor})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}
