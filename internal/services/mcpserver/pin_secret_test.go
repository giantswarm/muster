package mcpserver

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/services"
)

// TestPinSecretWithTheRealAdapter runs a pinned server's start against the
// Kubernetes-backed Secret reader: only a refusal (a protected Secret, or the
// admission policy on Secret references not in force) fails the server; a Secret
// that is read, or one that does not exist yet, leaves it Auth Required,
// where every login retries the pin.
func TestPinSecretWithTheRealAdapter(t *testing.T) {
	const secretName = "github-oauth-client"
	clientSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: "agent-platform"},
		Data:       map[string][]byte{"client-id": []byte("id"), "client-secret": []byte("shh")},
	}
	tests := []struct {
		name      string
		objects   []*corev1.Secret
		opts      []mcpserver.CredentialsOption
		wantState services.ServiceState
		wantErr   error
	}{
		{name: "readable", objects: []*corev1.Secret{clientSecret}, wantState: services.StateAuthRequired},
		{name: "protected", objects: []*corev1.Secret{clientSecret},
			opts:      []mcpserver.CredentialsOption{mcpserver.WithProtectedSecrets(types.NamespacedName{Namespace: "agent-platform", Name: secretName})},
			wantState: services.StateFailed, wantErr: api.ErrSecretProtected},
		{name: "policy not in force", objects: []*corev1.Secret{clientSecret},
			opts:      []mcpserver.CredentialsOption{mcpserver.WithSecretReferencePolicy("")},
			wantState: services.StateFailed, wantErr: api.ErrSecretReferencePolicyInactive},
		{name: "missing", wantState: services.StateAuthRequired},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for _, o := range tc.objects {
				builder = builder.WithObjects(o.DeepCopy())
			}
			prev := api.GetSecretCredentialsHandler()
			api.RegisterSecretCredentialsHandler(mcpserver.NewCredentialsAdapter(builder.Build(), tc.opts...))
			t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

			backend := startHTTPStatusServer(t, http.StatusUnauthorized)
			svc, err := NewService(&api.MCPServer{
				Name: "github", Namespace: "agent-platform", UID: "uid-github",
				Type: api.MCPServerTypeStreamableHTTP, URL: backend.URL + "/mcp",
				Auth: &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
					Issuer:                     "https://github.com/login/oauth",
					ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: secretName},
				}},
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = svc.Stop(context.Background()) })

			err = svc.Start(t.Context())
			assert.Equal(t, tc.wantState, svc.GetState())
			if tc.wantErr == nil {
				assert.True(t, api.IsAuthRequiredError(err), "%v", err)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
			assert.ErrorIs(t, svc.GetLastError(), tc.wantErr)
		})
	}
}
