package aggregator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
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
	// nested records the definition hash of every workflow a run calls, by
	// name: the approval binds them with the workflow's own (M1-MU-S-08).
	nested map[string]string
	// unknown counts the steps muster could not resolve up front (a tool
	// no session server lists yet, a missing tool).
	unknown int
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
	plan := &workflowPlan{shown: map[string]bool{}, nested: map[string]string{}}
	if err := a.planWorkflow(ctx, workflow, plan, 0); err != nil {
		return ctx, nil, nil, err
	}
	if plan.blocked != "" {
		return ctx, nil, nil, fmt.Errorf("workflow %s cannot run: step tool not found: %s", name, plan.blocked)
	}
	// The agent's own entry for the workflow (workflow:, tool:, default)
	// applies to the run itself.
	switch own := withAgentWorkflowSetting(api.WithoutReportingApproval(ctx), api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}, name); own.Setting {
	case api.ApprovalSettingBlock:
		return ctx, nil, nil, fmt.Errorf("tool not found: %s", toolName)
	case api.ApprovalSettingAsk:
		plan.asks = append(plan.asks, own)
	}
	if len(plan.asks) == 0 && plan.unknown > 0 && a.approvalUnavailable(ctx) == "" {
		// A step muster cannot resolve counts as asking where muster can
		// ask. Where it cannot, the run is not refused for it: the step is
		// checked at dispatch like every other, and fails the run there if
		// it asks.
		plan.asks = append(plan.asks, api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable})
	}
	if len(plan.asks) == 0 {
		return withWorkflowRun(ctx, &workflowRun{workflow: name, shown: plan.shown}), args, nil, nil
	}
	target := api.ApprovalTarget{Kind: api.ApprovalTargetWorkflow, Name: name, Exposed: toolName, Definition: runDefinitionHash(workflow, plan.nested)}
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

