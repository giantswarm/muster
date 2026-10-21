package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// textResult is a successful tool result carrying text.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(text)}}
}

// decodeDoc decodes a workflow's returned document.
func decodeDoc(t *testing.T, result *mcp.CallToolResult) map[string]interface{} {
	t.Helper()
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &doc))
	return doc
}

// fleetCaller answers list_clusters with the clusters of the management
// cluster it is asked about and scan with the target it scanned.
func fleetCaller() *scriptedToolCaller {
	return &scriptedToolCaller{
		responder: func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			switch toolName {
			case "list_clusters":
				clusters := map[string]string{"mc-a": `["a1","a2"]`, "mc-b": `["b1"]`}[args["mc"].(string)]
				return textResult(fmt.Sprintf(`{"items": %s}`, clusters)), nil
			default:
				return textResult(fmt.Sprintf(`{"scanned": "%s/%s"}`, args["mc"], args["cluster"])), nil
			}
		},
	}
}

// nestedFleetWorkflow loops over management clusters and, nested, over the
// clusters listed for each.
func nestedFleetWorkflow(outerParallelism, innerParallelism int, output map[string]interface{}) *api.Workflow {
	return &api.Workflow{
		Name: "nested",
		Args: map[string]api.ArgDefinition{"mcs": {Type: "array", Required: true}},
		Steps: []api.WorkflowStep{{
			ID: "fleet",
			ForEach: &api.WorkflowForEach{
				Items:       "{{ .input.mcs }}",
				As:          "mc",
				Parallelism: outerParallelism,
				Steps: []api.WorkflowSubStep{
					{ID: "clusters", Tool: "list_clusters", Args: map[string]interface{}{"mc": "{{ .vars.mc }}"}},
					{ID: "per-cluster", ForEach: &api.WorkflowForEach{
						Items:       "{{ .results.clusters.items }}",
						As:          "cluster",
						Parallelism: innerParallelism,
						Steps: []api.WorkflowSubStep{
							{ID: "scan", Tool: "scan", Args: map[string]interface{}{"mc": "{{ .vars.mc }}", "cluster": "{{ .vars.cluster }}"}},
						},
					}},
				},
			},
		}},
		Output: output,
	}
}

// TestWorkflowExecutor_NestedForEach verifies that a forEach body can loop
// over a list an earlier body step produced, with both loop variables in
// scope, that every inner iteration is kept as <id>_<outer>_<inner>, and that
// the loops' ids hold their per-iteration records -- the same whether the
// iterations run one after another or concurrently.
func TestWorkflowExecutor_NestedForEach(t *testing.T) {
	for _, p := range []struct{ outer, inner int }{{0, 0}, {2, 2}, {2, 0}, {0, 3}} {
		t.Run(fmt.Sprintf("parallelism %d/%d", p.outer, p.inner), func(t *testing.T) {
			executor := NewWorkflowExecutor(fleetCaller(), nil)
			wf := nestedFleetWorkflow(p.outer, p.inner, map[string]interface{}{
				"a1":       "{{ .results.scan_0_0.scanned }}",
				"a2":       "{{ .results.scan_0_1.scanned }}",
				"b1":       "{{ .results.scan_1_0.scanned }}",
				"last":     "{{ .results.scan.scanned }}",
				"records":  "{{ len .results.fleet }}",
				"mc_b":     "{{ (index .results.fleet 1).item }}",
				"inner_a":  `{{ len (index (index .results.fleet 0).results "per-cluster") }}`,
				"inner_a2": `{{ (index (index (index .results.fleet 0).results "per-cluster") 1).results.scan.scanned }}`,
			})

			result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"mcs": []interface{}{"mc-a", "mc-b"}})
			require.NoError(t, err)
			doc := decodeDoc(t, result)
			assert.Equal(t, "mc-a/a1", doc["a1"])
			assert.Equal(t, "mc-a/a2", doc["a2"])
			assert.Equal(t, "mc-b/b1", doc["b1"])
			assert.Equal(t, "mc-b/b1", doc["last"])
			assert.EqualValues(t, 2, doc["records"])
			assert.Equal(t, "mc-b", doc["mc_b"])
			assert.EqualValues(t, 2, doc["inner_a"])
			assert.Equal(t, "mc-a/a2", doc["inner_a2"])
		})
	}
}

