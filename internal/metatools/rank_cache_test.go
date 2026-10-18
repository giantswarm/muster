package metatools

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRankTermCache_RanksAsBefore verifies that ranking cached analyses gives
// exactly the scores and order of the ranker before the cache
// (referenceRankBM25), on a cold and a warm query.
func TestRankTermCache_RanksAsBefore(t *testing.T) {
	tools := benchTools(300)
	docs := make([]rankDoc, len(tools))
	for i, tool := range tools {
		summary, details := splitDescription(tool.Description)
		docs[i] = rankDoc{name: tool.Name, summary: summary, details: details}
	}
	cache := newRankTermCache()

	for _, query := range []string{"retention policies for hypertable chunks", "list pods", "https api endpoints", "the"} {
		want := referenceRankBM25(query, docs)
		require.NotEmpty(t, want, query)
		for pass := 0; pass < 2; pass++ {
			assert.Equal(t, want, rankAnalyzed(queryTerms(query), cache.analyze(tools)), "%q, pass %d", query, pass)
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

func TestRankTermCache_ReplacesTheAnalysisOfAChangedDescription(t *testing.T) {
	cache := newRankTermCache()
	before := cache.analyze([]mcp.Tool{{Name: "a", Description: "List pods."}})
	after := cache.analyze([]mcp.Tool{{Name: "a", Description: "List deployments."}})

	assert.NotSame(t, before[0], after[0])
	assert.Equal(t, 1, after[0].tf[1]["deployment"])
	assert.Zero(t, after[0].tf[1]["pod"])
	assert.Len(t, cache.entries, 1, "a tool has one entry however often its description changes")
}

// TestRankTermCache_StaysWarmAboveTheSweepSize verifies that a catalogue
// larger than rankCacheSweepAt is analysed once: no query evicts tools that
// queries use.
func TestRankTermCache_StaysWarmAboveTheSweepSize(t *testing.T) {
	cache := newRankTermCache()
	tools := make([]mcp.Tool, rankCacheSweepAt+1000)
	for i := range tools {
		tools[i] = mcp.Tool{Name: fmt.Sprintf("tool_%d", i), Description: "Do something."}
	}

	first := cache.analyze(tools)
	for pass := 0; pass < 3; pass++ {
		again := cache.analyze(tools)
		for i := range tools {
			require.Same(t, first[i], again[i], "pass %d, tool %d", pass, i)
		}
	}
}

// TestRankTermCache_DropsToolsNoQueryUses verifies the sweep: tools used once
// and never again are dropped, a tool queries keep using is kept, and the
// cache stays bounded.
func TestRankTermCache_DropsToolsNoQueryUses(t *testing.T) {
	cache := newRankTermCache()
	kept := mcp.Tool{Name: "kept", Description: "Still in use."}
	original := cache.analyze([]mcp.Tool{kept})[0]

	for i := 0; i < 3*rankCacheSweepAt; i++ {
		cache.analyze([]mcp.Tool{{Name: fmt.Sprintf("once_%d", i), Description: "Used once."}})
		if i%100 == 0 {
			require.Same(t, original, cache.analyze([]mcp.Tool{kept})[0], "after %d other tools", i)
		}
	}

	assert.NotContains(t, cache.entries, "once_0", "a tool no query uses is dropped")
	assert.Contains(t, cache.entries, "kept")
	assert.LessOrEqual(t, len(cache.entries), 2*rankCacheSweepAt+1)
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
	terms := queryTerms("pods in a namespace")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				subset := tools[(g*5)%len(tools):]
				rankAnalyzed(terms, cache.analyze(subset))
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

// TestFilterTools_QueryWithoutTermsAnalysesNothing verifies that a query of
// only punctuation returns no tools without analysing any.
func TestFilterTools_QueryWithoutTermsAnalysesNothing(t *testing.T) {
	mock := &mockMetaToolsHandler{tools: benchTools(10)}
	defer registerMockHandler(mock)()
	provider := NewProvider()

	result, err := provider.ExecuteTool(context.Background(), "filter_tools", map[string]interface{}{"query": "?! --"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Empty(t, filterResultWith(t, provider, map[string]interface{}{"query": "?"}).Tools)
	assert.Empty(t, provider.rankTerms.entries)
}

// referenceRankBM25 is the ranker as it was before tools' analyses were
// cached (muster#1547), kept to show that caching does not change rankings.
func referenceRankBM25(query string, docs []rankDoc) []rankedDoc {
	terms := queryTerms(query)
	if len(terms) == 0 || len(docs) == 0 {
		return nil
	}
	isQueryTerm := make(map[string]bool, len(terms))
	for _, t := range terms {
		isQueryTerm[t] = true
	}

	// Per document and field, count only the query terms; the field length
	// counts every token. A field's average length is taken over the documents
	// that have the field, so the many tools with a one-line description do not
	// shrink the average details length and crush every details match.
	fieldTf := make([][numRankFields]map[string]int, len(docs))
	fieldLen := make([][numRankFields]int, len(docs))
	docFreq := make(map[string]int, len(terms)) // documents containing a query term (in any field)
	var totalLen, withField [numRankFields]int
	for i, doc := range docs {
		seen := make(map[string]bool, len(terms))
		for f, text := range doc.fields() {
			tokens := rankTerms(text)
			tf := make(map[string]int, len(terms))
			for _, t := range tokens {
				if isQueryTerm[t] {
					tf[t]++
					seen[t] = true
				}
			}
			fieldTf[i][f] = tf
			fieldLen[i][f] = len(tokens)
			if len(tokens) > 0 {
				totalLen[f] += len(tokens)
				withField[f]++
			}
		}
		for t := range seen {
			docFreq[t]++
		}
	}
	var avgFieldLen [numRankFields]float64
	for f := range avgFieldLen {
		avgFieldLen[f] = avgLen(totalLen[f], withField[f])
	}

	n := float64(len(docs))
	idf := make(map[string]float64, len(terms))
	for _, term := range terms {
		df := float64(docFreq[term])
		// Okapi BM25 IDF with +1 to keep it non-negative for common terms.
		idf[term] = math.Log(1 + (n-df+0.5)/(df+0.5))
	}

	ranked := make([]rankedDoc, 0, len(docs))
	for i := range docs {
		var score float64
		// terms is sorted, so equally relevant documents sum the same
		// contributions in the same order and get the same score.
		for _, term := range terms {
			// BM25F: normalise each field's raw frequency by its own length,
			// then combine the weighted, length-normalised frequencies before
			// the saturating BM25 term.
			var wtf float64
			for f, weight := range rankFieldWeights {
				wtf += weight * normTf(float64(fieldTf[i][f][term]), float64(fieldLen[i][f]), avgFieldLen[f])
			}
			if wtf == 0 {
				continue
			}
			contribution := idf[term] * wtf * (bm25K1 + 1) / (wtf + bm25K1)
			if _, isStopVerb := stopVerbs[term]; isStopVerb {
				contribution *= stopVerbWeight
			}
			score += contribution
		}
		if score > 0 {
			ranked = append(ranked, rankedDoc{index: i, score: score})
		}
	}

	sort.SliceStable(ranked, func(a, b int) bool {
		return ranked[a].score > ranked[b].score
	})
	return ranked
}
