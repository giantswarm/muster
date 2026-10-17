package metatools

import (
	"fmt"
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

func TestFoldPlural_SingularAndPluralShareAKey(t *testing.T) {
	for _, pair := range [][2]string{
		{"policy", "policies"},
		{"query", "queries"},
		{"version", "versions"},
		{"pod", "pods"},
		{"metric", "metrics"},
		{"value", "values"},
		{"cache", "caches"},
		{"match", "matches"},
		{"box", "boxes"},
		{"process", "processes"},
		{"status", "statuses"},
		{"replica", "replicas"},
		{"database", "databases"},
		{"retry", "retries"},
		{"key", "keys"},
		{"cookie", "cookies"},
		{"tie", "ties"},
		{"use", "uses"},
		{"id", "ids"},
		{"api", "apis"},
		{"cpu", "cpus"},
		{"gpu", "gpus"},
		{"menu", "menus"},
		{"crd", "crds"},
		{"vm", "vms"},
		{"type", "types"},
		{"size", "sizes"},
		{"release", "releases"},
		{"service", "services"},
		{"namespace", "namespaces"},
		{"alias", "aliases"},
		{"bias", "biases"},
		{"bus", "buses"},
		{"index", "indices"},
		{"index", "indexes"},
		{"matrix", "matrices"},
		{"vertex", "vertices"},
		{"analysis", "analyses"},
		{"child", "children"},
	} {
		assert.Equal(t, foldPlural(pair[0]), foldPlural(pair[1]), "%q and %q", pair[0], pair[1])
	}
}

func TestFoldPlural_KeepsWordsThatAreNotPlurals(t *testing.T) {
	for _, word := range []string{"status", "access", "ingress", "analysis", "redis", "series", "news", "dns", "https", "aws", "kms", "has", "does", "this", "as", "k8s", "s3"} {
		assert.Equal(t, word, foldPlural(word))
	}
}

// TestFoldPlural_KeepsUnrelatedWordsApart verifies that folding a singular
// does not merge it with a different word.
func TestFoldPlural_KeepsUnrelatedWordsApart(t *testing.T) {
	for _, pair := range [][2]string{
		{"plane", "plan"},
		{"state", "stat"},
		{"state", "stats"},
		{"note", "not"},
		{"core", "cors"},
		{"side", "sid"},
		{"here", "her"},
		{"news", "new"},
		{"lens", "len"},
		{"gcs", "gc"},
		{"has", "ha"},
		{"https", "http"},
		{"kms", "km"},
		{"rds", "rd"},
	} {
		assert.NotEqual(t, foldPlural(pair[0]), foldPlural(pair[1]), "%q and %q", pair[0], pair[1])
	}
}

// TestFoldPlural_StopVerbsFoldToThemselves guards the stopVerbs lookup, which
// runs on folded query terms: a key that folded to something else would never
// be found.
func TestFoldPlural_StopVerbsFoldToThemselves(t *testing.T) {
	for verb := range stopVerbs {
		assert.Equal(t, verb, foldPlural(verb))
	}
}

// TestRankBM25_PluralQueryMatchesSingular verifies that a query need not use
// the same number as the tool: "policies" finds a tool described with
// "policy", and "version" one described with "versions".
func TestRankBM25_PluralQueryMatchesSingular(t *testing.T) {
	docs := []rankDoc{
		{name: "list_jobs", summary: "List jobs, such as the retention policy job."},
		{name: "describe_table", summary: "Describe a table and its versions."},
		{name: "service_restart", summary: "Restart a running service."},
	}

	ranked := rankBM25("retention policies", docs)
	require.Len(t, ranked, 1)
	assert.Equal(t, "list_jobs", docName(docs, ranked, 0))

	ranked = rankBM25("version", docs)
	require.Len(t, ranked, 1)
	assert.Equal(t, "describe_table", docName(docs, ranked, 0))
}

// TestQueryTerms_DropsStopWords verifies that the query keeps its folded
// content words only, and a query of only stop words keeps them as typed.
func TestQueryTerms_DropsStopWords(t *testing.T) {
	assert.Equal(t, []string{"restart", foldPlural("services")}, queryTerms("how does it restart the services"))
	assert.Equal(t, []string{"does"}, queryTerms("does"), "a query of only stop words keeps them")
}

// TestRankBM25_FunctionWordsAreNotFolded verifies that "has" in a description
// is not taken for the plural of "ha" (high availability).
func TestRankBM25_FunctionWordsAreNotFolded(t *testing.T) {
	docs := []rankDoc{
		{name: "x_pg_enable_ha", summary: "Enable high availability (HA) for a database cluster."},
		{name: "x_k8s_get_pod", summary: "Get a pod.", details: "Returns an error if the pod has no node."},
	}

	ranked := rankBM25("ha", docs)
	require.Len(t, ranked, 1)
	assert.Equal(t, "x_pg_enable_ha", docName(docs, ranked, 0))
}

// TestRankBM25_PluralStopVerbStaysDownWeighted verifies that "lists" is
// down-weighted like "list", so the noun still drives ranking.
func TestRankBM25_PluralStopVerbStaysDownWeighted(t *testing.T) {
	docs := []rankDoc{
		{name: "x_pd_list_services", summary: "List PagerDuty services."},
		{name: "workflow_failing-pods", summary: "Investigate failing pods in a cluster."},
	}

	ranked := rankBM25("lists pod", docs)
	require.Len(t, ranked, 2)
	assert.Equal(t, "workflow_failing-pods", docName(docs, ranked, 0))
}

func TestRankBM25_OrdersByRelevance(t *testing.T) {
	docs := []rankDoc{
		{name: "core_service_restart", summary: "restart a running service"},
		{name: "prometheus_query_range", summary: "query prometheus metrics over a time range"},
		{name: "core_workflow_list", summary: "list all workflows"},
	}

	ranked := rankBM25("prometheus metrics query", docs)
	require.NotEmpty(t, ranked)
	assert.Equal(t, 1, ranked[0].index, "the prometheus doc is the best match")
	for i := 1; i < len(ranked); i++ {
		assert.GreaterOrEqual(t, ranked[i-1].score, ranked[i].score)
	}
}

func TestRankBM25_DropsNonMatching(t *testing.T) {
	docs := []rankDoc{{name: "alpha", summary: "beta"}, {name: "gamma", summary: "delta"}}
	ranked := rankBM25("nonexistentterm", docs)
	assert.Empty(t, ranked, "a query matching nothing returns nothing, not the whole corpus")
}

func TestRankBM25_EmptyInputs(t *testing.T) {
	assert.Nil(t, rankBM25("", []rankDoc{{name: "a", summary: "b"}}))
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
		{name: "x_pd_list_services", summary: "List PagerDuty services."},
		{name: "x_pd_list_incidents", summary: "List PagerDuty incidents."},
		{name: "x_pd_list_schedules", summary: "List PagerDuty on-call schedules."},
		{name: "core_workflow_list", summary: "List all workflows."},
		{name: "x_kubernetes_list", summary: "List Kubernetes resources such as pods, deployments and services."},
		{name: "workflow_failing-pods", summary: "Investigate failing pods in a cluster."},
		{name: "workflow_pod-health", summary: "Report health of pods across namespaces."},
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
		{name: "core_service_restart", summary: "Restart a service. Useful when the prometheus exporter is stuck."},
		{name: "prometheus_query", summary: "Run a query."},
	}

	ranked := rankBM25("prometheus", docs)
	require.Len(t, ranked, 2)
	assert.Equal(t, "prometheus_query", docName(docs, ranked, 0),
		"a name match should outrank a description-only match")
}

