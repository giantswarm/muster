package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/callerwrite"
	"github.com/giantswarm/muster/v5/internal/server"
)

const (
	testClientID     = "hubspot-client-id"
	testClientSecret = "s3cr3t-never-shown" //nolint:gosec // G101: a test value
	testServerUID    = types.UID("uid-test-server")
)

// credsHarness is a caller-identity adapter whose writes land in a fake API
// server, which the SA-side reads see too. Every write is recorded by verb
// and kind; secretErrs fail the next Secret creates in turn, then secretErr
// every one; deleteErr and updateErr fail the matching write.
type credsHarness struct {
	adapter    *Adapter
	sa         *stubMusterClient
	store      ctrlclient.Client
	writes     []string
	secretErrs []error
	secretErr  error
	deleteErr  error
	updateErr  error
}

// storeReader is the SA client of credsHarness: it reads MCPServers from the
// fake API server the caller writes to.
type storeReader struct {
	*stubMusterClient
	store ctrlclient.Client
}

func (r *storeReader) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	obj := &musterv1alpha1.MCPServer{}
	if err := r.store.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: name}, obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func newCredsHarness(t *testing.T, existing *musterv1alpha1.MCPServer, objects ...ctrlclient.Object) *credsHarness {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, musterv1alpha1.AddToScheme(scheme))

	h := &credsHarness{sa: &stubMusterClient{}}
	kind := func(obj ctrlclient.Object) string {
		if _, ok := obj.(*corev1.Secret); ok {
			return "Secret"
		}
		return "MCPServer"
	}
	if existing != nil {
		objects = append(objects, existing)
	}
	h.store = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
			h.writes = append(h.writes, "create "+kind(obj))
			if _, ok := obj.(*corev1.Secret); ok {
				if len(h.secretErrs) > 0 {
					err := h.secretErrs[0]
					h.secretErrs = h.secretErrs[1:]
					return err
				}
				if h.secretErr != nil {
					return h.secretErr
				}
			}
			if server, ok := obj.(*musterv1alpha1.MCPServer); ok {
				server.UID = testServerUID
			}
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.UpdateOption) error {
			h.writes = append(h.writes, "update "+kind(obj))
			if h.updateErr != nil {
				return h.updateErr
			}
			return c.Update(ctx, obj, opts...)
		},
		Delete: func(ctx context.Context, c ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.DeleteOption) error {
			h.writes = append(h.writes, "delete "+kind(obj))
			if h.deleteErr != nil {
				return h.deleteErr
			}
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()

	h.adapter = NewAdapterWithClient(&storeReader{stubMusterClient: h.sa, store: h.store}, "test-ns")
	h.adapter.EnableWritesAsCaller(func(string) (ctrlclient.Client, error) { return h.store, nil }, "")
	return h
}

func (h *credsHarness) call(t *testing.T, tool string, args map[string]interface{}) (string, bool) {
	t.Helper()
	ctx := server.ContextWithIDToken(context.Background(), testJWT(t, callerwrite.DefaultKubernetesAudience))
	result, err := h.adapter.ExecuteTool(ctx, tool, args)
	require.NoError(t, err)
	return resultText(t, result), result.IsError
}

func (h *credsHarness) secrets(t *testing.T) []corev1.Secret {
	t.Helper()
	list := &corev1.SecretList{}
	require.NoError(t, h.store.List(context.Background(), list))
	return list.Items
}

// noServer asserts that test-server is gone.
func (h *credsHarness) noServer(t *testing.T) {
	t.Helper()
	err := h.store.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "test-ns", Name: "test-server"}, &musterv1alpha1.MCPServer{})
	assert.True(t, apierrors.IsNotFound(err), "the server stayed: %v", err)
}

func (h *credsHarness) server(t *testing.T) *musterv1alpha1.MCPServer {
	t.Helper()
	obj := &musterv1alpha1.MCPServer{}
	require.NoError(t, h.store.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "test-ns", Name: "test-server"}, obj))
	return obj
}

func credsArgs(extra map[string]interface{}) map[string]interface{} {
	args := map[string]interface{}{
		"name": "test-server",
		"type": "streamable-http",
		"url":  "https://mcp.example.com/mcp",
		"auth": map[string]interface{}{
			"type":                "oauth",
			"authorizationServer": map[string]interface{}{"issuer": "https://as.example.com"},
		},
		"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
	}
	for k, v := range extra {
		if v == nil {
			delete(args, k)
			continue
		}
		args[k] = v
	}
	return args
}

func forbiddenSecretCreate() error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", errors.New(`User "bob" cannot create resource "secrets"`))
}

