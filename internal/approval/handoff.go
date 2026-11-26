package approval

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// HandoffPath is where a client trusted with it asks for a sign-in handoff
// ticket, with the person's bearer.
const HandoffPath = "/handoff-tickets"

// HandoffParam is the query parameter that carries a ticket on the link the
// client opens.
const HandoffParam = "handoff"

// HandoffTTL is how long a ticket can be redeemed: long enough for the
// browser to open the link right after the client asked, too short to be
// worth keeping.
const HandoffTTL = 60 * time.Second

// maxTicketLength bounds a ticket read from a link (rand.Text is 26).
const maxTicketLength = 64

// maxHandoffBody caps the request asking for a ticket.
const maxHandoffBody = 8 << 10

// Handoffs issues and redeems sign-in handoffs (see api.SignInHandoff): a
// client on approval.trustedClients asks with the person's bearer for a
// ticket bound to one of muster's pages and that person; the page redeems it
// once and treats the browser as signed in as that person, for that page
// only. A ticket is never stored (only its SHA-256), never logged and never
// put anywhere but the answer to the client.
type Handoffs struct {
	// PublicURL is muster's public URL, the origin and base path of its pages.
	PublicURL string
	// StartPath is the OAuth proxy start's path under PublicURL; empty
	// without the OAuth proxy.
	StartPath string
	// Clients are the OAuth client ids whose bearers may ask for tickets.
	// Empty: muster issues none.
	Clients []string
	// Store returns the approval store, nil when approval is unavailable.
	Store func() api.ApprovalStore
	// ClientOf names the OAuth client the request's bearer was issued to.
	ClientOf func(context.Context) string
}

type handoffRequest struct {
	URL string `json:"url"`
}

type handoffAnswer struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expiresIn"`
}

// handoffTarget is what a ticket is bound to: an approval's id; a sign-in's
// state only by its digest, which is all muster needs to compare.
func handoffTarget(kind, raw string) string {
	if kind == api.HandoffKindSignIn {
		return digest(raw)
	}
	return raw
}

// targetOf names the page a link opens, by muster's public origin and the
// page's path: an approval page (its id) or a connector sign-in start (its
// state). ok is false for any other link.
func (h *Handoffs) targetOf(raw string) (kind, target string, ok bool) {
	link, err := url.Parse(raw)
	public, perr := url.Parse(h.PublicURL)
	if err != nil || perr != nil || link.Scheme != public.Scheme || link.Host != public.Host || link.User != nil {
		return "", "", false
	}
	base := strings.TrimSuffix(public.Path, "/")
	switch {
	case strings.HasPrefix(link.Path, base+PathPrefix):
		id := strings.TrimPrefix(link.Path, base+PathPrefix)
		if id == "" || strings.Contains(id, "/") {
			return "", "", false
		}
		return api.HandoffKindApproval, id, true
	case h.StartPath != "" && link.Path == base+h.StartPath:
		state := link.Query().Get("state")
		if state == "" {
			return "", "", false
		}
		return api.HandoffKindSignIn, handoffTarget(api.HandoffKindSignIn, state), true
	}
	return "", "", false
}

