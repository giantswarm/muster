package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// PathPrefix is the path of the approval pages on muster's public URL.
const PathPrefix = "/approvals/"

// flowTTL bounds a browser's flow on one approval: the identity sign-in and
// the decision after it.
const flowTTL = 10 * time.Minute

// maxFormBytes caps the decision form.
const maxFormBytes = 4 << 10

// The browser cookie of the approval page. Over https the __Host- prefix keeps
// a sibling subdomain from planting it.
const (
	cookieName       = "muster_approval_browser"
	secureCookieName = "__Host-" + cookieName
)

// Page serves the approval page: GET shows a request (after signing the
// browser in to muster's identity provider), POST records the decision. No
// GET changes a decision. Only muster's browser sign-in decides: a bearer or
// actor token on the request is never read.
type Page struct {
	// PublicURL is muster's public origin (https://muster.example.com); every
	// link and the identity sign-in's return are built from it.
	PublicURL string
	// Installation names the installation on the page.
	Installation string
	// Store returns the approval store, nil when approval is unavailable.
	Store func() api.ApprovalStore
	// Identity returns the browser identity check, nil without one.
	Identity func() api.BrowserIdentity
	// RedirectAllowed checks a client's return against the allowlist.
	RedirectAllowed func(string) bool
	// Now is the page's clock.
	Now func() time.Time
}

// Register mounts the page on mux.
func (p *Page) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+PathPrefix+"{id}", p.handleView)
	mux.HandleFunc("POST "+PathPrefix+"{id}", p.handleDecision)
}

// URL returns the page of an approval request.
func (p *Page) URL(id string) string {
	return strings.TrimSuffix(p.PublicURL, "/") + PathPrefix + url.PathEscape(id)
}

func (p *Page) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return clock.Now()
}

func (p *Page) secure() bool {
	return strings.HasPrefix(p.PublicURL, "https://")
}

func (p *Page) cookieName() string {
	if p.secure() {
		return secureCookieName
	}
	return cookieName
}

func (p *Page) cookieValue(r *http.Request) string {
	c, err := r.Cookie(p.cookieName())
	if err != nil {
		return ""
	}
	return c.Value
}

// ensureCookie returns the browser's cookie, issuing one when it has none.
func (p *Page) ensureCookie(w http.ResponseWriter, r *http.Request) string {
	if value := p.cookieValue(r); value != "" {
		return value
	}
	value := rand.Text()
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure whenever muster is served over https
		Name:     p.cookieName(),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   p.secure(),
		SameSite: http.SameSiteLaxMode,
	})
	return value
}

// flowKey binds a flow to one browser and one request.
func flowKey(cookie, id string) string {
	sum := sha256.Sum256([]byte(cookie + "\x00" + id))
	return hex.EncodeToString(sum[:])
}

// identityVerifier is the PKCE verifier of the identity sign-in, derived from
// the browser's cookie: only the browser that was sent to the sign-in can
// redeem the code it brings back.
func identityVerifier(cookie, nonce string) string {
	sum := sha256.Sum256([]byte("approval\x00" + cookie + "\x00" + nonce))
	return hex.EncodeToString(sum[:])
}

// securityHeaders sets the page's headers. returnOrigin is the origin the
// decision's 303 sends the browser to: Chromium applies form-action to every
// redirect of a form submission, under the policy of the page that holds the
// form, so that page names exactly that origin besides its own (and nothing
// when there is no return). Verified in Chromium: with form-action 'self'
// alone the redirect after the decision POST is blocked.
func securityHeaders(w http.ResponseWriter, scriptNonce, returnOrigin string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	formAction := "'self'"
	if returnOrigin != "" {
		formAction += " " + returnOrigin
	}
	csp := "default-src 'none'; style-src 'unsafe-inline'; form-action " + formAction + "; frame-ancestors 'none'; base-uri 'none'"
	if scriptNonce != "" {
		csp += "; script-src 'nonce-" + scriptNonce + "'"
	}
	h.Set("Content-Security-Policy", csp)
}

