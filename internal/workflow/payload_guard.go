package workflow

import (
	"encoding/json"

	"github.com/giantswarm/muster/internal/api"
)

// maxExecutionPayloadBytes bounds the marshaled size of a persisted execution
// record. It is well under etcd's ~1.5 MB object limit so the Kubernetes
// backend never rejects a record, and it keeps filesystem records small too.
const maxExecutionPayloadBytes = 256 * 1024

// executionTruncationMarker replaces an oversized Result or step result that
// was dropped to keep the record within maxExecutionPayloadBytes.
const executionTruncationMarker = "[truncated: payload exceeded retention size limit]"

// guardExecutionPayload bounds the size of an execution record before it is
// persisted. If the marshaled record is within limit it is left untouched and
// false is returned. Otherwise the workflow Result and any oversized step
// results are replaced with a truncation marker, the record is flagged
// Truncated, and true is returned.
//
// ponytail: the per-field cap is a simple heuristic (limit/8) rather than an
// exact fit. With many step results each just under the cap the record could
// still exceed limit, but limit is already an order of magnitude below etcd's
// object ceiling, so the record stays safely persistable. The upgrade path is
// an iterative shrink that re-measures after each truncation.
func guardExecutionPayload(exec *api.WorkflowExecution, limit int) bool {
	if exec == nil || limit <= 0 {
		return false
	}
	if payloadSize(exec) <= limit {
		return false
	}

	maxFieldBytes := limit / 8

	if payloadSize(exec.Result) > maxFieldBytes {
		exec.Result = executionTruncationMarker
	}
	for i := range exec.Steps {
		if payloadSize(exec.Steps[i].Result) > maxFieldBytes {
			exec.Steps[i].Result = executionTruncationMarker
		}
	}

	exec.Truncated = true
	return true
}

// payloadSize returns the marshaled JSON byte length of v, or 0 when v is nil
// or cannot be marshaled.
func payloadSize(v interface{}) int {
	if v == nil {
		return 0
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(raw)
}
