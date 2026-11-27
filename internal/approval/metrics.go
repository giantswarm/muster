package approval

import (
	"context"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
	"github.com/giantswarm/muster/v5/pkg/observability"
)

// Attribute keys of the approval metrics. The values are bounded: servers are
// MCPServer objects, the rest are the fixed sets below. Never a person, a
// session, an approval id, a tool name or an argument.
const (
	attrServer     = observability.AttrMCPServerName
	attrCallerKind = "caller_kind"
	attrSource     = "source"
	attrOutcome    = "outcome"
	attrReason     = "reason"
	attrScope      = "scope"
	attrPage       = "page"
	attrPath       = "path"
)

// Outcomes of a sign-in handoff, the values of the outcome attribute of
// muster_approval_handoffs_total.
const (
	HandoffIssued   = "issued"
	HandoffRedeemed = "redeemed"
	HandoffRefused  = "refused"
)

// Reasons a handoff is refused, the values of the reason attribute of
// muster_approval_handoffs_total (none when issued or redeemed).
const (
	// HandoffReasonNone is an issued or redeemed handoff.
	HandoffReasonNone = "none"
	// HandoffReasonClient is a request from an OAuth client that is not on
	// approval.trustedClients (or a bearer naming no client).
	HandoffReasonClient = "client"
	// HandoffReasonTarget is a link that is no page muster hands off to, or
	// a ticket redeemed on another page than it is bound to.
	HandoffReasonTarget = "target"
	// HandoffReasonForeign is a request or ticket for another person than the
	// page is bound to (or an unknown request).
	HandoffReasonForeign = "foreign"
	// HandoffReasonInvalid is a ticket muster does not hold: unknown,
	// expired or used.
	HandoffReasonInvalid = "invalid"
	// HandoffReasonWorkload is a ticket asked for without the client's own
	// workload token, or with one that does not pass the review.
	HandoffReasonWorkload = "workload"
	// HandoffReasonActingParty is a ticket asked for with an agent's
	// credential.
	HandoffReasonActingParty = "acting_party"
	// HandoffReasonUnavailable is a handoff while the store is missing or
	// does not answer.
	HandoffReasonUnavailable = "unavailable"
)

// Outcomes of an approval, the values of the outcome attribute of
// muster_approval_decisions_total.
const (
	OutcomeApproved = "approved"
	OutcomeDeclined = "declined"
	OutcomeExpired  = "expired"
	OutcomeUsed     = "used"
)

// Reasons a call that asks or its decision is refused, the values of the
// reason attribute of muster_approval_refusals_total.
const (
	// ReasonBlocked is a call refused because its effective approval is Block.
	ReasonBlocked = "blocked"
	// ReasonSettingsUnavailable is a call refused because its approval
	// settings could not be read or resolved (see also
	// muster_approval_settings_refusals_total, which says why).
	ReasonSettingsUnavailable = "settings_unavailable"
	// ReasonUnavailable is a call that asks while muster cannot ask: no
	// Valkey store, no browser sign-in, no public URL, no identified person.
	ReasonUnavailable = "unavailable"
	// ReasonStoreUnavailable is a call that asks while the approval store
	// does not answer.
	ReasonStoreUnavailable = "store_unavailable"
	// ReasonCapReached is a call refused by a cap on pending approvals.
	ReasonCapReached = "cap_reached"
	// ReasonForeign is a decision attempted by a person other than the one
	// the request was made for.
	ReasonForeign = "foreign"
	// ReasonPageCheck is a decision the approval page refused on a check
	// (origin, cookie, anti-forgery token, expired browser flow).
	ReasonPageCheck = "page_check"
	// ReasonUntrustedClient is a decision posted by a bearer of an OAuth
	// client not on approval.trustedClients.
	ReasonUntrustedClient = "untrusted_client"
	// ReasonActingParty is a decision posted with an agent's credential
	// (actor JWT, delegated token, a token naming an acting party).
	ReasonActingParty = "acting_party"
	// ReasonChanged is a decision whose digest is not the request's: the
	// client showed something else.
	ReasonChanged = "changed"
	// ReasonUntrustedWorkload is a decision posted by a trusted client's
	// bearer without the client's own workload token, or with one that does
	// not pass the review (decide toolapprovals).
	ReasonUntrustedWorkload = "untrusted_workload"
	// ReasonNotPending is a decision on a request that is already decided.
	ReasonNotPending = "not_pending"
	// ReasonNotFound is a decision or view of a request that is unknown,
	// expired from the store or used.
	ReasonNotFound = "not_found"
	// ReasonExpired is a decision on a request past its expiry while still
	// in the store.
	ReasonExpired = "expired"
	// ReasonBadRequest is a decision whose body is unreadable or names
	// neither approve nor decline.
	ReasonBadRequest = "bad_request"
)

