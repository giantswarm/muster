package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// githubServer is the MCPServer agent-platform/github the Secrets are read for.
var githubServer = api.SecretOwner{Name: "github", Namespace: "agent-platform", UID: "uid-github"}

// bindingSecret is a Secret in agent-platform with client credentials and a
// token, shaped by opts.
func bindingSecret(name string, opts ...func(*corev1.Secret)) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agent-platform"},
		Data: map[string][]byte{
			"client-id":     []byte("id"),
			"client-secret": []byte("secret"),
			"token":         []byte("t0ken"),
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func typed(t corev1.SecretType) func(*corev1.Secret) {
	return func(s *corev1.Secret) { s.Type = t }
}

func bindingAdapter(t *testing.T, secret *corev1.Secret, opts ...CredentialsOption) *CredentialsAdapter {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	return NewCredentialsAdapter(fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build(), opts...)
}

func captureBindingLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &buf)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })
	return &buf
}

// TestCredentialsAdapter_ReadsTheSecretForTheMCPServer: a Secret an
// MCPServer names is read for it as it is.
func TestCredentialsAdapter_ReadsTheSecretForTheMCPServer(t *testing.T) {
	adapter := bindingAdapter(t, bindingSecret("github-oauth-client"))
	ref := &api.ClientCredentialsSecretRef{Name: "github-oauth-client"}

	creds, err := adapter.LoadMCPServerClientCredentials(t.Context(), githubServer, api.SecretFieldAuthorizationServer, ref)
	require.NoError(t, err)
	assert.Equal(t, "secret", creds.ClientSecret)
	key, err := adapter.LoadMCPServerSecretKey(t.Context(), githubServer, api.SecretFieldHeadersFrom, ref, "token")
	require.NoError(t, err)
	assert.Equal(t, "t0ken", string(key))
}

// TestCredentialsAdapter_ProtectedSecrets: muster's own Secrets are never read
// for an MCPServer, while muster's configuration keeps reading them.
func TestCredentialsAdapter_ProtectedSecrets(t *testing.T) {
	protected := WithProtectedSecrets(types.NamespacedName{Namespace: "agent-platform", Name: "muster-oauth"})
	adapter := bindingAdapter(t, bindingSecret("muster-oauth"), protected)
	ref := &api.ClientCredentialsSecretRef{Name: "muster-oauth"}

	_, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldAuthorizationServer, ref)
	require.ErrorIs(t, err, api.ErrSecretProtected)
	_, err = adapter.LoadMCPServerSecretKey(context.Background(), githubServer, api.SecretFieldHeadersFrom, ref, "token")
	require.ErrorIs(t, err, api.ErrSecretProtected)

	creds, err := adapter.LoadClientCredentialsForConfig(context.Background(), ref, "agent-platform")
	require.NoError(t, err, "muster's configuration reads its own Secret")
	assert.Equal(t, "id", creds.ClientID)
}

// TestCredentialsAdapter_ConfigReads: a Secret muster's configuration names
// is read.
func TestCredentialsAdapter_ConfigReads(t *testing.T) {
	adapter := bindingAdapter(t, bindingSecret("broker-target"))

	creds, err := adapter.LoadClientCredentialsForConfig(context.Background(),
		&api.ClientCredentialsSecretRef{Name: "broker-target"}, "agent-platform")
	require.NoError(t, err)
	assert.Equal(t, "secret", creds.ClientSecret)
}

