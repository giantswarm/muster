package credprovider

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// GrantsPath is the HTTP path that removes grant marks from the bindings of
// an agent's running Sessions.
const GrantsPath = BindingsPath + "/grants"

// maxDropGrantsBytes bounds a grant drop: up to MaxAgentApprovalGrants
// marks to keep, each naming its tools.
const maxDropGrantsBytes = 256 << 10

// DropGrantsRequest is the body of PATCH /session-bindings/grants.
type DropGrantsRequest struct {
	// Agent is the agent, <namespace>/<name>, whose bindings are changed.
	Agent string `json:"agent"`
	// Keep lists the grant marks that stay; every other mark is removed.
	Keep []api.AgentApprovalGrant `json:"keep"`
}

// DropGrantsResponse answers a grant drop.
type DropGrantsResponse struct {
	// Bindings counts the agent's live bindings.
	Bindings int `json:"bindings"`
	// Dropped counts the grant marks removed.
	Dropped int `json:"dropped"`
}

// HandleDropGrants is PATCH /session-bindings/grants: removes from every live
// binding of the agent each grant mark not kept, changing nothing else
// (person, Session, providers, toolset, settings, expiry). Unlike a bind it
// may change another person's binding, so it is authorized by the approval
// sender alone (agent-manager's ServiceAccount token, reviewed like a
// bind's): without one, or when the review refuses, it answers 403; when the
// review fails, 503. No person's bearer is needed, so agent-manager can
// re-send a drop that did not reach muster at its start; the
// route takes no bearer, and the sender's identity is what is logged. It only
// ever removes marks, so a Session loses an exception at its next call and
// gains nothing.
func (p *Provider) HandleDropGrants(w http.ResponseWriter, r *http.Request) {
	// The sender is checked before the body is read: an unauthenticated
	// caller costs one header check, and the 256 KiB body is read only for
	// a sender that passed the review.
	if strings.TrimSpace(r.Header.Get(ApprovalSenderHeader)) == "" {
		http.Error(w, "forbidden: removing grant marks needs the sender of approval settings ("+ApprovalSenderHeader+")", http.StatusForbidden)
		return
	}
	sender, refused := p.reviewSender(r)
	if refused != 0 || sender == "" {
		status := refused
		if status == 0 {
			status = http.StatusForbidden
		}
		http.Error(w, http.StatusText(status)+": the sender of approval settings did not pass the review", status)
		return
	}
	var req DropGrantsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDropGrantsBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !agentPattern.MatchString(req.Agent) {
		http.Error(w, "bad request: agent must be <namespace>/<name>", http.StatusBadRequest)
		return
	}
	if len(req.Keep) > api.MaxAgentApprovalGrants {
		http.Error(w, "bad request: keep lists too many grants", http.StatusBadRequest)
		return
	}

	keep := make(map[string]bool, len(req.Keep))
	for _, g := range req.Keep {
		keep[g.Key()] = true
	}
	keeps := func(g api.AgentApprovalGrant) bool { return keep[g.Key()] }

	actors, err := p.store.ActorsOfAgent(r.Context(), req.Agent)
	if err != nil {
		logging.WarnWithAttrs(logSubsystem, "grant_drop_failed", slog.String("agent", req.Agent), slog.String("error", err.Error()))
		http.Error(w, "binding store unavailable", http.StatusServiceUnavailable)
		return
	}
	out := DropGrantsResponse{Bindings: len(actors)}
	for _, actor := range actors {
		dropped, err := p.store.DropGrants(r.Context(), actor, req.Agent, keeps)
		if err != nil {
			logging.WarnWithAttrs(logSubsystem, "grant_drop_failed", slog.String("agent", req.Agent),
				slog.String("actor", actor), slog.String("error", err.Error()))
			http.Error(w, "binding store unavailable", http.StatusServiceUnavailable)
			return
		}
		out.Dropped += dropped
	}
	logging.InfoWithAttrs(logSubsystem, "session_grants_dropped",
		slog.String("sender", sender), slog.String("agent", req.Agent),
		slog.Int("bindings", out.Bindings), slog.Int("dropped", out.Dropped))
	logging.Audit(logging.AuditEvent{
		Action:  "session_grants_dropped",
		Outcome: "success",
		Subject: logging.TruncateIdentifier(sender),
		Target:  req.Agent,
		Details: "bindings=" + strconv.Itoa(out.Bindings) + " dropped=" + strconv.Itoa(out.Dropped),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