// ServeHTTP issues a ticket: POST {"url": "<a page of muster>"} with the
// person's bearer (behind the OAuth server's subject check, which refuses a
// token naming an acting party). Answers 201 {"ticket", "expiresIn"}; 403
// for a client not on approval.trustedClients and for an approval that is not
// the person's (or unknown: both answer alike); 400 for a link to no page
// muster hands off to; 503 while the store is unavailable.
func (h *Handoffs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	m := DefaultMetrics()
	refuse := func(status int, kind, reason, message string) {
		m.Handoff(ctx, HandoffRefused, kind, reason)
		logging.InfoWithAttrs("Approval", "sign-in handoff refused",
			slog.String("page", kind), slog.String("reason", reason))
		http.Error(w, message, status)
	}

	identity := api.SignInIdentityFromContext(ctx)
	client := ""
	if h.ClientOf != nil {
		client = h.ClientOf(ctx)
	}
	if identity.Subject == "" || client == "" || !slices.Contains(h.Clients, client) {
		refuse(http.StatusForbidden, "", HandoffReasonClient, "muster hands off sign-ins only to the clients on approval.trustedClients")
		return
	}
	store := h.Store()
	if store == nil {
		refuse(http.StatusServiceUnavailable, "", HandoffReasonUnavailable, "muster's approval store is not set up")
		return
	}
	var body handoffRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHandoffBody)).Decode(&body); err != nil {
		refuse(http.StatusBadRequest, "", HandoffReasonTarget, `the body must be {"url": "<a page of muster>"}`)
		return
	}
	kind, target, ok := h.targetOf(body.URL)
	if !ok {
		refuse(http.StatusBadRequest, "", HandoffReasonTarget, "muster hands off sign-ins only to its approval pages and connector sign-in starts on its public URL")
		return
	}
	if kind == api.HandoffKindApproval {
		request, err := store.Get(ctx, target)
		switch {
		case errors.Is(err, api.ErrApprovalNotFound):
			refuse(http.StatusForbidden, kind, HandoffReasonForeign, "no handoff for this page")
			return
		case err != nil:
			logging.Warn("Approval", "Approval store unavailable for a handoff: %v", err)
			refuse(http.StatusServiceUnavailable, kind, HandoffReasonUnavailable, "muster's approval store cannot be reached")
			return
		case request.Identity != identity:
			refuse(http.StatusForbidden, kind, HandoffReasonForeign, "no handoff for this page")
			return
		}
	}
	// A connector sign-in's person is checked when the ticket is redeemed:
	// the start compares it with the person its flow was started for.

	ticket := rand.Text()
	if err := store.PutHandoff(ctx, digest(ticket), api.SignInHandoff{Kind: kind, Target: target, Identity: identity}, HandoffTTL); err != nil {
		logging.Warn("Approval", "Approval store unavailable for a handoff: %v", err)
		refuse(http.StatusServiceUnavailable, kind, HandoffReasonUnavailable, "muster's approval store cannot be reached")
		return
	}
	m.Handoff(ctx, HandoffIssued, kind, HandoffReasonNone)
	logging.InfoWithAttrs("Approval", "sign-in handoff issued",
		slog.String("page", kind), slog.String("client", client),
		slog.String("subject", logging.TruncateIdentifier(identity.Subject)))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(handoffAnswer{Ticket: ticket, ExpiresIn: int(HandoffTTL.Seconds())})
}

// Redeem implements api.HandoffRedeemer. The ticket is taken from the store
// whatever the outcome, so a ticket shown to a wrong page is spent.
func (h *Handoffs) Redeem(ctx context.Context, ticket, kind, target string, want api.SignInIdentity) bool {
	m := DefaultMetrics()
	refuse := func(reason string) bool {
		m.Handoff(ctx, HandoffRefused, kind, reason)
		logging.InfoWithAttrs("Approval", "sign-in handoff not redeemed: the page signs the browser in itself",
			slog.String("page", kind), slog.String("reason", reason))
		return false
	}
	if ticket == "" || len(ticket) > maxTicketLength {
		return refuse(HandoffReasonInvalid)
	}
	store := h.Store()
	if store == nil {
		return refuse(HandoffReasonUnavailable)
	}
	handoff, err := store.TakeHandoff(ctx, digest(ticket))
	switch {
	case errors.Is(err, api.ErrApprovalNotFound):
		return refuse(HandoffReasonInvalid)
	case err != nil:
		logging.Warn("Approval", "Approval store unavailable for a handoff: %v", err)
		return refuse(HandoffReasonUnavailable)
	case handoff.Kind != kind || handoff.Target != handoffTarget(kind, target):
		return refuse(HandoffReasonTarget)
	case want.Subject == "" || handoff.Identity != want:
		return refuse(HandoffReasonForeign)
	}
	m.Handoff(ctx, HandoffRedeemed, kind, HandoffReasonNone)
	logging.InfoWithAttrs("Approval", "sign-in handoff redeemed",
		slog.String("page", kind), slog.String("subject", logging.TruncateIdentifier(want.Subject)))
	return true
}
