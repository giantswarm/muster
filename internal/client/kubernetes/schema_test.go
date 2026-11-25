package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

func TestSpecHasField(t *testing.T) {
	doc := []byte(`{"components":{"schemas":{
		"io.giantswarm.muster.v1alpha1.MCPServer":{"x-kubernetes-group-version-kind":[{"group":"muster.giantswarm.io","version":"v1alpha1","kind":"MCPServer"}],
		 "properties":{"spec":{"properties":{"url":{},"approval":{}}}}},
		"io.giantswarm.muster.v1alpha1.Workflow":{"x-kubernetes-group-version-kind":[{"group":"muster.giantswarm.io","version":"v1alpha1","kind":"Workflow"}],
		 "properties":{"spec":{"properties":{"steps":{}}}}}}}}`)
	known, err := specHasField(doc, "approval")
	require.NoError(t, err)
	assert.True(t, known)
	known, err = specHasField(doc, "nonexistent")
	require.NoError(t, err)
	assert.False(t, known)
	_, err = specHasField([]byte(`{"components":{"schemas":{}}}`), "approval")
	require.ErrorContains(t, err, "has no MCPServer")
}

// TestMCPServerSpecHasFieldEnvtest reads the served schema of a real API
// server: the chart's CRD knows spec.approval; a CRD without it (an install
// whose CRD was not upgraded) is detected.
func TestMCPServerSpecHasFieldEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via `make test-envtest`")
	}
	chartCRD := filepath.Join("..", "..", "..", "helm", "muster", "crds", "muster.giantswarm.io_mcpservers.yaml")

	stale := t.TempDir()
	raw, err := os.ReadFile(chartCRD)
	require.NoError(t, err)
	var crd unstructured.Unstructured
	require.NoError(t, yaml.Unmarshal(raw, &crd.Object))
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	for i := range versions {
		unstructured.RemoveNestedField(versions[i].(map[string]any), "schema", "openAPIV3Schema", "properties", "spec", "properties", "approval")
	}
	require.NoError(t, unstructured.SetNestedSlice(crd.Object, versions, "spec", "versions"))
	staleRaw, err := yaml.Marshal(crd.Object)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(stale, "mcpservers.yaml"), staleRaw, 0o600))

	for name, tc := range map[string]struct {
		path string
		want bool
	}{
		"chart CRD": {chartCRD, true},
		"stale CRD": {filepath.Join(stale, "mcpservers.yaml"), false},
	} {
		t.Run(name, func(t *testing.T) {
			env := &envtest.Environment{CRDDirectoryPaths: []string{tc.path}, ErrorIfCRDPathMissing: true}
			cfg, err := env.Start()
			require.NoError(t, err)
			t.Cleanup(func() { _ = env.Stop() })
			c, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { _ = c.Close() })

			// The API server publishes a new CRD's schema shortly after it
			// is established.
			var known bool
			require.Eventually(t, func() bool {
				var err error
				known, err = c.MCPServerSpecHasField(context.Background(), "approval")
				return err == nil
			}, 30*time.Second, 200*time.Millisecond)
			assert.Equal(t, tc.want, known)
		})
	}
}
