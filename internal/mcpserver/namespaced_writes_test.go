package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

func TestNamespacedWritesKeepClientCredentialsInTheTargetNamespace(t *testing.T) {
	harness := newCredsHarness(t, nil)
	harness.adapter.SetScope(Scope{Namespaces: labels.Everything()})
	arguments := credsArgs(map[string]interface{}{"name": "organization-a/test-server"})
	message, failed := harness.call(t, "mcpserver_create", arguments)
	require.False(t, failed, message)
	created := &musterv1alpha1.MCPServer{}
	key := ctrlclient.ObjectKey{Namespace: "organization-a", Name: "test-server"}
	require.NoError(t, harness.store.Get(t.Context(), key, created))
	firstSecret := created.Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef.Name
	require.NotEmpty(t, firstSecret)
	require.False(t, created.Spec.Suspended)
	message, failed = harness.call(t, "mcpserver_update", arguments)
	require.False(t, failed, message)
	require.NoError(t, harness.store.Get(t.Context(), key, created))
	require.NotEqual(t, firstSecret, created.Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef.Name)
	secrets := harness.secrets(t)
	require.Len(t, secrets, 2)
	for _, secret := range secrets {
		require.Equal(t, "organization-a", secret.Namespace)
		require.Len(t, secret.OwnerReferences, 1)
		require.Equal(t, created.UID, secret.OwnerReferences[0].UID)
	}
	err := harness.store.Get(t.Context(), ctrlclient.ObjectKey{Namespace: "test-ns", Name: "test-server"}, &musterv1alpha1.MCPServer{})
	require.True(t, apierrors.IsNotFound(err))
}

func TestNamespacedSecretWriteErrorsNameTheTargetNamespace(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			harness := newCredsHarness(t, nil)
			harness.adapter.SetScope(Scope{Namespaces: labels.Everything()})
			arguments := credsArgs(map[string]interface{}{"name": "organization-a/test-server"})
			if operation == "update" {
				message, failed := harness.call(t, "mcpserver_create", arguments)
				require.False(t, failed, message)
			}
			harness.secretErr = apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "client", nil)
			message, failed := harness.call(t, "mcpserver_"+operation, arguments)
			require.True(t, failed)
			require.Contains(t, message, "organization-a")
			require.NotContains(t, message, "test-ns")
		})
	}
}

func TestNamespacedWritesRejectUnsafeKeysAndPlatformIdentity(t *testing.T) {
	for _, name := range []string{"", "/server", "namespace/", "namespace/../server", "../server", "namespace/server\nforged"} {
		for _, operation := range []string{"validate", "create", "update", "delete"} {
			t.Run(operation+"/"+name, func(t *testing.T) {
				harness := newCredsHarness(t, nil)
				harness.adapter.SetScope(Scope{Namespaces: labels.Everything()})
				message, failed := harness.call(t, "mcpserver_"+operation, map[string]interface{}{
					"name": name, "type": "streamable-http", "url": "https://example.com/mcp",
				})
				require.True(t, failed, message)
				require.Empty(t, harness.writes)
			})
		}
	}
	for _, operation := range []string{"validate", "create", "update"} {
		t.Run(operation+"/platform identity", func(t *testing.T) {
			var existing *musterv1alpha1.MCPServer
			if operation == "update" {
				existing = existingServer()
				existing.Namespace, existing.Name = "organization-a", "server"
			}
			harness := newCredsHarness(t, existing)
			harness.adapter.SetScope(Scope{Namespaces: labels.Everything()})
			message, failed := harness.call(t, "mcpserver_"+operation, map[string]interface{}{
				"name": "organization-a/server", "type": "streamable-http", "url": "https://example.com/mcp",
				"auth": map[string]interface{}{"type": "oauth", "forwardToken": true},
			})
			require.True(t, failed)
			require.Contains(t, message, "auth.forwardToken")
			require.Empty(t, harness.writes)
		})
	}
}

func TestConnectorWritesRefuseUnselectedOrMissingNamespaces(t *testing.T) {
	for _, operation := range []string{"validate", "create", "update", "delete"} {
		for _, namespaceCase := range []struct {
			name   string
			exists bool
		}{{name: "unselected", exists: true}, {name: "missing"}} {
			t.Run(operation+"/"+namespaceCase.name, func(t *testing.T) {
				objects := []ctrlclient.Object{}
				if namespaceCase.exists {
					objects = append(objects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "organization-a"}})
				}
				harness := newCredsHarness(t, nil, objects...)
				harness.adapter.SetScope(Scope{Namespaces: labels.SelectorFromSet(labels.Set{"served": "true"})})
				message, failed := harness.call(t, "mcpserver_"+operation, map[string]interface{}{
					"name": "organization-a/server", "type": "streamable-http", "url": "https://example.com/mcp",
				})
				require.True(t, failed)
				require.Contains(t, message, "is not served by muster")
				require.Empty(t, harness.writes)
			})
		}
	}
}

func TestConnectorWritesRespectTheServerSelector(t *testing.T) {
	for _, operation := range []string{"validate", "create", "update", "delete"} {
		for _, name := range []string{"test-server", "test-ns/test-server", "organization-a/test-server"} {
			t.Run(operation+"/"+name, func(t *testing.T) {
				existing := existingServer()
				if name == "organization-a/test-server" {
					existing.Namespace = "organization-a"
				}
				harness := newCredsHarness(t, existing)
				harness.adapter.SetScope(Scope{
					Namespaces: labels.Everything(),
					Servers:    labels.SelectorFromSet(labels.Set{"served": "true"}),
				})
				message, failed := harness.call(t, "mcpserver_"+operation, map[string]interface{}{
					"name": name, "type": "streamable-http", "url": "https://example.com/mcp",
				})
				require.True(t, failed, message)
				require.Empty(t, harness.writes)
			})
		}
	}
}
