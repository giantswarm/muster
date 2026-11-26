package approval

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// DecisionSuffix is the path, under an approval's page, where a trusted
// client posts the person's decision.
const DecisionSuffix = "/decision"

// The paths a decision is taken on, the values of the path attribute of the
// decision metrics and of the approval_decision audit event.
const (
	// PathPage is a decision on muster's approval page.
	PathPage = "page"
	// PathAPI is a decision a trusted client posted with the person's bearer.
	PathAPI = "api"
)

// maxDecisionBody caps a decision posted by a trusted client.
const maxDecisionBody = 4 << 10

// Error codes of the decision API, the "error" field of a refusal.
// (untrusted_client, untrusted_workload, acting_party, foreign and
// unavailable are shared with the handoff endpoint: trusted_caller.go.)
const (
	apiErrNotFound   = "not_found"
	apiErrDecided    = "decided"
	apiErrExpired    = "expired"
	apiErrChanged    = "changed"
	apiErrBadRequest = "bad_request"
)

// DecisionAPI lets a trusted client (approval.trustedClients: the portal's
// own OAuth client) show a person's pending approval and decide it with the
// person's own bearer, inline in its UI, instead of opening muster's page:
//
//	GET  /approvals/{id}           (with Authorization: Bearer) -> what the page shows, and its digest
//	POST /approvals/{id}/decision  {"decision": "approve"|"decline", "digest": "<from the GET>"}
//
// Both sit behind the OAuth server's subject check, which validates the
// bearer and refuses a token naming an acting party (act). DecisionAPI then
// refuses any agent's credential (actor JWT, delegated token, an actor
// Session), any bearer whose OAuth client is not trusted, any request without
// the trusted client's own workload token (ClientWorkloadHeader, reviewed by
// Workload), and any person but the request's. The person's bearer proves who
// decides; the workload token proves the portal backend presents it, so a
// person's portal token held elsewhere (the browser, a forwardToken server,
// an agent runtime handed the caller's credential) decides nothing. A decision is taken once, on a pending, unexpired request
// whose digest is the one the client showed; it is audited and counted like
// a page decision, with path=api and the client.
//
// Security: the trusted client's backend (holding both the person's bearer
// and its own workload token) can decide without muster's browser sign-in, so
// it must decide only on the person's click; a script injected into its UI
// (XSS) can ask it to. This is the same exposure as the sign-in handoff
// (Handoffs), which the same backend can turn into a signed-in approval page.
type DecisionAPI struct {
	// Clients are the trusted OAuth client ids (azp, else the single
	// audience). Empty: every request is refused.
	Clients []string
	// Installation names the installation, as on the page.
	Installation string
	// Store returns the approval store, nil when approval is unavailable.
	Store func() api.ApprovalStore
	// ClientOf names the OAuth client the request's bearer was issued to.
	ClientOf func(context.Context) string
	// AgentCredential returns why the request's credential is an agent's
	// (actor JWT, delegated token, an actor Session, the actor issuer), ""
	// for a person's own bearer.
	AgentCredential func(context.Context) string
	// Workload reviews the trusted client's own workload token
	// (ClientWorkloadHeader); nil: every request is refused.
	Workload WorkloadReviewer
	// Now is the clock; nil: muster's clock.
	Now func() time.Time
	// OnDecided is called once for the request a decision decided, as for
	// the page (elicitation/complete to the asked session).
	OnDecided func(*api.ApprovalRequest)
}

type decisionBody struct {
	Decision string `json:"decision"`
	Digest   string `json:"digest"`
}

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	// Status is what stands when the request was already answered.
	Status string `json:"status,omitempty"`
}

func (d *DecisionAPI) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return clock.Now()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// caller checks the bearer and the client's workload (callerCheck). It
// answers the refusal itself and returns ok false.
func (d *DecisionAPI) caller(r *http.Request, refuse refuseFunc) (trustedCaller, bool) {
	caller, refusal := callerCheck{
		clients:         d.Clients,
		clientOf:        d.ClientOf,
		agentCredential: d.AgentCredential,
		workload:        d.Workload,
	}.check(r)
	if refusal != nil {
		refuse.with(refusal.subject, refusal.client)(refusal.status, refusal.code, refusal.reason, refusal.message)
		return trustedCaller{}, false
	}
	return caller, true
}

