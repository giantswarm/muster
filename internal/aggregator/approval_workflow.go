package aggregator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
)

// A workflow asks once, up front. Before a run starts, every step's tool --
// the steps, their parallel and loop sub-steps, conditions and cleanup steps,
// and the steps of workflows it calls -- is resolved with the caller's
// settings. A step that is blocked refuses the run before anything runs; if
// any step would ask, the whole run asks once, for the workflow's definition
// and its canonical inputs. The run then carries what was checked
// (workflowRun): each step is resolved again at dispatch, and one that is
// blocked, calls a tool the check did not see (a templated name), or asks in
// a run nobody approved (a setting changed since), fails the run there.

// maxWorkflowDepth bounds how deep the check follows workflows calling
// workflows; a deeper one counts as asking.
const maxWorkflowDepth = 4

// workflowManagementTools are the workflow_ tools that manage workflows
// rather than run one.
var workflowManagementTools = []string{"workflow_list", "workflow_get", "workflow_create",
	"workflow_update", "workflow_delete", "workflow_validate", "workflow_available",
	"workflow_execution_list", "workflow_execution_get"}

// isWorkflowRun reports whether a core tool name runs a workflow.
func isWorkflowRun(toolName string) bool {
	name := strings.TrimPrefix(toolName, "core_")
	return strings.HasPrefix(name, "workflow_") && !slices.Contains(workflowManagementTools, name)
}

// workflowRun is what the up-front check of a run found.
type workflowRun struct {
	workflow string
	approved bool
	shown    map[string]bool
}

type workflowRunKey struct{}

func withWorkflowRun(ctx context.Context, run *workflowRun) context.Context {
	return context.WithValue(ctx, workflowRunKey{}, run)
}

func workflowRunFromContext(ctx context.Context) *workflowRun {
	run, _ := ctx.Value(workflowRunKey{}).(*workflowRun)
	return run
}

// check applies a step's effective approval at dispatch.
func (r *workflowRun) check(toolName string, effective api.ToolApproval) error {
	if !r.shown[toolName] {
		return fmt.Errorf("workflow %s stopped: step tool %s was not part of the run when it was checked", r.workflow, toolName)
	}
	if effective.Setting != api.ApprovalSettingAllow && !r.approved {
		return fmt.Errorf("workflow %s stopped: step tool %s asks before it runs (%s from %s) and this run was not approved up front; run the workflow again", r.workflow, toolName, effective.Setting, effective.Source)
	}
	return nil
}

// workflowPlan is the up-front check's result.
type workflowPlan struct {
	shown   map[string]bool
	steps   []api.ApprovalStep
	asks    []api.ToolApproval
	blocked string
}

// gateWorkflow checks a client's workflow run up front and returns the
// context the run proceeds with, or the answer or error that stops it.
func (a *AggregatorServer) gateWorkflow(ctx context.Context, toolName string, args map[string]any) (context.Context, map[string]any, *mcp.CallToolResult, error) {
	if run := workflowRunFromContext(ctx); run != nil {
		// A workflow a run calls was checked with the run.
		return ctx, args, nil, run.check(toolName, api.ToolApproval{Setting: api.ApprovalSettingAllow})
	}
	if !api.ApprovalGateFromContext(ctx) {
		return ctx, args, nil, nil
	}
	name := strings.TrimPrefix(strings.TrimPrefix(toolName, "core_"), "workflow_")
	workflow, err := lookupWorkflow(name)
	if err != nil {
		return ctx, nil, nil, err
	}
	plan := &workflowPlan{shown: map[string]bool{}}
	if err := a.planWorkflow(ctx, workflow, plan, 0); err != nil {
		return ctx, nil, nil, err
	}
	if plan.blocked != "" {
		return ctx, nil, nil, fmt.Errorf("workflow %s cannot run: step tool not found: %s", name, plan.blocked)
	}
	if len(plan.asks) == 0 {
		return withWorkflowRun(ctx, &workflowRun{workflow: name, shown: plan.shown}), args, nil, nil
	}
	target := api.ApprovalTarget{Kind: api.ApprovalTargetWorkflow, Name: name, Exposed: toolName, Definition: workflowDefinitionHash(workflow)}
	strictest := api.StrictestApproval(plan.asks...)
	approved, answer, err := a.ask(ctx, target, strictest, args, func() api.ApprovalDisplay {
		title := "workflow " + name
		return api.ApprovalDisplay{
			Message:     approvalMessage(ctx, title, ""),
			Title:       title,
			ChangesData: true,
			Arguments:   labelledArguments(mcp.ToolInputSchema{}, args),
			Steps:       plan.steps,
		}
	})
	if err != nil || answer != nil {
		return ctx, nil, answer, err
	}
	return withWorkflowRun(ctx, &workflowRun{workflow: name, approved: true, shown: plan.shown}), approved, nil, nil
}

func lookupWorkflow(name string) (*api.Workflow, error) {
	handler := api.GetWorkflow()
	if handler == nil {
		return nil, fmt.Errorf("workflow handler not available")
	}
	workflow, err := handler.GetWorkflow(name)
	if err != nil {
		return nil, err
	}
	return workflow, nil
}