// handleView shows a request, after the browser signed in, or finishes that
// sign-in when the identity provider sent the browser back with a code.
func (p *Page) handleView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	store, identity := p.Store(), p.Identity()
	if store == nil || identity == nil {
		p.renderMessage(w, http.StatusServiceUnavailable, "Approval is not available", "muster cannot take decisions here: its shared store or its browser sign-in is not set up.")
		return
	}
	request, err := store.Get(r.Context(), id)
	if err != nil {
		p.renderStoreError(w, err)
		return
	}
	if r.URL.Query().Has("code") {
		p.finishSignIn(w, r, store, identity, request)
		return
	}

	cookie := p.ensureCookie(w, r)
	key := flowKey(cookie, id)
	flow, err := store.GetFlow(r.Context(), key)
	if errors.Is(err, api.ErrApprovalNotFound) {
		flow, err = &api.ApprovalPageFlow{Token: rand.Text()}, nil
	}
	if err != nil {
		p.renderStoreError(w, err)
		return
	}
	// The first allowed return of this browser's flow is kept; a later link
	// cannot replace it (M1-MU-S-12).
	if redirect := r.URL.Query().Get("redirect"); redirect != "" && flow.Redirect == "" {
		if p.RedirectAllowed != nil && p.RedirectAllowed(redirect) {
			flow.Redirect = redirect
		} else {
			logging.Warn("Approval", "Ignoring a return not in oauth.mcpClient.postLoginRedirectAllowlist for approval %s", logging.TruncateIdentifier(id))
		}
	}

	if flow.Identity == (api.SignInIdentity{}) {
		p.sendToSignIn(w, r, store, identity, request, cookie, key, flow)
		return
	}
	if err := store.PutFlow(r.Context(), key, *flow, flowTTL); err != nil {
		p.renderStoreError(w, err)
		return
	}
	p.renderRequest(w, request, flow)
}

// sendToSignIn sends the browser to muster's identity provider to learn who
// is in it, expecting the person the request is bound to.
func (p *Page) sendToSignIn(w http.ResponseWriter, r *http.Request, store api.ApprovalStore, identity api.BrowserIdentity, request *api.ApprovalRequest, cookie, key string, flow *api.ApprovalPageFlow) {
	if err := identity.CheckRefusal(request.Identity); err != nil {
		p.renderMessage(w, http.StatusForbidden, "muster cannot confirm who you are", "This request cannot be decided here. Ask for it again from a session muster can tell the person of.")
		return
	}
	flow.Nonce = rand.Text()
	if err := store.PutFlow(r.Context(), key, *flow, flowTTL); err != nil {
		p.renderStoreError(w, err)
		return
	}
	returnURL, err := url.Parse(p.URL(request.ID))
	if err != nil {
		p.renderMessage(w, http.StatusInternalServerError, "The approval page is misconfigured", "")
		return
	}
	sum := sha256.Sum256([]byte(identityVerifier(cookie, flow.Nonce)))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	signIn, err := identity.SignInURL(r.Context(), returnURL, flow.Nonce, challenge, request.Identity)
	if err != nil {
		logging.Warn("Approval", "Cannot start the sign-in for approval %s: %v", logging.TruncateIdentifier(request.ID), err)
		p.renderMessage(w, http.StatusServiceUnavailable, "The sign-in could not be started", "Try again in a moment.")
		return
	}
	http.Redirect(w, r, signIn, http.StatusFound) //nolint:gosec // G710: built by muster's own OAuth server
}

// finishSignIn records who signed in in this browser and shows the page again.
func (p *Page) finishSignIn(w http.ResponseWriter, r *http.Request, store api.ApprovalStore, identity api.BrowserIdentity, request *api.ApprovalRequest) {
	cookie := p.cookieValue(r)
	if cookie == "" {
		p.renderMessage(w, http.StatusBadRequest, "The sign-in came back to another browser", "Open the approval link again in the browser you signed in with.")
		return
	}
	key := flowKey(cookie, request.ID)
	flow, err := store.GetFlow(r.Context(), key)
	if err != nil {
		p.renderStoreError(w, err)
		return
	}
	state := r.URL.Query().Get("state")
	if flow.Nonce == "" || subtle.ConstantTimeCompare([]byte(state), []byte(flow.Nonce)) != 1 {
		p.renderMessage(w, http.StatusBadRequest, "The sign-in does not belong to this page", "Open the approval link again.")
		return
	}
	returnURL, err := url.Parse(p.URL(request.ID))
	if err != nil {
		p.renderMessage(w, http.StatusInternalServerError, "The approval page is misconfigured", "")
		return
	}
	signedIn, err := identity.Identity(r.Context(), returnURL, r.URL.Query().Get("code"), identityVerifier(cookie, flow.Nonce))
	if err != nil {
		logging.Warn("Approval", "Sign-in for approval %s failed: %v", logging.TruncateIdentifier(request.ID), err)
		p.renderMessage(w, http.StatusForbidden, "The sign-in could not be completed", "Open the approval link again.")
		return
	}
	flow.Identity, flow.Nonce = signedIn, ""
	if err := store.PutFlow(r.Context(), key, *flow, flowTTL); err != nil {
		p.renderStoreError(w, err)
		return
	}
	http.Redirect(w, r, p.URL(request.ID), http.StatusSeeOther) //nolint:gosec // G710: muster's own page, from its public URL and the stored request's id
}

