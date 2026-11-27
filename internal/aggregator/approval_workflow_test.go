package aggregator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// fakeWorkflows is a workflow handler whose runs dispatch their steps
// through the aggregator with the run's context, as the executor does: a
// step's "{{name}}" argument or tool takes the run's input of that name.
// beforeSteps runs between the up-front check and the first step.
type fakeWorkflows struct {
	api.WorkflowHandler
	server      *AggregatorServer
	defs        map[string]*api.Workflow
	beforeSteps func()
	ran         []string
}

func (f *fakeWorkflows) GetWorkflow(name string) (*api.Workflow, error) {
	if wf, ok := f.defs[name]; ok {
		copied := *wf
		return &copied, nil
	}
	return nil, fmt.Errorf("workflow %s not found", name)
}

func (f *fakeWorkflows) GetTools() []api.ToolMetadata { return nil }

func resolveInput(value any, inputs map[string]any) any {
	if s, ok := value.(string); ok && strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
		return inputs[strings.TrimSuffix(strings.TrimPrefix(s, "{{"), "}}")]
	}
	return value
}

func (f *fakeWorkflows) ExecuteTool(ctx context.Context, toolName string, inputs map[string]any) (*api.CallToolResult, error) {
	wf, err := f.GetWorkflow(strings.TrimPrefix(toolName, "action_"))
	if err != nil {
		return nil, err
	}
	if f.beforeSteps != nil {
		f.beforeSteps()
	}
	for _, step := range wf.Steps {
		args := map[string]any{}
		for key, value := range step.Args {
			args[key] = resolveInput(value, inputs)
		}
		tool, _ := resolveInput(step.Tool, inputs).(string)
		result, err := f.server.CallToolInternal(ctx, tool, args)
		if err != nil {
			return nil, fmt.Errorf("step %s: %w", step.ID, err)
		}
		if result != nil && result.IsError {
			return nil, fmt.Errorf("step %s answered an error", step.ID)
		}
		f.ran = append(f.ran, step.ID)
	}
	return &api.CallToolResult{Content: []any{"done"}}, nil
}

type workflowRig struct {
	*gateRig
	workflows *fakeWorkflows
}

// newWorkflowRig is the gate rig plus workflows: "release" reads an issue
// (Allow) and merges (Ask), "readonly" only reads, "blocked" deletes a
// repository (Block), "outer" calls "release".
func newWorkflowRig(t *testing.T) *workflowRig {
	rig := newGateRig(t)
	workflows := &fakeWorkflows{server: rig.server, defs: map[string]*api.Workflow{
		"release": {Name: "release", Args: map[string]api.ArgDefinition{"pr": {Type: "number"}}, Steps: []api.WorkflowStep{
			{ID: "find", Tool: "x_github_get_issue", Args: map[string]any{"id": "{{pr}}"}},
			{ID: "merge", Tool: "x_github_merge_pull_request", Args: map[string]any{"pr": "{{pr}}"}},
		}},
		"readonly": {Name: "readonly", Steps: []api.WorkflowStep{
			{ID: "find", Tool: "x_github_get_issue", Args: map[string]any{"id": 1.0}},
			{ID: "find-again", Tool: "x_github_get_issue", Args: map[string]any{"id": 2.0}},
		}},
		"blocked": {Name: "blocked", Steps: []api.WorkflowStep{
			{ID: "find", Tool: "x_github_get_issue", Args: map[string]any{"id": 1.0}},
			{ID: "delete", Tool: "x_github_delete_repository", Args: map[string]any{}},
		}},
		"templated": {Name: "templated", Steps: []api.WorkflowStep{
			{ID: "any", Tool: "{{tool}}", Args: map[string]any{}},
		}},
		"outer": {Name: "outer", Steps: []api.WorkflowStep{
			{ID: "inner", Tool: "workflow_release", Args: map[string]any{"pr": 5.0}},
		}},
	}}
	previous := api.GetWorkflow()
	api.RegisterWorkflow(workflows)
	t.Cleanup(func() { api.RegisterWorkflow(previous) })
	return &workflowRig{gateRig: rig, workflows: workflows}
}