// Metrics holds the instruments of approval:
//
//	muster_approval_requests_total{mcpserver_name,caller_kind,source}
//	muster_approval_decisions_total{outcome[,path]}
//	muster_approval_refusals_total{reason,caller_kind[,path]}
//	muster_approval_cap_hits_total{scope,caller_kind}
//	muster_approval_time_to_decision_seconds{outcome,path}
//	muster_approval_handoffs_total{outcome,page,reason}
//	muster_approval_completions_total{outcome}
//	muster_approval_pending
//	muster_approval_store_up
type Metrics struct {
	requests       metric.Int64Counter
	decisions      metric.Int64Counter
	refusals       metric.Int64Counter
	capHits        metric.Int64Counter
	timeToDecision metric.Float64Histogram
	handoffs       metric.Int64Counter
	completions    metric.Int64Counter
	pending        *Pending
}

// NewMetrics creates the approval instruments on mp. The pending gauge reads
// the Pending tracker; storeUp, when not nil, is read for the store gauge on
// every collection and returns whether the store answered, and whether a
// store is registered at all.
func NewMetrics(mp metric.MeterProvider, storeUp func(context.Context) (up, registered bool)) *Metrics {
	meter := mp.Meter(observability.TracerName)
	m := &Metrics{pending: NewPending()}
	counter := func(name, desc, unit string) metric.Int64Counter {
		c, cerr := meter.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if cerr != nil {
			logging.Warn("Approval", "create %s counter: %v", name, cerr)
		}
		return c
	}
	m.requests = counter("muster.approval.requests", "Calls answered with an approval request, by the server called, the caller kind and what made the call ask (source: connector, agent, governance, builtin, ...).", "{request}")
	m.decisions = counter("muster.approval.decisions", "Approval outcomes: approved or declined (path page: on the approval page, api: by a trusted client), used (an approved call ran once), expired (a request nobody decided or used in time).", "{decision}")
	m.refusals = counter("muster.approval.refusals", "Calls and decisions refused by the approval gate, by reason (blocked, settings_unavailable, unavailable, store_unavailable, cap_reached, foreign, page_check, untrusted_client, acting_party, changed, not_pending); decisions refused carry path (page, api).", "{refusal}")
	m.capHits = counter("muster.approval.cap_hits", "Calls refused by a cap on pending approvals: scope person (per person, across clients) or session (per client).", "{refusal}")
	m.handoffs = counter("muster.approval.handoffs", "Sign-in handoffs: tickets issued to a trusted client, redeemed on muster's page, or refused (by reason: client, target, foreign, invalid, unavailable), by page (approval, sign_in).", "{handoff}")
	m.completions = counter("muster.approval.completions", "Completion notices (elicitation/complete) shared between replicas, by outcome: published, publish_failed (the asked session may then not be told when it is on another replica), received (from another replica or this one), subscription_lost.", "{notice}")
	var herr error
	if m.timeToDecision, herr = meter.Float64Histogram("muster.approval.time_to_decision",
		metric.WithDescription("Seconds from a request being made to its decision on the approval page."), metric.WithUnit("s")); herr != nil {
		logging.Warn("Approval", "create muster.approval.time_to_decision histogram: %v", herr)
	}
	if _, gerr := meter.Int64ObservableGauge("muster.approval.pending",
		metric.WithDescription("Approval requests this replica made that nobody has decided, used or let expire yet (refreshed every few seconds)."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(m.pending.Len()))
			return nil
		})); gerr != nil {
		logging.Warn("Approval", "create muster.approval.pending gauge: %v", gerr)
	}
	if storeUp != nil {
		if _, gerr := meter.Int64ObservableGauge("muster.approval.store_up",
			metric.WithDescription("1 when muster's approval store (Valkey) answered its last check, 0 when it did not; absent while no store is registered."),
			metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
				if up, registered := storeUp(ctx); registered {
					o.Observe(boolGauge(up))
				}
				return nil
			})); gerr != nil {
			logging.Warn("Approval", "create muster.approval.store_up gauge: %v", gerr)
		}
	}
	return m
}

func boolGauge(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Pending returns the tracker the pending gauge reads.
func (m *Metrics) Pending() *Pending { return m.pending }

// ServerLabel is the server attribute of a target: the MCPServer, else the
// kind of target (core, workflow).
func ServerLabel(target api.ApprovalTarget) string {
	if target.Server != "" {
		return target.Server
	}
	return target.Kind
}

// CallerKindLabel shortens api.CallerKind to person, actor or delegated.
func CallerKindLabel(kind string) string {
	switch kind {
	case api.CallerKindPerson:
		return "person"
	case api.CallerKindActor:
		return "actor"
	case api.CallerKindDelegated:
		return "delegated"
	}
	return "other"
}

// sourceLabel keeps the source attribute to a fixed set.
func sourceLabel(source string) string {
	switch source {
	case api.ApprovalSourceTool, api.ApprovalSourceConnector, api.ApprovalSourceUnreviewed, api.ApprovalSourceBuiltin,
		api.ApprovalSourceUnavailable, api.ApprovalSourceAgent, api.ApprovalSourceAgentException, api.ApprovalSourceGovernance:
		return source
	}
	return "other"
}

// Requested counts a call answered with an approval request.
func (m *Metrics) Requested(ctx context.Context, target api.ApprovalTarget, callerKind, source string) {
	m.requests.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrServer, ServerLabel(target)),
		attribute.String(attrCallerKind, CallerKindLabel(callerKind)),
		attribute.String(attrSource, sourceLabel(source))))
}

