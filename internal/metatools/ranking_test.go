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
	docs := []string{
		"core_service_restart restart a running service",
		"prometheus_query_range query prometheus metrics over a time range",
		"core_workflow_list list all workflows",
	}

	ranked := rankBM25("prometheus metrics query", docs)
	require.NotEmpty(t, ranked)
	assert.Equal(t, 1, ranked[0].index, "the prometheus doc is the best match")
	for i := 1; i < len(ranked); i++ {
		assert.GreaterOrEqual(t, ranked[i-1].score, ranked[i].score)
	}
}

func TestRankBM25_DropsNonMatching(t *testing.T) {
	docs := []string{"alpha beta", "gamma delta"}
	ranked := rankBM25("nonexistentterm", docs)
	assert.Empty(t, ranked, "a query matching nothing returns nothing, not the whole corpus")
}

func TestRankBM25_EmptyInputs(t *testing.T) {
	assert.Nil(t, rankBM25("", []string{"a b"}))
	assert.Nil(t, rankBM25("a", nil))
}

func TestRoundScore_KeepsTinyPositiveScorePresent(t *testing.T) {
	// A score that would round to 0 must not collapse to 0, otherwise the
	// Score field's omitempty tag would drop it from a ranked result.
	assert.Equal(t, 0.0001, roundScore(1e-9))
	assert.Equal(t, 0.0, roundScore(0), "an unscored result stays 0 (and is omitted)")
	assert.Equal(t, 0.1235, roundScore(0.12345))
}