// load reads the request and checks it is the caller's.
func (d *DecisionAPI) load(r *http.Request, identity api.SignInIdentity, refuse func(int, string, string, string)) (*api.ApprovalRequest, api.ApprovalStore, bool) {
	store := d.Store()
	if store == nil {
		refuse(http.StatusServiceUnavailable, apiErrUnavailable, "no approval store", "Approval is unavailable: muster's shared store is not set up.")
		return nil, nil, false
	}
	request, err := store.Get(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, api.ErrApprovalNotFound):
		refuse(http.StatusNotFound, apiErrNotFound, "unknown, expired or used", "Nothing to decide: this request expired or was already used. If the tool still needs to run, run it again; it will ask anew.")
		return nil, nil, false
	case err != nil:
		logging.Warn("Approval", "Approval store unavailable for the decision API: %v", err)
		refuse(http.StatusServiceUnavailable, apiErrUnavailable, "store unavailable", "Approval is unavailable right now. Try again in a moment.")
		return nil, nil, false
	case request.Identity != identity:
		refuse(http.StatusForbidden, apiErrForeign, "another person's request", "This request was made for someone else; only they can decide it.")
		return nil, nil, false
	}
	return request, store, true
}

// refuseFunc answers a refusal; with names the subject and client the audit
// event carries.
type refuseFunc struct {
	w    http.ResponseWriter
	r    *http.Request
	kind string
}

func (d *DecisionAPI) refuser(w http.ResponseWriter, r *http.Request, kind string) refuseFunc {
	return refuseFunc{w: w, r: r, kind: kind}
}

// with returns the refusal answer for a caller of subject through client.
func (f refuseFunc) with(subject, client string) func(int, string, string, string) {
	return func(status int, code, reason, message string) {
		ctx := f.r.Context()
		metricReason := refusalReason(code)
		DefaultMetrics().DecisionRefused(ctx, metricReason, "", PathAPI)
		logging.WarnWithAttrs("Approval", "decision API refused: nothing was decided",
			slog.String("approval", logging.TruncateIdentifier(f.r.PathValue("id"))),
			slog.String("request", f.kind), slog.String("reason", reason))
		if audited(code) {
			auditRefusal(ctx, f.r.PathValue("id"), f.kind, metricReason, reason, subject, client)
		}
		writeJSON(f.w, status, apiError{Error: code, Message: message})
	}
}

// audited says which refusals are written to the audit log: who tried to
// decide what they may not (an agent, a foreign client or workload, another
// person) and a decision on something else than was shown. Store outages,
// expiry and malformed bodies are counted and logged, not audited.
func audited(code string) bool {
	switch code {
	case apiErrActingParty, apiErrClient, apiErrWorkload, apiErrForeign, apiErrChanged:
		return true
	}
	return false
}

// auditRefusal records a refused view or decision as an approval_decision
// event with outcome refused: the reason, the caller's subject (truncated),
// its OAuth client and the path.
func auditRefusal(ctx context.Context, id, kind, reason, detail, subject, client string) {
	api.AuditApproval(ctx, "approval_decision",
		slog.String("outcome", "refused"),
		slog.String("approval", logging.TruncateIdentifier(id)),
		slog.String("request", kind),
		slog.String("reason", reason),
		slog.String("detail", detail),
		slog.String("callerSubject", logging.TruncateIdentifier(subject)),
		slog.String("path", PathAPI),
		slog.String("client", client))
}

// refusalReason maps an API error code onto the refusal metric's reasons.
func refusalReason(code string) string {
	switch code {
	case apiErrForeign:
		return ReasonForeign
	case apiErrClient:
		return ReasonUntrustedClient
	case apiErrWorkload:
		return ReasonUntrustedWorkload
	case apiErrActingParty:
		return ReasonActingParty
	case apiErrChanged:
		return ReasonChanged
	case apiErrUnavailable:
		return ReasonStoreUnavailable
	case apiErrNotFound:
		return ReasonNotFound
	case apiErrExpired:
		return ReasonExpired
	case apiErrBadRequest:
		return ReasonBadRequest
	}
	return ReasonNotPending
}