func TestClientCredentials_Create(t *testing.T) {
	logs := captureBindingLogs(t)
	h := newCredsHarness(t, nil)

	text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
	require.False(t, isErr, text)

	secrets := h.secrets(t)
	require.Len(t, secrets, 1)
	secret := secrets[0]
	assert.True(t, isGeneratedClientSecretName("test-server", secret.Name), secret.Name)
	assert.Equal(t, "test-ns", secret.Namespace)
	assert.Equal(t, corev1.SecretTypeOpaque, secret.Type)
	assert.Equal(t, testClientID, string(secret.Data["client-id"]))
	assert.Equal(t, testClientSecret, string(secret.Data["client-secret"]))
	assert.Equal(t, "test-server", secret.Annotations[MCPServerSecretAnnotation])
	assert.Equal(t, api.AuthorizationServerDigest(&api.MCPServerAuthAuthorizationServer{Issuer: "https://as.example.com"}),
		secret.Annotations[api.AuthorizationServerAnnotation])
	assert.Equal(t, "test-server", secret.Labels[MCPServerSecretAnnotation])
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, metav1.OwnerReference{APIVersion: "muster.giantswarm.io/v1alpha1", Kind: "MCPServer", Name: "test-server", UID: testServerUID},
		secret.OwnerReferences[0])

	ref := h.server(t).Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef
	assert.Equal(t, &musterv1alpha1.ClientCredentialsSecretRef{Name: secret.Name}, ref)

	assert.Equal(t, []string{"create MCPServer", "create Secret", "update MCPServer"}, h.writes,
		"the server names its Secret only once it exists")
	assert.Empty(t, h.sa.created, "the SA client wrote")
	assert.Contains(t, text, "MCP server 'test-server' created successfully")
	assert.Contains(t, text, secret.Name)
	assert.NotContains(t, text, testClientSecret)
	assert.NotContains(t, logs.String(), testClientSecret)
}

func TestClientCredentials_CreateSecretForbidden(t *testing.T) {
	logs := captureBindingLogs(t)
	h := newCredsHarness(t, nil)
	h.secretErr = forbiddenSecretCreate()

	text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
	require.True(t, isErr)
	assert.Equal(t, []string{"create MCPServer", "create Secret", "delete MCPServer"}, h.writes)
	h.noServer(t)
	for _, want := range []string{"so it was not created", "Permission denied", "may not create Secret", "(secrets)", `"test-ns"`, "mcpserver-editor"} {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, testClientSecret)
	assert.NotContains(t, logs.String(), testClientSecret)
}

func TestClientCredentials_CreateRollbackFails(t *testing.T) {
	h := newCredsHarness(t, nil)
	h.secretErr = forbiddenSecretCreate()
	h.deleteErr = errors.New("etcd unavailable")

	text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
	require.True(t, isErr)
	for _, want := range []string{"may not create Secret", "Removing the server failed too", "left behind", "delete MCP server 'test-server'", "etcd unavailable"} {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, testClientSecret)
}

func TestClientCredentials_CreateOtherSecretError(t *testing.T) {
	h := newCredsHarness(t, nil)
	h.secretErr = errors.New("connection refused")

	text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
	require.True(t, isErr)
	assert.Contains(t, text, "so it was not created")
	assert.Contains(t, text, "connection refused")
	assert.Equal(t, []string{"create MCPServer", "create Secret", "delete MCPServer"}, h.writes)
	h.noServer(t)
}

func alreadyExists() error {
	return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, "taken")
}

func TestClientCredentials_CreateSecretNameTaken(t *testing.T) {
	t.Run("once: another name", func(t *testing.T) {
		h := newCredsHarness(t, nil)
		h.secretErrs = []error{alreadyExists()}
		text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
		require.False(t, isErr, text)
		assert.Equal(t, []string{"create MCPServer", "create Secret", "create Secret", "update MCPServer"}, h.writes)
		secrets := h.secrets(t)
		require.Len(t, secrets, 1)
		assert.Equal(t, secrets[0].Name, h.server(t).Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef.Name)
	})
	t.Run("twice: the server is removed", func(t *testing.T) {
		h := newCredsHarness(t, nil)
		h.secretErr = alreadyExists()
		text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
		require.True(t, isErr)
		assert.Contains(t, text, "so it was not created")
		assert.Contains(t, text, "already exists")
		assert.Equal(t, []string{"create MCPServer", "create Secret", "create Secret", "delete MCPServer"}, h.writes)
		h.noServer(t)
	})
}

