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

func ownedBy(name, uid string) func(*corev1.Secret) {
	return func(s *corev1.Secret) {
		s.OwnerReferences = append(s.OwnerReferences, metav1.OwnerReference{
			APIVersion: "muster.giantswarm.io/v1alpha1",
			Kind:       "MCPServer",
			Name:       name,
			UID:        types.UID(uid),
		})
	}
}

func annotated(value string) func(*corev1.Secret) {
	return func(s *corev1.Secret) {
		s.Annotations = map[string]string{api.MCPServerSecretAnnotation: value}
	}
}

func typed(t corev1.SecretType) func(*corev1.Secret) {
	return func(s *corev1.Secret) { s.Type = t }
}

func labelled(value string) func(*corev1.Secret) {
	return func(s *corev1.Secret) {
		s.Labels = map[string]string{api.MCPServerSecretAnnotation: value}
	}
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

func TestCredentialsAdapter_SecretBinding(t *testing.T) {
	enforce := WithSecretBindingMode(api.SecretBindingEnforce)
	ref := &api.ClientCredentialsSecretRef{Name: "github-oauth-client"}

	tests := []struct {
		name    string
		secret  *corev1.Secret
		wantErr error
	}{
		{"ownerReference with the MCPServer's UID binds", bindingSecret("github-oauth-client", ownedBy("github", "uid-github")), nil},
		{"ownerReference with another UID does not bind", bindingSecret("github-oauth-client", ownedBy("github", "uid-earlier")), api.ErrSecretNotBound},
		{"ownerReference to another MCPServer does not bind", bindingSecret("github-oauth-client", ownedBy("gitlab", "uid-github")), api.ErrSecretNotBound},
		{"a label of the annotation's key does not bind", bindingSecret("github-oauth-client", labelled("github")), api.ErrSecretNotBound},
		{"annotation with the MCPServer's name binds", bindingSecret("github-oauth-client", annotated("github")), nil},
		{"annotation with namespace and name binds", bindingSecret("github-oauth-client", annotated("agent-platform/github")), nil},
		{"annotation for another namespace's MCPServer does not bind", bindingSecret("github-oauth-client", annotated("org-acme/github")), api.ErrSecretNotBound},
		{"an unbound Secret is refused", bindingSecret("github-oauth-client"), api.ErrSecretNotBound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter := bindingAdapter(t, tc.secret, enforce)

			creds, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldAuthorizationServer, ref)
			key, keyErr := adapter.LoadMCPServerSecretKey(context.Background(), githubServer, api.SecretFieldHeadersFrom, ref, "token")

			if tc.wantErr == nil {
				require.NoError(t, err)
				require.NoError(t, keyErr)
				assert.Equal(t, "secret", creds.ClientSecret)
				assert.Equal(t, "t0ken", string(key))
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
			require.ErrorIs(t, keyErr, tc.wantErr)
			assert.Nil(t, creds, "nothing of a refused Secret is returned")
			assert.Nil(t, key)
			assert.Contains(t, err.Error(), "agent-platform/github")
			assert.Contains(t, err.Error(), api.SecretFieldAuthorizationServer)
			assert.Contains(t, err.Error(), "add github to the Secret's annotation "+api.MCPServerSecretAnnotation, "the error says how to bind it")
			assert.Contains(t, err.Error(), "uid-github")
		})
	}
}

func TestCredentialsAdapter_SecretBindingWarnMode(t *testing.T) {
	logs := captureBindingLogs(t)
	adapter := bindingAdapter(t, bindingSecret("github-oauth-client"), WithSecretBindingMode(api.SecretBindingWarn))
	ref := &api.ClientCredentialsSecretRef{Name: "github-oauth-client"}

	for range 3 {
		creds, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldTokenExchange, ref)
		require.NoError(t, err, "warn mode still reads an unbound Secret")
		assert.Equal(t, "id", creds.ClientID)
	}

	out := logs.String()
	assert.Equal(t, 1, strings.Count(out, "which is not bound to it"), "one warning per MCPServer and Secret, not one per read")
	assert.Contains(t, out, "agent-platform/github")
	assert.Contains(t, out, "agent-platform/github-oauth-client")
	assert.Contains(t, out, api.SecretFieldTokenExchange)
	assert.Contains(t, out, "add github to the Secret's annotation "+api.MCPServerSecretAnnotation)
}

