package oauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/giantswarm/pagekit"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/signinpage"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// handoffParam carries a sign-in handoff ticket on a start link (see
// api.SignInHandoff); the approval package issues and redeems them.
const handoffParam = "handoff"

// handoffRedeemTimeout bounds the redemption's store round trip.
const handoffRedeemTimeout = 5 * time.Second

// maxConfirmBody caps the confirmation form.
const maxConfirmBody = 4 << 10

// confirmContinue is the confirmation's decision that goes on; any other
// (the form's "cancel") connects nothing.
const confirmContinue = "continue"

// redeemHandoff takes a start link's sign-in handoff: a ticket bound to this
// flow and issued to the person the flow was started for marks the flow as
// handed to this browser, pending its confirmation. Whatever the outcome the
// browser is sent to the start's clean address (303), without the ticket: a
// handed-over flow shows muster's confirmation there ("Connect <connector>
// as <person>?"), any other is sent to the identity check as before. A
// ticket is never logged.
//
// The confirmation is what keeps a handoff from binding whichever browser
// opens the link: a ticket names the person who asked for it, not the browser
// that opens it, so a person could pass a link to someone else's browser
// and have that browser's connector account filed under their own session.
// Only a browser that is told whose session it connects, and confirms, is
// bound to the flow.
func (h *Handler) redeemHandoff(w http.ResponseWriter, r *http.Request, stateParam string, state *OAuthState) {
	ticket := r.URL.Query().Get(handoffParam)
	// Its own bounded context: the redemption needs nothing the request
	// carries, and gosec's taint analysis follows a request's context into
	// every function the registered redeemer could be (G704 elsewhere).
	ctx, cancel := context.WithTimeout(context.Background(), handoffRedeemTimeout)
	defer cancel()
	if handoff, ok := api.RedeemHandoff(ctx, ticket, api.HandoffKindSignIn, stateParam, state.SignIn); ok {
		cookie := h.browserCookie(w, r)
		if pending := h.client.stateStore.Update(stateParam, func(s *OAuthState) {
			s.HandoffBinding = browserBinding(cookie)
			s.HandoffToken = rand.Text()
			s.HandoffName = handoff.Name
		}); pending == nil {
			h.renderErrorPage(w, summaryExpiredLink, "")
			return
		}
	}
	h.redirectToCleanStart(w, r, stateParam)
}

// redirectToCleanStart sends the browser to the start's address carrying
// only the flow's state.
func (h *Handler) redirectToCleanStart(w http.ResponseWriter, r *http.Request, stateParam string) {
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

// handedToBrowser reports whether the request comes from the browser that
// redeemed a handoff for the flow and has not confirmed it yet.
func (h *Handler) handedToBrowser(r *http.Request, state *OAuthState) bool {
	value := h.browserCookieValue(r)
	if value == "" || state.HandoffBinding == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(browserBinding(value)), []byte(state.HandoffBinding)) == 1
}

// handoffPerson names the person a handed-over flow signs in for: the
// ticket's label (approval.PersonLabel), else the flow's own subject at its
// issuer.
func handoffPerson(state *OAuthState) string {
	if state.HandoffName != "" {
		return state.HandoffName
	}
	return "the person this sign-in was started for (" + logging.TruncateIdentifier(state.SignIn.Subject) + " at " + state.SignIn.Issuer + ")"
}

// confirmData is what the confirmation shows.
type confirmData struct {
	Connector   string
	Person      string
	StartPath   string
	State       string
	Token       string
	ScriptNonce string
}