// TestWorkflowExecutor_NestedForEachStartsClean verifies that an outer
// iteration does not see the previous outer iteration's nested results under
// their plain ids.
func TestWorkflowExecutor_NestedForEachStartsClean(t *testing.T) {
	wf := nestedFleetWorkflow(0, 0, map[string]interface{}{
		"seen": []interface{}{"{{ .results.seen_0.said }}", "{{ .results.seen_1.said }}"},
	})
	// Before the nested loop, each outer iteration reports whether it sees a
	// "scan" result.
	body := wf.Steps[0].ForEach.Steps
	wf.Steps[0].ForEach.Steps = []api.WorkflowSubStep{body[0], {ID: "seen", Tool: "echo", Args: map[string]interface{}{"text": `{{ hasKey .results "scan" }}`}}, body[1]}
	caller := fleetCaller()
	inner := caller.responder
	caller.responder = func(toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
		if toolName == "echo" {
			return textResult(fmt.Sprintf(`{"said": %q}`, fmt.Sprint(args["text"]))), nil
		}
		return inner(toolName, args)
	}
	executor := NewWorkflowExecutor(caller, nil)

	result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"mcs": []interface{}{"mc-a", "mc-b"}})
	require.NoError(t, err)
	assert.Equal(t, []interface{}{"false", "false"}, decodeDoc(t, result)["seen"])
}

// TestWorkflowExecutor_NestedForEachStepRecords verifies the step records of
// a nested loop: in loop order, an inner record naming its inner and outer
// iteration and showing that iteration's result.
func TestWorkflowExecutor_NestedForEachStepRecords(t *testing.T) {
	executor := NewWorkflowExecutor(fleetCaller(), nil)
	wf := nestedFleetWorkflow(2, 2, nil)

	result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"mcs": []interface{}{"mc-a", "mc-b"}, debugArgKey: true})
	require.NoError(t, err)
	steps := decodeDoc(t, result)["steps"].([]interface{})
	require.Len(t, steps, 5)

	want := []struct {
		id         string
		iteration  float64
		outer      interface{}
		scannedRes interface{}
	}{
		{"clusters", 0, nil, nil},
		{"scan", 0, float64(0), "mc-a/a1"},
		{"scan", 1, float64(0), "mc-a/a2"},
		{"clusters", 1, nil, nil},
		{"scan", 0, float64(1), "mc-b/b1"},
	}
	for i, w := range want {
		step := steps[i].(map[string]interface{})
		assert.Equal(t, w.id, step["id"], "record %d", i)
		assert.Equal(t, w.iteration, step["iteration"], "record %d", i)
		assert.Equal(t, w.outer, step["outer_iteration"], "record %d", i)
		if w.scannedRes != nil {
			assert.Equal(t, w.scannedRes, step["result"].(map[string]interface{})["scanned"], "record %d", i)
		}
	}
}

// gatedCaller blocks every call until the test releases it by the target it
// was called for, reporting each arrival and the most calls in flight at once.
type gatedCaller struct {
	arrivals chan string
	gates    sync.Map // target -> chan struct{}

	mu          sync.Mutex
	inFlight    int
	maxInFlight int
}

func newGatedCaller(targets ...string) *gatedCaller {
	g := &gatedCaller{arrivals: make(chan string, len(targets))}
	for _, target := range targets {
		g.gates.Store(target, make(chan struct{}))
	}
	return g
}

