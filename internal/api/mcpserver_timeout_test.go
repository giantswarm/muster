package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func TestOperationTimeout(t *testing.T) {
	tests := []struct {
		name        string
		timeout     int
		want        time.Duration
		wantClamped bool
	}{
		{name: "unset", want: 30 * time.Second},
		{name: "negative", timeout: -5, want: 30 * time.Second},
		{name: "set", timeout: 120, want: 2 * time.Minute},
		{name: "at the maximum", timeout: MaxTimeoutSeconds, want: 5 * time.Minute},
		{name: "above the maximum", timeout: MaxTimeoutSeconds + 1, want: 5 * time.Minute, wantClamped: true},
		{name: "far above the maximum", timeout: 3600, want: 5 * time.Minute, wantClamped: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeout, clamped := (&MCPServer{Timeout: tt.timeout}).OperationTimeout()
			require.Equal(t, tt.want, timeout)
			require.Equal(t, tt.wantClamped, clamped)
		})
	}
}

func TestMaxTimeoutSecondsMatchesCRD(t *testing.T) {
	crdPath := filepath.Join("..", "..", "helm", "muster-crds", "files", "crds", "muster.giantswarm.io_mcpservers.yaml")
	raw, err := os.ReadFile(crdPath)
	require.NoError(t, err)

	var crd apiextensionsv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(raw, &crd))
	require.NotEmpty(t, crd.Spec.Versions)

	for _, version := range crd.Spec.Versions {
		timeoutSchema := version.Schema.OpenAPIV3Schema.Properties["spec"].Properties["timeout"]
		require.NotNil(t, timeoutSchema.Maximum, "version %s", version.Name)
		require.Equal(t, float64(MaxTimeoutSeconds), *timeoutSchema.Maximum, "version %s", version.Name)
	}
}