func (rig *workflowRig) run(t *testing.T, tool string, inputs map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	return rig.server.CallToolInternal(rig.ctx, tool, inputs)
}

func (rig *workflowRig) setGitHub(settings *api.MCPServerApproval) {
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	info := manager.servers["github"]
	info.Approval = settings
	manager.servers["github"] = info
}

// A Block step refuses the run before any step runs.
func TestWorkflowGate_BlockedStepRefusesUpFront(t *testing.T) {
	rig := newWorkflowRig(t)
	_, err := rig.run(t, "workflow_blocked", map[string]any{})
	require.ErrorContains(t, err, "workflow blocked cannot run: step tool not found: x_github_delete_repository")
	assert.Empty(t, rig.workflows.ran)
	assert.Zero(t, rig.client.count("get_issue"))
}

// One asking step makes the run ask once, with its inputs and steps; after
// approval every step runs once, unasked; the approval is used up.
func TestWorkflowGate_AsksOnceUpFront(t *testing.T) {
	rig := newWorkflowRig(t)
	result, err := rig.run(t, "workflow_release", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	assert.Empty(t, rig.workflows.ran, "asking runs nothing")

	request, err := rig.store.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalTargetWorkflow, request.Target.Kind)
	require.Len(t, request.Display.Steps, 2)
	assert.False(t, request.Display.Steps[0].Asks)
	assert.True(t, request.Display.Steps[1].Asks, "the asking step is marked")
	assert.Equal(t, `{"pr":7}`, request.Arguments)

	require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))
	_, err = rig.run(t, "workflow_release", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	assert.Equal(t, []string{"find", "merge"}, rig.workflows.ran)
	assert.Equal(t, 1, rig.client.count("merge_pull_request"))

	again, err := rig.run(t, "workflow_release", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	assert.NotEqual(t, id, approvalIDOf(t, again), "single use")
}

// The approval binds the definition and the inputs: a changed definition or
// other inputs ask again, and the approved run does not run them.
func TestWorkflowGate_ChangedDefinitionOrInputsAskAgain(t *testing.T) {
	rig := newWorkflowRig(t)
	result, err := rig.run(t, "workflow_release", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))

	other, err := rig.run(t, "workflow_release", map[string]any{"pr": 8.0})
	require.NoError(t, err)
	assert.NotEqual(t, id, approvalIDOf(t, other), "other inputs ask again")

	rig.workflows.defs["release"].Steps[0].Args = map[string]any{"id": 99.0}
	changed, err := rig.run(t, "workflow_release", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	assert.NotEqual(t, id, approvalIDOf(t, changed), "a changed definition asks again")
	assert.Empty(t, rig.workflows.ran)
}

// core_workflow_X and workflow_X are one workflow: an approval asked through
// one runs through the other.
func TestWorkflowGate_AliasesAreOneTarget(t *testing.T) {
	rig := newWorkflowRig(t)
	result, err := rig.run(t, "workflow_release", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	require.NoError(t, rig.store.Decide(context.Background(), approvalIDOf(t, result), true, alice))
	_, err = rig.run(t, "core_workflow_release", map[string]any{"pr": 7.0})
	require.NoError(t, err)
	assert.Equal(t, []string{"find", "merge"}, rig.workflows.ran)
}

// Each step is resolved again at dispatch: a step that became Block after
// the approval, or asks in a run nobody approved (a setting changed between
// the check and the step), fails the run there.
func TestWorkflowGate_DispatchTimeChecks(t *testing.T) {
	t.Run("Block after the approval", func(t *testing.T) {
		rig := newWorkflowRig(t)
		result, err := rig.run(t, "workflow_release", map[string]any{"pr": 7.0})
		require.NoError(t, err)
		require.NoError(t, rig.store.Decide(context.Background(), approvalIDOf(t, result), true, alice))
		rig.workflows.beforeSteps = func() {
			rig.setGitHub(&api.MCPServerApproval{Rule: api.ApprovalAsk, Tools: map[string]string{"get_issue": api.ApprovalAllow, "merge_pull_request": api.ApprovalBlock}})
		}
		_, err = rig.run(t, "workflow_release", map[string]any{"pr": 7.0})
		require.ErrorContains(t, err, "step merge: tool not found: x_github_merge_pull_request")
		assert.Equal(t, []string{"find"}, rig.workflows.ran)
		assert.Zero(t, rig.client.count("merge_pull_request"))
	})
	t.Run("asks in a run not approved up front", func(t *testing.T) {
		rig := newWorkflowRig(t)
		rig.workflows.beforeSteps = func() {
			rig.setGitHub(&api.MCPServerApproval{Rule: api.ApprovalAsk, Tools: map[string]string{"get_issue": api.ApprovalAsk}})
		}
		_, err := rig.run(t, "workflow_readonly", map[string]any{})
		require.ErrorContains(t, err, "asks before it runs (ask from tool) and this run was not approved up front")
		assert.Empty(t, rig.workflows.ran)
	})
	t.Run("a templated tool name the check did not see", func(t *testing.T) {
		rig := newWorkflowRig(t)
		// A step muster cannot resolve up front counts as asking.
		result, err := rig.run(t, "workflow_templated", map[string]any{"tool": "x_github_get_issue"})
		require.NoError(t, err)
		require.NoError(t, rig.store.Decide(context.Background(), approvalIDOf(t, result), true, alice))
		_, err = rig.run(t, "workflow_templated", map[string]any{"tool": "x_github_get_issue"})
		require.ErrorContains(t, err, "step tool x_github_get_issue was not part of the run when it was checked")
		assert.Zero(t, rig.client.count("get_issue"))
	})
}

// A workflow calling another is planned with it: the nested steps are shown
// with their workflow, ask up front, and the approval binds the nested
// definition, so changing it asks again.
func TestWorkflowGate_NestedWorkflowsShownAndBound(t *testing.T) {
	rig := newWorkflowRig(t)
	result, err := rig.run(t, "workflow_outer", map[string]any{})
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	request, err := rig.store.Get(context.Background(), id)
	require.NoError(t, err)
	var nested []string
	for _, step := range request.Display.Steps {
		if step.Workflow == "release" {
			nested = append(nested, step.ID)
		}
	}
	assert.Equal(t, []string{"find", "merge"}, nested, "the nested workflow's steps are shown")
	require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))

	rig.workflows.defs["release"].Steps[1].Args = map[string]any{"pr": 666.0}
	changed, err := rig.run(t, "workflow_outer", map[string]any{})
	require.NoError(t, err)
	assert.NotEqual(t, id, approvalIDOf(t, changed), "a changed nested workflow asks again")
	assert.Zero(t, rig.client.count("merge_pull_request"))
}