// Decided counts an outcome (OutcomeApproved, OutcomeDeclined, OutcomeUsed,
// OutcomeExpired).
func (m *Metrics) Decided(ctx context.Context, outcome string) {
	m.decisions.Add(ctx, 1, metric.WithAttributes(attribute.String(attrOutcome, outcome)))
}

// DecidedOn counts a decision (OutcomeApproved, OutcomeDeclined) by the path
// it was taken on (PathPage, PathAPI).
func (m *Metrics) DecidedOn(ctx context.Context, outcome, path string) {
	m.decisions.Add(ctx, 1, metric.WithAttributes(attribute.String(attrOutcome, outcome), attribute.String(attrPath, path)))
}

// TimeToDecision records how long a request waited for its decision, by the
// path it was taken on.
func (m *Metrics) TimeToDecision(ctx context.Context, outcome, path string, seconds float64) {
	if m.timeToDecision != nil {
		m.timeToDecision.Record(ctx, max(seconds, 0), metric.WithAttributes(attribute.String(attrOutcome, outcome), attribute.String(attrPath, path)))
	}
}

// DecisionRefused counts a refused decision by reason and the path it was
// posted on (PathPage, PathAPI).
func (m *Metrics) DecisionRefused(ctx context.Context, reason, callerKind, path string) {
	m.refusals.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrReason, reason),
		attribute.String(attrCallerKind, CallerKindLabel(callerKind)),
		attribute.String(attrPath, path)))
}

// Refused counts a refusal by reason (the Reason constants).
func (m *Metrics) Refused(ctx context.Context, reason, callerKind string) {
	m.refusals.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrReason, reason),
		attribute.String(attrCallerKind, CallerKindLabel(callerKind))))
}

// Handoff counts a sign-in handoff by outcome (HandoffIssued,
// HandoffRedeemed, HandoffRefused), page (api.HandoffKindApproval,
// api.HandoffKindSignIn, else other) and reason (the HandoffReason
// constants).
func (m *Metrics) Handoff(ctx context.Context, outcome, page, reason string) {
	if m.handoffs == nil {
		return
	}
	if page != api.HandoffKindApproval && page != api.HandoffKindSignIn {
		page = "other"
	}
	m.handoffs.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrOutcome, outcome),
		attribute.String(attrPage, page),
		attribute.String(attrReason, reason)))
}

// Outcomes of a completion notice, the values of the outcome attribute of
// muster_approval_completions_total.
const (
	CompletionPublished        = "published"
	CompletionPublishFailed    = "publish_failed"
	CompletionReceived         = "received"
	CompletionSubscriptionLost = "subscription_lost"
)

// Completion counts a completion notice by outcome (the Completion*
// constants).
func (m *Metrics) Completion(ctx context.Context, outcome string) {
	if m.completions == nil {
		return
	}
	m.completions.Add(ctx, 1, metric.WithAttributes(attribute.String(attrOutcome, outcome)))
}

// CapHit counts a call refused by a cap; scope is api.ApprovalLimitPerson or
// api.ApprovalLimitSession.
func (m *Metrics) CapHit(ctx context.Context, scope, callerKind string) {
	m.capHits.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrScope, scope),
		attribute.String(attrCallerKind, CallerKindLabel(callerKind))))
}

var (
	defaultMetrics atomic.Pointer[Metrics]
	defaultOnce    sync.Once
	storeProbe     atomic.Pointer[func(context.Context) (bool, bool)]
)

// SetStoreProbe sets what the store gauge reads; muster's aggregator sets it
// to a cached ping of the registered store.
func SetStoreProbe(probe func(context.Context) (up, registered bool)) {
	storeProbe.Store(&probe)
}

// DefaultMetrics returns the process-wide approval metrics on the global
// meter provider, created on first use. Instruments created before the
// provider is installed bind to it when it is.
func DefaultMetrics() *Metrics {
	defaultOnce.Do(func() {
		if defaultMetrics.Load() == nil {
			defaultMetrics.Store(NewMetrics(otel.GetMeterProvider(), func(ctx context.Context) (bool, bool) {
				if p := storeProbe.Load(); p != nil && *p != nil {
					return (*p)(ctx)
				}
				return false, false
			}))
		}
	})
	return defaultMetrics.Load()
}

// SetDefaultMetrics replaces the process-wide metrics, for tests.
func SetDefaultMetrics(m *Metrics) {
	defaultOnce.Do(func() {})
	defaultMetrics.Store(m)
}
