package workflow

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// Fields of the record a forEach keeps per iteration under its own step id.
const (
	iterationFieldIndex   = "index"
	iterationFieldItem    = "item"
	iterationFieldResults = "results"
)

// loop is a forEach as the executor runs it: a top-level forEach step or a
// forEach sub-step nested in a top-level forEach body.
type loop struct {
	ID           string
	ForEach      *api.WorkflowForEach
	AllowFailure bool
	JQ           string
}

// loopScope says where a loop runs. A body sub-step's result is kept per
// iteration as "<id><suffix>_<index>": suffix is empty at the top level and
// "_<outer index>" in a loop nested in an outer iteration, whose index outer
// holds.
type loopScope struct {
	suffix string
	outer  *int
}

// nested is the scope of a loop nested in iteration idx of this scope's loop.
func (s loopScope) nested(idx int) loopScope {
	i := idx
	return loopScope{suffix: s.suffix + "_" + strconv.Itoa(idx), outer: &i}
}

// iteration is what one iteration of a loop produced, before it is merged
// into the context the loop runs in.
type iteration struct {
	local   *executionContext
	written map[string]interface{} // result keys to set in the loop's context
	record  map[string]interface{} // the iteration's entry under the loop's id
	outcome stepOutcome
	err     error
}

// runForEach executes a top-level forEach step.
func (we *WorkflowExecutor) runForEach(ctx context.Context, workflowName string, step api.WorkflowStep, execCtx *executionContext) (stepOutcome, error) {
	if skip, outcome, err := we.evaluateCompositeCondition(ctx, workflowName, plainStepView(step), execCtx); err != nil || skip {
		return outcome, err
	}
	l := loop{ID: step.ID, ForEach: step.ForEach, AllowFailure: step.AllowFailure, JQ: step.JQ}
	outcome, _, err := we.runLoop(ctx, workflowName, l, execCtx, loopScope{})
	return outcome, err
}

// runLoop runs the body once per item, binding the item to
// "{{ .vars.<as> }}" and its index to "{{ .vars.<as>_index }}".
//
// Every iteration starts from the loop's context with the body's results
// removed: within it a sub-step's result lives under its plain id, so later
// sub-steps chain off it. Once the iteration is merged back, the result is
// also kept as "<id><suffix>_<index>", the plain id keeps the result of the
// last iteration that ran the sub-step, and the iteration's step records are
// appended in item order. A sub-step skipped by its condition leaves no key
// for its iteration. After the loop its own id holds one record per iteration
// that ran -- index, item, status, the body's results and the error of a
// failed iteration -- reshaped by the loop's jq program when it has one.
//
// Iterations run one after another, or up to forEach.parallelism at once.
// Concurrent iterations start from the context as it was before the loop and
// are merged in item order once they all finished, so the outcome does not
// depend on which one finished first. A failure that stops the loop starts no
// further iteration; those already running finish and are reported.
//
// It returns every result key it set in execCtx, so an outer iteration can
// carry a nested loop's results out.
func (we *WorkflowExecutor) runLoop(ctx context.Context, workflowName string, l loop, execCtx *executionContext, scope loopScope) (stepOutcome, map[string]interface{}, error) {
	items, err := we.resolveForEachItems(l.ForEach.Items, execCtx)
	if err != nil {
		return stepOutcome{}, nil, fmt.Errorf("forEach step %s: %w", l.ID, err)
	}

	written := make(map[string]interface{})
	records := make([]interface{}, 0, len(items))
	var stop *stepOutcome
	merge := func(it *iteration) {
		offset := len(execCtx.stepMetadata)
		execCtx.stepMetadata = append(execCtx.stepMetadata, it.local.stepMetadata...)
		for _, v := range it.local.templateVars {
			if !slices.Contains(execCtx.templateVars, v) {
				execCtx.templateVars = append(execCtx.templateVars, v)
			}
		}
		for k, v := range it.written {
			execCtx.results[k] = v
			written[k] = v
		}
		records = append(records, it.record)
		if it.outcome.stop && stop == nil {
			oc := it.outcome
			if oc.failedRecord >= 0 {
				oc.failedRecord += offset
			}
			stop = &oc
		}
	}

	if parallelism := l.ForEach.Parallelism; parallelism <= 1 {
		for idx, item := range items {
			it := we.runIteration(ctx, workflowName, l, execCtx, scope, idx, item)
			if it.err != nil {
				return stepOutcome{}, nil, it.err
			}
			merge(it)
			if it.outcome.stop {
				break
			}
		}
	} else {
		for _, it := range we.runIterationsConcurrently(ctx, workflowName, l, execCtx, scope, items, parallelism) {
			if it == nil {
				continue // never started: an earlier iteration stopped the loop
			}
			if it.err != nil {
				return stepOutcome{}, nil, it.err
			}
			merge(it)
		}
	}

	var collected interface{} = records
	if l.JQ != "" {
		projected, err := we.project(ctx, l.JQ, collected, execCtx)
		switch {
		case err == nil:
			collected = projected
		case stop == nil:
			// The loop ran but its records cannot be reshaped: the step
			// fails like a tool step whose jq fails.
			logging.Error("WorkflowExecutor", err, "forEach step %s: reshaping its records failed", l.ID)
			collected = map[string]interface{}{api.FieldError: err.Error(), api.FieldSuccess: false, "isError": true}
			if !l.AllowFailure {
				stop = &stepOutcome{stop: true, fatalErr: err, failedStepID: l.ID, errorMessage: err.Error(), failedRecord: -1}
			}
		}
	}
	execCtx.results[l.ID] = collected
	written[l.ID] = collected

	if stop != nil {
		if l.AllowFailure {
			logging.Debug("WorkflowExecutor", "forEach step %s stopped on a failure but allow_failure is true", l.ID)
			return stepOutcome{}, written, nil
		}
		return *stop, written, nil
	}
	return stepOutcome{}, written, nil
}