// handleDecision records a decision: only from the browser signed in as the
// person the request is bound to, with the form's anti-forgery token, once.
// The browser then goes back to its client's return, never told the decision.
func (p *Page) handleDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	store := p.Store()
	if store == nil || p.Identity() == nil {
		p.renderMessage(w, http.StatusServiceUnavailable, "Approval is not available", "")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, p.PublicURL) {
		p.renderMessage(w, http.StatusForbidden, "The decision did not come from this page", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		p.renderMessage(w, http.StatusBadRequest, "The decision could not be read", "")
		return
	}
	cookie := p.cookieValue(r)
	if cookie == "" {
		p.renderMessage(w, http.StatusForbidden, "The decision did not come from this page", "Open the approval link again.")
		return
	}
	key := flowKey(cookie, id)
	flow, err := store.GetFlow(r.Context(), key)
	if err != nil {
		if errors.Is(err, api.ErrApprovalNotFound) {
			p.renderMessage(w, http.StatusForbidden, "The decision did not come from this page", "Open the approval link again.")
			return
		}
		p.renderStoreError(w, err)
		return
	}
	if flow.Token == "" || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("token")), []byte(flow.Token)) != 1 {
		p.renderMessage(w, http.StatusForbidden, "The decision did not come from this page", "Open the approval link again.")
		return
	}
	request, err := store.Get(r.Context(), id)
	if err != nil {
		p.renderStoreError(w, err)
		return
	}
	if flow.Identity == (api.SignInIdentity{}) || flow.Identity != request.Identity {
		p.renderMessage(w, http.StatusForbidden, "Only the person who asked can decide", "")
		return
	}
	var approve bool
	switch r.PostForm.Get("decision") {
	case "approve":
		approve = true
	case "decline":
	default:
		p.renderMessage(w, http.StatusBadRequest, "The decision could not be read", "")
		return
	}
	if err := store.Decide(r.Context(), id, approve, flow.Identity); err != nil {
		p.renderStoreError(w, err)
		return
	}
	_ = store.DeleteFlow(r.Context(), key)
	auditDecision(r.Context(), request, approve, flow.Identity)

	if flow.Redirect != "" {
		if target, err := url.Parse(flow.Redirect); err == nil {
			query := target.Query()
			query.Set("elicitationId", request.ElicitationID)
			target.RawQuery = query.Encode()
			securityHeaders(w, "", "")
			http.Redirect(w, r, target.String(), http.StatusSeeOther) //nolint:gosec // G710: allowlist-checked when the page stored it
			return
		}
	}
	p.renderMessage(w, http.StatusOK, "Your answer is recorded", "Go back to where the request came from and continue there.")
}

// originOf returns the scheme and host of raw, "" when it has none.
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func sameOrigin(origin, publicURL string) bool {
	o, err1 := url.Parse(origin)
	pu, err2 := url.Parse(publicURL)
	return err1 == nil && err2 == nil && o.Scheme == pu.Scheme && o.Host == pu.Host
}

func (p *Page) renderStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, api.ErrApprovalNotFound):
		p.renderMessage(w, http.StatusNotFound, "This request has expired or was already used", "Run the tool again to ask anew.")
	case errors.Is(err, api.ErrApprovalDecided):
		p.renderMessage(w, http.StatusConflict, "This request was already answered", "Go back to where the request came from and continue there.")
	default:
		logging.Warn("Approval", "Approval store unavailable: %v", err)
		p.renderMessage(w, http.StatusServiceUnavailable, "Approval is unavailable right now", "Try again in a moment.")
	}
}

// auditDecision records who decided what, for whom, on which target.
func auditDecision(ctx context.Context, request *api.ApprovalRequest, approve bool, approver api.SignInIdentity) {
	outcome := "declined"
	if approve {
		outcome = "approved"
	}
	logging.InfoWithAttrsCtx(ctx, "Approval", "[AUDIT] approval decided",
		slog.String("action", "approval_decision"),
		slog.String("outcome", outcome),
		slog.String("approval", logging.TruncateIdentifier(request.ID)),
		slog.String("approver", approver.Issuer+"#"+approver.Subject),
		slog.String("requester", request.Identity.Issuer+"#"+request.Identity.Subject),
		slog.String("callerKind", request.CallerKind),
		slog.String("session", logging.TruncateIdentifier(request.SessionID)),
		slog.String("targetKind", request.Target.Kind),
		slog.String("server", request.Target.Server),
		slog.String("target", request.Target.Name),
		slog.String("argumentsHash", request.ArgumentsHash),
		slog.String("setting", request.Display.Setting),
		slog.String("source", request.Display.Source),
		slog.String("governance", api.GetGovernance().Mode))
}
