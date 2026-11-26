package aggregator

import (
	"context"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/approval"
	"github.com/giantswarm/muster/v5/internal/clock"
)

// Metrics of approval (see approval.Metrics for the instruments), emitted at
// the points the audit events are logged: auditApproval maps each audit
// action to its metric, so a call path cannot log one without the other.
//
//	approval_requested -> muster_approval_requests_total
//	approval_used      -> muster_approval_decisions_total{outcome="used"}
//	approval_refused   -> muster_approval_refusals_total{reason=unavailable|store_unavailable|cap_reached}
//	call_refused       -> muster_approval_refusals_total{reason=blocked|settings_unavailable}
//	approval_decision  -> muster_approval_decisions_total{outcome=approved|declined} (approval page)
//	approval_declined  -> not counted: the page counted the decision
//
// A request that expires unanswered has no audit event; the sweeper counts it
// as outcome="expired".

// approvalSweepInterval is how often the replica reconciles its pending
// requests with the store.
const approvalSweepInterval = 15 * time.Second

// storeCheckTimeout bounds the store check a metrics collection triggers.
const storeCheckTimeout = 2 * time.Second

// recordApprovalEvent emits the metric of an audit event.
func (a *AggregatorServer) recordApprovalEvent(ctx context.Context, action string, target api.ApprovalTarget, reason string, effective api.ToolApproval) {
	m := approval.DefaultMetrics()
	kind := api.CallerKind(ctx, getSessionIDFromContext(ctx))
	switch action {
	case "approval_requested":
		m.Requested(ctx, target, kind, effective.Source)
	case "approval_used":
		m.Decided(ctx, approval.OutcomeUsed)
	case "approval_refused":
		m.Refused(ctx, approvalRefusalReason(reason), kind)
	case "call_refused":
		if strings.HasPrefix(reason, "blocked by") {
			m.Refused(ctx, approval.ReasonBlocked, kind)
		} else {
			m.Refused(ctx, approval.ReasonSettingsUnavailable, kind)
		}
	}
}

// approvalRefusalReason classifies the reason text of an approval_refused
// event.
func approvalRefusalReason(reason string) string {
	switch reason {
	case "too many pending approvals":
		return approval.ReasonCapReached
	case "store unavailable":
		return approval.ReasonStoreUnavailable
	}
	return approval.ReasonUnavailable
}

// runApprovalMetrics keeps the pending gauge and the expired count right and
// wires the store gauge to the aggregator's cached store check.
func (a *AggregatorServer) runApprovalMetrics() {
	defer a.wg.Done()
	approval.SetStoreProbe(func(ctx context.Context) (bool, bool) {
		if api.GetApprovalStore() == nil {
			return false, false
		}
		ctx, cancel := context.WithTimeout(ctx, storeCheckTimeout)
		defer cancel()
		return a.approvalStoreReachable(ctx), true
	})
	defer approval.SetStoreProbe(nil)
	ticker := time.NewTicker(approvalSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.sweepApprovals(a.ctx)
		}
	}
}

// sweepApprovals reconciles this replica's pending requests with the store.
func (a *AggregatorServer) sweepApprovals(ctx context.Context) {
	m := approval.DefaultMetrics()
	m.Pending().Sweep(ctx, api.GetApprovalStore(), clock.Now(), m)
}
