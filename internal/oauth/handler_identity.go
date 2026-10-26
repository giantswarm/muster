package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// sendToIdentityCheck sends the browser to sign in to muster's identity
// provider before the server's sign-in, so the start endpoint learns who is
// in the browser. The browser comes back to the start endpoint with code and
// a state naming the flow by its nonce (finishIdentityCheck).
func (h *Handler) sendToIdentityCheck(w http.ResponseWriter, r *http.Request, identity api.BrowserIdentity, stateParam string, state *OAuthState) {
	verifier := rand.Text() + rand.Text()
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if h.client.stateStore.Update(stateParam, func(s *OAuthState) { s.BrowserVerifier = verifier }) == nil {
		h.renderErrorPage(w, summaryExpiredLink, "")
		return
	}
	identityState, err := nonceState(state.Nonce)
	if err != nil {
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	signInURL, err := identity.SignInURL(r.Context(), identityState, challenge)
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
// elicitation requires binding the sign-in to that user). A link opened by
// someone else is refused and stays usable by its owner.
func (h *Handler) finishIdentityCheck(w http.ResponseWriter, r *http.Request, identity api.BrowserIdentity) {
	code := r.URL.Query().Get("code")
	stateParam := r.URL.Query().Get("state")

	state := h.client.stateStore.Update(stateParam, func(*OAuthState) {})
	if state == nil {
		h.renderErrorPage(w, summaryExpiredLink, "")
		return
	}
	if state.BrowserVerifier == "" {
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}

	subject, err := identity.Subject(r.Context(), code, state.BrowserVerifier)
	if err != nil {
		logging.Warn("OAuth", "Sign-in check failed for server=%s: %v", state.ServerName, err)
		h.renderErrorPage(w, summaryNotCompleted, "")
		return
	}
	if state.UserID == "" || subject != state.UserID {
		logging.Warn("OAuth", "Refusing sign-in to server=%s: the browser signed in as %s, the link was issued to %s",
			state.ServerName, logging.TruncateIdentifier(subject), logging.TruncateIdentifier(state.UserID))
		h.renderErrorPage(w, summaryOtherPerson, "")
		return
	}

	verified := h.client.stateStore.Update(stateParam, func(s *OAuthState) {
		s.BrowserVerified = true
		s.BrowserVerifier = ""
	})
	if verified == nil {
		h.renderErrorPage(w, summaryExpiredLink, "")
		return
	}
	// The target is the upstream authorization URL stored server-side with
	// the state; the request only supplies the state lookup key.
	http.Redirect(w, r, verified.AuthorizationURL, http.StatusFound) //nolint:gosec // G710: server-side stored URL, not request input
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
