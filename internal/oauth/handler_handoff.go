package oauth

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
)

// handoffParam carries a sign-in handoff ticket on a start link (see
// api.SignInHandoff); the approval package issues and redeems them.
const handoffParam = "handoff"

// handoffRedeemTimeout bounds the redemption's store round trip.
const handoffRedeemTimeout = 5 * time.Second

// redeemHandoff takes a start link's sign-in handoff: a ticket bound to this
// flow and issued to the person the flow was started for binds the flow to
// this browser, as passing the identity check would. Whatever the outcome
// the browser is sent to the start's clean address (303), without the
// ticket: a bound flow goes on to the server's sign-in, any other is sent to
// the identity check as before. A ticket is never logged.
func (h *Handler) redeemHandoff(w http.ResponseWriter, r *http.Request, stateParam string, state *OAuthState) {
	ticket := r.URL.Query().Get(handoffParam)
	// Its own bounded context: the redemption needs nothing the request
	// carries, and gosec's taint analysis follows a request's context into
	// every function the registered redeemer could be (G704 elsewhere).
	ctx, cancel := context.WithTimeout(context.Background(), handoffRedeemTimeout)
	defer cancel()
	if api.RedeemHandoff(ctx, ticket, api.HandoffKindSignIn, stateParam, state.SignIn) {
		cookie := h.browserCookie(w, r)
		if bound := h.client.stateStore.Update(stateParam, func(s *OAuthState) {
			s.BrowserBinding = browserBinding(cookie)
		}); bound == nil {
			h.renderErrorPage(w, summaryExpiredLink, "")
			return
		}
	}
	clean, err := url.Parse(h.client.startEndpoint())
	if err != nil {
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	clean.RawQuery = url.Values{"state": {stateParam}}.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, clean.String(), http.StatusSeeOther) //nolint:gosec // G710: muster's own start endpoint
}