func (g *gatedCaller) CallToolInternal(_ context.Context, _ string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	target := args["target"].(string)
	g.mu.Lock()
	g.inFlight++
	g.maxInFlight = max(g.maxInFlight, g.inFlight)
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.inFlight--
		g.mu.Unlock()
	}()

	g.arrivals <- target
	gate, _ := g.gates.Load(target)
	<-gate.(chan struct{})
	return textResult(fmt.Sprintf(`{"target": %q}`, target)), nil
}

func (g *gatedCaller) release(target string) {
	gate, _ := g.gates.Load(target)
	close(gate.(chan struct{}))
}

// awaitArrival returns the target of the next call, failing the test when no
// call arrives (a loop that does not run its iterations concurrently would
// wait for a release that never comes).
func (g *gatedCaller) awaitArrival(t *testing.T) string {
	t.Helper()
	select {
	case target := <-g.arrivals:
		return target
	case <-time.After(10 * time.Second):
		t.Fatal("no call arrived: the iterations did not run concurrently")
		return ""
	}
}

func loopWorkflow(parallelism int) *api.Workflow {
	return &api.Workflow{
		Name: "loop",
		Args: map[string]api.ArgDefinition{"targets": {Type: "array", Required: true}},
		Steps: []api.WorkflowStep{{
			ID: "scan",
			ForEach: &api.WorkflowForEach{
				Items:       "{{ .input.targets }}",
				As:          "target",
				Parallelism: parallelism,
				Steps: []api.WorkflowSubStep{
					{ID: "probe", Tool: "probe", Args: map[string]interface{}{"target": "{{ .vars.target }}"}},
				},
			},
		}},
	}
}

// TestWorkflowExecutor_ForEachParallelismKeepsItemOrder verifies that
// iterations running at once and finishing in reverse order are still
// reported in item order: step records, <id>_<index> keys and the loop's
// records.
func TestWorkflowExecutor_ForEachParallelismKeepsItemOrder(t *testing.T) {
	targets := []string{"t0", "t1", "t2", "t3"}
	caller := newGatedCaller(targets...)
	executor := NewWorkflowExecutor(caller, nil)
	wf := loopWorkflow(len(targets))
	wf.Output = map[string]interface{}{
		"keys":    []interface{}{"{{ .results.probe_0.target }}", "{{ .results.probe_3.target }}"},
		"records": []interface{}{"{{ (index .results.scan 0).item }}", "{{ (index .results.scan 3).item }}"},
	}

	type outcome struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"targets": []interface{}{"t0", "t1", "t2", "t3"}})
		done <- outcome{result, err}
	}()

	// All four are in flight at once before any is released.
	for range targets {
		caller.awaitArrival(t)
	}
	for i := len(targets) - 1; i >= 0; i-- {
		caller.release(targets[i])
	}
	got := <-done
	require.NoError(t, got.err)
	doc := decodeDoc(t, got.result)
	assert.Equal(t, []interface{}{"t0", "t3"}, doc["keys"])
	assert.Equal(t, []interface{}{"t0", "t3"}, doc["records"])
	assert.Equal(t, len(targets), caller.maxInFlight)
}

// TestWorkflowExecutor_ForEachParallelismBound verifies that no more than
// forEach.parallelism iterations are in flight at once.
func TestWorkflowExecutor_ForEachParallelismBound(t *testing.T) {
	targets := []string{"t0", "t1", "t2", "t3", "t4", "t5"}
	caller := newGatedCaller(targets...)
	executor := NewWorkflowExecutor(caller, nil)

	done := make(chan error, 1)
	go func() {
		_, err := executor.ExecuteWorkflow(context.Background(), loopWorkflow(2), map[string]interface{}{"targets": []interface{}{"t0", "t1", "t2", "t3", "t4", "t5"}})
		done <- err
	}()

	// Two arrive; from then on every release lets one more in, until all
	// have arrived.
	arrived := []string{caller.awaitArrival(t), caller.awaitArrival(t)}
	for released := 0; released < len(targets); released++ {
		caller.release(arrived[released])
		if len(arrived) < len(targets) {
			arrived = append(arrived, caller.awaitArrival(t))
		}
	}
	require.NoError(t, <-done)
	assert.Equal(t, 2, caller.maxInFlight)
}

