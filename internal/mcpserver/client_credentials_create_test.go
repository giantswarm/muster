package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// A server created with clientCredentials starts stopped and is released in
// the update that names its client Secret: no sign-in runs without it.
func TestClientCredentials_CreateStartsStopped(t *testing.T) {
	h := newCredsHarness(t, nil)
	text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
	require.False(t, isErr, text)
	assert.Equal(t, []bool{true}, h.createdSuspended, "created stopped")
	server := h.server(t)
	assert.False(t, server.Spec.Suspended, "released with its client")
	assert.NotNil(t, server.Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef)
}

func TestClientCredentials_CreateWithoutClientIsNotStopped(t *testing.T) {
	h := newCredsHarness(t, nil)
	text, isErr := h.call(t, "mcpserver_create", credsArgs(map[string]interface{}{"clientCredentials": nil}))
	require.False(t, isErr, text)
	assert.Equal(t, []bool{false}, h.createdSuspended)
}

// The rollback deletes only the server it created.
func TestClientCredentials_RollbackDeletesOnlyTheCreatedServer(t *testing.T) {
	h := newCredsHarness(t, nil)
	h.secretErr = forbiddenSecretCreate()
	text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
	require.True(t, isErr)
	assert.Contains(t, text, "so it was not created")
	assert.Equal(t, []string{"create MCPServer", "create Secret", "delete MCPServer"}, h.writes)
	assert.Equal(t, []types.UID{testServerUID}, h.deleteUIDs, "deleted only while it has the UID it was created with")
}

// Another writer's change while the client was stored is left as it is.
func TestClientCredentials_CreateChangedMeanwhile(t *testing.T) {
	for name, change := range map[string]func(ctx context.Context, c ctrlclient.WithWatch){
		"issuer edited": func(ctx context.Context, c ctrlclient.WithWatch) {
			obj := &musterv1alpha1.MCPServer{}
			require.NoError(t, c.Get(ctx, ctrlclient.ObjectKey{Namespace: "test-ns", Name: "test-server"}, obj))
			obj.Spec.Auth.AuthorizationServer.Issuer = "https://other.example.com"
			require.NoError(t, c.Update(ctx, obj))
		},
		"replaced": func(ctx context.Context, c ctrlclient.WithWatch) {
			obj := &musterv1alpha1.MCPServer{}
			require.NoError(t, c.Get(ctx, ctrlclient.ObjectKey{Namespace: "test-ns", Name: "test-server"}, obj))
			require.NoError(t, c.Delete(ctx, obj))
			replacement := generatedServer("someone-elses")
			replacement.UID = "uid-replacement"
			require.NoError(t, c.Create(ctx, replacement))
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCredsHarness(t, nil)
			changed := false
			h.onSecret = func(ctx context.Context, c ctrlclient.WithWatch) {
				if !changed {
					changed = true
					change(ctx, c)
				}
			}
			text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
			require.True(t, isErr)
			assert.Contains(t, text, "changed while its client was stored")
			assert.Contains(t, text, "left as it is")
			assert.NotContains(t, h.writes, "delete MCPServer", "another writer's server is not deleted")
			assert.Empty(t, h.deleteUIDs)
			h.server(t) // still there
		})
	}
}

// clientCredentials is sensitive on both tools: its schema says writeOnly,
// and a workflow execution record does not keep what is passed into it.
func TestClientCredentials_ArgIsSensitive(t *testing.T) {
	adapter := NewAdapterWithClient(&stubMusterClient{}, "test-ns")
	for _, tool := range adapter.GetTools() {
		for _, arg := range tool.Args {
			if arg.Name == "clientCredentials" {
				assert.True(t, arg.Sensitive, tool.Name)
			}
		}
	}
}
