package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/giantswarm/muster/v5/internal/api"
)

// githubGenerated is a generated client Secret name for githubServer.
const githubGenerated = "github-oauth-client-x1y2z"

func ownerRefTo(uid, kind string) func(*corev1.Secret) {
	return func(s *corev1.Secret) {
		s.OwnerReferences = append(s.OwnerReferences, metav1.OwnerReference{
			APIVersion: "muster.giantswarm.io/v1alpha1", Kind: kind, Name: "github", UID: types.UID(uid),
		})
	}
}

// githubDestination is the authorization server githubClientOwner's client is
// read for.
var githubDestination = &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}

// githubClientOwner is githubServer reading the client of githubDestination.
var githubClientOwner = githubServer.ForAuthorizationServer(githubDestination)

func storedFor(as *api.MCPServerAuthAuthorizationServer) func(*corev1.Secret) {
	return func(s *corev1.Secret) {
		s.Annotations = map[string]string{api.AuthorizationServerAnnotation: api.AuthorizationServerDigest(as)}
	}
}

func TestReadCheck_GeneratedClientSecret(t *testing.T) {
	owned := ownerRefTo("uid-github", "MCPServer")
	here := storedFor(githubDestination)
	tests := []struct {
		name    string
		secret  *corev1.Secret
		owner   api.SecretOwner
		field   string
		refused error
	}{
		{name: "owned, for this authorization server", secret: bindingSecret(githubGenerated, owned, here), owner: githubClientOwner},
		{name: "unowned", secret: bindingSecret(githubGenerated, here), owner: githubClientOwner, refused: api.ErrSecretNotOwned},
		{name: "owned by another server", secret: bindingSecret(githubGenerated, ownerRefTo("uid-other", "MCPServer"), here), owner: githubClientOwner, refused: api.ErrSecretNotOwned},
		{name: "owned by another kind", secret: bindingSecret(githubGenerated, ownerRefTo("uid-github", "Workflow"), here), owner: githubClientOwner, refused: api.ErrSecretNotOwned},
		{name: "owner without UID", secret: bindingSecret(githubGenerated, ownerRefTo("", "MCPServer"), here),
			owner: api.SecretOwner{Name: "github", Namespace: "agent-platform"}.ForAuthorizationServer(githubDestination), refused: api.ErrSecretNotOwned},
		{name: "stored for another authorization server", owner: githubClientOwner, refused: api.ErrSecretOtherAuthorizationServer,
			secret: bindingSecret(githubGenerated, owned, storedFor(&api.MCPServerAuthAuthorizationServer{Issuer: "https://evil.example.com"}))},
		{name: "stored for no authorization server", secret: bindingSecret(githubGenerated, owned), owner: githubClientOwner, refused: api.ErrSecretOtherAuthorizationServer},
		{name: "read for no authorization server", secret: bindingSecret(githubGenerated, owned, here), owner: githubServer, refused: api.ErrSecretOtherAuthorizationServer},
		{name: "a name not generated reads as before", secret: bindingSecret("github-app"), owner: githubServer},
		{name: "token exchange reads as before", secret: bindingSecret(githubGenerated), owner: githubServer, field: api.SecretFieldTokenExchange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := bindingAdapter(t, tt.secret)
			field := tt.field
			if field == "" {
				field = api.SecretFieldAuthorizationServer
			}
			creds, err := adapter.LoadMCPServerClientCredentials(context.Background(), tt.owner, field, &api.ClientCredentialsSecretRef{Name: tt.secret.Name})
			if tt.refused != nil {
				require.ErrorIs(t, err, tt.refused)
				assert.Contains(t, err.Error(), "core_mcpserver_update clientCredentials")
				assert.Nil(t, creds)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "id", creds.ClientID)
		})
	}

	t.Run("in another namespace", func(t *testing.T) {
		secret := bindingSecret(githubGenerated, owned, here)
		secret.Namespace = "elsewhere"
		adapter := bindingAdapter(t, secret)
		_, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubClientOwner, api.SecretFieldAuthorizationServer,
			&api.ClientCredentialsSecretRef{Name: githubGenerated, Namespace: "elsewhere"})
		require.ErrorIs(t, err, api.ErrSecretNotOwned)
	})
}
