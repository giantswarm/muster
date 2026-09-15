package aggregator

import (
	"strings"

	"github.com/giantswarm/muster/v5/internal/toolset"

	"github.com/mark3labs/mcp-go/mcp"
)

// workflowToolPrefix is the exposed-name prefix of workflow execution tools.
const workflowToolPrefix = "workflow_"

// deriveWorkflowReadOnlyHints fills the readOnlyHint annotation of every
// workflow execution tool in tools: a workflow is read-only when every tool
// its steps reference resolves, in this same catalogue, to a tool annotated
// read-only. Nested workflows are followed; a step whose tool is not in the
// catalogue, a core tool (which carries no annotation) or a cycle makes the
// workflow not read-only. The derived hint fills the same annotation slot a
// server's own readOnlyHint does, so the read-only preset and the meta-tools
// treat workflows and server tools alike.
//
// steps maps each workflow execution tool's exposed name to the tools its
// steps call, as the workflow provider declared them (ToolMetadata.StepTools)
// when the core catalogue was built. A workflow tool absent from steps is
// unknown and not read-only. The catalogue passed in is the caller's own
// (already session-scoped), so the derivation is per request like every other
// catalogue property, and it is a single pass over data the aggregator
// already holds: it used to fetch every workflow's definition from the
// definition source per listing -- 282 GETs against the API server for each
// meta-tool call on one installation (#1225).
func deriveWorkflowReadOnlyHints(tools []mcp.Tool, steps map[string][]string) {
	if len(steps) == 0 {
		return
	}
	byName := make(map[string]*mcp.Tool, len(tools))
	for i := range tools {
		byName[tools[i].Name] = &tools[i]
	}
	memo := map[string]bool{}
	for i := range tools {
		if !isWorkflowExecutionTool(tools[i]) {
			continue
		}
		if workflowIsReadOnly(tools[i].Name, steps, byName, memo, map[string]bool{}) {
			yes := true
			tools[i].Annotations.ReadOnlyHint = &yes
		}
	}
}

// isWorkflowExecutionTool reports whether tool is a workflow_<name> tool,
// preferring the recorded origin over the name prefix.
func isWorkflowExecutionTool(tool mcp.Tool) bool {
	if origin, ok := toolset.ToolOriginOf(tool); ok {
		return origin.Kind == toolset.OriginKindWorkflow
	}
	return strings.HasPrefix(tool.Name, workflowToolPrefix)
}

// workflowIsReadOnly decides for the workflow execution tool named exposed
// (workflow_<name>): every step tool must be a read-only tool of the
// catalogue or a read-only nested workflow.
func workflowIsReadOnly(exposed string, steps map[string][]string, byName map[string]*mcp.Tool, memo map[string]bool, visiting map[string]bool) bool {
	if ro, done := memo[exposed]; done {
		return ro
	}
	if visiting[exposed] {
		return false // a cycle can never be proven read-only
	}
	visiting[exposed] = true
	defer delete(visiting, exposed)

	stepTools, known := steps[exposed]
	if !known {
		memo[exposed] = false
		return false
	}
	readOnly := true
	for _, stepTool := range stepTools {
		if _, nested := steps[stepTool]; nested && strings.HasPrefix(stepTool, workflowToolPrefix) {
			if !workflowIsReadOnly(stepTool, steps, byName, memo, visiting) {
				readOnly = false
				break
			}
			continue
		}
		t, ok := byName[stepTool]
		if !ok || t.Annotations.ReadOnlyHint == nil || !*t.Annotations.ReadOnlyHint {
			readOnly = false
			break
		}
	}
	memo[exposed] = readOnly
	return readOnly
}