// A family step whose member the run chooses takes the strictest member's
// setting up front.
func TestWorkflowGate_FamilyStepTakesTheStrictestMember(t *testing.T) {
	rig := newWorkflowRig(t)
	ctx := context.Background()
	family := &api.MCPServerFamily{Name: "kubernetes", InstanceArg: "cluster"}
	for _, member := range []string{"k8s-a", "k8s-b"} {
		client := &countingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "list_pods"}}}}
		require.NoError(t, rig.server.RegisterServer(ctx, ServerRegistration{Name: member, Family: family}, client))
	}
	_ = rig.server.registry.GetAllTools()
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	manager.servers["k8s-a"] = api.MCPServerInfo{Name: "k8s-a", Type: "streamable-http", Approval: &api.MCPServerApproval{Rule: api.ApprovalAllow}}
	manager.servers["k8s-b"] = api.MCPServerInfo{Name: "k8s-b", Type: "streamable-http", Approval: &api.MCPServerApproval{Rule: api.ApprovalAsk}}
	rig.workflows.defs["pods"] = &api.Workflow{Name: "pods", Args: map[string]api.ArgDefinition{"cluster": {Type: "string"}}, Steps: []api.WorkflowStep{
		{ID: "list", Tool: "x_kubernetes_list_pods", Args: map[string]any{"cluster": "{{cluster}}"}},
	}}
	result, err := rig.run(t, "workflow_pods", map[string]any{"cluster": "k8s-a"})
	require.NoError(t, err)
	approvalIDOf(t, result)
	assert.Empty(t, rig.workflows.ran, "a member that asks makes the templated family step ask")
}
