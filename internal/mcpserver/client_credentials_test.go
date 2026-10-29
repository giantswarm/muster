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
// server. Every write is recorded by verb and kind; secretErr, deleteErr and
// updateErr fail the matching write.
type credsHarness struct {
	adapter   *Adapter
	sa        *stubMusterClient
	store     ctrlclient.Client
	writes    []string
	secretErr error
	deleteErr error
	updateErr error
}

func newCredsHarness(t *testing.T, existing *musterv1alpha1.MCPServer) *credsHarness {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, musterv1alpha1.AddToScheme(scheme))

	h := &credsHarness{sa: &stubMusterClient{existing: existing}}
	kind := func(obj ctrlclient.Object) string {
		if _, ok := obj.(*corev1.Secret); ok {
			return "Secret"
		}
		return "MCPServer"
	}
	h.store = fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
			h.writes = append(h.writes, "create "+kind(obj))
			if _, ok := obj.(*corev1.Secret); ok && h.secretErr != nil {
				return h.secretErr
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
			// The existing server lives in the SA stub; the store keeps
			// the updated one for the test to read.
			return c.Create(ctx, obj.DeepCopyObject().(ctrlclient.Object))
		},
		Delete: func(_ context.Context, _ ctrlclient.WithWatch, obj ctrlclient.Object, _ ...ctrlclient.DeleteOption) error {
			h.writes = append(h.writes, "delete "+kind(obj))
			return h.deleteErr
		},
	}).Build()

	h.adapter = NewAdapterWithClient(h.sa, "test-ns")
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
	assert.Equal(t, "test-server", secret.Labels[MCPServerSecretAnnotation])
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, metav1.OwnerReference{APIVersion: "muster.giantswarm.io/v1alpha1", Kind: "MCPServer", Name: "test-server", UID: testServerUID},
		secret.OwnerReferences[0])

	ref := h.server(t).Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef
	assert.Equal(t, &musterv1alpha1.ClientCredentialsSecretRef{Name: secret.Name}, ref)

	assert.Equal(t, []string{"create MCPServer", "create Secret"}, h.writes)
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

// generatedServer is test-server in test-ns with a client stored under name.
func generatedServer(name string) *musterv1alpha1.MCPServer {
	obj := existingServer()
	obj.UID = testServerUID
	obj.Spec.URL = "https://mcp.example.com/mcp"
	obj.Spec.Auth = &musterv1alpha1.MCPServerAuth{
		Type: "oauth",
		AuthorizationServer: &musterv1alpha1.MCPServerAuthAuthorizationServer{
			Issuer:                     "https://as.example.com",
			ClientCredentialsSecretRef: &musterv1alpha1.ClientCredentialsSecretRef{Name: name},
		},
	}
	return obj
}

func TestClientCredentials_Replace(t *testing.T) {
	logs := captureBindingLogs(t)
	const old = "test-server-oauth-client-aaaaa"
	h := newCredsHarness(t, generatedServer(old))

	text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
		"name":              "test-server",
		"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
	})
	require.False(t, isErr, text)

	secrets := h.secrets(t)
	require.Len(t, secrets, 1)
	secret := secrets[0]
	assert.NotEqual(t, old, secret.Name)
	assert.True(t, isGeneratedClientSecretName("test-server", secret.Name))
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, testServerUID, secret.OwnerReferences[0].UID)

	assert.Equal(t, secret.Name, h.server(t).Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef.Name)
	assert.Equal(t, []string{"create Secret", "update MCPServer"}, h.writes, "no update or delete on Secrets")
	assert.Contains(t, text, "updated successfully")
	assert.NotContains(t, text, testClientSecret)
	assert.NotContains(t, logs.String(), testClientSecret)
}

func TestClientCredentials_ReplaceSecretForbidden(t *testing.T) {
	h := newCredsHarness(t, generatedServer("test-server-oauth-client-aaaaa"))
	h.secretErr = forbiddenSecretCreate()

	text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
		"name":              "test-server",
		"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
	})
	require.True(t, isErr)
	assert.Contains(t, text, "so it was not updated")
	assert.Contains(t, text, "may not create Secret")
	assert.Equal(t, []string{"create Secret"}, h.writes)
}

