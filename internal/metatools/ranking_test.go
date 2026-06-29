package metatools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenize(t *testing.T) {
	assert.Equal(t, []string{"workflow", "deploy", "app"}, tokenize("workflow_deploy_app"))
	assert.Equal(t, []string{"list", "pods"}, tokenize("ListPods"))
	assert.Equal(t, []string{"core", "service", "restart"}, tokenize("core-service.restart"))
	assert.Empty(t, tokenize("   "))
}

func TestRankBM25_OrdersByRelevance(t *testing.T) {
	docs := []rankDoc{
		{name: "core_service_restart", description: "restart a running service"},
		{name: "prometheus_query_range", description: "query prometheus metrics over a time range"},
		{name: "core_workflow_list", description: "list all workflows"},
	}

	ranked := rankBM25("prometheus metrics query", docs)
	require.NotEmpty(t, ranked)
	assert.Equal(t, 1, ranked[0].index, "the prometheus doc is the best match")
	for i := 1; i < len(ranked); i++ {
		assert.GreaterOrEqual(t, ranked[i-1].score, ranked[i].score)
	}
}

func TestRankBM25_DropsNonMatching(t *testing.T) {
	docs := []rankDoc{{name: "alpha", description: "beta"}, {name: "gamma", description: "delta"}}
	ranked := rankBM25("nonexistentterm", docs)
	assert.Empty(t, ranked, "a query matching nothing returns nothing, not the whole corpus")
}

func TestRankBM25_EmptyInputs(t *testing.T) {
	assert.Nil(t, rankBM25("", []rankDoc{{name: "a", description: "b"}}))
	assert.Nil(t, rankBM25("a", nil))
}

// docName returns the name of the doc at the given ranked position, for readable
// ordering assertions.
func docName(docs []rankDoc, ranked []rankedDoc, pos int) string {
	return docs[ranked[pos].index].name
}

// TestRankBM25_IntentBeatsUbiquitousVerb reproduces the tools-F3 finding: the
// placeholder example "list pods" must surface pod/kubernetes tools above the
// pagerduty *_list_* tools whose only match is the ubiquitous "list" verb.
func TestRankBM25_IntentBeatsUbiquitousVerb(t *testing.T) {
	docs := []rankDoc{
		{name: "x_pd_list_services", description: "List PagerDuty services."},
		{name: "x_pd_list_incidents", description: "List PagerDuty incidents."},
		{name: "x_pd_list_schedules", description: "List PagerDuty on-call schedules."},
		{name: "core_workflow_list", description: "List all workflows."},
		{name: "x_kubernetes_list", description: "List Kubernetes resources such as pods, deployments and services."},
		{name: "workflow_failing-pods", description: "Investigate failing pods in a cluster."},
		{name: "workflow_pod-health", description: "Report health of pods across namespaces."},
	}

	ranked := rankBM25("list pods", docs)
	require.NotEmpty(t, ranked)

	// The pod-oriented tools must outrank the pagerduty list_* tools, which only
	// match the down-weighted "list" verb.
	top := docName(docs, ranked, 0)
	assert.Contains(t, []string{"workflow_failing-pods", "workflow_pod-health", "x_kubernetes_list"}, top,
		"a pod tool should rank first for 'list pods', got %q", top)

	pos := make(map[string]int, len(ranked))
	for i := range ranked {
		pos[docName(docs, ranked, i)] = i
	}
	for _, podTool := range []string{"workflow_failing-pods", "workflow_pod-health", "x_kubernetes_list"} {
		for _, pdTool := range []string{"x_pd_list_services", "x_pd_list_incidents", "x_pd_list_schedules"} {
			require.Contains(t, pos, podTool)
			require.Contains(t, pos, pdTool)
			assert.Less(t, pos[podTool], pos[pdTool],
				"%q must rank above %q for 'list pods'", podTool, pdTool)
		}
	}
}

// TestRankBM25_NameMatchBeatsDescriptionMatch verifies the field weighting: a
// query term in the tool name outranks the same term buried in a description.
func TestRankBM25_NameMatchBeatsDescriptionMatch(t *testing.T) {
	docs := []rankDoc{
		{name: "core_service_restart", description: "Restart a service. Useful when the prometheus exporter is stuck."},
		{name: "prometheus_query", description: "Run a query."},
	}

	ranked := rankBM25("prometheus", docs)
	require.Len(t, ranked, 2)
	assert.Equal(t, "prometheus_query", docName(docs, ranked, 0),
		"a name match should outrank a description-only match")
}

func TestRoundScore_KeepsTinyPositiveScorePresent(t *testing.T) {
	// A score that would round to 0 must not collapse to 0, otherwise the
	// Score field's omitempty tag would drop it from a ranked result.
	assert.Equal(t, 0.0001, roundScore(1e-9))
	assert.Equal(t, 0.0, roundScore(0), "an unscored result stays 0 (and is omitted)")
	assert.Equal(t, 0.1235, roundScore(0.12345))
}