// TestRankBM25_DetailsOnlyMatchIsReturned verifies that words below the first
// line of a description are searchable: a tool whose only match is in its
// details is ranked, not dropped.
func TestRankBM25_DetailsOnlyMatchIsReturned(t *testing.T) {
	docs := []rankDoc{
		{name: "list_jobs", summary: "List background jobs.", details: "Includes the retention policy jobs and their last run."},
		{name: "service_restart", summary: "Restart a running service."},
	}

	ranked := rankBM25("retention policy", docs)
	require.Len(t, ranked, 1)
	assert.Equal(t, "list_jobs", docName(docs, ranked, 0))
}

// TestRankBM25_SummaryMatchBeatsDetailsMatch verifies the field weighting
// across the description: the same match ranks a name above a summary, and a
// summary above the details.
func TestRankBM25_SummaryMatchBeatsDetailsMatch(t *testing.T) {
	docs := []rankDoc{
		{name: "details_tool", summary: "Run a check.", details: "Reads the hypertable catalogue."},
		{name: "summary_tool", summary: "Run a hypertable check.", details: "Reads the catalogue."},
		{name: "hypertable_tool", summary: "Run a check.", details: "Reads the catalogue."},
	}

	ranked := rankBM25("hypertable", docs)
	require.Len(t, ranked, 3)
	assert.Equal(t, "hypertable_tool", docName(docs, ranked, 0), "a name match ranks first")
	assert.Equal(t, "summary_tool", docName(docs, ranked, 1), "a summary match beats a details match")
	assert.Equal(t, "details_tool", docName(docs, ranked, 2))
}