func TestClientCredentials_ReplaceUpdateFails(t *testing.T) {
	h := newCredsHarness(t, generatedServer("test-server-oauth-client-aaaaa"))
	h.updateErr = errors.New("conflict")

	text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
		"name":              "test-server",
		"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
	})
	require.True(t, isErr)
	secrets := h.secrets(t)
	require.Len(t, secrets, 1)
	assert.Contains(t, text, "conflict")
	assert.Contains(t, text, fmt.Sprintf("stored in Secret '%s'", secrets[0].Name))
	assert.Contains(t, text, "deleted with the server")
}

func TestClientCredentials_UpdateAuthKeepsGeneratedRef(t *testing.T) {
	const generated = "test-server-oauth-client-aaaaa"
	auth := func(as map[string]interface{}) map[string]interface{} {
		a := map[string]interface{}{"type": "oauth"}
		if as != nil {
			a["authorizationServer"] = as
		}
		return a
	}
	tests := []struct {
		name     string
		previous string
		auth     map[string]interface{}
		want     *musterv1alpha1.ClientCredentialsSecretRef
	}{
		{"auth re-sent without ref keeps it", generated,
			auth(map[string]interface{}{"issuer": "https://as.example.com", "scopes": "crm"}),
			&musterv1alpha1.ClientCredentialsSecretRef{Name: generated}},
		{"explicit ref wins", generated,
			auth(map[string]interface{}{"issuer": "https://as.example.com", "clientCredentialsSecretRef": map[string]interface{}{"name": "gitops"}}),
			&musterv1alpha1.ClientCredentialsSecretRef{Name: "gitops"}},
		{"auth without authorizationServer drops it", generated, auth(nil), nil},
		{"a ref not generated is not kept", "gitops",
			auth(map[string]interface{}{"issuer": "https://as.example.com"}), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCredsHarness(t, generatedServer(tt.previous))
			text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{"name": "test-server", "auth": tt.auth})
			require.False(t, isErr, text)
			as := h.server(t).Spec.Auth.AuthorizationServer
			if tt.want == nil {
				if as != nil {
					assert.Nil(t, as.ClientCredentialsSecretRef)
				}
				return
			}
			require.NotNil(t, as)
			assert.Equal(t, tt.want, as.ClientCredentialsSecretRef)
			assert.Equal(t, []string{"update MCPServer"}, h.writes)
		})
	}
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

// githubGenerated is a generated client Secret name for githubServer.
const githubGenerated = "github-oauth-client-x1y2z"

func ownedBy(uid, kind string) func(*corev1.Secret) {
	return func(s *corev1.Secret) {
		s.OwnerReferences = append(s.OwnerReferences, metav1.OwnerReference{
			APIVersion: "muster.giantswarm.io/v1alpha1", Kind: kind, Name: "github", UID: types.UID(uid),
		})
	}
}

func TestReadCheck_GeneratedClientSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  *corev1.Secret
		owner   api.SecretOwner
		field   string
		refused bool
	}{
		{name: "owned", secret: bindingSecret(githubGenerated, ownedBy("uid-github", "MCPServer")), owner: githubServer},
		{name: "unowned", secret: bindingSecret(githubGenerated), owner: githubServer, refused: true},
		{name: "owned by another server", secret: bindingSecret(githubGenerated, ownedBy("uid-other", "MCPServer")), owner: githubServer, refused: true},
		{name: "owned by another kind", secret: bindingSecret(githubGenerated, ownedBy("uid-github", "Workflow")), owner: githubServer, refused: true},
		{name: "owner without UID", secret: bindingSecret(githubGenerated, ownedBy("", "MCPServer")),
			owner: api.SecretOwner{Name: "github", Namespace: "agent-platform"}, refused: true},
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
			if tt.refused {
				require.ErrorIs(t, err, api.ErrSecretNotOwned)
				assert.Contains(t, err.Error(), "ownerReference to this MCPServer")
				assert.Nil(t, creds)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "id", creds.ClientID)
		})
	}

	t.Run("in another namespace", func(t *testing.T) {
		secret := bindingSecret(githubGenerated, ownedBy("uid-github", "MCPServer"))
		secret.Namespace = "elsewhere"
		adapter := bindingAdapter(t, secret)
		_, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldAuthorizationServer,
			&api.ClientCredentialsSecretRef{Name: githubGenerated, Namespace: "elsewhere"})
		require.ErrorIs(t, err, api.ErrSecretNotOwned)
	})
}
