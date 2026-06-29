package workflow

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// These tests define the contract for the execution payload-size guard (Phase 3
// of #930): before a record is persisted, an oversized record has its Result and
// oversized step results bounded and is flagged Truncated, so per-run documents
// stay well under the etcd object limit in Kubernetes mode. Written test-first;
// they expect guardExecutionPayload and the api.WorkflowExecution.Truncated
// field to be added.

func TestGuardExecutionPayloadTruncatesOversized(t *testing.T) {
	huge := strings.Repeat("x", 8*1024)
	exec := &api.WorkflowExecution{
		ExecutionID:  "exec-1",
		WorkflowName: "alpha",
		Status:       api.WorkflowExecutionCompleted,
		Result:       huge,
		Steps: []api.WorkflowExecutionStep{
			{StepID: "step-1", Tool: "x_echo_echo", Status: api.WorkflowExecutionCompleted, Result: huge},
		},
	}

	truncated := guardExecutionPayload(exec, 1024)
	require.True(t, truncated, "an oversized record must be reported as truncated")
	require.True(t, exec.Truncated, "the record must be flagged Truncated")

	// The bounded record must actually be smaller than the original payload.
	require.NotEqual(t, huge, exec.Result)
}

func TestGuardExecutionPayloadLeavesSmallRecord(t *testing.T) {
	exec := &api.WorkflowExecution{
		ExecutionID:  "exec-1",
		WorkflowName: "alpha",
		Status:       api.WorkflowExecutionCompleted,
		Result:       "small",
		Steps: []api.WorkflowExecutionStep{
			{StepID: "step-1", Tool: "x_echo_echo", Status: api.WorkflowExecutionCompleted, Result: "ok"},
		},
	}

	truncated := guardExecutionPayload(exec, 256*1024)
	require.False(t, truncated, "a small record must not be truncated")
	require.False(t, exec.Truncated)
	require.Equal(t, "small", exec.Result)
}
