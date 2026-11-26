package approval

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// ClientWorkloadHeader carries the trusted client's own workload identity
// beside the person's bearer: the portal backend's projected ServiceAccount
// token, audience muster. The decision API and the handoff endpoint take a
// person's bearer of a trusted client only together with it, so a person's
// portal token that reached the browser, a forwardToken server or an agent
// runtime decides nothing and mints no ticket on its own.
const ClientWorkloadHeader = "X-Muster-Approval-Client" //nolint:gosec // G101: a header name, not a credential

// ErrWorkloadNotAllowed is a workload token that does not pass the review: not
// a token for audience muster, or of an identity Kubernetes does not allow to
// decide toolapprovals in muster's namespace.
var ErrWorkloadNotAllowed = errors.New("the client's workload may not decide approvals")

// WorkloadReviewer reviews a trusted client's workload token (TokenReview,
// audience muster, then a LocalSubjectAccessReview for verb "decide" on
// toolapprovals.muster.giantswarm.io in muster's namespace). It returns the
// workload's Kubernetes username, ErrWorkloadNotAllowed when the token does not
// pass, and another error when the review could not be made.
type WorkloadReviewer func(ctx context.Context, token string) (string, error)

// Error codes of a refused trusted caller, shared by the decision API (the
// "error" field) and the handoff endpoint.
const (
	apiErrClient      = "untrusted_client"
	apiErrWorkload    = "untrusted_workload"
	apiErrActingParty = "acting_party"
	apiErrForeign     = "foreign"
	apiErrUnavailable = "unavailable"
)

// trustedCaller is who a request of a trusted client acts for, and through
// which client and workload.
type trustedCaller struct {
	identity api.SignInIdentity
	client   string
	workload string
}

// callerRefusal is why a request is not a trusted client's.
type callerRefusal struct {
	status  int
	code    string
	reason  string
	message string
	// subject and client, as far as they are known, for the audit event.
	subject string
	client  string
}

// callerCheck is what both endpoints of a trusted client check, in order:
// the credential is no agent's, its OAuth client is trusted, it names a
// person, and the client's own workload passes the review.
type callerCheck struct {
	clients         []string
	clientOf        func(context.Context) string
	agentCredential func(context.Context) string
	workload        WorkloadReviewer
}

func (c callerCheck) check(r *http.Request) (trustedCaller, *callerRefusal) {
	ctx := r.Context()
	identity := api.SignInIdentityFromContext(ctx)
	client := ""
	if c.clientOf != nil {
		client = c.clientOf(ctx)
	}
	refusal := func(status int, code, reason, message string) *callerRefusal {
		return &callerRefusal{status: status, code: code, reason: reason, message: message, subject: identity.Subject, client: client}
	}
	if c.agentCredential != nil {
		if why := c.agentCredential(ctx); why != "" {
			return trustedCaller{}, refusal(http.StatusForbidden, apiErrActingParty, why,
				"Only the person can decide an approval: a token of an agent or naming an acting party cannot.")
		}
	}
	if client == "" || !slices.Contains(c.clients, client) {
		return trustedCaller{}, refusal(http.StatusForbidden, apiErrClient, "client not on approval.trustedClients",
			"This client may not decide approvals: muster takes decisions only on its approval page and from the clients on approval.trustedClients.")
	}
	if identity.Subject == "" {
		return trustedCaller{}, refusal(http.StatusForbidden, apiErrForeign, "no person on the bearer",
			"muster cannot tell which person this request is for.")
	}
	token := strings.TrimSpace(r.Header.Get(ClientWorkloadHeader))
	switch {
	case token == "":
		return trustedCaller{}, refusal(http.StatusForbidden, apiErrWorkload, "no workload token of the client",
			"This client did not prove its own identity: muster takes a person's approval from a trusted client only with the client's own ServiceAccount token ("+ClientWorkloadHeader+").")
	case c.workload == nil:
		return trustedCaller{}, refusal(http.StatusForbidden, apiErrWorkload, "muster cannot review workload tokens (no Kubernetes API)",
			"muster cannot verify this client's identity here, so it takes no decision from it. Use muster's approval page.")
	}
	workload, err := c.workload(ctx, token)
	switch {
	case errors.Is(err, ErrWorkloadNotAllowed):
		return trustedCaller{}, refusal(http.StatusForbidden, apiErrWorkload, err.Error(),
			"This client's own identity may not decide approvals.")
	case err != nil:
		logging.Warn("Approval", "Cannot review a trusted client's workload token: %v", err)
		return trustedCaller{}, refusal(http.StatusServiceUnavailable, apiErrUnavailable, "workload review failed",
			"muster cannot verify this client's identity right now. Try again in a moment.")
	}
	return trustedCaller{identity: identity, client: client, workload: workload}, nil
}