// TestWorkflowExecutor_ForEachParallelFailure verifies that a failure that is
// not allowed stops a concurrent loop with that iteration's error on its own
// step record, and that a tolerated one is reported in the iteration's record
// while the others complete.
func TestWorkflowExecutor_ForEachParallelFailure(t *testing.T) {
	caller := &scriptedToolCaller{
		responder: func(_ string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			if args["target"] == "t1" {
				return nil, fmt.Errorf("t1 is unreachable")
			}
			return textResult(fmt.Sprintf(`{"target": %q}`, args["target"])), nil
		},
	}

	t.Run("fatal", func(t *testing.T) {
		executor := NewWorkflowExecutor(caller, nil)
		result, err := executor.ExecuteWorkflow(context.Background(), loopWorkflow(3), map[string]interface{}{"targets": []interface{}{"t0", "t1", "t2"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "t1 is unreachable")
		var failed []map[string]interface{}
		for _, entry := range stepEntries(t, result, "probe") {
			if entry["error"] != nil {
				failed = append(failed, entry)
			}
		}
		require.Len(t, failed, 1, "only the failing iteration's record carries the error")
		assert.Equal(t, float64(1), failed[0]["iteration"])
	})

	t.Run("tolerated", func(t *testing.T) {
		executor := NewWorkflowExecutor(caller, nil)
		wf := loopWorkflow(3)
		wf.Steps[0].ForEach.Steps[0].AllowFailure = true
		wf.Output = map[string]interface{}{
			"statuses": []interface{}{"{{ (index .results.scan 0).status }}", "{{ (index .results.scan 1).status }}", "{{ (index .results.scan 2).status }}"},
			"error":    "{{ (index .results.scan 1).error }}",
		}
		result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"targets": []interface{}{"t0", "t1", "t2"}})
		require.NoError(t, err)
		doc := decodeDoc(t, result)
		assert.Equal(t, []interface{}{"completed", "failed", "completed"}, doc["statuses"])
		assert.Equal(t, "probe: t1 is unreachable", doc["error"])
	})
}

// TestWorkflowExecutor_FromStepAcrossLoopsAndGroups verifies that a fromStep
// condition inside a forEach body -- sequential, concurrent or nested -- or a
// parallel group can reference a step that ran before it.
func TestWorkflowExecutor_FromStepAcrossLoopsAndGroups(t *testing.T) {
	gate := &api.WorkflowCondition{FromStep: "seed", Expect: api.WorkflowConditionExpectation{Success: true}}
	body := []api.WorkflowSubStep{{ID: "gated", Tool: "probe", Condition: gate, Args: map[string]interface{}{"target": "x"}}}
	cases := map[string]api.WorkflowStep{
		"sequential forEach": {ID: "loop", ForEach: &api.WorkflowForEach{Items: "{{ .input.items }}", Steps: body}},
		"concurrent forEach": {ID: "loop", ForEach: &api.WorkflowForEach{Items: "{{ .input.items }}", Parallelism: 2, Steps: body}},
		"nested forEach": {ID: "loop", ForEach: &api.WorkflowForEach{Items: "{{ .input.items }}", Parallelism: 2, Steps: []api.WorkflowSubStep{
			{ID: "inner", ForEach: &api.WorkflowForEach{Items: "{{ .input.items }}", As: "inner", Parallelism: 2, Steps: body}},
		}}},
		"parallel group": {ID: "group", Parallel: body},
	}
	for name, step := range cases {
		t.Run(name, func(t *testing.T) {
			caller := &scriptedToolCaller{}
			executor := NewWorkflowExecutor(caller, nil)
			wf := &api.Workflow{
				Name: "fromstep",
				Args: map[string]api.ArgDefinition{"items": {Type: "array", Required: true}},
				Steps: []api.WorkflowStep{
					{ID: "seed", Tool: "seed"},
					step,
				},
			}
			_, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"items": []interface{}{"a", "b"}})
			require.NoError(t, err)
			assert.True(t, caller.calledTools()["probe"], "the gated step ran")
		})
	}
}

