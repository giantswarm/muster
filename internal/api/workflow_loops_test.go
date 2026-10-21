package api

import (
	"errors"
	"strings"
	"testing"
)

// checkJQ stands in for the jq compiler: a program containing "BAD" does not
// compile.
func checkJQ(program string) error {
	if strings.Contains(program, "BAD") {
		return errors.New("syntax error")
	}
	return nil
}

func tool(id string) WorkflowSubStep { return WorkflowSubStep{ID: id, Tool: "x_t"} }

func loopOf(as string, parallelism int, steps ...WorkflowSubStep) *WorkflowForEach {
	return &WorkflowForEach{Items: "{{ .input.x }}", As: as, Parallelism: parallelism, Steps: steps}
}

func nestedLoop(id string, f *WorkflowForEach) WorkflowSubStep {
	return WorkflowSubStep{ID: id, ForEach: f}
}

func TestValidateLoopsPagingAndJQ(t *testing.T) {
	tests := []struct {
		name    string
		wf      Workflow
		wantErr []string
	}{
		{
			name: "a fleet scan",
			wf: Workflow{Steps: []WorkflowStep{{
				ID: "fleet", JQ: "[.[]]",
				ForEach: loopOf("mc", 4,
					WorkflowSubStep{ID: "clusters", Tool: "x_list", Paginate: &WorkflowPaginate{MaxPages: 5}, JQ: "[.items[]]"},
					nestedLoop("per-cluster", loopOf("cluster", 8, WorkflowSubStep{ID: "pods", Tool: "x_list", Paginate: &WorkflowPaginate{}})),
				),
			}}},
		},
		{
			name:    "two levels of nesting",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "a", ForEach: loopOf("a", 0, nestedLoop("b", loopOf("b", 0, nestedLoop("c", loopOf("c", 0, tool("leaf"))))))}}},
			wantErr: []string{"sub-step c", "one level"},
		},
		{
			name:    "a forEach in a parallel group",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "g", Parallel: []WorkflowSubStep{nestedLoop("loop", loopOf("", 0, tool("leaf")))}}}},
			wantErr: []string{"loop", "forEach body"},
		},
		{
			name:    "a forEach in onFailure",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "s", Tool: "x_t"}}, OnFailure: []WorkflowSubStep{nestedLoop("cleanup", loopOf("", 0, tool("leaf")))}},
			wantErr: []string{"cleanup", "forEach body"},
		},
		{
			name:    "both loops on the default variable",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "a", ForEach: loopOf("", 0, nestedLoop("b", loopOf("", 0, tool("leaf"))))}}},
			wantErr: []string{"sub-step b", `"item"`},
		},
		{
			name:    "a nested id shared with the outer body",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "a", ForEach: loopOf("a", 0, tool("pods"), nestedLoop("b", loopOf("b", 0, tool("pods"))))}}},
			wantErr: []string{"sub-step b", `"pods"`},
		},
		{
			name:    "parallelism above the bound",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "loop", ForEach: loopOf("", MaxForEachParallelism+1, tool("leaf"))}}},
			wantErr: []string{"step loop", "parallelism", "50"},
		},
		{
			name:    "nested parallelism above the bound",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "a", ForEach: loopOf("a", 0, nestedLoop("b", loopOf("b", 99, tool("leaf"))))}}},
			wantErr: []string{"sub-step b", "parallelism"},
		},
		{
			name:    "paginate on a forEach",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "loop", Paginate: &WorkflowPaginate{}, ForEach: loopOf("", 0, tool("leaf"))}}},
			wantErr: []string{"step loop", "paginate"},
		},
		{
			name:    "maxPages above the bound",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "pods", Tool: "x_t", Paginate: &WorkflowPaginate{MaxPages: MaxPaginateMaxPages + 1}}}},
			wantErr: []string{"step pods", "maxPages"},
		},
		{
			name:    "a bad jq program in a nested body",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "a", ForEach: loopOf("a", 0, nestedLoop("b", loopOf("b", 0, WorkflowSubStep{ID: "pods", Tool: "x_t", JQ: "BAD"})))}}},
			wantErr: []string{"sub-step pods", "invalid jq program", "syntax error"},
		},
		{
			name:    "a bad jq program in onFailure",
			wf:      Workflow{Steps: []WorkflowStep{{ID: "s", Tool: "x_t"}}, OnFailure: []WorkflowSubStep{{ID: "undo", Tool: "x_t", JQ: "BAD"}}},
			wantErr: []string{"onFailure sub-step undo", "jq"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLoopsPagingAndJQ(&tt.wf, checkJQ)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %v", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestWorkflowStepToolsIncludesNestedLoops(t *testing.T) {
	wf := &Workflow{Steps: []WorkflowStep{{
		ID: "a",
		ForEach: loopOf("a", 0, nestedLoop("b", loopOf("b", 0, WorkflowSubStep{
			ID: "c", Tool: "x_inner", Condition: &WorkflowCondition{Tool: "x_gate"},
		}))),
	}}}
	got := strings.Join(WorkflowStepTools(wf), ",")
	if got != "x_gate,x_inner" {
		t.Fatalf("WorkflowStepTools = %s, want x_gate,x_inner", got)
	}
}
