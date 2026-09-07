package aggregator

import (
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"

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
// The catalogue passed in is the caller's own (already session-scoped), so the
// derivation is per request like every other catalogue property. It is a
// single pass over data the aggregator already holds; no workflow handler
// (tests, bootstrap order) means no hint is derived.
func deriveWorkflowReadOnlyHints(tools []mcp.Tool, workflows api.WorkflowHandler) {
	if workflows == nil {
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
		if workflowIsReadOnly(strings.TrimPrefix(tools[i].Name, workflowToolPrefix), workflows, byName, memo, map[string]bool{}) {
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

func workflowIsReadOnly(name string, workflows api.WorkflowHandler, byName map[string]*mcp.Tool, memo map[string]bool, visiting map[string]bool) bool {
	if ro, done := memo[name]; done {
		return ro
	}
	if visiting[name] {
		return false // a cycle can never be proven read-only
	}
	visiting[name] = true
	defer delete(visiting, name)

	wf, err := workflows.GetWorkflow(name)
	if err != nil || wf == nil {
		memo[name] = false
		return false
	}
	readOnly := true
	for _, stepTool := range api.WorkflowStepTools(wf) {
		if nested, ok := strings.CutPrefix(stepTool, workflowToolPrefix); ok {
			if !workflowIsReadOnly(nested, workflows, byName, memo, visiting) {
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
	memo[name] = readOnly
	return readOnly
}
