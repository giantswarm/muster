package aggregator

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/internal/toolset"
)

func readOnlyTool(name string, readOnly bool) mcp.Tool {
	t := mcp.Tool{Name: name}
	t.Annotations.ReadOnlyHint = &readOnly
	toolset.SetToolOrigin(&t, toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "k8s"})
	return t
}

func workflowTool(name string) mcp.Tool {
	t := mcp.Tool{Name: "workflow_" + name}
	toolset.SetToolOrigin(&t, toolset.ToolOrigin{Kind: toolset.OriginKindWorkflow})
	return t
}

func hint(tools []mcp.Tool, name string) *bool {
	for i := range tools {
		if tools[i].Name == name {
			return tools[i].Annotations.ReadOnlyHint
		}
	}
	return nil
}

// The read-only hint of a workflow is derived from the step tools the
// provider declared with the tool, resolved in the caller's own catalogue --
// no lookup of the workflow definition per listing (#1225).
func TestDeriveWorkflowReadOnlyHints(t *testing.T) {
	tools := []mcp.Tool{
		readOnlyTool("x_k8s_get_pods", true),
		readOnlyTool("x_k8s_delete_pod", false),
		{Name: "core_workflow_list"}, // a core tool without an annotation
		workflowTool("reads"),
		workflowTool("writes"),
		workflowTool("nested-reads"),
		workflowTool("nested-writes"),
		workflowTool("dangling"),
		workflowTool("unknown-step"),
		workflowTool("no-steps"),
		workflowTool("loop-a"),
		workflowTool("loop-b"),
		workflowTool("undeclared"),
	}
	steps := map[string][]string{
		"workflow_reads":         {"x_k8s_get_pods"},
		"workflow_writes":        {"x_k8s_get_pods", "x_k8s_delete_pod"},
		"workflow_nested-reads":  {"workflow_reads"},
		"workflow_nested-writes": {"workflow_reads", "workflow_writes"},
		"workflow_dangling":      {"workflow_does-not-exist"},
		"workflow_unknown-step":  {"x_k8s_not_in_catalogue"},
		"workflow_no-steps":      {},
		"workflow_loop-a":        {"workflow_loop-b"},
		"workflow_loop-b":        {"workflow_loop-a"},
	}

	deriveWorkflowReadOnlyHints(tools, steps)

	isTrue := func(name string) bool { h := hint(tools, name); return h != nil && *h }
	assert.True(t, isTrue("workflow_reads"), "every step tool is read-only")
	assert.False(t, isTrue("workflow_writes"), "one write step")
	assert.True(t, isTrue("workflow_nested-reads"), "a nested read-only workflow")
	assert.False(t, isTrue("workflow_nested-writes"), "a nested workflow with a write")
	assert.False(t, isTrue("workflow_dangling"), "a nested workflow that does not exist")
	assert.False(t, isTrue("workflow_unknown-step"), "a step tool outside the catalogue")
	assert.True(t, isTrue("workflow_no-steps"), "nothing to write with")
	assert.False(t, isTrue("workflow_loop-a"), "a cycle is never proven read-only")
	assert.False(t, isTrue("workflow_loop-b"))
	assert.False(t, isTrue("workflow_undeclared"), "a workflow tool without declared steps is unknown")

	assert.Nil(t, hint(tools, "core_workflow_list"), "only workflow tools are touched")
	assert.False(t, *hint(tools, "x_k8s_delete_pod"), "server tools keep their own hint")
}

func TestDeriveWorkflowReadOnlyHints_NoStepsNoChange(t *testing.T) {
	tools := []mcp.Tool{workflowTool("reads")}
	deriveWorkflowReadOnlyHints(tools, nil)
	assert.Nil(t, hint(tools, "workflow_reads"))
}