// workflowDefinitionHash identifies a definition: a changed one is another
// target, so an approval of the old one does not run it.
func workflowDefinitionHash(workflow *api.Workflow) string {
	raw, _ := json.Marshal(struct {
		Args      map[string]api.ArgDefinition `json:"args"`
		Steps     []api.WorkflowStep           `json:"steps"`
		OnFailure []api.WorkflowSubStep        `json:"onFailure"`
	}{workflow.Args, workflow.Steps, workflow.OnFailure})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// planWorkflow resolves every step's tool of a workflow into plan.
func (a *AggregatorServer) planWorkflow(ctx context.Context, workflow *api.Workflow, plan *workflowPlan, depth int) error {
	visit := func(id, tool, description string, args map[string]any, condition *api.WorkflowCondition) error {
		if condition != nil && condition.Tool != "" {
			if err := a.planStep(ctx, id+" (condition)", condition.Tool, "", condition.Args, plan, depth); err != nil {
				return err
			}
		}
		if tool == "" {
			return nil
		}
		return a.planStep(ctx, id, tool, description, args, plan, depth)
	}
	var visitSubs func(prefix string, subs []api.WorkflowSubStep) error
	visitSubs = func(prefix string, subs []api.WorkflowSubStep) error {
		for _, sub := range subs {
			if err := visit(prefix+sub.ID, sub.Tool, sub.Description, sub.Args, sub.Condition); err != nil {
				return err
			}
			if sub.ForEach != nil {
				if err := visitSubs(prefix+sub.ID+".", sub.ForEach.Steps); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, step := range workflow.Steps {
		if err := visit(step.ID, step.Tool, step.Description, step.Args, step.Condition); err != nil {
			return err
		}
		if step.ForEach != nil {
			if err := visitSubs(step.ID+".", step.ForEach.Steps); err != nil {
				return err
			}
		}
		if err := visitSubs(step.ID+".", step.Parallel); err != nil {
			return err
		}
	}
	return visitSubs("onFailure.", workflow.OnFailure)
}

// planStep resolves one step's tool.
func (a *AggregatorServer) planStep(ctx context.Context, id, tool, description string, args map[string]any, plan *workflowPlan, depth int) error {
	plan.shown[tool] = true
	effective, err := a.stepApproval(ctx, tool, args, plan, depth)
	if err != nil {
		return err
	}
	if effective.Setting == api.ApprovalSettingBlock {
		if plan.blocked == "" {
			plan.blocked = tool
		}
		return nil
	}
	asks := effective.Setting != api.ApprovalSettingAllow
	if asks {
		plan.asks = append(plan.asks, effective)
	}
	if depth == 0 {
		label := description
		if label == "" {
			label = readable(tool)
		}
		plan.steps = append(plan.steps, api.ApprovalStep{ID: id, Label: label, Tool: tool, Asks: asks, Arguments: labelledArguments(mcp.ToolInputSchema{}, args)})
	}
	return nil
}

// stepApproval resolves the effective approval of one step's tool for the
// caller. A tool muster cannot resolve now, or a family step whose member is
// not written out, counts as asking.
func (a *AggregatorServer) stepApproval(ctx context.Context, tool string, args map[string]any, plan *workflowPlan, depth int) (api.ToolApproval, error) {
	unresolved := api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}
	if a.registry.IsFamilyTool(tool) {
		member, ok := args[a.registry.FamilyInstanceArgFor(tool)].(string)
		if !ok || member == "" || strings.Contains(member, "{{") {
			return unresolved, nil
		}
		original, err := a.registry.ResolveToolNameForServer(tool, member)
		if err != nil {
			return unresolved, nil
		}
		_, effective, err := serverToolApproval(ctx, member, original, args)
		return effective, err
	}
	if server, original, err := a.registry.ResolveToolName(tool); err == nil {
		_, effective, err := serverToolApproval(ctx, server, original, args)
		return effective, err
	}
	if sessionID := getSessionIDFromContext(ctx); sessionID != "" {
		if server, original, err := a.resolveUserTool(sessionID, tool); err == nil {
			_, effective, err := serverToolApproval(ctx, server, original, args)
			return effective, err
		}
	}
	if !a.isCoreToolByName(tool) {
		return unresolved, nil
	}
	if !isWorkflowRun(tool) {
		return coreToolApproval(ctx, coreName(tool), args), nil
	}
	if depth >= maxWorkflowDepth {
		return unresolved, nil
	}
	nested, err := lookupWorkflow(strings.TrimPrefix(strings.TrimPrefix(tool, "core_"), "workflow_"))
	if err != nil {
		return unresolved, nil
	}
	before := len(plan.asks)
	if err := a.planWorkflow(ctx, nested, plan, depth+1); err != nil {
		return api.ToolApproval{}, err
	}
	if len(plan.asks) > before {
		return api.StrictestApproval(plan.asks[before:]...), nil
	}
	return api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}, nil
}