func TestClientCredentials_CreateNamingFails(t *testing.T) {
	h := newCredsHarness(t, nil)
	h.updateErr = apierrors.NewForbidden(schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"}, "test-server", errors.New("no update"))
	text, isErr := h.call(t, "mcpserver_create", credsArgs(nil))
	require.True(t, isErr)
	assert.Equal(t, []string{"create MCPServer", "create Secret", "update MCPServer", "delete MCPServer"}, h.writes)
	assert.Contains(t, text, "so it was not created")
	assert.Contains(t, text, "may not update MCPServer")
	h.noServer(t)
}

func TestClientCredentials_Refused(t *testing.T) {
	withRef := map[string]interface{}{
		"type": "oauth",
		"authorizationServer": map[string]interface{}{
			"issuer":                     "https://as.example.com",
			"clientCredentialsSecretRef": map[string]interface{}{"name": "mine"},
		},
	}
	tests := []struct {
		name  string
		extra map[string]interface{}
		want  string
	}{
		{"explicit ref", map[string]interface{}{"auth": withRef}, "mutually exclusive"},
		{"no auth", map[string]interface{}{"auth": nil}, "needs auth.authorizationServer"},
		{"no authorization server", map[string]interface{}{"auth": map[string]interface{}{"type": "oauth"}}, "needs auth.authorizationServer"},
		{"empty secret", map[string]interface{}{"clientCredentials": map[string]interface{}{"clientId": "id", "clientSecret": ""}}, "needs clientId and clientSecret"},
		{"missing id", map[string]interface{}{"clientCredentials": map[string]interface{}{"clientSecret": testClientSecret}}, "needs clientId and clientSecret"},
	}
	for _, tool := range []string{"mcpserver_create", "mcpserver_update"} {
		for _, tt := range tests {
			t.Run(tool+"/"+tt.name, func(t *testing.T) {
				h := newCredsHarness(t, existingServer())
				text, isErr := h.call(t, tool, credsArgs(tt.extra))
				require.True(t, isErr)
				assert.Contains(t, text, tt.want)
				assert.NotContains(t, text, testClientSecret)
				assert.Empty(t, h.writes)
			})
		}
	}
	t.Run("unknown field", func(t *testing.T) {
		h := newCredsHarness(t, nil)
		text, isErr := h.call(t, "mcpserver_create", credsArgs(map[string]interface{}{
			"clientCredentials": map[string]interface{}{"clientId": "id", "clientSecret": testClientSecret, "extra": "x"},
		}))
		require.True(t, isErr)
		assert.NotContains(t, text, testClientSecret)
		assert.Empty(t, h.writes)
	})
}

func TestClientCredentials_FilesystemMode(t *testing.T) {
	sa := &stubMusterClient{filesystem: true}
	adapter := NewAdapterWithClient(sa, "test-ns")
	args := credsArgs(nil)
	result, err := adapter.ExecuteTool(context.Background(), "mcpserver_create", args)
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "need muster in Kubernetes mode")
	assert.Empty(t, sa.created)
}

func TestGeneratedClientSecretName(t *testing.T) {
	name, err := newGeneratedClientSecretName("hubspot")
	require.NoError(t, err)
	assert.Regexp(t, `^hubspot-oauth-client-[a-z0-9]{5}$`, name)
	assert.True(t, isGeneratedClientSecretName("hubspot", name))

	long := strings.Repeat("a", 60)
	name, err = newGeneratedClientSecretName(long)
	require.NoError(t, err)
	assert.Len(t, name, 63)
	assert.Regexp(t, `^a{44}-oauth-client-[a-z0-9]{5}$`, name)
	assert.True(t, isGeneratedClientSecretName(long, name))

	for _, other := range []string{
		"hubspot-oauth-client-abcd", "hubspot-oauth-client-abcdef", "hubspot-oauth-client-ABCDE",
		"hubspot-oauth-client-ab-de", "other-oauth-client-abcde", "hubspot2-oauth-client-abcde", "hubspot",
	} {
		assert.False(t, isGeneratedClientSecretName("hubspot", other), other)
	}
}

func TestClientCredentials_Redacted(t *testing.T) {
	req := api.MCPServerCreateRequest{Name: "x", ClientCredentials: &api.MCPServerClientCredentials{ClientID: testClientID, ClientSecret: testClientSecret}}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		assert.NotContains(t, fmt.Sprintf(format, req), testClientSecret, format)
		assert.NotContains(t, fmt.Sprintf(format, req.ClientCredentials), testClientSecret, format)
		assert.NotContains(t, fmt.Sprintf(format, *req.ClientCredentials), testClientSecret, format)
	}
}