// TestWorkflowExecutor_NestedForEachCondition verifies that a nested forEach
// skipped by its condition runs nothing and records one skipped step for its
// outer iteration.
func TestWorkflowExecutor_NestedForEachCondition(t *testing.T) {
	executor := NewWorkflowExecutor(fleetCaller(), nil)
	wf := nestedFleetWorkflow(0, 0, nil)
	wf.Steps[0].ForEach.Steps[1].Condition = &api.WorkflowCondition{Template: `{{ eq .vars.mc "mc-b" }}`}

	result, err := executor.ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"mcs": []interface{}{"mc-a", "mc-b"}})
	require.NoError(t, err)
	skipped := stepEntries(t, result, "per-cluster")
	require.Len(t, skipped, 1)
	assert.Equal(t, statusSkipped, skipped[0]["status"])
	assert.Equal(t, float64(0), skipped[0]["iteration"])
	scans := stepEntries(t, result, "scan")
	require.Len(t, scans, 1)
	assert.Equal(t, float64(1), scans[0]["outer_iteration"])
}

// TestWorkflowExecutor_ForEachJQ verifies that a forEach's jq program
// reshapes its per-iteration records before they are stored, and that a
// program failing on them fails the step unless the step allows failure.
func TestWorkflowExecutor_ForEachJQ(t *testing.T) {
	caller := &scriptedToolCaller{
		responder: func(_ string, args map[string]interface{}) (*mcp.CallToolResult, error) {
			return textResult(fmt.Sprintf(`{"target": %q}`, args["target"])), nil
		},
	}

	t.Run("reshapes", func(t *testing.T) {
		wf := loopWorkflow(0)
		wf.Steps[0].JQ = `[.[] | .results.probe.target]`
		wf.Output = map[string]interface{}{"targets": "{{ .results.scan }}"}
		result, err := NewWorkflowExecutor(caller, nil).ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"targets": []interface{}{"t0", "t1"}})
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"t0", "t1"}, decodeDoc(t, result)["targets"])
	})

	t.Run("fails the step", func(t *testing.T) {
		wf := loopWorkflow(0)
		wf.Steps[0].JQ = `.[] | .index`
		_, err := NewWorkflowExecutor(caller, nil).ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"targets": []interface{}{"t0", "t1"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "yielded 2 values")
	})

	t.Run("tolerated", func(t *testing.T) {
		wf := loopWorkflow(0)
		wf.Steps[0].JQ = `.[] | .index`
		wf.Steps[0].AllowFailure = true
		wf.Output = map[string]interface{}{"failed": "{{ .results.scan.isError }}"}
		result, err := NewWorkflowExecutor(caller, nil).ExecuteWorkflow(context.Background(), wf, map[string]interface{}{"targets": []interface{}{"t0", "t1"}})
		require.NoError(t, err)
		assert.Equal(t, true, decodeDoc(t, result)["failed"])
	})
}

func TestFailureMessage(t *testing.T) {
	errorText := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("quota exceeded")}, IsError: true}
	cases := map[string]struct {
		result  interface{}
		outcome stepOutcome
		want    string
	}{
		"the error a result carries":      {result: map[string]interface{}{"error": "timeout"}, want: "scan: timeout"},
		"a text result":                   {result: "denied", want: "scan: denied"},
		"the error that stopped the loop": {result: map[string]interface{}{}, outcome: stepOutcome{errorMessage: "boom"}, want: "scan: boom"},
		"the error result's text":         {outcome: stepOutcome{result: errorText}, want: "scan: quota exceeded"},
		"nothing to go by":                {want: "scan: the tool returned an error result"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, failureMessage("scan", c.result, c.outcome))
		})
	}
}