// TestCredentialsAdapter_ReservedSecretTypes: a Secret of a type that holds
// muster's or the cluster's credentials -- a Helm release, a TLS key, a
// ServiceAccount token -- is refused, and nothing of it is returned.
func TestCredentialsAdapter_ReservedSecretTypes(t *testing.T) {
	for _, secretType := range []corev1.SecretType{"helm.sh/release.v1", corev1.SecretTypeTLS, corev1.SecretTypeServiceAccountToken,
		corev1.SecretTypeBootstrapToken, corev1.SecretTypeDockerConfigJson, corev1.SecretTypeDockercfg} {
		t.Run(string(secretType), func(t *testing.T) {
			secret := bindingSecret("sh.helm.release.v1.muster.v3", typed(secretType))
			adapter := bindingAdapter(t, secret)
			ref := &api.ClientCredentialsSecretRef{Name: secret.Name}

			creds, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldAuthorizationServer, ref)
			require.ErrorIs(t, err, api.ErrSecretProtected)
			assert.Nil(t, creds)
			assert.Contains(t, err.Error(), string(secretType))
			key, err := adapter.LoadMCPServerSecretKey(context.Background(), githubServer, api.SecretFieldHeadersFrom, ref, "token")
			require.ErrorIs(t, err, api.ErrSecretProtected)
			assert.Nil(t, key)
		})
	}
}

// TestCredentialsAdapter_RefusalsAreLoggedOnce: a refused Secret read at
// every connect or login is logged once.
func TestCredentialsAdapter_RefusalsAreLoggedOnce(t *testing.T) {
	logs := captureBindingLogs(t)
	protected := WithProtectedSecrets(types.NamespacedName{Namespace: "agent-platform", Name: "muster-oauth"})
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	adapter := NewCredentialsAdapter(fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(bindingSecret("muster-oauth"), bindingSecret("tls", typed(corev1.SecretTypeTLS))).Build(), protected)

	for range 5 {
		_, _ = adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldAuthorizationServer,
			&api.ClientCredentialsSecretRef{Name: "muster-oauth"})
		_, _ = adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldAuthorizationServer,
			&api.ClientCredentialsSecretRef{Name: "tls"})
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "names muster's own Secret"))
	assert.Equal(t, 1, strings.Count(logs.String(), "of type kubernetes.io/tls"))
}

func TestMCPServerSecretOwner(t *testing.T) {
	assert.Equal(t, api.SecretOwner{Name: "github", Namespace: "org-acme", UID: "u"}, api.MCPServerSecretOwner("org-acme/github", "", "u"))
	assert.Equal(t, api.SecretOwner{Name: "github", Namespace: "muster", UID: "u"}, api.MCPServerSecretOwner("github", "muster", "u"))
}

// TestCredentialsAdapter_SharedSecret: one Secret serves several MCPServers.
func TestCredentialsAdapter_SharedSecret(t *testing.T) {
	ref := &api.ClientCredentialsSecretRef{Name: "gazelle-token-exchange-credentials"}
	adapter := bindingAdapter(t, bindingSecret(ref.Name))

	_, err := adapter.LoadClientCredentialsForConfig(t.Context(), ref, "agent-platform")
	require.NoError(t, err, "the broker target's read")
	for _, name := range []string{"gazelle-kubernetes", "gazelle-prometheus", "gazelle-capi"} {
		owner := api.SecretOwner{Name: name, Namespace: "agent-platform", UID: "uid-" + name}
		_, err := adapter.LoadMCPServerClientCredentials(t.Context(), owner, api.SecretFieldTokenExchange, ref)
		require.NoError(t, err, name)
	}
}

// TestCredentialsAdapter_LoggedReadsAreCapped: the record of written warnings
// does not grow past maxLoggedReads, however many MCPServers come and go.
func TestCredentialsAdapter_LoggedReadsAreCapped(t *testing.T) {
	_ = captureBindingLogs(t)
	b := newSecretBinding()
	for i := range maxLoggedReads + 10 {
		req := &secretRequest{owner: api.SecretOwner{Name: fmt.Sprintf("s%d", i), Namespace: "ns"}, field: api.SecretFieldHeadersFrom}
		b.warnOnce(req, "ns", "x", "protected", "warn %d", i)
	}
	entries := 0
	b.logged.Range(func(any, any) bool { entries++; return true })
	assert.LessOrEqual(t, entries, maxLoggedReads)
	assert.LessOrEqual(t, b.loggedCount.Load(), int64(maxLoggedReads))
}
