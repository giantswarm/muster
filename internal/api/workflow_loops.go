package api

import "fmt"

// Bounds and defaults of a forEach loop and of a paginated tool step.
const (
	// DefaultLoopVariable is the name a forEach binds its item to when it
	// declares no `as`.
	DefaultLoopVariable = "item"
	// MaxForEachParallelism bounds forEach.parallelism. A nested loop runs
	// inside each outer iteration, so the calls in flight at once are at most
	// the outer parallelism times the inner one.
	MaxForEachParallelism = 50
	// DefaultPaginateMaxPages is paginate.maxPages when a step declares none.
	DefaultPaginateMaxPages = 10
	// MaxPaginateMaxPages bounds paginate.maxPages.
	MaxPaginateMaxPages = 1000
)

// ValidateLoopsPagingAndJQ checks the options a fleet-wide scan is built from,
// in every step of the workflow: a forEach nests one level and only inside a
// top-level forEach body, a nested loop's variable does not shadow the outer
// one, its body's ids do not collide with the outer body's, parallelism and
// paginate.maxPages are within bounds, paginate is set on tool steps only, and
// every jq program compiles (checkJQ reports why one does not). It is shared
// by the structured create/validate path and the CRD reconciler, so a
// definition is held to the same rules however it is authored.
func ValidateLoopsPagingAndJQ(wf *Workflow, checkJQ func(program string) error) error {
	for _, step := range wf.Steps {
		label := "step " + step.ID
		if err := validateToolOptions(label, step.Tool, step.Paginate, step.JQ, checkJQ); err != nil {
			return err
		}
		if step.ForEach != nil {
			if err := validateLoop(label, step.ForEach, nil, checkJQ); err != nil {
				return err
			}
		}
		if err := validateFlatSubSteps(label+" parallel", step.Parallel, checkJQ); err != nil {
			return err
		}
	}
	return validateFlatSubSteps("onFailure", wf.OnFailure, checkJQ)
}

// validateLoop checks a forEach and its body. outer is the loop it is nested
// in, nil for a top-level forEach; only a top-level body may hold a loop.
func validateLoop(label string, loop *WorkflowForEach, outer *WorkflowForEach, checkJQ func(string) error) error {
	// Zero is "not set" here; a structured definition that spells out 0 is
	// rejected where its fields are read, the CRD by its minimum.
	if loop.Parallelism < 0 || loop.Parallelism > MaxForEachParallelism {
		return fmt.Errorf("%s: forEach.parallelism must be between 1 and %d, got %d", label, MaxForEachParallelism, loop.Parallelism)
	}
	if outer != nil && loop.LoopVariable() == outer.LoopVariable() {
		return fmt.Errorf("%s: the loop variable %q is already the outer forEach's; give the nested forEach its own `as`", label, loop.LoopVariable())
	}
	for _, sub := range loop.Steps {
		subLabel := fmt.Sprintf("%s forEach sub-step %s", label, sub.ID)
		if err := validateToolOptions(subLabel, sub.Tool, sub.Paginate, sub.JQ, checkJQ); err != nil {
			return err
		}
		if sub.ForEach == nil {
			continue
		}
		if outer != nil {
			return fmt.Errorf("%s: a forEach nests one level, and this one is inside a nested forEach", subLabel)
		}
		if err := validateLoop(subLabel, sub.ForEach, loop, checkJQ); err != nil {
			return err
		}
		if err := validateNestedIDs(subLabel, loop.Steps, sub.ForEach.Steps); err != nil {
			return err
		}
	}
	return nil
}

// validateNestedIDs rejects a nested body sub-step that shares its id with a
// sub-step of the outer body: both live in the outer iteration's results.
func validateNestedIDs(label string, outerBody, innerBody []WorkflowSubStep) error {
	outer := make(map[string]bool, len(outerBody))
	for _, sub := range outerBody {
		outer[sub.ID] = true
	}
	for _, sub := range innerBody {
		if outer[sub.ID] {
			return fmt.Errorf("%s: sub-step id %q is already used in the outer forEach body", label, sub.ID)
		}
	}
	return nil
}

// validateFlatSubSteps checks the sub-steps of a parallel group or of
// onFailure, where a sub-step is a tool call and never a loop.
func validateFlatSubSteps(label string, subs []WorkflowSubStep, checkJQ func(string) error) error {
	for _, sub := range subs {
		subLabel := fmt.Sprintf("%s sub-step %s", label, sub.ID)
		if sub.ForEach != nil {
			return fmt.Errorf("%s: a forEach sub-step is only allowed in a forEach body", subLabel)
		}
		if err := validateToolOptions(subLabel, sub.Tool, sub.Paginate, sub.JQ, checkJQ); err != nil {
			return err
		}
	}
	return nil
}

// validateToolOptions checks a step's paginate and jq options.
func validateToolOptions(label, tool string, paginate *WorkflowPaginate, jq string, checkJQ func(string) error) error {
	if paginate != nil {
		if tool == "" {
			return fmt.Errorf("%s: paginate applies to a tool step only", label)
		}
		if paginate.MaxPages < 0 || paginate.MaxPages > MaxPaginateMaxPages {
			return fmt.Errorf("%s: paginate.maxPages must be between 1 and %d, got %d", label, MaxPaginateMaxPages, paginate.MaxPages)
		}
	}
	if jq != "" && checkJQ != nil {
		if err := checkJQ(jq); err != nil {
			return fmt.Errorf("%s: invalid jq program: %w", label, err)
		}
	}
	return nil
}
