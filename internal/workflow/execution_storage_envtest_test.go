package workflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/giantswarm/muster/v5/internal/api"
	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
)

// TestExecutionStorageEnvtest stores and lists WorkflowExecutions against a
// real kube-apiserver with the generated CRD, including a workflow name longer
// than the 63-character label-value limit: List filters on the CRD's
// selectable fields, which the fake client does not check.
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestExecutionStorageEnvtest(t *testing.T) {
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

	storage := newK8sExecutionStorage(k8s.Client, "default")
	ctx := t.Context()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	require.NoError(t, storage.Store(ctx, sampleExecution("long-old", longWorkflowName, api.WorkflowExecutionFailed, now.Add(-2*time.Hour))))
	require.NoError(t, storage.Store(ctx, sampleExecution("long-new", longWorkflowName, api.WorkflowExecutionCompleted, now.Add(-time.Hour))))
	require.NoError(t, storage.Store(ctx, sampleExecution("short-1", "short", api.WorkflowExecutionCompleted, now)))

	resp, err := storage.List(ctx, &api.ListWorkflowExecutionsRequest{WorkflowName: longWorkflowName})
	require.NoError(t, err)
	require.Equal(t, 2, resp.Total)
	require.Equal(t, "long-new", resp.Executions[0].ExecutionID)
	require.Equal(t, "long-old", resp.Executions[1].ExecutionID)

	resp, err = storage.List(ctx, &api.ListWorkflowExecutionsRequest{Status: api.WorkflowExecutionCompleted})
	require.NoError(t, err)
	require.Equal(t, 2, resp.Total)

	resp, err = storage.List(ctx, &api.ListWorkflowExecutionsRequest{WorkflowName: longWorkflowName, Status: api.WorkflowExecutionFailed})
	require.NoError(t, err)
	require.Equal(t, 1, resp.Total)
	require.Equal(t, "long-old", resp.Executions[0].ExecutionID)
}