func TestCredentialsAdapter_SecretBindingDefaultsToWarn(t *testing.T) {
	adapter := bindingAdapter(t, bindingSecret("github-oauth-client"))

	_, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldTokenExchange,
		&api.ClientCredentialsSecretRef{Name: "github-oauth-client"})
	require.NoError(t, err)
}

// TestCredentialsAdapter_ProtectedSecrets: muster's own Secrets are never read
// for an MCPServer, in either mode and whatever binds them, while muster's
// configuration keeps reading them.
func TestCredentialsAdapter_ProtectedSecrets(t *testing.T) {
	protected := WithProtectedSecrets(types.NamespacedName{Namespace: "agent-platform", Name: "muster-oauth"})
	secret := bindingSecret("muster-oauth", annotated("github"), ownedBy("github", "uid-github"))
	ref := &api.ClientCredentialsSecretRef{Name: "muster-oauth"}

	for _, mode := range []api.SecretBindingMode{api.SecretBindingWarn, api.SecretBindingEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			adapter := bindingAdapter(t, secret, protected, WithSecretBindingMode(mode))

			_, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldAuthorizationServer, ref)
			require.ErrorIs(t, err, api.ErrSecretProtected)
			_, err = adapter.LoadMCPServerSecretKey(context.Background(), githubServer, api.SecretFieldHeadersFrom, ref, "token")
			require.ErrorIs(t, err, api.ErrSecretProtected)

			creds, err := adapter.LoadClientCredentialsForConfig(context.Background(), ref, "agent-platform")
			require.NoError(t, err, "muster's configuration reads its own Secret")
			assert.Equal(t, "id", creds.ClientID)
		})
	}
}

// TestCredentialsAdapter_ConfigReadsAreUnbound: a Secret muster's
// configuration names is read without any binding, in enforce mode too.
func TestCredentialsAdapter_ConfigReadsAreUnbound(t *testing.T) {
	adapter := bindingAdapter(t, bindingSecret("broker-target"), WithSecretBindingMode(api.SecretBindingEnforce))

	creds, err := adapter.LoadClientCredentialsForConfig(context.Background(),
		&api.ClientCredentialsSecretRef{Name: "broker-target"}, "agent-platform")
	require.NoError(t, err)
	assert.Equal(t, "secret", creds.ClientSecret)
}

// TestCredentialsAdapter_CrossNamespaceBinding: an ownerReference cannot
// reach across namespaces, so a Secret in another namespace than the
// MCPServer's is bound by the annotation "<namespace>/<name>" only.
func TestCredentialsAdapter_CrossNamespaceBinding(t *testing.T) {
	enforce := WithSecretBindingMode(api.SecretBindingEnforce)
	ref := &api.ClientCredentialsSecretRef{Name: "shared", Namespace: "shared-secrets"}
	inShared := func(opts ...func(*corev1.Secret)) *corev1.Secret {
		s := bindingSecret("shared", opts...)
		s.Namespace = "shared-secrets"
		return s
	}

	for name, tc := range map[string]struct {
		secret *corev1.Secret
		bound  bool
	}{
		"qualified annotation binds":    {inShared(annotated("agent-platform/github")), true},
		"bare name annotation does not": {inShared(annotated("github")), false},
		"ownerReference does not":       {inShared(ownedBy("github", "uid-github")), false},
		"another namespace's server":    {inShared(annotated("org-acme/github")), false},
	} {
		t.Run(name, func(t *testing.T) {
			adapter := bindingAdapter(t, tc.secret, enforce)
			_, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldTokenExchange, ref)
			if tc.bound {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, api.ErrSecretNotBound)
			assert.Contains(t, err.Error(), "add agent-platform/github to the Secret's annotation "+api.MCPServerSecretAnnotation)
			assert.NotContains(t, err.Error(), "ownerReference", "an ownerReference cannot cross namespaces")
		})
	}
}

