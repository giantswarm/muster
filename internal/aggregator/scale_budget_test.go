package aggregator

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/metatools"
)

// The scale budgets: hard limits on what a warm meta-tool call and a session
// may cost on an installation-shaped catalogue (giantswarm/muster#1240). Each
// is set with headroom over the value measured on CI's medium executor and
// is never "faster than last run"; a failing budget names the metric, the
// measured value and the limit. The bugs they hold the line on:
//
//   - #1225: every meta-tool call rebuilt the core catalogue with two
//     API-server reads per workflow (about 560 per call) and read every
//     server's capability document with its own round trips (about 160 per
//     call) -- a 3-second floor over a 22 ms backend.
//   - #1217: every session stored every server's full document; 448 sessions
//     filled a 462 MB store.
//   - #1193: list_tools answered the whole catalogue, 402 KB per call.
const (
	// scaleBudgetWarmCall bounds the median duration of a warm meta-tool
	// call in-process (no HTTP, no MCP framing).
	scaleBudgetWarmCall = 100 * time.Millisecond
	// scaleBudgetValkeyCommandsPerCall bounds the store commands a warm
	// meta-tool call issues: one HGETALL of the session's capability hash
	// and one HKEYS of its auth hash.
	scaleBudgetValkeyCommandsPerCall = 2
	// scaleBudgetDefinitionReadsPerCall bounds the API-server requests a warm
	// meta-tool call issues for definitions: none, the catalogue is built
	// once and invalidated on change.
	scaleBudgetDefinitionReadsPerCall = 0
	// scaleBudgetListToolsPageBytes bounds list_tools' default page.
	scaleBudgetListToolsPageBytes = 40 * 1024
	// scaleBudgetCapabilityBytesPerSession bounds the capability store's
	// bytes per session over the fixture's 450 sessions: the session's
	// references (one per server) plus its share of the 18 shared documents.
	scaleBudgetCapabilityBytesPerSession = 16 * 1024
	// scaleWarmCallSamples is how many warm calls a duration median is over.
	scaleWarmCallSamples = 15
)

// warmMetaToolCall is one meta-tool call the budgets are asserted on.
type warmMetaToolCall struct {
	name string
	args map[string]any
}

// scaleWarmCalls are the meta-tool calls an agent makes per turn: the
// listing, a filter over a family, a description and a call routed to a
// family member by its instance argument.
func scaleWarmCalls() []warmMetaToolCall {
	return []warmMetaToolCall{
		{metatools.ToolListTools, nil},
		{metatools.ToolFilterTools, map[string]any{"pattern": "x_clusters_*", "limit": 20}},
		{metatools.ToolDescribeTool, map[string]any{"name": "x_clusters_list_clusters"}},
		{metatools.ToolCallTool, map[string]any{"name": "x_clusters_list_clusters", "arguments": map[string]any{"installation": "amber-clusters"}}},
	}
}

