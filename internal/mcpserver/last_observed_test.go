package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// flakyClient answers GetMCPServer with server, or with err when set.
type flakyClient struct {
	client.MusterClient
	server *musterv1alpha1.MCPServer
	err    error
}

func (c *flakyClient) IsKubernetesMode() bool { return true }

func (c *flakyClient) GetMCPServer(context.Context, string, string) (*musterv1alpha1.MCPServer, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.server, nil
}

// A successful read is remembered, a failed one keeps it, and a read that
// finds the server deleted forgets it.
func TestAdapter_RemembersTheLastObservedDefinition(t *testing.T) {
	fake := &flakyClient{server: &musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "muster", UID: "uid-1"},
		Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", Approval: &musterv1alpha1.MCPServerApproval{Rule: api.ApprovalAsk}},
	}}
	adapter := NewAdapterWithClient(fake, "muster")

	_, _, ok := adapter.LastObservedMCPServer("github")
	assert.False(t, ok, "never read")

	_, err := adapter.GetMCPServer(context.Background(), "github")
	require.NoError(t, err)

	fake.err = errors.New("dial tcp: connection refused")
	_, err = adapter.GetMCPServer(context.Background(), "github")
	require.Error(t, err)
	info, observedAt, ok := adapter.LastObservedMCPServer("github")
	require.True(t, ok, "a failed read keeps the last definition")
	assert.False(t, observedAt.IsZero())
	assert.Equal(t, "uid-1", info.UID)
	require.NotNil(t, info.Approval)
	assert.Equal(t, api.ApprovalAsk, info.Approval.Rule)

	fake.err = apierrors.NewNotFound(schema.GroupResource{Resource: "mcpservers"}, "github")
	_, err = adapter.GetMCPServer(context.Background(), "github")
	require.True(t, api.IsNotFound(err))
	_, _, ok = adapter.LastObservedMCPServer("github")
	assert.False(t, ok, "a deleted server is forgotten")

	api.RegisterMCPServerManager(adapter)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
	_, _, ok = api.LastObservedMCPServer("github")
	assert.False(t, ok)
}