// runIterationsConcurrently runs the iterations of a loop up to parallelism
// at once, each from the loop's context as it was before the loop, and
// returns them in item order. An iteration not started because an earlier
// one stopped the loop is nil.
func (we *WorkflowExecutor) runIterationsConcurrently(ctx context.Context, workflowName string, l loop, execCtx *executionContext, scope loopScope, items []interface{}, parallelism int) []*iteration {
	done := make([]*iteration, len(items))
	var (
		mu      sync.Mutex
		next    int
		stopped bool
	)
	// claim hands out the next item index, or -1 once the items are gone or
	// an iteration stopped the loop. Indexes are claimed in order, so the
	// iterations that ran are always a prefix of the items.
	claim := func() int {
		mu.Lock()
		defer mu.Unlock()
		if stopped || next == len(items) {
			return -1
		}
		next++
		return next - 1
	}
	var wg sync.WaitGroup
	for range min(parallelism, len(items)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := claim(); idx >= 0; idx = claim() {
				it := we.runIteration(ctx, workflowName, l, execCtx, scope, idx, items[idx])
				done[idx] = it
				if it.err != nil || it.outcome.stop {
					mu.Lock()
					stopped = true
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return done
}

// runIteration runs the loop's body for one item against its own copy of the
// loop's context. It only reads execCtx, so iterations may run concurrently.
func (we *WorkflowExecutor) runIteration(ctx context.Context, workflowName string, l loop, execCtx *executionContext, scope loopScope, idx int, item interface{}) *iteration {
	as := l.ForEach.LoopVariable()
	variables := make(map[string]interface{}, len(execCtx.variables)+2)
	for k, v := range execCtx.variables {
		variables[k] = v
	}
	variables[as] = item
	variables[as+"_index"] = idx

	// The iteration sees none of the body's results under their plain ids, a
	// nested body's included: those are an earlier iteration's.
	results := copyResults(execCtx.results)
	for _, ss := range l.ForEach.Steps {
		delete(results, ss.ID)
		if ss.ForEach != nil {
			for _, inner := range ss.ForEach.Steps {
				delete(results, inner.ID)
			}
		}
	}

	local := &executionContext{
		input:        execCtx.input,
		variables:    variables,
		results:      results,
		templateVars: make([]string, 0),
		stepMetadata: make([]stepMetadata, 0),
		outer:        execCtx,
		outerRecords: len(execCtx.stepMetadata),
	}
	it := &iteration{local: local, written: make(map[string]interface{})}
	ran := make(map[string]interface{})
	failure := ""

	for _, ss := range l.ForEach.Steps {
		first := len(local.stepMetadata)
		var outcome stepOutcome
		var err error
		if ss.ForEach != nil {
			var inner map[string]interface{}
			outcome, inner, err = we.runNestedLoop(ctx, workflowName, ss, local, scope, idx)
			for k, v := range inner {
				it.written[k] = v
			}
		} else {
			outcome, err = we.runStep(ctx, workflowName, subStepViewFrom(ss), local)
		}
		if err != nil {
			it.err = err
			return it
		}

		// A tool sub-step's record, or a skipped nested loop's, is this
		// iteration's: it points at the per-iteration key, so the returned
		// document shows what this iteration produced, and nothing for a
		// skipped one. A nested loop's own records are tagged by it.
		key := ss.ID + scope.suffix + "_" + strconv.Itoa(idx)
		for i := first; i < len(local.stepMetadata); i++ {
			if rec := &local.stepMetadata[i]; rec.Iteration == nil {
				tagRecord(rec, key, idx, scope.outer)
			}
			if failure == "" && local.stepMetadata[i].Status == statusFailed && local.stepMetadata[i].ID == ss.ID {
				failure = failureMessage(ss.ID, local.results[ss.ID], outcome)
			}
		}
		if v, ok := local.results[ss.ID]; ok {
			it.written[ss.ID] = v
			it.written[key] = v
			ran[ss.ID] = v
		}
		if outcome.stop {
			it.outcome = outcome
			if failure == "" {
				failure = failureMessage(ss.ID, local.results[ss.ID], outcome)
			}
			break
		}
	}

	it.record = map[string]interface{}{
		iterationFieldIndex:   idx,
		iterationFieldItem:    item,
		api.FieldStatus:       statusCompleted,
		iterationFieldResults: ran,
	}
	if failure != "" {
		it.record[api.FieldStatus] = statusFailed
		it.record[api.FieldError] = failure
	}
	return it
}

// runNestedLoop runs a forEach sub-step inside iteration idx of the loop in
// scope, honoring the sub-step's condition.
func (we *WorkflowExecutor) runNestedLoop(ctx context.Context, workflowName string, ss api.WorkflowSubStep, local *executionContext, scope loopScope, idx int) (stepOutcome, map[string]interface{}, error) {
	if skip, outcome, err := we.evaluateCompositeCondition(ctx, workflowName, subStepViewFrom(ss), local); err != nil || skip {
		return outcome, nil, err
	}
	l := loop{ID: ss.ID, ForEach: ss.ForEach, AllowFailure: ss.AllowFailure, JQ: ss.JQ}
	return we.runLoop(ctx, workflowName, l, local, scope.nested(idx))
}

// tagRecord points a loop body's step record at its iteration's result key.
func tagRecord(rec *stepMetadata, key string, idx int, outer *int) {
	i := idx
	rec.ResultKey = key
	rec.Iteration = &i
	rec.OuterIteration = outer
}

// failureMessage describes why a sub-step failed for its iteration's record:
// the error its result carries, else the error that stopped the loop, else
// the text of the error result the tool returned.
func failureMessage(id string, result interface{}, outcome stepOutcome) string {
	switch r := result.(type) {
	case map[string]interface{}:
		if msg, ok := r[api.FieldError].(string); ok && msg != "" {
			return id + ": " + msg
		}
	case string:
		if r != "" {
			return id + ": " + r
		}
	}
	if outcome.errorMessage != "" {
		return id + ": " + outcome.errorMessage
	}
	if outcome.result != nil && len(outcome.result.Content) > 0 {
		if text, ok := outcome.result.Content[0].(mcp.TextContent); ok && text.Text != "" {
			return id + ": " + text.Text
		}
	}
	return id + ": the tool returned an error result"
}
