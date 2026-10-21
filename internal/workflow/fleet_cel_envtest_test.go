package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	kubernetesclient "github.com/giantswarm/muster/v5/internal/client/kubernetes"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// TestFleetScanCELEnvtest applies Workflows to a real kube-apiserver with the
// generated CRD: a nested forEach with parallelism, paginate and jq is
// admitted and read back intact; a sub-step that is both a tool call and a
// loop, paginate on a loop, and parallelism or maxPages out of bounds are
// rejected at apply time. api.ValidateLoopsPagingAndJQ holds the rules the
// schema cannot (where a loop may nest, shadowed loop variables, jq).
//
// Requires envtest binaries: run via `make test-envtest`, which resolves
// KUBEBUILDER_ASSETS with setup-envtest. Skipped otherwise.
func TestFleetScanCELEnvtest(t *testing.T) {
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

	leaf := musterv1alpha1.WorkflowNestedStep{ID: "pods", Tool: "x_kubernetes_list", Paginate: &musterv1alpha1.WorkflowPaginate{MaxPages: 20}, JQ: "[.items[] | .metadata.name]"}
	nested := func(parallelism int) musterv1alpha1.WorkflowSubStep {
		return musterv1alpha1.WorkflowSubStep{ID: "per-cluster", ForEach: &musterv1alpha1.WorkflowNestedForEach{
			Items: "{{ .results.clusters.items }}", As: "cluster", Parallelism: parallelism, Steps: []musterv1alpha1.WorkflowNestedStep{leaf},
		}}
	}
	fleet := func(parallelism int, body ...musterv1alpha1.WorkflowSubStep) musterv1alpha1.WorkflowStep {
		return musterv1alpha1.WorkflowStep{ID: "fleet", JQ: "[.[] | .item]", ForEach: &musterv1alpha1.WorkflowForEach{
			Items: "{{ .input.mcs }}", As: "mc", Parallelism: parallelism, Steps: body,
		}}
	}
	clusters := musterv1alpha1.WorkflowSubStep{ID: "clusters", Tool: "x_kubernetes_list", Paginate: &musterv1alpha1.WorkflowPaginate{}}

	tests := []struct {
		name     string
		step     musterv1alpha1.WorkflowStep
		rejected string
	}{
		{name: "fleet-scan", step: fleet(4, clusters, nested(8))},
		{
			name:     "sub-step-tool-and-loop",
			step:     fleet(0, musterv1alpha1.WorkflowSubStep{ID: "both", Tool: "x_t", ForEach: nested(0).ForEach}),
			rejected: "exactly one of tool or forEach must be set",
		},
		{
			name:     "sub-step-neither-tool-nor-loop",
			step:     fleet(0, musterv1alpha1.WorkflowSubStep{ID: "empty"}),
			rejected: "exactly one of tool or forEach must be set",
		},
		{
			name:     "paginate-on-a-loop",
			step:     musterv1alpha1.WorkflowStep{ID: "loop", Paginate: &musterv1alpha1.WorkflowPaginate{}, ForEach: fleet(0, clusters).ForEach},
			rejected: "paginate applies to a tool step only",
		},
		{name: "parallelism-unset", step: fleet(0, clusters)},
		{name: "parallelism-above-the-limit", step: fleet(51, clusters), rejected: "should be less than or equal to 50"},
		{name: "nested-parallelism-above-the-limit", step: fleet(2, clusters, nested(51)), rejected: "should be less than or equal to 50"},
		{
			name:     "max-pages-above-the-limit",
			step:     musterv1alpha1.WorkflowStep{ID: "pods", Tool: "x_t", Paginate: &musterv1alpha1.WorkflowPaginate{MaxPages: 1001}},
			rejected: "should be less than or equal to 1000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := &musterv1alpha1.Workflow{
				ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: "default"},
				Spec:       musterv1alpha1.WorkflowSpec{Steps: []musterv1alpha1.WorkflowStep{tt.step}},
			}
			err := k8s.Create(t.Context(), wf)
			if tt.rejected != "" {
				require.True(t, apierrors.IsInvalid(err), "want an admission rejection, got %v", err)
				require.ErrorContains(t, err, tt.rejected)
				return
			}
			require.NoError(t, err)

			got := &musterv1alpha1.Workflow{}
			require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(wf), got))
			require.Equal(t, tt.step.ForEach.Parallelism, got.Spec.Steps[0].ForEach.Parallelism)
			require.Equal(t, tt.step.JQ, got.Spec.Steps[0].JQ)
			if len(tt.step.ForEach.Steps) > 1 {
				inner := got.Spec.Steps[0].ForEach.Steps[1].ForEach
				require.NotNil(t, inner)
				require.Equal(t, "cluster", inner.As)
				require.Equal(t, leaf.JQ, inner.Steps[0].JQ)
				require.Equal(t, 20, inner.Steps[0].Paginate.MaxPages)
			}
		})
	}
}