// runDefinitionHash identifies what a run executes: the workflow's own
// definition and that of every workflow it calls, so a nested workflow
// changed after the approval no longer matches it.
func runDefinitionHash(workflow *api.Workflow, nested map[string]string) string {
	own := workflowDefinitionHash(workflow)
	if len(nested) == 0 {
		return own
	}
	parts := []string{own}
	for _, name := range sortedKeys(nested) {
		parts = append(parts, name+"="+nested[name])
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// planWorkflow resolves every step's tool of a workflow into plan. owner is
// the name of a nested workflow, empty for the workflow being run.
func (a *AggregatorServer) planWorkflow(ctx context.Context, workflow *api.Workflow, plan *workflowPlan, depth int) error {
	owner := ""
	if depth > 0 {
		owner = workflow.Name
	}
	visit := func(id, tool, description string, args map[string]any, condition *api.WorkflowCondition) error {
		if condition != nil && condition.Tool != "" {
			if err := a.planStep(ctx, owner, id+" (condition)", condition.Tool, "", condition.Args, plan, depth); err != nil {
				return err
			}
		}
		if tool == "" {
			return nil
		}
		return a.planStep(ctx, owner, id, tool, description, args, plan, depth)
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

// planStep resolves one step's tool. Steps of nested workflows are shown
// too, marked with their workflow.
func (a *AggregatorServer) planStep(ctx context.Context, owner, id, tool, description string, args map[string]any, plan *workflowPlan, depth int) error {
	plan.shown[tool] = true
	effective, err := a.stepApproval(ctx, tool, args, plan, depth)
	if err != nil {
		return err
	}
	if effective == unresolvedStep {
		plan.unknown++
		effective = api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnavailable}
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
	label := description
	if label == "" {
		label = a.stepLabel(tool)
	}
	plan.steps = append(plan.steps, api.ApprovalStep{ID: id, Label: label, Tool: tool, Asks: asks, Workflow: owner, Arguments: labelledArguments(mcp.ToolInputSchema{}, args)})
	return nil
}

// stepLabel names an undescribed step by its tool's own name made readable
// ("List agents", not "X agent manager list agents").
func (a *AggregatorServer) stepLabel(tool string) string {
	if _, original, err := a.registry.ResolveToolName(tool); err == nil && original != "" {
		return readable(original)
	}
	return readable(strings.TrimPrefix(tool, "core_"))
}

// stepApproval resolves the effective approval of one step's tool for the
// caller. A tool muster cannot resolve now, or a family step whose member is
// not written out, counts as asking.
func (a *AggregatorServer) stepApproval(ctx context.Context, tool string, args map[string]any, plan *workflowPlan, depth int) (api.ToolApproval, error) {
	unresolved := unresolvedStep
	if a.registry.IsFamilyTool(tool) {
		member, ok := args[a.registry.FamilyInstanceArgFor(tool)].(string)
		if !ok || member == "" || strings.Contains(member, "{{") {
			// The member is chosen at run time: the strictest member's
			// setting holds for the step.
			return a.familyStepApproval(ctx, tool, args)
		}
		original, err := a.registry.ResolveToolNameForServer(tool, member)
		if err != nil {
			return unresolved, nil
		}
		_, effective, err := a.serverToolApproval(ctx, member, original, tool, args)
		return effective, err
	}
	if server, original, err := a.registry.ResolveToolName(tool); err == nil {
		_, effective, err := a.serverToolApproval(ctx, server, original, tool, args)
		return effective, err
	}
	if sessionID := getSessionIDFromContext(ctx); sessionID != "" {
		if server, original, err := a.resolveUserTool(sessionID, tool); err == nil {
			_, effective, err := a.serverToolApproval(ctx, server, original, tool, args)
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
	plan.nested[nested.Name] = workflowDefinitionHash(nested)
	before := len(plan.asks)
	if err := a.planWorkflow(ctx, nested, plan, depth+1); err != nil {
		return api.ToolApproval{}, err
	}
	if len(plan.asks) > before {
		return api.StrictestApproval(plan.asks[before:]...), nil
	}
	return api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}, nil
}

// unresolvedStep marks a step muster could not resolve up front.
var unresolvedStep = api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: "unresolved"}

// familyStepApproval resolves a family step whose member the run chooses:
// the strictest setting of the members that offer the tool; asking when
// none can be resolved.
func (a *AggregatorServer) familyStepApproval(ctx context.Context, tool string, args map[string]any) (api.ToolApproval, error) {
	var settings []api.ToolApproval
	for _, member := range a.registry.GetToolServerNames(tool) {
		original, err := a.registry.ResolveToolNameForServer(tool, member)
		if err != nil {
			continue
		}
		_, effective, err := a.serverToolApproval(ctx, member, original, tool, args)
		if err != nil {
			return api.ToolApproval{}, err
		}
		settings = append(settings, effective)
	}
	if len(settings) == 0 {
		return unresolvedStep, nil
	}
	return api.StrictestApproval(settings...), nil
}

// workflowReport resolves a workflow tool of a listing to its strictest
// step, as filter_tools reports it (PRD: "filter_tools reports a workflow as
// its strictest step"): the steps of the workflows it calls included, from
// the connectors' settings the listing read; a step muster cannot resolve
// counts as asking. ok is false when the workflow cannot be read.
func (a *AggregatorServer) workflowReport(ctx context.Context, l *approvalListing, toolName string) (api.ToolApproval, bool) {
	workflow, err := a.reportedWorkflow(strings.TrimPrefix(strings.TrimPrefix(toolName, "core_"), "workflow_"))
	if err != nil {
		return api.ToolApproval{}, false
	}
	l.list(ctx)
	settings := a.workflowStepSettings(ctx, l, workflow, 0)
	steps := api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}
	if len(settings) > 0 {
		steps = api.StrictestApproval(settings...)
	}
	return withAgentWorkflowSetting(ctx, steps, workflow.Name), true
}

// workflowStepSettings lists the settings of a workflow's step tools.
func (a *AggregatorServer) workflowStepSettings(ctx context.Context, l *approvalListing, workflow *api.Workflow, depth int) []api.ToolApproval {
	unresolved := api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}
	var settings []api.ToolApproval
	resolve := func(tool string) {
		if tool == "" {
			return
		}
		if !l.readable {
			settings = append(settings, l.unavailable)
			return
		}
		if server, original, err := a.registry.ResolveToolName(tool); err == nil {
			if setting, found := l.server(ctx, server, original, nil); found {
				settings = append(settings, setting)
				return
			}
			settings = append(settings, unresolved)
			return
		}
		if !a.isCoreToolByName(tool) {
			settings = append(settings, unresolved)
			return
		}
		if !isWorkflowRun(tool) {
			if builtin, ok := api.BuiltinApproval(coreName(tool)); ok {
				settings = append(settings, builtin)
				return
			}
			settings = append(settings, l.core(ctx, tool))
			return
		}
		nested, err := a.reportedWorkflow(strings.TrimPrefix(strings.TrimPrefix(tool, "core_"), "workflow_"))
		if err != nil || depth >= maxWorkflowDepth {
			settings = append(settings, unresolved)
			return
		}
		settings = append(settings, a.workflowStepSettings(ctx, l, nested, depth+1)...)
	}
	var visitSubs func(subs []api.WorkflowSubStep)
	visitSubs = func(subs []api.WorkflowSubStep) {
		for _, sub := range subs {
			if sub.Condition != nil {
				resolve(sub.Condition.Tool)
			}
			resolve(sub.Tool)
			if sub.ForEach != nil {
				visitSubs(sub.ForEach.Steps)
			}
		}
	}
	for _, step := range workflow.Steps {
		if step.Condition != nil {
			resolve(step.Condition.Tool)
		}
		resolve(step.Tool)
		if step.ForEach != nil {
			visitSubs(step.ForEach.Steps)
		}
		visitSubs(step.Parallel)
	}
	visitSubs(workflow.OnFailure)
	return settings
}

// workflowReportTTL is how long a definition read for reporting is reused:
// listings are frequent and must not read every workflow each time. What a
// run does is decided on a fresh read (gateWorkflow); only the report can be
// this much behind a changed definition.
const workflowReportTTL = 30 * time.Second

// reportedWorkflows caches definitions read for reporting.
type reportedWorkflows struct {
	mu      sync.Mutex
	entries map[string]reportedWorkflow
}

type reportedWorkflow struct {
	workflow *api.Workflow
	err      error
	readAt   time.Time
}

// reportedWorkflow reads a workflow's definition for reporting, at most once
// per workflowReportTTL.
func (a *AggregatorServer) reportedWorkflow(name string) (*api.Workflow, error) {
	cache := &a.workflowReports
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := clock.Now()
	if entry, ok := cache.entries[name]; ok && now.Sub(entry.readAt) < workflowReportTTL {
		return entry.workflow, entry.err
	}
	workflow, err := lookupWorkflow(name)
	if cache.entries == nil {
		cache.entries = map[string]reportedWorkflow{}
	}
	cache.entries[name] = reportedWorkflow{workflow: workflow, err: err, readAt: now}
	return workflow, err
}