// ServeView answers GET /approvals/{id} for a trusted client: what the page
// shows, made safe the same way, and the digest a decision must carry.
func (d *DecisionAPI) ServeView(w http.ResponseWriter, r *http.Request) {
	refuser := d.refuser(w, r, "view")
	caller, ok := d.caller(r, refuser)
	if !ok {
		return
	}
	request, _, ok := d.load(r, caller.identity, refuser.with(caller.identity.Subject, caller.client))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, d.view(request))
}

// ServeDecision answers POST /approvals/{id}/decision.
func (d *DecisionAPI) ServeDecision(w http.ResponseWriter, r *http.Request) {
	refuser := d.refuser(w, r, "decision")
	caller, ok := d.caller(r, refuser)
	if !ok {
		return
	}
	identity, client := caller.identity, caller.client
	refuse := refuser.with(identity.Subject, client)
	var body decisionBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDecisionBody)).Decode(&body); err != nil {
		refuse(http.StatusBadRequest, apiErrBadRequest, "body unreadable", `The body must be {"decision": "approve"|"decline", "digest": "<digest>"}.`)
		return
	}
	var approve bool
	switch body.Decision {
	case "approve":
		approve = true
	case "decline":
	default:
		refuse(http.StatusBadRequest, apiErrBadRequest, "decision neither approve nor decline", `decision must be "approve" or "decline".`)
		return
	}
	request, store, ok := d.load(r, identity, refuse)
	if !ok {
		return
	}
	if request.Status != api.ApprovalStatusPending {
		answered(w, r, request)
		return
	}
	if !d.now().Before(request.ExpiresAt) {
		refuse(http.StatusGone, apiErrExpired, "expired", "Nothing was decided: this request expired. If the tool still needs to run, run it again; it will ask anew.")
		return
	}
	if body.Digest == "" || body.Digest != DisplayDigest(request, d.Installation) {
		refuse(http.StatusConflict, apiErrChanged, "digest does not match", "Nothing was decided: the request is not the one shown. Look at it again before deciding.")
		return
	}
	if err := store.Decide(r.Context(), request.ID, approve, identity); err != nil {
		if errors.Is(err, api.ErrApprovalDecided) {
			if current, getErr := store.Get(r.Context(), request.ID); getErr == nil {
				answered(w, r, current)
				return
			}
		}
		if errors.Is(err, api.ErrApprovalNotFound) {
			refuse(http.StatusNotFound, apiErrNotFound, "gone meanwhile", "Nothing to decide: this request expired or was already used.")
			return
		}
		logging.Warn("Approval", "Approval store unavailable for the decision API: %v", err)
		refuse(http.StatusServiceUnavailable, apiErrUnavailable, "store unavailable", "Nothing was decided: approval is unavailable right now. Try again in a moment.")
		return
	}
	auditDecision(r.Context(), request, approve, identity, PathAPI, client)
	recordDecision(r.Context(), request, approve, d.now(), PathAPI)
	if d.OnDecided != nil {
		d.OnDecided(request)
	}
	status := api.ApprovalStatusDeclined
	if approve {
		status = api.ApprovalStatusApproved
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// answered tells the client the request was answered already, and how.
func answered(w http.ResponseWriter, r *http.Request, request *api.ApprovalRequest) {
	DefaultMetrics().DecisionRefused(r.Context(), ReasonNotPending, request.CallerKind, PathAPI)
	writeJSON(w, http.StatusConflict, apiError{Error: apiErrDecided, Status: request.Status,
		Message: "This request was already " + request.Status + "; this answer changes nothing."})
}

// HasBearer reports whether a request carries an Authorization bearer: the
// approval page's GET hands such requests to the decision API and never
// reads the bearer itself.
func HasBearer(r *http.Request) bool {
	scheme, _, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return ok && strings.EqualFold(scheme, "Bearer")
}
