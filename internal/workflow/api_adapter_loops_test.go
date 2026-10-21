package workflow

import (
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkflowCRDRoundTripKeepsFleetOptions verifies that a nested forEach,
// parallelism, paginate and jq survive the conversion to the Workflow
// resource and back -- the path every created workflow takes.
func TestWorkflowCRDRoundTripKeepsFleetOptions(t *testing.T) {
	yes := true
	wf := &api.Workflow{
		Name: "fleet",
		Steps: []api.WorkflowStep{
			{ID: "mcs", Tool: "x_list", Paginate: &api.WorkflowPaginate{ItemsField: "data.items", TokenField: "data.next", TokenArg: "cursor", MaxPages: 3}, JQ: "[.data.items[]]"},
			{
				ID: "fleet",
				JQ: "[.[] | .results]",
				ForEach: &api.WorkflowForEach{
					Items:       "{{ .results.mcs }}",
					As:          "mc",
					Parallelism: 4,
					Steps: []api.WorkflowSubStep{
						{ID: "clusters", Tool: "x_list", Paginate: &api.WorkflowPaginate{MaxPages: 5}, JQ: "[.items[]]"},
						{
							ID:           "per-cluster",
							JQ:           "[.[] | .item]",
							AllowFailure: true,
							Condition:    &api.WorkflowCondition{Template: "{{ true }}"},
							ForEach: &api.WorkflowForEach{
								Items:       "{{ .results.clusters }}",
								As:          "cluster",
								Parallelism: 8,
								Steps: []api.WorkflowSubStep{{
									ID:           "pods",
									Tool:         "x_list",
									Args:         map[string]interface{}{"cluster": "{{ .vars.cluster }}"},
									Paginate:     &api.WorkflowPaginate{MaxPages: 20},
									JQ:           "[.items[] | .name]",
									AllowFailure: true,
									Output:       &yes,
									Condition:    &api.WorkflowCondition{Template: "{{ true }}"},
								}},
							},
						},
					},
				},
			},
		},
		OnFailure: []api.WorkflowSubStep{{ID: "undo", Tool: "x_undo", JQ: ".ok"}},
	}

	a := &Adapter{}
	back := a.convertCRDToWorkflow(a.convertWorkflowToCRD(wf))

	require.Len(t, back.Steps, 2)
	assert.Equal(t, wf.Steps[0].Paginate, back.Steps[0].Paginate)
	assert.Equal(t, wf.Steps[0].JQ, back.Steps[0].JQ)
	fleet := back.Steps[1]
	assert.Equal(t, "[.[] | .results]", fleet.JQ)
	assert.Equal(t, 4, fleet.ForEach.Parallelism)
	assert.Equal(t, wf.Steps[1].ForEach.Steps[0].Paginate, fleet.ForEach.Steps[0].Paginate)
	nested := fleet.ForEach.Steps[1]
	assert.Equal(t, "[.[] | .item]", nested.JQ)
	assert.True(t, nested.AllowFailure)
	assert.NotNil(t, nested.Condition)
	require.NotNil(t, nested.ForEach)
	assert.Equal(t, "cluster", nested.ForEach.As)
	assert.Equal(t, 8, nested.ForEach.Parallelism)
	require.Len(t, nested.ForEach.Steps, 1)
	want, got := wf.Steps[1].ForEach.Steps[1].ForEach.Steps[0], nested.ForEach.Steps[0]
	assert.Equal(t, want.ID, got.ID)
	assert.Equal(t, want.Tool, got.Tool)
	assert.Equal(t, want.Args, got.Args)
	assert.Equal(t, want.Paginate, got.Paginate)
	assert.Equal(t, want.JQ, got.JQ)
	assert.Equal(t, want.AllowFailure, got.AllowFailure)
	assert.Equal(t, want.Output, got.Output)
	require.NotNil(t, got.Condition)
	assert.Equal(t, want.Condition.Template, got.Condition.Template)
	assert.Equal(t, ".ok", back.OnFailure[0].JQ)
}
