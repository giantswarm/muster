package approval

import (
	"crypto/rand"
	"errors"
	"net/http"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// keepRedirect records the link's return on the browser's flow: the first
// allowed one is kept, a later link cannot replace it.
func (p *Page) keepRedirect(r *http.Request, flow *api.ApprovalPageFlow, id string) {
	if redirect := r.URL.Query().Get("redirect"); redirect != "" && flow.Redirect == "" {
		if p.RedirectAllowed != nil && p.RedirectAllowed(redirect) {
			flow.Redirect = redirect
		} else {
			logging.Warn("Approval", "Ignoring a return not in oauth.mcpClient.postLoginRedirectAllowlist for approval %s", logging.TruncateIdentifier(id))
		}
	}
}

// redeemHandoff takes a link's sign-in handoff: a ticket bound to this
// request and its person signs this browser's flow in as that person, as
// muster's own sign-in would. Whatever the outcome the browser is sent to
// the page's clean address (303), so the ticket leaves the address bar; a
// ticket that does not redeem (unknown, expired, used, another page or
// person) leaves the flow as it was, and the page signs the browser in
// itself. The link's return is kept on the flow either way.
func (p *Page) redeemHandoff(w http.ResponseWriter, r *http.Request, store api.ApprovalStore, id string) {
	ticket := r.URL.Query().Get(HandoffParam)
	cookie := p.ensureCookie(w, r)
	request, err := store.Get(r.Context(), id)
	if err != nil {
		// Spend the ticket: it opens nothing here.
		if p.Handoffs != nil {
			p.Handoffs.Redeem(r.Context(), ticket, api.HandoffKindApproval, id, api.SignInIdentity{})
		}
		securityHeaders(w, "", "")
		http.Redirect(w, r, p.URL(id), http.StatusSeeOther) //nolint:gosec // G710: muster's own page
		return
	}
	key := flowKey(cookie, id)
	flow, err := store.GetFlow(r.Context(), key)
	if errors.Is(err, api.ErrApprovalNotFound) {
		flow, err = &api.ApprovalPageFlow{Token: rand.Text()}, nil
	}
	if err != nil {
		p.renderStoreError(w, err)
		return
	}
	p.keepRedirect(r, flow, id)
	if p.Handoffs != nil && p.Handoffs.Redeem(r.Context(), ticket, api.HandoffKindApproval, id, request.Identity) {
		flow.Identity, flow.Nonce = request.Identity, ""
	}
	if err := store.PutFlow(r.Context(), key, *flow, flowTTL); err != nil {
		p.renderStoreError(w, err)
		return
	}
	securityHeaders(w, "", "")
	http.Redirect(w, r, p.URL(id), http.StatusSeeOther) //nolint:gosec // G710: muster's own page, from its public URL and the stored request's id
}
