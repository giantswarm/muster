package credprovider

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// BindingsPath is the HTTP path of the Session binding endpoints.
const BindingsPath = "/session-bindings"

// maxBindRequestBytes bounds a bind request body: a toolset of up to 8 KiB
// plus an agent's approval settings (at most 128 entries and 256 grants).
const maxBindRequestBytes = 64 << 10

// ApprovalSettingsHeader is the response header that tells the bind's sender
// what muster did with the agent and its approval settings: bound, ignored
// (sent without an approval sender) or none (none sent). A muster that
// answers without it does not take approval settings.
const ApprovalSettingsHeader = "X-Muster-Approval-Settings"

// Values of ApprovalSettingsHeader.
const (
	ApprovalSettingsBound   = "bound"
	ApprovalSettingsIgnored = "ignored"
	ApprovalSettingsNone    = "none"
	// ApprovalSettingsRefused answers a bind refused because of the
	// approval settings or their sender: nothing was bound.
	ApprovalSettingsRefused = "refused"
)

// BindRequest is the body of PUT /session-bindings.
type BindRequest struct {
	// Actor is the Session's sandbox actor SPIFFE id.
	Actor string `json:"actor"`
	// Session names the Session (<namespace>/<name>), for the audit log.
	Session string `json:"session"`
	// Providers are the provider instances the Session may use.
	Providers []string `json:"providers"`
	// Toolset is the agent's toolset, the inline selectors that bound every
	// request the actor makes to muster (X-Muster-Toolset grammar). Absent
	// binds none: the actor's requests are unscoped unless they send the
	// header.
	Toolset []string `json:"toolset,omitempty"`
	// Agent is the Session's agent, <namespace>/<name>. Taken only from an
	// approval sender.
	Agent string `json:"agent,omitempty"`
	// Approval is the agent's approval settings: entries with a setting
	// (each also in Toolset), the default and the grant marks. Taken only
	// from an approval sender.
	Approval *api.AgentApproval `json:"approval,omitempty"`
}

