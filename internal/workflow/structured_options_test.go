package workflow

import (
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundedInt(t *testing.T) {
	tests := []struct {
		name    string
		value   interface{}
		want    int
		wantErr bool
	}{
		{name: "a JSON number", value: float64(4), want: 4},
		{name: "a Go int", value: 7, want: 7},
		{name: "an int64", value: int64(9), want: 9},
		{name: "the maximum", value: float64(50), want: 50},
		{name: "zero is spelt out, not unset", value: float64(0), wantErr: true},
		{name: "above the maximum", value: float64(51), wantErr: true},
		{name: "a fraction", value: 2.5, wantErr: true},
		{name: "a string", value: "4", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := boundedInt(map[string]interface{}{"parallelism": tt.value}, "parallelism", 50)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "parallelism")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
	got, err := boundedInt(map[string]interface{}{}, "parallelism", 50)
	require.NoError(t, err)
	assert.Zero(t, got, "an absent field is unset")
}

// TestConvertWorkflowStepsFleetOptions verifies that the structured
// create/validate arguments carry a nested forEach, parallelism, paginate and
// jq into the definition.
func TestConvertWorkflowStepsFleetOptions(t *testing.T) {
	steps, err := convertWorkflowSteps([]interface{}{map[string]interface{}{
		"id": "fleet",
		"jq": "[.[] | .item]",
		"forEach": map[string]interface{}{
			"items":       "{{ .input.mcs }}",
			"as":          "mc",
			"parallelism": float64(4),
			"steps": []interface{}{
				map[string]interface{}{
					"id":       "clusters",
					"tool":     "x_kubernetes_list",
					"paginate": map[string]interface{}{"maxPages": float64(5), "itemsField": "data.items", "tokenField": "data.next", "tokenArg": "cursor"},
					"jq":       "[.items[] | .name]",
				},
				map[string]interface{}{
					"id": "per-cluster",
					"forEach": map[string]interface{}{
						"items":       "{{ .results.clusters }}",
						"as":          "cluster",
						"parallelism": float64(8),
						"steps": []interface{}{
							map[string]interface{}{"id": "pods", "tool": "x_kubernetes_list"},
						},
					},
				},
			},
		},
	}})
	require.NoError(t, err)
	require.Len(t, steps, 1)
	fleet := steps[0]
	assert.Equal(t, "[.[] | .item]", fleet.JQ)
	assert.Equal(t, 4, fleet.ForEach.Parallelism)
	clusters := fleet.ForEach.Steps[0]
	assert.Equal(t, &api.WorkflowPaginate{ItemsField: "data.items", TokenField: "data.next", TokenArg: "cursor", MaxPages: 5}, clusters.Paginate)
	assert.Equal(t, "[.items[] | .name]", clusters.JQ)
	nested := fleet.ForEach.Steps[1]
	assert.Empty(t, nested.Tool)
	require.NotNil(t, nested.ForEach)
	assert.Equal(t, 8, nested.ForEach.Parallelism)
	assert.Equal(t, "pods", nested.ForEach.Steps[0].ID)
}

func TestConvertToolOptionsRejectsMalformedFields(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"paginate is not an object": {"paginate": "yes"},
		"an empty items field":      {"paginate": map[string]interface{}{"itemsField": ""}},
		"maxPages above the bound":  {"paginate": map[string]interface{}{"maxPages": float64(1001)}},
		"jq is not a string":        {"jq": 42},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := convertToolOptions(m)
			assert.Error(t, err)
		})
	}
}
