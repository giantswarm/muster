package api

// WorkflowStepTools returns, in discovery order and deduplicated, every tool
// name a workflow's steps reference: top-level step tools, condition tools,
// the tools of forEach and parallel sub-steps (a nested forEach's included),
// and onFailure handler tools.
// Nested workflow_<name> steps are returned as such; callers that need
// transitive resolution descend into them.
func WorkflowStepTools(wf *Workflow) []string {
	if wf == nil {
		return nil
	}
	var tools []string
	seen := map[string]struct{}{}
	add := func(name string) {
		if name == "" {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		tools = append(tools, name)
	}
	addCondition := func(c *WorkflowCondition) {
		if c != nil {
			add(c.Tool)
		}
	}
	var addSub func(subs []WorkflowSubStep)
	addSub = func(subs []WorkflowSubStep) {
		for _, sub := range subs {
			addCondition(sub.Condition)
			add(sub.Tool)
			if sub.ForEach != nil {
				addSub(sub.ForEach.Steps)
			}
		}
	}
	for _, step := range wf.Steps {
		addCondition(step.Condition)
		add(step.Tool)
		if step.ForEach != nil {
			addSub(step.ForEach.Steps)
		}
		addSub(step.Parallel)
	}
	addSub(wf.OnFailure)
	return tools
}
