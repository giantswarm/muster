package workflow

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// listingClient answers ListWorkflows from a fixed set and counts every
// GetWorkflow, which building the tools must never need (#1225: the tools
// were built with one GET per workflow on top of the list, twice per meta-tool
// call on an installation with 282 workflows).
type listingClient struct {
	client.MusterClient
	workflows []musterv1alpha1.Workflow
	gets      int
}

func (c *listingClient) IsKubernetesMode() bool { return true }

func (c *listingClient) ListWorkflows(context.Context, string) ([]musterv1alpha1.Workflow, error) {
	return c.workflows, nil
}

func (c *listingClient) GetWorkflow(_ context.Context, name, _ string) (*musterv1alpha1.Workflow, error) {
	c.gets++
	for i := range c.workflows {
		if c.workflows[i].Name == name {
			return c.workflows[i].DeepCopy(), nil
		}
	}
	return nil, api.NewWorkflowNotFoundError(name)
}

func TestAdapter_GetTools_BuildsWorkflowToolsFromTheListing(t *testing.T) {
	cl := &listingClient{workflows: []musterv1alpha1.Workflow{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "triage", Labels: map[string]string{"team": "sre"}},
			Spec: musterv1alpha1.WorkflowSpec{
				Description: "Triage a cluster",
				Args: map[string]musterv1alpha1.ArgDefinition{
					"cluster": {Type: "string", Required: true, Description: "Cluster name"},
				},
				Steps: []musterv1alpha1.WorkflowStep{
					{ID: "pods", Tool: "x_kubernetes_list"},
					{ID: "alerts", Tool: "x_prometheus_get_alerts"},
					{ID: "health", Tool: "workflow_management-cluster-health"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "management-cluster-health"},
			Spec: musterv1alpha1.WorkflowSpec{
				Steps: []musterv1alpha1.WorkflowStep{{ID: "nodes", Tool: "x_kubernetes_list"}},
			},
		},
	}}
	adapter := NewAdapterWithClient(cl, "test-ns", nil, nil, "")
	t.Cleanup(adapter.stopGC)

	byName := map[string]api.ToolMetadata{}
	for _, tool := range adapter.GetTools() {
		byName[tool.Name] = tool
	}

	assert.Zero(t, cl.gets, "the tools are built from the listing alone")

	triage, ok := byName["action_triage"]
	require.True(t, ok, "one execution tool per listed workflow")
	assert.Equal(t, "Triage a cluster", triage.Description)
	assert.Equal(t, map[string]string{"team": "sre"}, triage.Labels)
	require.Len(t, triage.Args, 1)
	assert.Equal(t, "cluster", triage.Args[0].Name)
	assert.Equal(t, api.ArgTypeString, triage.Args[0].Type)
	assert.True(t, triage.Args[0].Required)
	steps := append([]string(nil), triage.StepTools...)
	sort.Strings(steps)
	assert.Equal(t, []string{"workflow_management-cluster-health", "x_kubernetes_list", "x_prometheus_get_alerts"}, steps,
		"the step tools travel with the tool so the aggregator derives the read-only hint without another lookup")

	health, ok := byName["action_management-cluster-health"]
	require.True(t, ok)
	assert.Empty(t, health.Args)
	assert.Equal(t, []string{"x_kubernetes_list"}, health.StepTools)

	_, ok = byName["workflow_list"]
	assert.True(t, ok, "the management tools are still there")
	assert.Empty(t, byName["workflow_list"].StepTools, "only execution tools carry step tools")
}
