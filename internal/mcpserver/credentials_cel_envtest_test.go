package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// TestCredentialCELEnvtest installs the generated CRD in a real kube-apiserver,
// which also proves that its CEL rules pass the API server's cost check, and
// applies MCPServers to it: a server with a value in a credential header (in
// any case), with a user name or password in its URL, or with env on a remote
// server is refused, and so is a headersFrom that names one header twice in
// two cases; a normal server and one with headersFrom are accepted.
// api.ValidateNoInlineCredential is the same rule where muster loads a server
// (TestValidateNoInlineCredential).
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestCredentialCELEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via `make test-envtest`")
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "helm", "muster", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err, "start envtest")
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	k8s, err := kubernetesclient.New(cfg)
	require.NoError(t, err)

	const (
		inHeaders = "headers must not hold a credential"
		inURL     = "url must not hold a user name or password"
		inEnv     = "env field is only allowed when type is stdio"
		twice     = "headersFrom names a header twice"
	)
	fromSecret := func(names ...string) []musterv1alpha1.MCPServerHeaderFrom {
		entries := make([]musterv1alpha1.MCPServerHeaderFrom, 0, len(names))
		for _, name := range names {
			entries = append(entries, musterv1alpha1.MCPServerHeaderFrom{
				Name:      name,
				ValueFrom: musterv1alpha1.MCPServerValueSource{Type: "Secret", Name: "github-token", Key: "token"},
			})
		}
		return entries
	}

	tests := []struct {
		name     string
		url      string
		headers  map[string]string
		env      map[string]string
		from     []musterv1alpha1.MCPServerHeaderFrom
		rejected string
	}{
		{name: "plain", headers: map[string]string{"X-MCP-Toolsets": "repos"}},
		{name: "empty-authorization", headers: map[string]string{"Authorization": ""}},
		{name: "headers-from", from: fromSecret("Authorization")},
		{name: "headers-from-two-headers", from: fromSecret("Authorization", "X-Org")},
		{name: "headers-from-same-header-in-two-cases", from: fromSecret("Authorization", "authorization"), rejected: twice},
		{name: "at-in-path", url: "https://marge.example.com/users/@me"},
		{name: "authorization", headers: map[string]string{"Authorization": "Bearer t"}, rejected: inHeaders},
		{name: "authorization-lower-case", headers: map[string]string{"authorization": "Bearer t"}, rejected: inHeaders},
		{name: "proxy-authorization", headers: map[string]string{"Proxy-Authorization": "Basic eDp5"}, rejected: inHeaders},
		{name: "cookie", headers: map[string]string{"COOKIE": "session=1"}, rejected: inHeaders},
		{name: "x-api-key", headers: map[string]string{"X-Api-Key": "k"}, rejected: inHeaders},
		{name: "url-user-and-password", url: "https://user:pass@marge.example.com/mcp", rejected: inURL},
		{name: "url-user", url: "https://user@marge.example.com/mcp", rejected: inURL},
		{name: "env-on-remote", env: map[string]string{"TOKEN": "t"}, rejected: inEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.url
			if url == "" {
				url = "https://marge.example.com/mcp"
			}
			server := &musterv1alpha1.MCPServer{
				ObjectMeta: metav1.ObjectMeta{Name: "credentials-" + tt.name, Namespace: "default"},
				Spec: musterv1alpha1.MCPServerSpec{
					Type:        "streamable-http",
					URL:         url,
					Headers:     tt.headers,
					Env:         tt.env,
					HeadersFrom: tt.from,
				},
			}
			err := k8s.Create(t.Context(), server)
			if tt.rejected == "" {
				require.NoError(t, err)
				return
			}
			require.True(t, apierrors.IsInvalid(err), "want an admission rejection, got %v", err)
			require.ErrorContains(t, err, tt.rejected)
		})
	}
}
