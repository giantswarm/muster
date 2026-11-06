package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// namespacedClient holds MCPServer objects of several namespaces and answers
// List and Get like the API server: "" lists every namespace. It answers
// Namespace reads from namespaces, filtered by a label selector.
type namespacedClient struct {
	client.MusterClient
	servers    []musterv1alpha1.MCPServer
	namespaces []corev1.Namespace
}

func (c *namespacedClient) Get(_ context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, _ ...ctrlclient.GetOption) error {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		return fmt.Errorf("unexpected Get of %T", obj)
	}
	for i := range c.namespaces {
		if c.namespaces[i].Name == key.Name {
			*ns = c.namespaces[i]
			return nil
		}
	}
	return apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, key.Name)
}

func (c *namespacedClient) List(_ context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
	namespaces, ok := list.(*corev1.NamespaceList)
	if !ok {
		return fmt.Errorf("unexpected List of %T", list)
	}
	var listOpts ctrlclient.ListOptions
	listOpts.ApplyOptions(opts)
	for _, ns := range c.namespaces {
		if listOpts.LabelSelector == nil || listOpts.LabelSelector.Matches(labels.Set(ns.Labels)) {
			namespaces.Items = append(namespaces.Items, ns)
		}
	}
	return nil
}

func namespace(name string, nsLabels map[string]string) corev1.Namespace {
	return corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: nsLabels}}
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
			ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: namespace, UID: types.UID("uid-" + namespace)},
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
	adapter.SetScope(Scope{Namespaces: labels.Everything()})

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
	assert.Equal(t, "uid-org-a", tenant.ToMCPServer().UID, "the service carries the object's UID, which the aggregator adopts a tenant server by")

	_, err = adapter.GetMCPServer(t.Context(), "org-b/github")
	assert.True(t, api.IsNotFound(err))
}

// TestAdapterServesTheSelectedNamespaces: with a namespace selector, the
// servers of muster's namespace and of the namespaces whose labels match are
// listed and got by their key; those of other namespaces are unknown.
func TestAdapterServesTheSelectedNamespaces(t *testing.T) {
	c := threeGitHubs()
	c.namespaces = []corev1.Namespace{
		namespace("agent-platform", nil),
		namespace("agents-a-x7k2p", map[string]string{"agent-platform.giantswarm.io/organization": "a"}),
		namespace("org-a", map[string]string{"giantswarm.io/organization": "a"}),
	}
	adapter := NewAdapterWithClient(c, "agent-platform")
	selector, err := labels.Parse("agent-platform.giantswarm.io/organization")
	require.NoError(t, err)
	adapter.SetScope(Scope{Namespaces: selector})

	assert.ElementsMatch(t, []string{"github", "agents-a-x7k2p/github"}, listedKeys(t, adapter))

	served, err := adapter.GetMCPServer(t.Context(), "agents-a-x7k2p/github")
	require.NoError(t, err)
	assert.Equal(t, "agents-a-x7k2p", served.Namespace)

	_, err = adapter.GetMCPServer(t.Context(), "org-a/github")
	assert.True(t, api.IsNotFound(err), "a namespace the selector does not select is not served: %v", err)
	_, err = adapter.GetMCPServer(t.Context(), "gone/github")
	assert.True(t, api.IsNotFound(err), "a namespace that does not exist is not served: %v", err)
}

// TestAdapterServesTheSelectedServers: a server selector applies in muster's
// namespace and in the selected namespaces alike.
func TestAdapterServesTheSelectedServers(t *testing.T) {
	c := threeGitHubs()
	c.servers[0].Labels = map[string]string{"muster.giantswarm.io/served": "true"}
	c.servers[2].Labels = map[string]string{"muster.giantswarm.io/served": "true"}
	adapter := NewAdapterWithClient(c, "agent-platform")
	adapter.SetScope(Scope{Namespaces: labels.Everything(), Servers: labels.SelectorFromSet(labels.Set{"muster.giantswarm.io/served": "true"})})

	assert.ElementsMatch(t, []string{"github", "agents-a-x7k2p/github"}, listedKeys(t, adapter))
	_, err := adapter.GetMCPServer(t.Context(), "org-a/github")
	assert.True(t, api.IsNotFound(err), "an object the server selector does not select is unknown: %v", err)

	own := NewAdapterWithClient(c, "agent-platform")
	own.SetScope(Scope{Servers: labels.SelectorFromSet(labels.Set{"muster.giantswarm.io/served": "false"})})
	assert.Empty(t, listedKeys(t, own), "without a namespace selector the server selector applies in muster's namespace")
	_, err = own.GetMCPServer(t.Context(), "github")
	assert.True(t, api.IsNotFound(err))
}

func threeGitHubs() *namespacedClient {
	c := twoGitHubs()
	c.servers = append(c.servers, musterv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "agents-a-x7k2p", UID: types.UID("uid-agents-a")},
		Spec:       musterv1alpha1.MCPServerSpec{Type: "streamable-http", URL: "https://mcp.example.com/agents-a"},
	})
	return c
}

func listedKeys(t *testing.T, adapter *Adapter) []string {
	t.Helper()
	servers, err := adapter.ListMCPServers(t.Context())
	require.NoError(t, err)
	keys := make([]string, 0, len(servers))
	for _, s := range servers {
		keys = append(keys, s.ServerKey())
	}
	return keys
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
