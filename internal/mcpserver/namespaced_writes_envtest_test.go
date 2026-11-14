package mcpserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"

	"github.com/giantswarm/muster/v5/internal/callerwrite"
	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
	"github.com/giantswarm/muster/v5/internal/server"
)

func testNamespacedConnectorWrites(t *testing.T, adminConfiguration *rest.Config) {
	t.Helper()
	client, err := kubernetesclient.New(adminConfiguration)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	adapter := NewAdapterWithClient(client, "default")
	adapter.SetScope(Scope{Namespaces: labels.SelectorFromSet(labels.Set{organizationLabel: "true"})})
	adapter.EnableWritesAsCaller(callerwrite.NewKubernetesClientFactory(adminConfiguration), "")
	callers := []struct {
		user      string
		namespace string
		context   context.Context
	}{
		{"allowed-user", "organization-a", server.ContextWithIDToken(t.Context(), testJWT(t, callerwrite.DefaultKubernetesAudience))},
		{"denied-user", "organization-b", server.ContextWithIDToken(t.Context(), testJWT(t, []string{callerwrite.DefaultKubernetesAudience, "muster"}))},
	}
	for _, caller := range callers {
		require.NoError(t, client.Create(t.Context(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: caller.namespace, Labels: map[string]string{organizationLabel: "true"}},
		}))
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "connector-admin", Namespace: caller.namespace},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{"muster.giantswarm.io"}, Resources: []string{"mcpservers"},
				Verbs: []string{"get", "list", "create", "update", "delete"},
			}},
		}
		require.NoError(t, client.Create(t.Context(), role))
		require.NoError(t, client.Create(t.Context(), &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: caller.namespace},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: caller.user}},
		}))
		arguments := map[string]interface{}{
			"name": caller.namespace + "/shared-name", "type": "streamable-http", "url": "https://example.com/mcp",
		}
		for _, tool := range []string{"mcpserver_validate", "mcpserver_create"} {
			result, err := adapter.ExecuteTool(caller.context, tool, arguments)
			require.NoError(t, err)
			require.False(t, result.IsError, "%s: %s", tool, resultText(t, result))
		}
		created, err := client.GetMCPServer(t.Context(), "shared-name", caller.namespace)
		require.NoError(t, err)
		require.Equal(t, caller.namespace, created.Namespace)
		result, err := adapter.ExecuteTool(caller.context, "mcpserver_update", map[string]interface{}{
			"name": caller.namespace + "/shared-name", "url": "https://example.com/updated",
		})
		require.NoError(t, err)
		require.False(t, result.IsError, resultText(t, result))
	}
	for index, caller := range callers {
		other := callers[1-index]
		for _, tool := range []string{"mcpserver_create", "mcpserver_update", "mcpserver_delete"} {
			name := "shared-name"
			if tool == "mcpserver_create" {
				name = "forbidden-create"
			}
			result, err := adapter.ExecuteTool(caller.context, tool, map[string]interface{}{
				"name": other.namespace + "/" + name, "type": "streamable-http", "url": "https://example.com/forbidden",
			})
			require.NoError(t, err)
			require.True(t, result.IsError, "%s unexpectedly succeeded for %s", tool, caller.user)
			require.Contains(t, resultText(t, result), "Permission denied")
		}
		unchanged, err := client.GetMCPServer(t.Context(), "shared-name", other.namespace)
		require.NoError(t, err)
		require.Equal(t, "https://example.com/updated", unchanged.Spec.URL)
		_, err = client.GetMCPServer(t.Context(), "forbidden-create", other.namespace)
		require.True(t, apierrors.IsNotFound(err), "denied create wrote an object: %v", err)
	}
	for _, caller := range callers {
		result, err := adapter.ExecuteTool(caller.context, "mcpserver_delete", map[string]interface{}{"name": caller.namespace + "/shared-name"})
		require.NoError(t, err)
		require.False(t, result.IsError, resultText(t, result))
		_, err = client.GetMCPServer(t.Context(), "shared-name", caller.namespace)
		require.True(t, apierrors.IsNotFound(err))
	}
}