// TestRankBM25_DetailsMatchCountsInAMixedCatalogue verifies that details keep
// their weight when most tools have one-line descriptions: the details length
// is averaged over the tools that have details, not over every tool, so a
// tool matching both query terms in its details beats one matching the common
// term in its summary however many one-line tools the catalogue holds.
func TestRankBM25_DetailsMatchCountsInAMixedCatalogue(t *testing.T) {
	for _, oneLine := range []int{0, 10, 100, 500} {
		docs := []rankDoc{
			{name: "list_jobs", summary: "List background jobs.", details: "Includes each retention policy job and its last run time and owner."},
			{name: "describe_table", summary: "Describe a table and its policy."},
		}
		for i := 0; i < oneLine; i++ {
			docs = append(docs, rankDoc{name: fmt.Sprintf("tool_%03d", i), summary: "Do something else."})
		}

		ranked := rankBM25("retention policy", docs)
		require.Len(t, ranked, 2)
		assert.Equal(t, "list_jobs", docName(docs, ranked, 0), "with %d one-line tools", oneLine)
	}
}

// TestRankBM25_IntentSurvivesRealisticDetails is the "list pods" case from
// #932 with details that mention pods in passing: the pod tools still outrank
// the pagerduty list tools. The catalogue holds unrelated tools with
// summaries and details of realistic length too, as a real one does; in the
// seven tools alone "pods" is in six, which leaves it almost no weight
// against "list".
func TestRankBM25_IntentSurvivesRealisticDetails(t *testing.T) {
	pdDetails := "Returns a list of results. Use limit and offset to page through them, including teams that run pods."
	docs := []rankDoc{
		{name: "x_pd_list_services", summary: "List PagerDuty services.", details: pdDetails},
		{name: "x_pd_list_incidents", summary: "List PagerDuty incidents.", details: pdDetails},
		{name: "x_pd_list_schedules", summary: "List PagerDuty on-call schedules.", details: pdDetails},
		{name: "core_workflow_list", summary: "List all workflows."},
		{name: "x_kubernetes_list", summary: "List Kubernetes resources.", details: "Resources such as pods, deployments and services, filtered by namespace and labels."},
		{name: "workflow_failing-pods", summary: "Investigate failing pods in a cluster."},
		{name: "workflow_pod-health", summary: "Report health of pods across namespaces."},
	}
	for i := 0; i < 50; i++ {
		docs = append(docs, rankDoc{
			name:    fmt.Sprintf("x_other_tool_%03d", i),
			summary: "Return the stored record for an id, with its owner and state.",
			details: "Takes an id and returns the record, with paging through a cursor and an optional filter by owner and state.",
		})
	}

	ranked := rankBM25("list pods", docs)
	pos := make(map[string]int, len(ranked))
	for i := range ranked {
		pos[docName(docs, ranked, i)] = i
	}
	for _, podTool := range []string{"workflow_failing-pods", "workflow_pod-health", "x_kubernetes_list"} {
		for _, pdTool := range []string{"x_pd_list_services", "x_pd_list_incidents", "x_pd_list_schedules"} {
			require.Contains(t, pos, podTool)
			require.Contains(t, pos, pdTool)
			assert.Less(t, pos[podTool], pos[pdTool], "%q must rank above %q for 'list pods'", podTool, pdTool)
		}
	}
}

// TestRankBM25_FunctionWordsDoNotMatch verifies that a natural-language query
// narrows by its content words: "the" in every description matches nothing.
func TestRankBM25_FunctionWordsDoNotMatch(t *testing.T) {
	docs := []rankDoc{
		{name: "service_restart", summary: "Restart a running service."},
		{name: "list_jobs", summary: "List jobs.", details: "Shows the schedule of the job."},
		{name: "grafana_dashboard", summary: "Fetch a dashboard.", details: "Returns the panels of the dashboard."},
	}

	ranked := rankBM25("how do I restart the service", docs)
	require.Len(t, ranked, 1)
	assert.Equal(t, "service_restart", docName(docs, ranked, 0))

	ranked = rankBM25("the", docs)
	assert.Len(t, ranked, 2, "a query of only function words still ranks by them")
}

// TestRankBM25_EqualDocsScoreEqually verifies that scoring does not depend on
// map iteration order: two equally relevant tools get the same score on every
// call and keep their input order.
func TestRankBM25_EqualDocsScoreEqually(t *testing.T) {
	docs := []rankDoc{
		{name: "tool_a", summary: "Query prometheus metrics over a time range.", details: "Supports step and timeout."},
		{name: "tool_b", summary: "Query prometheus metrics over a time range.", details: "Supports step and timeout."},
	}
	for i := 0; i < 50; i++ {
		ranked := rankBM25("prometheus metrics time range step timeout query", docs)
		require.Len(t, ranked, 2)
		require.Equal(t, ranked[0].score, ranked[1].score)
		require.Equal(t, "tool_a", docName(docs, ranked, 0))
	}
}

func TestRoundScore_KeepsTinyPositiveScorePresent(t *testing.T) {
	// A score that would round to 0 must not collapse to 0, otherwise the
	// Score field's omitempty tag would drop it from a ranked result.
	assert.Equal(t, 0.0001, roundScore(1e-9))
	assert.Equal(t, 0.0, roundScore(0), "an unscored result stays 0 (and is omitted)")
	assert.Equal(t, 0.1235, roundScore(0.12345))
}