var confirmTemplate = template.Must(template.New("confirm").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer"><title>Connect {{.Connector}}?</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;margin:0;padding:2rem 1rem;background:#f6f8fa;color:#1f2328}
main{max-width:34rem;margin:0 auto;background:#fff;border:1px solid #d0d7de;border-radius:8px;padding:1.5rem}
h1{font-size:1.25rem;margin:0 0 1rem}p{line-height:1.5}strong{word-break:break-word}
.actions{display:flex;gap:.75rem;justify-content:flex-end;margin-top:1.5rem}
button{font:inherit;padding:.5rem 1rem;border-radius:6px;border:1px solid #d0d7de;background:#f6f8fa;cursor:pointer}
button.continue{background:#1f6feb;color:#fff;border-color:#1f6feb}button[disabled]{opacity:.5;cursor:default}
@media (prefers-color-scheme:dark){body{background:#0d1117;color:#e6edf3}main{background:#161b22;border-color:#30363d}button{background:#21262d;color:#e6edf3;border-color:#30363d}}
</style></head><body><main>
<h1>Connect <strong>{{.Connector}}</strong> as <strong>{{.Person}}</strong>?</h1>
<p>This sign-in was started for <strong>{{.Person}}</strong>. Continuing signs you in to <strong>{{.Connector}}</strong> and keeps that account for <strong>{{.Person}}</strong> and the agents acting for them.</p>
<p>If you are not <strong>{{.Person}}</strong>, or did not start this sign-in, cancel: nothing is connected.</p>
<form id="confirm" method="post" action="{{.StartPath}}">
<input type="hidden" name="state" value="{{.State}}"><input type="hidden" name="token" value="{{.Token}}">
<div class="actions"><button name="decision" value="cancel" disabled>Cancel</button><button class="continue" name="decision" value="continue" disabled>Continue</button></div>
</form></main>
<script nonce="{{.ScriptNonce}}">
(function(){function arm(){setTimeout(function(){document.querySelectorAll('button[name=decision]').forEach(function(b){b.disabled=false})},1000)}
if(document.visibilityState==='visible'){arm()}else{document.addEventListener('visibilitychange',function v(){if(document.visibilityState==='visible'){document.removeEventListener('visibilitychange',v);arm()}})}
document.getElementById('confirm').addEventListener('submit',function(){setTimeout(function(){document.querySelectorAll('button[name=decision]').forEach(function(b){b.disabled=true})},0)})})();
</script></body></html>`))

// confirmHeaders sets the confirmation's headers: never cached, framed or
// referred; scripts only with the render's nonce; forms post only to muster
// and, through the continue's redirect, to the connector's authorization
// server (Chromium applies form-action to a form's redirects).
func confirmHeaders(w http.ResponseWriter, scriptNonce, authorizationURL string) {
	formAction := "'self'"
	if target, err := url.Parse(authorizationURL); err == nil && target.Scheme != "" && target.Host != "" {
		formAction += " " + target.Scheme + "://" + target.Host
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-"+scriptNonce+
		"'; form-action "+formAction+"; frame-ancestors 'none'; base-uri 'none'")
	h.Set("Content-Type", "text/html; charset=utf-8")
}

// showHandoffConfirmation renders the confirmation of a handed-over flow.
func (h *Handler) showHandoffConfirmation(w http.ResponseWriter, r *http.Request, stateParam string, state *OAuthState) {
	start, err := url.Parse(h.client.startEndpoint())
	if err != nil {
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	nonce := rand.Text()
	confirmHeaders(w, nonce, state.AuthorizationURL)
	w.WriteHeader(http.StatusOK)
	_ = confirmTemplate.Execute(w, confirmData{
		Connector:   serverDisplayName(r.Context(), state.ServerName),
		Person:      handoffPerson(state),
		StartPath:   start.Path,
		State:       stateParam,
		Token:       state.HandoffToken,
		ScriptNonce: nonce,
	})
}

// confirmHandoff takes the confirmation's POST: from the browser the flow was
// handed to, with its anti-forgery token and, when the browser sends one,
// muster's own Origin. Continue binds the flow to the browser and goes on to
// the connector's sign-in (303); cancel drops the handoff and connects
// nothing. Anything else is refused and changes nothing.
func (h *Handler) confirmHandoff(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxConfirmBody)
	if err := r.ParseForm(); err != nil {
		h.renderErrorPage(w, summaryIncompleteLink, "")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" && origin != h.publicOrigin() {
		logging.Warn("OAuth", "Refusing a sign-in confirmation posted from another origin")
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	stateParam := r.PostForm.Get("state")
	state := h.client.stateStore.Update(stateParam, func(*OAuthState) {})
	if state == nil {
		h.renderErrorPage(w, summaryExpiredLink, "")
		return
	}
	token := r.PostForm.Get("token")
	if !h.handedToBrowser(r, state) || token == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(state.HandoffToken)) != 1 {
		logging.Warn("OAuth", "Refusing a sign-in confirmation for server=%s: not from the browser the sign-in was handed to, or without its token", state.ServerName)
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	decision := r.PostForm.Get("decision")
	updated := h.client.stateStore.Update(stateParam, func(s *OAuthState) {
		if decision == confirmContinue {
			s.BrowserBinding = s.HandoffBinding
		}
		s.HandoffBinding, s.HandoffToken, s.HandoffName = "", "", ""
	})
	if updated == nil {
		h.renderErrorPage(w, summaryExpiredLink, "")
		return
	}
	if decision != confirmContinue {
		logging.Info("OAuth", "Sign-in to server=%s cancelled on its handoff confirmation", state.ServerName)
		pagekit.Render(w, http.StatusOK, signinpage.Cancelled(serverDisplayName(r.Context(), state.ServerName)))
		return
	}
	// The target is the upstream authorization URL stored server-side with
	// the state; the request only supplies the state lookup key.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, updated.AuthorizationURL, http.StatusSeeOther) //nolint:gosec // G710: server-side stored URL, not request input
}

// publicOrigin is the origin of muster's public URL.
func (h *Handler) publicOrigin() string {
	public, err := url.Parse(h.client.publicURL)
	if err != nil {
		return ""
	}
	return public.Scheme + "://" + public.Host
}
