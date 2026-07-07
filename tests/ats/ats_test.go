//go:build smoke || upgrade

package ats

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/tests/assertions"
)

const deploymentReadyTimeout = 3 * time.Minute

// newClusterClient builds a controller-runtime client from the kubeconfig
// ATS hands to test code, per app-test-suite docs/TEST_CONTRACT.md.
func newClusterClient(t *testing.T) client.Client {
	t.Helper()
	kubeconfigPath := os.Getenv("KUBECONFIG")
	require.NotEmpty(t, kubeconfigPath, "KUBECONFIG must be set by ATS")
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	require.NoError(t, err)
	kubeClient, err := client.New(restConfig, client.Options{Scheme: clientgoscheme.Scheme})
	require.NoError(t, err)
	return kubeClient
}

// newTarget maps the ATS_* environment contract onto the shared Target.
func newTarget(t *testing.T) assertions.Target {
	t.Helper()
	releaseName := os.Getenv("ATS_RELEASE_NAME")
	require.NotEmpty(t, releaseName, "ATS_RELEASE_NAME must be set by ATS")
	namespace := os.Getenv("ATS_RELEASE_NAMESPACE")
	require.NotEmpty(t, namespace, "ATS_RELEASE_NAMESPACE must be set by ATS")
	return assertions.Target{
		Namespace:   namespace,
		ReleaseName: releaseName,
		AppVersion:  os.Getenv("ATS_CHART_VERSION"),
	}
}

func TestClusterReachable(t *testing.T) {
	require.NoError(t, assertions.ClusterReachable(t.Context(), newClusterClient(t)))
}

func TestDeploymentReady(t *testing.T) {
	err := assertions.DeploymentReady(t.Context(), newClusterClient(t), newTarget(t), deploymentReadyTimeout)
	require.NoError(t, err)
}
