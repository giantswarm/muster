package metatools

import (
	"fmt"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRankTermCache_RanksAsUncached verifies that ranking cached analyses
// gives exactly the scores and order of analysing every tool per query, on a
// first (cold) and second (warm) query.
func TestRankTermCache_RanksAsUncached(t *testing.T) {
	tools := benchTools(300)
	docs := make([]rankDoc, len(tools))
	for i, tool := range tools {
		summary, details := splitDescription(tool.Description)
		docs[i] = rankDoc{name: tool.Name, summary: summary, details: details}
	}
	cache := newRankTermCache()

	for _, query := range []string{"retention policies for hypertable chunks", "list pods", "https api endpoints", "the"} {
		want := rankBM25(query, docs)
		require.NotEmpty(t, want, query)
		for pass := 0; pass < 2; pass++ {
			assert.Equal(t, want, rankAnalyzed(query, cache.analyze(tools)), "%q, pass %d", query, pass)
		}
	}
}

func TestRankTermCache_ReusesAnAnalysis(t *testing.T) {
	cache := newRankTermCache()
	tools := []mcp.Tool{{Name: "a", Description: "List pods."}, {Name: "b", Description: "Restart a service."}}

	first := cache.analyze(tools)
	second := cache.analyze(tools)
	require.Len(t, second, 2)
	assert.Same(t, first[0], second[0])
	assert.Same(t, first[1], second[1])
}

func TestRankTermCache_AnalysesAChangedDescriptionAgain(t *testing.T) {
	cache := newRankTermCache()
	before := cache.analyze([]mcp.Tool{{Name: "a", Description: "List pods."}})
	after := cache.analyze([]mcp.Tool{{Name: "a", Description: "List deployments."}})

	assert.NotSame(t, before[0], after[0])
	assert.Equal(t, 1, after[0].tf[1]["deployment"])
	assert.Zero(t, after[0].tf[1]["pod"])
}

// TestRankTermCache_StaysBounded verifies the two generations: the cache never
// holds more than two generations of tools, and a tool still in use when its
// generation is retired is kept.
func TestRankTermCache_StaysBounded(t *testing.T) {
	cache := newRankTermCache()
	kept := mcp.Tool{Name: "kept", Description: "Still in use."}
	original := cache.analyze([]mcp.Tool{kept})[0]

	for i := 0; i < 2*rankCacheGeneration; i++ {
		cache.analyze([]mcp.Tool{{Name: fmt.Sprintf("tool_%d", i), Description: "Do something."}})
		if i == rankCacheGeneration {
			// "kept" is in the previous generation now; using it moves it to
			// the current one.
			assert.Same(t, original, cache.analyze([]mcp.Tool{kept})[0])
		}
	}

	assert.LessOrEqual(t, len(cache.current)+len(cache.previous), 2*rankCacheGeneration)
	assert.Same(t, original, cache.analyze([]mcp.Tool{kept})[0], "a tool used in every generation stays cached")
}

func TestRankTermCache_NilAnalysesEveryTool(t *testing.T) {
	var cache *rankTermCache
	docs := cache.analyze([]mcp.Tool{{Name: "a", Description: "List pods."}})
	require.Len(t, docs, 1)
	assert.Equal(t, 1, docs[0].tf[1]["pod"])
}

// TestRankTermCache_ConcurrentQueries runs queries over overlapping tools from
// several goroutines; under the race detector it shows the cache and the
// shared analyses are safe to use concurrently.
func TestRankTermCache_ConcurrentQueries(t *testing.T) {
	cache := newRankTermCache()
	tools := benchTools(50)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				subset := tools[(g*5)%len(tools):]
				rankAnalyzed("pods in a namespace", cache.analyze(subset))
			}
		}(g)
	}
	wg.Wait()
}

// TestFilterTools_RanksAChangedToolByItsNewDescription verifies the cache end
// to end: after a server's tool changes its description, filter_tools ranks it
// by the new one.
func TestFilterTools_RanksAChangedToolByItsNewDescription(t *testing.T) {
	mock := &mockMetaToolsHandler{tools: []mcp.Tool{
		{Name: "x_jobs_list", Description: "List background jobs."},
		{Name: "x_service_restart", Description: "Restart a running service."},
	}}
	defer registerMockHandler(mock)()
	provider := NewProvider()

	resp := filterResultWith(t, provider, map[string]interface{}{"query": "retention policy"})
	assert.Empty(t, resp.Tools)

	mock.tools[0].Description = "List background jobs.\nIncludes each retention policy job."
	resp = filterResultWith(t, provider, map[string]interface{}{"query": "retention policy"})
	require.Len(t, resp.Tools, 1)
	assert.Equal(t, "x_jobs_list", resp.Tools[0].Name)
}