// agentPattern is <namespace>/<name> of Kubernetes names.
var agentPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?/[a-z0-9]([-.a-z0-9]{0,251}[a-z0-9])?$`)

// validateAgentSettings checks the agent and its approval settings of a bind.
func validateAgentSettings(req BindRequest) error {
	if req.Agent != "" && !agentPattern.MatchString(req.Agent) {
		return fmt.Errorf("agent %q is not <namespace>/<name>", req.Agent)
	}
	if req.Approval == nil {
		return nil
	}
	if err := req.Approval.Validate(); err != nil {
		return err
	}
	for _, e := range req.Approval.Entries {
		if !slices.Contains(req.Toolset, e.Entry) {
			return fmt.Errorf("approval entry %q is not in the toolset", e.Entry)
		}
	}
	return nil
}

// HandleBind is PUT /session-bindings: binds the actor to the calling person.
// The caller must be authenticated (api.GetSubjectFromContext non-empty).
func (p *Provider) HandleBind(w http.ResponseWriter, r *http.Request) {
	sub := api.GetSubjectFromContext(r.Context())
	if sub == "" {
		http.Error(w, "unauthorized: no person identity", http.StatusUnauthorized)
		return
	}
	var req BindRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBindRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(req.Actor, actorSPIFFEPrefix) || validateSPIFFE(req.Actor) != nil {
		http.Error(w, "bad request: actor must be a Substrate actor SPIFFE id ("+actorSPIFFEPrefix+"<atespace>/<actor>)", http.StatusBadRequest)
		return
	}
	if req.Session == "" {
		http.Error(w, "bad request: session is required", http.StatusBadRequest)
		return
	}
	if len(req.Providers) == 0 {
		http.Error(w, "bad request: providers is empty", http.StatusBadRequest)
		return
	}
	for _, provider := range req.Providers {
		if provider == MusterProvider && p.musterBearer {
			continue
		}
		if _, ok := p.releasable[provider]; !ok {
			http.Error(w, "bad request: provider "+provider+" is not releasable; releasable: "+strings.Join(p.releasableNames(), ", "), http.StatusBadRequest)
			return
		}
	}

	// An unknown preset is no bind error: presets are configuration that can
	// change under a running Session, and every meta-tool call of the Session
	// names it, as for a header.
	if req.Toolset != nil {
		if _, err := toolset.ParseInline(req.Toolset); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if err := validateAgentSettings(req); err != nil {
		w.Header().Set(ApprovalSettingsHeader, ApprovalSettingsRefused)
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	sender, refused := p.reviewSender(r)
	approvalSender := sender != ""
	if refused != 0 {
		logging.WarnWithAttrs(logSubsystem, "bind_refused",
			slog.String("reason", "the approval settings sender did not pass the review"), slog.String("sub", sub),
			slog.String("actor", req.Actor), slog.String("session", req.Session))
		w.Header().Set(ApprovalSettingsHeader, ApprovalSettingsRefused)
		http.Error(w, http.StatusText(refused)+": the sender of approval settings did not pass the review", refused)
		return
	}

	if !approvalSender && (req.Agent != "" || req.Approval != nil) {
		// Settings come only from a sender Kubernetes allows: the rest of
		// the bind is taken, the agent and its settings are not.
		logging.WarnWithAttrs(logSubsystem, "approval_settings_ignored",
			slog.String("reason", "the bind carries an agent or approval settings but no approval sender"),
			slog.String("sub", sub), slog.String("actor", req.Actor), slog.String("session", req.Session))
		req.Agent, req.Approval = "", nil
		w.Header().Set(ApprovalSettingsHeader, ApprovalSettingsIgnored)
	} else if req.Agent != "" || req.Approval != nil {
		w.Header().Set(ApprovalSettingsHeader, ApprovalSettingsBound)
	} else {
		w.Header().Set(ApprovalSettingsHeader, ApprovalSettingsNone)
	}

	signIn := api.SignInIdentityFromContext(r.Context())
	b := Binding{
		Agent: req.Agent, Approval: req.Approval,
		ApprovalSender: approvalSender, Sender: sender,
		Subject: sub, Session: req.Session, Providers: req.Providers, Toolset: req.Toolset,
		SignInIssuer: signIn.Issuer, SignInSubject: signIn.Subject, BoundAt: time.Now().UTC(),
	}
	b.LoginSession, b.LoginIssuer, b.LoginSubject = bindLogin(r)
	err := p.store.Put(r.Context(), req.Actor, b, p.ttl)
	if errors.Is(err, ErrBoundToAnother) || errors.Is(err, ErrSenderBound) {
		logging.WarnWithAttrs(logSubsystem, "bind_refused",
			slog.String("reason", err.Error()), slog.String("sub", sub),
			slog.String("actor", req.Actor), slog.String("session", req.Session))
		w.Header().Set(ApprovalSettingsHeader, ApprovalSettingsRefused)
		if errors.Is(err, ErrSenderBound) && !approvalSender {
			http.Error(w, "conflict: "+err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}
	if err != nil {
		logging.WarnWithAttrs(logSubsystem, "bind_failed", slog.String("actor", req.Actor), slog.String("error", err.Error()))
		http.Error(w, "binding store unavailable", http.StatusServiceUnavailable)
		return
	}
	logging.InfoWithAttrs(logSubsystem, "session_bound",
		slog.String("sub", sub), slog.String("session", req.Session),
		slog.String("actor", req.Actor), slog.Any("providers", req.Providers),
		slog.String("login", loginKind(b.LoginSession)),
		slog.Any("toolset", req.Toolset), slog.Bool("approvalSender", approvalSender), slog.Duration("ttl", p.ttl),
		slog.String("agent", req.Agent), slog.Int("approvalEntries", approvalEntries(req.Approval)),
		slog.String("default", approvalDefault(req.Approval)), slog.Any("grants", grantNames(req.Approval)))
	w.WriteHeader(http.StatusNoContent)
}

func approvalEntries(a *api.AgentApproval) int {
	if a == nil {
		return 0
	}
	return len(a.Entries)
}

func approvalDefault(a *api.AgentApproval) string {
	if a == nil {
		return ""
	}
	return a.Default
}

// grantNames lists a binding's grant marks as entry@server, for the logs.
func grantNames(a *api.AgentApproval) []string {
	if a == nil {
		return nil
	}
	names := make([]string, 0, len(a.Grants))
	for _, g := range a.Grants {
		names = append(names, g.Entry+"@"+g.Server)
	}
	return names
}

// bindLogin returns the session the bind request came from, whose login ID
// token the actor's requests carry, and for a forwarded session the issuer and
// subject its token must carry. The component that starts a Session binds
// with the ID token muster forwarded to it, so that token names the session
// that filed it: the login family muster obtained it for, or the session a
// client forwarded it in. A muster access token names its family. A forwarded
// token muster never filed, or one without iss and sub, pins nothing.
func bindLogin(r *http.Request) (session, issuer, subject string) {
	scheme, bearer, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	bearer = strings.TrimSpace(bearer)
	if ok && strings.EqualFold(scheme, "Bearer") {
		session = api.LoginSessionForIDToken(api.GetOAuthHandler(), bearer)
	}
	if session == "" {
		if session = api.GetSessionIDFromContext(r.Context()); api.IsForwardedSession(session) || api.IsActorSession(session) {
			return "", "", ""
		}
		return session, "", ""
	}
	if !api.IsForwardedSession(session) {
		return session, "", ""
	}
	issuer, issErr := pkgoauth.Issuer(bearer)
	subject, subErr := pkgoauth.Subject(bearer)
	if issErr != nil || subErr != nil || issuer == "" || subject == "" {
		return "", "", ""
	}
	return session, issuer, subject
}

// loginKind names, for the logs, the kind of session an actor's login ID
// token comes from.
func loginKind(loginSession string) string {
	switch {
	case loginSession == "":
		return "none"
	case api.IsForwardedSession(loginSession):
		return "forwarded"
	default:
		return "family"
	}
}

// HandleUnbind is DELETE /session-bindings?actor=<id>: removes the calling
// person's binding of the actor. A binding an approval sender wrote is
// removed only with that sender (X-Muster-Approval-Sender): 409 without it,
// 403 with another. Unbinding an unbound actor succeeds.
func (p *Provider) HandleUnbind(w http.ResponseWriter, r *http.Request) {
	sub := api.GetSubjectFromContext(r.Context())
	if sub == "" {
		http.Error(w, "unauthorized: no person identity", http.StatusUnauthorized)
		return
	}
	actor := r.URL.Query().Get("actor")
	if actor == "" {
		http.Error(w, "bad request: actor is required", http.StatusBadRequest)
		return
	}
	sender, refused := p.reviewSender(r)
	if refused != 0 {
		http.Error(w, http.StatusText(refused)+": the sender of approval settings did not pass the review", refused)
		return
	}
	err := p.store.Delete(r.Context(), actor, sub, sender)
	if errors.Is(err, ErrSenderBound) {
		logging.WarnWithAttrs(logSubsystem, "unbind_refused",
			slog.String("reason", err.Error()), slog.String("sub", sub), slog.String("actor", actor))
		status, text := http.StatusConflict, "conflict: "
		if sender != "" {
			status, text = http.StatusForbidden, "forbidden: "
		}
		http.Error(w, text+err.Error(), status)
		return
	}
	if errors.Is(err, ErrBoundToAnother) {
		logging.WarnWithAttrs(logSubsystem, "unbind_refused",
			slog.String("reason", err.Error()), slog.String("sub", sub), slog.String("actor", actor))
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}
	if err != nil {
		logging.WarnWithAttrs(logSubsystem, "unbind_failed", slog.String("actor", actor), slog.String("error", err.Error()))
		http.Error(w, "binding store unavailable", http.StatusServiceUnavailable)
		return
	}
	logging.InfoWithAttrs(logSubsystem, "session_unbound", slog.String("sub", sub), slog.String("actor", actor))
	w.WriteHeader(http.StatusNoContent)
}

func (p *Provider) releasableNames() []string {
	names := make([]string, 0, len(p.releasable)+1)
	for name := range p.releasable {
		names = append(names, name)
	}
	if p.musterBearer {
		names = append(names, MusterProvider)
	}
	sort.Strings(names)
	return names
}

// reviewSender reviews the bind's ApprovalSenderHeader token. Without one
// the bind is taken without approval settings ("", 0); with one that passes
// the review, the sender's identity is returned. A token that fails the
// review refuses the bind (403), as does a review that could not be made
// (503) or a muster that cannot review (403): an outage never strips an
// agent's Ask or Block.
func (p *Provider) reviewSender(r *http.Request) (string, int) {
	token := strings.TrimSpace(r.Header.Get(ApprovalSenderHeader))
	if token == "" {
		return "", 0
	}
	if p.senders == nil {
		return "", http.StatusForbidden
	}
	sender, err := p.senders.Review(r.Context(), token)
	switch {
	case err == nil && sender != "":
		return sender, 0
	case err == nil || errors.Is(err, ErrSenderNotAllowed):
		if err != nil {
			logging.WarnWithAttrs(logSubsystem, "approval_sender_refused", slog.String("error", err.Error()))
		}
		return "", http.StatusForbidden
	default:
		logging.WarnWithAttrs(logSubsystem, "approval_sender_review_failed", slog.String("error", err.Error()))
		return "", http.StatusServiceUnavailable
	}
}
