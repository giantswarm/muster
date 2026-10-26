package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// browserCookieName names the cookie that identifies a browser to the OAuth
// proxy. A flow is bound to the browser that passed the sign-in check: only
// that browser goes on to the server's authorization server and has its
// callback accepted, so a link forwarded after its owner passed the check
// connects nobody else's account.
const browserCookieName = "muster_oauth_browser"

// browserCookie returns the browser's cookie value, issuing one when the
// request carries none.
func (h *Handler) browserCookie(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(browserCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	value := rand.Text()
	// Path "/" because the callback path is configurable. SameSite=Lax still
	// sends the cookie on the authorization servers' top-level redirects back.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure whenever muster is served over https
		Name:     browserCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(h.client.publicURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
	return value
}

// browserBinding is what a flow records of the browser that passed the check.
func browserBinding(cookie string) string {
	sum := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(sum[:])
}

// boundToBrowser reports whether the request comes from the browser the flow
// is bound to.
func boundToBrowser(r *http.Request, state *OAuthState) bool {
	c, err := r.Cookie(browserCookieName)
	if err != nil || c.Value == "" || state.BrowserBinding == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(browserBinding(c.Value)), []byte(state.BrowserBinding)) == 1
}

// identityVerifier is the PKCE verifier of a flow's identity sign-in. It is
// derived from the browser cookie, so only the browser that was sent to the
// sign-in can redeem the code it brings back.
func identityVerifier(cookie, nonce string) string {
	sum := sha256.Sum256([]byte(cookie + "\x00" + nonce))
	return hex.EncodeToString(sum[:])
}

// identityReturnURL is where the identity sign-in sends the browser back: the
// start endpoint, on the host the browser cookie belongs to.
func (h *Handler) identityReturnURL() (*url.URL, error) {
	return url.Parse(strings.TrimSuffix(h.client.publicURL, "/") + config.DefaultOAuthProxyStartPath)
}

// sendToIdentityCheck sends the browser to sign in to muster's identity
// provider before the server's sign-in, so the start endpoint learns who is
// in the browser. The browser comes back to the start endpoint with code and
// a state naming the flow by its nonce (finishIdentityCheck).
func (h *Handler) sendToIdentityCheck(w http.ResponseWriter, r *http.Request, identity api.BrowserIdentity, state *OAuthState) {
	sum := sha256.Sum256([]byte(identityVerifier(h.browserCookie(w, r), state.Nonce)))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	identityState, err := nonceState(state.Nonce)
	if err != nil {
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	returnURL, err := h.identityReturnURL()
	if err != nil {
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	signInURL, err := identity.SignInURL(r.Context(), returnURL, identityState, challenge)
	if err != nil {
		logging.Warn("OAuth", "Cannot start the sign-in check for server=%s: %v", state.ServerName, err)
		h.renderErrorPage(w, summaryNotCompleted, "")
		return
	}
	http.Redirect(w, r, signInURL, http.StatusFound) //nolint:gosec // G710: URL built by muster's own OAuth server
}

// finishIdentityCheck handles the browser's return from the identity sign-in:
// the flow continues to the server's authorization server only when the
// person who signed in is the person the flow was started for (MCP URL mode
// elicitation requires binding the sign-in to that user), and is bound to
// this browser. A link opened by someone else is refused and stays usable by
// its owner.
func (h *Handler) finishIdentityCheck(w http.ResponseWriter, r *http.Request, identity api.BrowserIdentity) {
	code := r.URL.Query().Get("code")
	stateParam := r.URL.Query().Get("state")

	state := h.client.stateStore.Update(stateParam, func(*OAuthState) {})
	if state == nil {
		h.renderErrorPage(w, summaryExpiredLink, "")
		return
	}
	cookie, err := r.Cookie(browserCookieName)
	if err != nil || cookie.Value == "" {
		logging.Warn("OAuth", "Refusing sign-in check for server=%s: the browser came back without its cookie", state.ServerName)
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	returnURL, err := h.identityReturnURL()
	if err != nil {
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}

	signedIn, err := identity.Identity(r.Context(), returnURL, code, identityVerifier(cookie.Value, state.Nonce))
	if err != nil {
		logging.Warn("OAuth", "Sign-in check failed for server=%s: %v", state.ServerName, err)
		h.renderErrorPage(w, summaryNotCompleted, "")
		return
	}
	// The issuer and raw sub, never a mapped claim such as email: together
	// they are the only stable identifier of a person (OIDC Core §5.7).
	switch {
	case state.SignIn.Subject == "" || state.SignIn.Issuer != signedIn.Issuer:
		logging.Warn("OAuth", "Refusing sign-in to server=%s: the link was issued to an identity of issuer %q, the check signs in at %q",
			state.ServerName, state.SignIn.Issuer, signedIn.Issuer)
		h.renderErrorPage(w, summaryUnconfirmedPerson, "")
		return
	case state.SignIn.Subject != signedIn.Subject:
		logging.Warn("OAuth", "Refusing sign-in to server=%s: the browser signed in as %s, the link was issued to %s",
			state.ServerName, logging.TruncateIdentifier(signedIn.Subject), logging.TruncateIdentifier(state.SignIn.Subject))
		h.renderErrorPage(w, summaryOtherPerson, "")
		return
	}

	bound := h.client.stateStore.Update(stateParam, func(s *OAuthState) {
		s.BrowserBinding = browserBinding(cookie.Value)
	})
	if bound == nil {
		h.renderErrorPage(w, summaryExpiredLink, "")
		return
	}
	// The target is the upstream authorization URL stored server-side with
	// the state; the request only supplies the state lookup key.
	http.Redirect(w, r, bound.AuthorizationURL, http.StatusFound) //nolint:gosec // G710: server-side stored URL, not request input
}

// nonceState encodes a state parameter that names a stored flow by its nonce
// alone, which is all the state stores look a flow up by. It stays well under
// the length limit of an authorization request's state.
func nonceState(nonce string) (string, error) {
	encoded, err := json.Marshal(struct {
		Nonce string `json:"nonce"`
	}{Nonce: nonce})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(encoded), nil
}
