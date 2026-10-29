package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/callerwrite"
	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
	"github.com/giantswarm/muster/v5/internal/server"
)

// chartSecretReferencePolicy renders the chart's mcpserver-secret-references
// template for muster's namespace: the policy and its binding as the chart
// ships them, so the test runs the chart's own CEL.
func chartSecretReferencePolicy(t *testing.T, namespace string) (*admissionregistrationv1.ValidatingAdmissionPolicy, *admissionregistrationv1.ValidatingAdmissionPolicyBinding) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "helm", "muster", "templates", "mcpserver-secret-references.yaml"))
	require.NoError(t, err)
	text := regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`).ReplaceAllString(string(raw), "")
	text = regexp.MustCompile(`(?m)^\s*\{\{-.*\}\}\s*$\n?`).ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "{{ $name }}", "muster-mcpserver-secret-references")
	text = strings.ReplaceAll(text, "{{ $namespace }}", namespace)
	require.NotContains(t, text, "{{", "a template directive the test does not render")
	docs := strings.Split(text, "\n---\n")
	require.Len(t, docs, 2)
	policy := &admissionregistrationv1.ValidatingAdmissionPolicy{}
	require.NoError(t, yaml.UnmarshalStrict([]byte(docs[0]), policy))
	binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
	require.NoError(t, yaml.UnmarshalStrict([]byte(docs[1]), binding))
	return policy, binding
}

// TestClientCredentialsEnvtest runs clientCredentials against a real
// kube-apiserver with RBAC and the chart's admission policy: an editor who
// may create Secrets but not get them creates a server with its client and
// replaces the client, the policy admits only the generated reference, and
// an editor who may not create Secrets is told so and leaves no server.
//
// Requires envtest binaries: run via `make test-envtest`. Skipped otherwise.
func TestClientCredentialsEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via `make test-envtest`")
	}
	const ns = "default"

	editorToken := testJWT(t, callerwrite.DefaultKubernetesAudience)
	noSecretsToken := testJWT(t, []string{callerwrite.DefaultKubernetesAudience, "muster"})
	tokenFile := filepath.Join(t.TempDir(), "tokens.csv")
	tokens := fmt.Sprintf("%s,editor,1000,\n%s,no-secrets,1001,\n", editorToken, noSecretsToken)
	require.NoError(t, os.WriteFile(tokenFile, []byte(tokens), 0o600))

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "helm", "muster", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	testEnv.ControlPlane.GetAPIServer().Configure().Set("token-auth-file", tokenFile)
	adminCfg, err := testEnv.Start()
	require.NoError(t, err, "start envtest")
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	saClient, err := kubernetesclient.New(adminCfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = saClient.Close() })
	ctx := context.Background()

	// The chart's mcpserver-editor Role, and one without Secrets.
	editorRules := []rbacv1.PolicyRule{
		{APIGroups: []string{"muster.giantswarm.io"}, Resources: []string{"mcpservers"}, Verbs: []string{"create", "update", "patch", "delete"}},
		{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"create"}},
	}
	for user, rules := range map[string][]rbacv1.PolicyRule{"editor": editorRules, "no-secrets": editorRules[:1]} {
		require.NoError(t, saClient.Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: user, Namespace: ns}, Rules: rules}))
		require.NoError(t, saClient.Create(ctx, &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: user, Namespace: ns},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: user}},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: user},
		}))
	}
	policy, binding := chartSecretReferencePolicy(t, ns)
	require.NoError(t, saClient.Create(ctx, policy))
	require.NoError(t, saClient.Create(ctx, binding))

	adapter := NewAdapterWithClient(saClient, ns)
	adapter.EnableWritesAsCaller(callerwrite.NewKubernetesClientFactory(adminCfg), "")
	editorCtx := server.ContextWithIDToken(ctx, editorToken)
	noSecretsCtx := server.ContextWithIDToken(ctx, noSecretsToken)

	call := func(ctx context.Context, tool string, args map[string]interface{}) (string, bool) {
		t.Helper()
		result, err := adapter.ExecuteTool(ctx, tool, args)
		require.NoError(t, err)
		return resultText(t, result), result.IsError
	}
	args := func(name string, as map[string]interface{}, creds bool) map[string]interface{} {
		a := map[string]interface{}{
			"name": name, "type": "streamable-http", "url": "https://mcp.example.com/mcp",
			"auth": map[string]interface{}{"type": "oauth", "authorizationServer": as},
		}
		if creds {
			a["clientCredentials"] = map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret}
		}
		return a
	}
	issuer := map[string]interface{}{"issuer": "https://as.example.com"}

	// The policy is in force once it refuses a Secret the editor may not get.
	probe := 0
	require.Eventually(t, func() bool {
		probe++
		name := fmt.Sprintf("probe-%d", probe)
		_, isErr := call(editorCtx, "mcpserver_create", args(name, map[string]interface{}{
			"issuer": "https://as.example.com", "clientCredentialsSecretRef": map[string]interface{}{"name": "not-yours"},
		}, false))
		if !isErr {
			_ = saClient.DeleteMCPServer(ctx, name, ns)
		}
		return isErr
	}, 30*time.Second, 200*time.Millisecond, "admission policy never took effect")

	secretOf := func(name string) *corev1.Secret {
		t.Helper()
		secret := &corev1.Secret{}
		require.NoError(t, saClient.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: name}, secret))
		return secret
	}
	refOf := func(name string) string {
		t.Helper()
		obj, err := saClient.GetMCPServer(ctx, name, ns)
		require.NoError(t, err)
		return obj.Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef.Name
	}

	t.Run("editor creates and replaces the client", func(t *testing.T) {
		text, isErr := call(editorCtx, "mcpserver_create", args("hubspot", issuer, true))
		require.False(t, isErr, text)
		assert.NotContains(t, text, testClientSecret)

		obj, err := saClient.GetMCPServer(ctx, "hubspot", ns)
		require.NoError(t, err)
		first := refOf("hubspot")
		require.True(t, isGeneratedClientSecretName("hubspot", first), first)
		secret := secretOf(first)
		assert.Equal(t, testClientSecret, string(secret.Data["client-secret"]))
		require.Len(t, secret.OwnerReferences, 1)
		assert.Equal(t, obj.UID, secret.OwnerReferences[0].UID)

		reader := NewCredentialsAdapter(saClient)
		owner := api.SecretOwner{Name: "hubspot", Namespace: ns, UID: string(obj.UID)}.
			ForAuthorizationServer(&api.MCPServerAuthAuthorizationServer{Issuer: "https://as.example.com"})
		creds, err := reader.LoadMCPServerClientCredentials(ctx, owner, api.SecretFieldAuthorizationServer,
			&api.ClientCredentialsSecretRef{Name: first})
		require.NoError(t, err)
		assert.Equal(t, testClientID, creds.ClientID)

		text, isErr = call(editorCtx, "mcpserver_update", map[string]interface{}{
			"name":              "hubspot",
			"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": "rotated"},
		})
		require.False(t, isErr, text)
		second := refOf("hubspot")
		assert.NotEqual(t, first, second)
		assert.Equal(t, "rotated", string(secretOf(second).Data["client-secret"]))
		secretOf(first) // the old one stays until the server goes

		// The portal re-sends auth without the reference: the client stays.
		text, isErr = call(editorCtx, "mcpserver_update", map[string]interface{}{
			"name": "hubspot", "auth": map[string]interface{}{"type": "oauth", "authorizationServer": map[string]interface{}{
				"issuer": "https://as.example.com", "scopes": "crm.objects.contacts.read",
			}},
		})
		require.False(t, isErr, text)
		assert.Equal(t, second, refOf("hubspot"))
	})

	t.Run("another editor cannot send the client elsewhere", func(t *testing.T) {
		before, err := saClient.GetMCPServer(ctx, "hubspot", ns)
		require.NoError(t, err)
		stored := before.Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef.Name
		evil := map[string]interface{}{"type": "oauth", "authorizationServer": map[string]interface{}{"issuer": "https://evil.example.com"}}

		// Through muster: refused before any write.
		text, isErr := call(noSecretsCtx, "mcpserver_update", map[string]interface{}{"name": "hubspot", "auth": evil})
		require.True(t, isErr, text)
		assert.Contains(t, text, "re-enter it with clientCredentials")
		after, err := saClient.GetMCPServer(ctx, "hubspot", ns)
		require.NoError(t, err)
		assert.Equal(t, before.ResourceVersion, after.ResourceVersion)

		// Straight to the API server: admitted, but muster does not send the
		// client there.
		direct, err := callerwrite.NewKubernetesClientFactory(adminCfg)(noSecretsToken)
		require.NoError(t, err)
		after.Spec.Auth.AuthorizationServer.Issuer = "https://evil.example.com"
		require.NoError(t, direct.Update(ctx, after))
		reader := NewCredentialsAdapter(saClient)
		owner := api.SecretOwner{Name: "hubspot", Namespace: ns, UID: string(after.UID)}.
			ForAuthorizationServer(&api.MCPServerAuthAuthorizationServer{Issuer: "https://evil.example.com"})
		_, err = reader.LoadMCPServerClientCredentials(ctx, owner, api.SecretFieldAuthorizationServer,
			&api.ClientCredentialsSecretRef{Name: stored})
		require.ErrorIs(t, err, api.ErrSecretOtherAuthorizationServer)
	})

	t.Run("policy admits only the server's own generated name", func(t *testing.T) {
		for name, ref := range map[string]string{
			"another server's name": "hubspot-oauth-client-abcde",
			"a sixth character":     "thief-oauth-client-abcdef",
		} {
			text, isErr := call(editorCtx, "mcpserver_create", args("thief", map[string]interface{}{
				"issuer": "https://as.example.com", "clientCredentialsSecretRef": map[string]interface{}{"name": ref},
			}, false))
			assert.True(t, isErr, "%s: %s", name, text)
			assert.Contains(t, text, "may name only Secrets they may get", name)
		}
		text, isErr := call(editorCtx, "mcpserver_create", args("thief", map[string]interface{}{
			"issuer": "https://as.example.com", "clientCredentialsSecretRef": map[string]interface{}{"name": "thief-oauth-client-abcde"},
		}, false))
		require.False(t, isErr, "the server's own generated name: %s", text)
	})

	t.Run("editor without create on secrets", func(t *testing.T) {
		text, isErr := call(noSecretsCtx, "mcpserver_create", args("denied", issuer, true))
		require.True(t, isErr)
		for _, want := range []string{"so it was not created", "Permission denied", "may not create Secret", "(secrets)", `"default"`} {
			assert.Contains(t, text, want)
		}
		assert.NotContains(t, text, testClientSecret)
		_, err := saClient.GetMCPServer(ctx, "denied", ns)
		assert.True(t, apierrors.IsNotFound(err), "the server stayed: %v", err)
	})
}
