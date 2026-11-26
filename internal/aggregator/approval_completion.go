package aggregator

import (
	"context"
	"time"

	"github.com/giantswarm/muster/v5/internal/approval"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// Completion notices (notifications/elicitation/complete) go to the transport
// session that was asked, and only the replica that answered its call holds
// that session. A decision on the approval page, or a sign-in's callback, may
// be served by another replica, so the replica that serves it publishes a
// minimal notice (approval.Completion: what ended, never a decision or a
// person) on the shared store, and every replica delivers it to the sessions
// it holds. A replica that holds none ignores it; one that gets it twice finds
// nothing the second time, and the client treats the notification as
// idempotent anyway.

const (
	// completionPublishTimeout bounds a publish: the decider must not wait on
	// an unavailable store.
	completionPublishTimeout = 2 * time.Second
	// completionResubscribeMin and Max bound the wait before the subscription
	// is taken up again after it was lost.
	completionResubscribeMin = time.Second
	completionResubscribeMax = 30 * time.Second
)

// newCompletions returns the completion bus of the replica: shared through
// Valkey when the stores are on it, in-process otherwise.
func newCompletions(stores storeBundle) approval.Completions {
	if stores.valkeyClient != nil {
		return approval.NewValkeyCompletions(stores.valkeyClient, stores.keyPrefix)
	}
	return approval.NewMemoryCompletions()
}

// publishCompletion shares the notice with the other replicas. It never
// fails the caller: an unavailable store is logged and counted, and the
// asked session then hears nothing if another replica holds it (its client
// retries on its own; the portal has Done).
func (a *AggregatorServer) publishCompletion(c approval.Completion) {
	if a.completions == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.refreshContext()), completionPublishTimeout)
	defer cancel()
	m := approval.DefaultMetrics()
	if err := a.completions.Publish(ctx, c); err != nil {
		m.Completion(ctx, approval.CompletionPublishFailed)
		logging.Warn("Aggregator", "Sharing an elicitation completion with the other replicas failed: %v", err)
		return
	}
	m.Completion(ctx, approval.CompletionPublished)
}

// receiveCompletion delivers a notice to the sessions this replica holds.
func (a *AggregatorServer) receiveCompletion(c approval.Completion) {
	approval.DefaultMetrics().Completion(a.refreshContext(), approval.CompletionReceived)
	switch c.Kind {
	case approval.CompletionApproval:
		a.deliverApprovalCompletion(c.Key)
	case approval.CompletionSignIn:
		if a.mcpServer == nil {
			return
		}
		for _, sent := range a.signInElicitations.takeKey(c.Key) {
			a.sendElicitationComplete(sent, "sign-in")
		}
	}
}

// runCompletions subscribes to the completion bus until the aggregator
// stops, taking the subscription up again, with a growing wait, when it is
// lost. Notices published meanwhile are missed, which only costs the asked
// client its notification.
func (a *AggregatorServer) runCompletions() {
	defer a.wg.Done()
	if a.completions == nil {
		return
	}
	wait := completionResubscribeMin
	for {
		started := time.Now()
		err := a.completions.Subscribe(a.ctx, a.receiveCompletion)
		if a.ctx.Err() != nil {
			return
		}
		approval.DefaultMetrics().Completion(a.ctx, approval.CompletionSubscriptionLost)
		logging.Warn("Aggregator", "Subscription to elicitation completions lost: %v", err)
		if time.Since(started) > completionResubscribeMax {
			wait = completionResubscribeMin
		}
		timer := time.NewTimer(wait)
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		wait = min(wait*2, completionResubscribeMax)
	}
}