// TestScaleBudgets_WarmMetaToolCalls asserts, per meta-tool, the duration,
// store-command and definition-read budgets of a warm call over the fixture:
// 87 servers, 84 of them session-authenticated, 282 workflows, the calling
// session authenticated to all 84.
func TestScaleBudgets_WarmMetaToolCalls(t *testing.T) {
	rig := newScaleRig(t)
	ctx := rig.sessionContext(0)
	rig.agg.connPool.Put(rig.sessionID(0), "amber-clusters", &callToolMockClient{
		callToolResult: &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(`{"ok":true}`)}},
	})

	// The first call of the process builds the core catalogue (one list of
	// the definitions) and decodes the session's documents into the cache.
	rig.callMetaTool(t, ctx, metatools.ToolListTools, nil)
	t.Logf("cold start: %d definition reads, %d store commands", rig.reads.requests(), rig.valkeyCommands())

	for _, call := range scaleWarmCalls() {
		t.Run(call.name, func(t *testing.T) {
			var durations []time.Duration
			minCommands, maxCommands := int(^uint(0)>>1), 0
			for i := 0; i < scaleWarmCallSamples; i++ {
				readsBefore, commandsBefore := rig.reads.requests(), rig.valkeyCommands()
				start := time.Now()
				rig.callMetaTool(t, ctx, call.name, call.args)
				durations = append(durations, time.Since(start))
				reads, commands := int(rig.reads.requests()-readsBefore), rig.valkeyCommands()-commandsBefore
				assert.LessOrEqual(t, reads, scaleBudgetDefinitionReadsPerCall,
					"budget definition reads per warm %s call: measured %d, limit %d (sample %d)", call.name, reads, scaleBudgetDefinitionReadsPerCall, i)
				minCommands, maxCommands = min(minCommands, commands), max(maxCommands, commands)
			}
			med := median(durations)
			t.Logf("%s: median %s over %d warm calls, store commands %d..%d per call", call.name, med.Round(10*time.Microsecond), len(durations), minCommands, maxCommands)
			assert.LessOrEqual(t, minCommands, scaleBudgetValkeyCommandsPerCall,
				"budget store commands per warm %s call: measured %d, limit %d", call.name, minCommands, scaleBudgetValkeyCommandsPerCall)
			if raceDetectorEnabled {
				t.Logf("%s: duration budget not asserted under the race detector", call.name)
				return
			}
			assert.LessOrEqual(t, med, scaleBudgetWarmCall,
				"budget warm %s call: measured median %s, limit %s", call.name, med.Round(time.Millisecond), scaleBudgetWarmCall)
		})
	}
}

// TestScaleBudgets_ListToolsDefaultPage asserts list_tools' default answer
// over the fixture is one bounded, summarised page.
func TestScaleBudgets_ListToolsDefaultPage(t *testing.T) {
	rig := newScaleRig(t)
	ctx := rig.sessionContext(1)

	result, bytes := rig.callMetaTool(t, ctx, metatools.ToolListTools, nil)
	var page metatools.ListToolsResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(result)), &page))
	t.Logf("list_tools default page: %d bytes, %d of %d tools, truncated=%v", bytes, page.FilteredCount, page.Total, page.Truncated)

	assert.LessOrEqual(t, bytes, scaleBudgetListToolsPageBytes,
		"budget list_tools default page: measured %d bytes, limit %d", bytes, scaleBudgetListToolsPageBytes)
	assert.True(t, page.Truncated, "the catalogue is larger than one page")
	assert.Equal(t, 50, page.FilteredCount, "the default page")
	shape := rig.fixture.Shape()
	assert.Greater(t, page.Total, shape.Workflows+50, "the whole catalogue: every workflow, the families' tools, the in-house tools and the core tools")
}

// TestScaleBudgets_CapabilityStoreBytesPerSession asserts the store holds
// each capability document once and a reference per session and server.
func TestScaleBudgets_CapabilityStoreBytesPerSession(t *testing.T) {
	rig := newScaleRig(t)
	shape := rig.fixture.Shape()

	sessions, entries, documents, bytes := rig.capabilityFootprint()
	perSession := bytes / max(sessions, 1)
	t.Logf("capability store: %d sessions, %d entries, %d documents, %d bytes, %d bytes per session", sessions, entries, documents, bytes, perSession)

	assert.Equal(t, shape.Sessions, sessions)
	assert.Equal(t, shape.Sessions*shape.SessionAuthServers, entries)
	assert.Equal(t, shape.SessionDocuments, documents, "one document per distinct tool list, however many sessions reference it")
	assert.LessOrEqual(t, perSession, scaleBudgetCapabilityBytesPerSession,
		"budget capability-store bytes per session: measured %d, limit %d", perSession, scaleBudgetCapabilityBytesPerSession)
}

// BenchmarkScaleListTools is the warm list_tools call over the fixture, for
// profiling; the budgets above are what CI asserts.
func BenchmarkScaleListTools(b *testing.B) {
	t := &testing.T{}
	rig := newScaleRig(t)
	ctx := rig.sessionContext(0)
	rig.callMetaTool(t, ctx, metatools.ToolListTools, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rig.metaTools.ExecuteTool(ctx, metatools.ToolListTools, nil); err != nil {
			b.Fatal(err)
		}
	}
}
