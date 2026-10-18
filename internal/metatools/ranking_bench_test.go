package metatools

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// benchVocabulary is the word pool the benchmark descriptions are drawn from:
// infrastructure nouns and verbs, plurals and function words, as in real tool
// descriptions.
var benchVocabulary = strings.Fields(`
	the a an of to in for with by from on and or is are this that it its
	list get create delete update describe query search show return returns
	pod pods node nodes cluster clusters namespace namespaces deployment deployments
	service services ingress secret secrets config configmap volume volumes
	metric metrics alert alerts incident incidents schedule schedules dashboard
	dashboards panel panels log logs trace traces span spans event events
	policy policies retention chunk chunks table tables hypertable index indices
	database databases schema schemas job jobs run runs status statuses
	label labels selector selectors limit offset page cursor filter filters
	time range window bucket interval duration timeout retry retries
	user users team teams owner owners role roles permission permissions
	release releases chart charts version versions image images registry
	https api apis endpoint endpoints request requests response responses
`)

// benchTools builds n tools with a one-line summary and two paragraphs of
// details, about 120 words each, from a fixed seed.
func benchTools(n int) []mcp.Tool {
	rng := rand.New(rand.NewSource(1534))
	words := func(k int) string {
		w := make([]string, k)
		for i := range w {
			w[i] = benchVocabulary[rng.Intn(len(benchVocabulary))]
		}
		return strings.Join(w, " ")
	}
	tools := make([]mcp.Tool, n)
	for i := range tools {
		tools[i] = mcp.Tool{
			Name:        fmt.Sprintf("x_bench_%s_%s_%03d", benchVocabulary[rng.Intn(len(benchVocabulary))], benchVocabulary[rng.Intn(len(benchVocabulary))], i),
			Description: fmt.Sprintf("%s.\n\n%s.\n\n%s.", words(12), words(60), words(60)),
			InputSchema: mcp.ToolInputSchema{Type: "object"},
		}
	}
	return tools
}

// BenchmarkRankBM25 measures ranking alone over 300 tools with multi-paragraph
// descriptions, analysing every tool: a query's cost without the cache.
func BenchmarkRankBM25(b *testing.B) {
	tools := benchTools(300)
	docs := make([]rankDoc, len(tools))
	for i, t := range tools {
		summary, details := splitDescription(t.Description)
		docs[i] = rankDoc{name: t.Name, summary: summary, details: details}
	}
	b.ReportAllocs()
	for b.Loop() {
		rankBM25("retention policies for hypertable chunks", docs)
	}
}

// BenchmarkRankAnalyzedCached measures the same ranking with every tool's
// analysis already cached, the cost a filter_tools query pays once the
// provider has seen the catalogue.
func BenchmarkRankAnalyzedCached(b *testing.B) {
	tools := benchTools(300)
	cache := newRankTermCache()
	cache.analyze(tools)
	b.ReportAllocs()
	for b.Loop() {
		rankAnalyzed("retention policies for hypertable chunks", cache.analyze(tools))
	}
}

// BenchmarkFilterToolsQuery measures a whole filter_tools query over the same
// catalogue on one provider, whose cache is warm after the first query, to
// weigh the ranking against the rest of the call.
func BenchmarkFilterToolsQuery(b *testing.B) {
	mock := &mockMetaToolsHandler{tools: benchTools(300)}
	defer registerMockHandler(mock)()
	provider := NewProvider()
	args := map[string]interface{}{"query": "retention policies for hypertable chunks"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := provider.ExecuteTool(context.Background(), "filter_tools", args); err != nil {
			b.Fatal(err)
		}
	}
}