// TestCredentialsAdapter_OwnerReferenceNeedsTheUID: without the MCPServer's
// UID an ownerReference binds nothing, whatever its name.
func TestCredentialsAdapter_OwnerReferenceNeedsTheUID(t *testing.T) {
	adapter := bindingAdapter(t, bindingSecret("github-oauth-client", ownedBy("github", "")), WithSecretBindingMode(api.SecretBindingEnforce))
	owner := githubServer
	owner.UID = ""

	_, err := adapter.LoadMCPServerClientCredentials(context.Background(), owner, api.SecretFieldTokenExchange,
		&api.ClientCredentialsSecretRef{Name: "github-oauth-client"})
	require.ErrorIs(t, err, api.ErrSecretNotBound)
}

// TestCredentialsAdapter_ReservedSecretTypes: a Secret of a type that holds
// muster's or the cluster's credentials -- a Helm release, a TLS key, a
// ServiceAccount token -- is refused in either mode, however it is bound,
// and nothing of it is returned.
func TestCredentialsAdapter_ReservedSecretTypes(t *testing.T) {
	for _, secretType := range []corev1.SecretType{"helm.sh/release.v1", corev1.SecretTypeTLS, corev1.SecretTypeServiceAccountToken,
		corev1.SecretTypeBootstrapToken, corev1.SecretTypeDockerConfigJson, corev1.SecretTypeDockercfg} {
		for _, mode := range []api.SecretBindingMode{api.SecretBindingWarn, api.SecretBindingEnforce} {
			t.Run(string(secretType)+"/"+string(mode), func(t *testing.T) {
				secret := bindingSecret("sh.helm.release.v1.muster.v3", typed(secretType), annotated("github"), ownedBy("github", "uid-github"))
				adapter := bindingAdapter(t, secret, WithSecretBindingMode(mode))
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
}

// TestCredentialsAdapter_RefusalsAreLoggedOnce: a refused or unbound Secret
// read at every connect or login is logged once.
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

// TestCredentialsAdapter_OwnerWithoutNameBindsNothing: an unbound Secret
// must not count as bound to a request that names no MCPServer.
func TestCredentialsAdapter_OwnerWithoutNameBindsNothing(t *testing.T) {
	adapter := bindingAdapter(t, bindingSecret("github-oauth-client"), WithSecretBindingMode(api.SecretBindingEnforce))

	_, err := adapter.LoadMCPServerClientCredentials(context.Background(), api.SecretOwner{Namespace: "agent-platform"},
		api.SecretFieldTokenExchange, &api.ClientCredentialsSecretRef{Name: "github-oauth-client"})
	require.ErrorIs(t, err, api.ErrSecretNotBound)
}

func TestMCPServerSecretOwner(t *testing.T) {
	assert.Equal(t, api.SecretOwner{Name: "github", Namespace: "org-acme", UID: "u"}, api.MCPServerSecretOwner("org-acme/github", "", "u"))
	assert.Equal(t, api.SecretOwner{Name: "github", Namespace: "muster", UID: "u"}, api.MCPServerSecretOwner("github", "muster", "u"))
}

// TestCredentialsAdapter_SharedSecret: one Secret serves several MCPServers.
// The annotation lists them, comma separated with any whitespace; several
// ownerReferences work as well.
func TestCredentialsAdapter_SharedSecret(t *testing.T) {
	enforce := WithSecretBindingMode(api.SecretBindingEnforce)
	ref := &api.ClientCredentialsSecretRef{Name: "gazelle-token-exchange-credentials"}
	kubernetes := api.SecretOwner{Name: "gazelle-kubernetes", Namespace: "agent-platform", UID: "uid-k8s"}
	prometheus := api.SecretOwner{Name: "gazelle-prometheus", Namespace: "agent-platform", UID: "uid-prom"}
	capi := api.SecretOwner{Name: "gazelle-capi", Namespace: "agent-platform", UID: "uid-capi"}

	for name, secret := range map[string]*corev1.Secret{
		"annotation list": bindingSecret(ref.Name, annotated(" gazelle-kubernetes ,agent-platform/gazelle-prometheus,\tgazelle-capi")),
		"ownerReferences": bindingSecret(ref.Name, ownedBy("gazelle-kubernetes", "uid-k8s"), ownedBy("gazelle-prometheus", "uid-prom"), ownedBy("gazelle-capi", "uid-capi")),
	} {
		t.Run(name, func(t *testing.T) {
			adapter := bindingAdapter(t, secret, enforce)
			for _, owner := range []api.SecretOwner{kubernetes, prometheus, capi} {
				_, err := adapter.LoadMCPServerClientCredentials(context.Background(), owner, api.SecretFieldTokenExchange, ref)
				require.NoError(t, err, owner.Name)
			}
			_, err := adapter.LoadMCPServerClientCredentials(context.Background(), githubServer, api.SecretFieldTokenExchange, ref)
			require.ErrorIs(t, err, api.ErrSecretNotBound, "a server the Secret does not list")
		})
	}
}

// TestCredentialsAdapter_BrokerTargetSecretSharedWithAnMCPServer: a broker
// target's token-exchange Secret is also the tokenExchange Secret of that
// cluster's MCPServers. muster's configuration keeps reading it; an MCPServer
// reads it unbound in warn mode and bound in enforce mode.
func TestCredentialsAdapter_BrokerTargetSecretSharedWithAnMCPServer(t *testing.T) {
	ref := &api.ClientCredentialsSecretRef{Name: "gazelle-token-exchange-credentials"}
	owner := api.SecretOwner{Name: "gazelle-kubernetes", Namespace: "agent-platform", UID: "uid-k8s"}

	t.Run("warn, unbound", func(t *testing.T) {
		adapter := bindingAdapter(t, bindingSecret(ref.Name), WithSecretBindingMode(api.SecretBindingWarn))
		_, err := adapter.LoadClientCredentialsForConfig(context.Background(), ref, "agent-platform")
		require.NoError(t, err)
		_, err = adapter.LoadMCPServerClientCredentials(context.Background(), owner, api.SecretFieldTokenExchange, ref)
		require.NoError(t, err)
	})
	t.Run("enforce, annotated", func(t *testing.T) {
		adapter := bindingAdapter(t, bindingSecret(ref.Name, annotated("gazelle-kubernetes")), WithSecretBindingMode(api.SecretBindingEnforce))
		_, err := adapter.LoadClientCredentialsForConfig(context.Background(), ref, "agent-platform")
		require.NoError(t, err)
		_, err = adapter.LoadMCPServerClientCredentials(context.Background(), owner, api.SecretFieldTokenExchange, ref)
		require.NoError(t, err)
	})
}

// TestCredentialsAdapter_LoggedReadsAreCapped: the record of written warnings
// does not grow past maxLoggedReads, however many MCPServers come and go.
func TestCredentialsAdapter_LoggedReadsAreCapped(t *testing.T) {
	_ = captureBindingLogs(t)
	b := newSecretBinding()
	for i := range maxLoggedReads + 10 {
		req := &secretRequest{owner: api.SecretOwner{Name: fmt.Sprintf("s%d", i), Namespace: "ns"}, field: api.SecretFieldHeadersFrom}
		b.warnOnce(req, "ns", "x", "unbound", "warn %d", i)
	}
	entries := 0
	b.logged.Range(func(any, any) bool { entries++; return true })
	assert.LessOrEqual(t, entries, maxLoggedReads)
	assert.LessOrEqual(t, b.loggedCount.Load(), int64(maxLoggedReads))
}
