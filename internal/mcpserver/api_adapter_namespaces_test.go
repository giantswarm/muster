package mcpserver

import (
	"context"
	"fmt"
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

// namespacedClient holds MCPServer objects of several namespaces and answers
// List and Get like the API server: "" lists every namespace.
type namespacedClient struct {
	client.MusterClient
	servers []musterv1alpha1.MCPServer
}

func (c *namespacedClient) IsKubernetesMode() bool { return true }

func (c *namespacedClient) ListMCPServers(_ context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	var out []musterv1alpha1.MCPServer
	for _, s := range c.servers {
		if namespace == "" || s.Namespace == namespace {
			out = append(out, s)
		}
	}
	return out, nil
}

func (c *namespacedClient) GetMCPServer(_ context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	for i := range c.servers {
		if c.servers[i].Name == name && c.servers[i].Namespace == namespace {
			return &c.servers[i], nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"}, name)
}

func twoGitHubs() *namespacedClient {
	server := func(namespace string) musterv1alpha1.MCPServer {
		return musterv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: namespace},
			Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: "https://mcp.example.com/" + namespace},
		}
	}
	return &namespacedClient{servers: []musterv1alpha1.MCPServer{server("agent-platform"), server("org-a")}}
}

// TestAdapterServesMustersNamespaceByDefault: with the setting off, muster
// lists and gets the servers of its own namespace only, as today.
func TestAdapterServesMustersNamespaceByDefault(t *testing.T) {
	c := twoGitHubs()
	adapter := NewAdapterWithClient(c, "agent-platform")

	servers, err := adapter.ListMCPServers(t.Context())
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "github", servers[0].ServerKey())

	_, err = adapter.GetMCPServer(t.Context(), "org-a/github")
	assert.True(t, api.IsNotFound(err), "a tenant server is unknown while muster serves its own namespace only: %v", err)
}

// TestAdapterKeysServersByNamespaceAndName: with the setting on, both servers
// called github are listed; the one in muster's namespace keeps its bare name
// as its key, the tenant's carries its namespace, and each is got by its key.
func TestAdapterKeysServersByNamespaceAndName(t *testing.T) {
	c := twoGitHubs()
	adapter := NewAdapterWithClient(c, "agent-platform")
	adapter.ServeAllNamespaces()

	servers, err := adapter.ListMCPServers(t.Context())
	require.NoError(t, err)
	keys := make([]string, 0, len(servers))
	for _, s := range servers {
		keys = append(keys, s.ServerKey())
	}
	assert.ElementsMatch(t, []string{"github", "org-a/github"}, keys)

	own, err := adapter.GetMCPServer(t.Context(), "github")
	require.NoError(t, err)
	assert.Equal(t, "agent-platform", own.Namespace)
	assert.Equal(t, "github", own.ToMCPServer().Name, "the service keeps today's name")

	tenant, err := adapter.GetMCPServer(t.Context(), "org-a/github")
	require.NoError(t, err)
	assert.Equal(t, "org-a", tenant.Namespace)
	assert.Equal(t, "github", tenant.Name, "the object keeps its name")
	assert.Equal(t, "org-a/github", tenant.ToMCPServer().Name, "the service is registered under the key")
	assert.Equal(t, "https://mcp.example.com/org-a", tenant.URL)

	_, err = adapter.GetMCPServer(t.Context(), "org-b/github")
	assert.True(t, api.IsNotFound(err))
}

// TestValidateMCPServerRefusesATenantPrefix: a server written through the
// core tools lives in muster's namespace and may not take an org- name or
// toolPrefix.
func TestValidateMCPServerRefusesATenantPrefix(t *testing.T) {
	adapter := NewAdapterWithClient(twoGitHubs(), "agent-platform")
	server := func(name, toolPrefix string) *musterv1alpha1.MCPServer {
		return &musterv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agent-platform"},
			Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: "https://mcp.example.com/mcp", ToolPrefix: toolPrefix},
		}
	}
	require.NoError(t, adapter.validateMCPServer(server("github", "gh")))
	require.ErrorIs(t, adapter.validateMCPServer(server("org-a-github", "")), api.ErrReservedServerName)
	require.ErrorIs(t, adapter.validateMCPServer(server("github", "org-a")), api.ErrReservedServerName)
}

// filesystemClient is the filesystem store: it gives every object the
// namespace "default", whatever muster's namespace is.
type filesystemClient struct{ namespacedClient }

func (c *filesystemClient) IsKubernetesMode() bool { return false }

// ListMCPServers ignores the namespace, as the filesystem store does.
func (c *filesystemClient) ListMCPServers(context.Context, string) ([]musterv1alpha1.MCPServer, error) {
	return c.servers, nil
}

// TestFilesystemModeKeepsBareNames: in filesystem mode with a namespace
// setting other than "default", every server keeps its bare name as its key.
func TestFilesystemModeKeepsBareNames(t *testing.T) {
	c := &filesystemClient{namespacedClient{servers: []musterv1alpha1.MCPServer{{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "default"},
		Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: "https://mcp.example.com/mcp"},
	}}}}
	adapter := NewAdapterWithClient(c, "team-alpha")

	servers, err := adapter.ListMCPServers(t.Context())
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Empty(t, servers[0].Key)
	assert.Equal(t, "github", servers[0].ServerKey())
	assert.Equal(t, "github", servers[0].ToMCPServer().Name)

	// An SSO server of the store's "default" namespace is muster's own.
	sso := c.servers[0].DeepCopy()
	sso.Spec.Auth = &musterv1alpha1.MCPServerAuth{ForwardToken: true}
	require.NoError(t, adapter.validateMCPServer(sso))
}

// TestCoreWritesRefuseATenantKey: core_mcpserver_update and delete write in
// muster's namespace only, and say so for a tenant server's key.
func TestCoreWritesRefuseATenantKey(t *testing.T) {
	adapter := NewAdapterWithClient(twoGitHubs(), "agent-platform")
	for _, tool := range []string{"mcpserver_update", "mcpserver_delete"} {
		result, err := adapter.ExecuteTool(t.Context(), tool, map[string]interface{}{"name": "org-a/github", "type": "streamable-http", "url": "https://mcp.example.com/mcp"})
		require.NoError(t, err)
		require.True(t, result.IsError, tool)
		assert.Contains(t, fmt.Sprint(result.Content...), "writes servers of muster's namespace only", tool)
	}
}
