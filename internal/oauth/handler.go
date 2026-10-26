package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/giantswarm/pagekit"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/signinpage"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// Handler provides HTTP handlers for OAuth callback endpoints.
type Handler struct {
	client  *Client
	manager *Manager
	// postLoginRedirectAllowlist bounds the redirect targets the start
	// endpoint accepts from its "redirect" query parameter. Entries are
	// operator-configured absolute URL prefixes; an empty list rejects all
	// redirect requests.
	postLoginRedirectAllowlist []*url.URL
}

// NewHandler creates a new OAuth HTTP handler.
func NewHandler(client *Client) *Handler {
	return &Handler{
		client: client,
	}
}

// SetManager sets the manager reference for callback handling.
// This is called by the Manager after creating the Handler.
func (h *Handler) SetManager(manager *Manager) {
	h.manager = manager
}

// SetPostLoginRedirectAllowlist sets the operator-configured URL prefixes the
// start endpoint accepts as post-login redirect targets. An empty list keeps
// the static success page for every flow.
func (h *Handler) SetPostLoginRedirectAllowlist(prefixes []*url.URL) {
	h.postLoginRedirectAllowlist = prefixes
}

// Summaries of the sign-in error pages. Every error page tells the person to
// start the sign-in again, since no failed step can be retried on its own.
const (
	summaryIncompleteLink     = "The sign-in link is incomplete"
	summaryExpiredLink        = "The sign-in link has expired or was already used"
	summaryInvalidSession     = "The sign-in session is invalid"
	summaryExpiredSession     = "The sign-in session has expired"
	summaryIncompleteResponse = "The sign-in response is incomplete"
	summaryUnverifiedResponse = "The sign-in response could not be verified"
	summaryDenied             = "The sign-in was denied or failed"
	summaryNotCompleted       = "The sign-in could not be completed"
	summaryOtherPerson        = "This sign-in link was issued to someone else"
)

// HandleStart handles the OAuth proxy start endpoint. Auth challenges point
// the browser here; it redirects to the upstream authorization URL stored
// with the flow's state. An optional "redirect" query parameter, validated
// against the operator allowlist, is recorded on the state so a successful
// callback sends the browser there instead of the static success page. An
// unacceptable redirect target is dropped (the login still proceeds).
//
// When muster runs its own OAuth server (api.BrowserIdentity), the browser
// first signs in to muster's identity provider and comes back here with a
// code: the flow continues only for the person it was started for, and only
// in the browser that passed the check (handler_identity.go).
func (h *Handler) HandleStart(w http.ResponseWriter, r *http.Request) {
	identity := api.GetBrowserIdentity()
	if identity != nil && r.URL.Query().Has("code") {
		h.finishIdentityCheck(w, r, identity)
		return
	}

	stateParam := r.URL.Query().Get("state")
	if stateParam == "" {
		logging.Warn("OAuth", "Start endpoint called without state parameter")
		h.renderErrorPage(w, summaryIncompleteLink, "")
		return
	}

	redirectParam := r.URL.Query().Get("redirect")
	acceptedRedirect := ""
	if redirectParam != "" {
		if h.redirectAllowed(redirectParam) {
			acceptedRedirect = redirectParam
		} else {
			logging.Warn("OAuth", "Rejecting post-login redirect target not in allowlist: %q", redirectParam)
		}
	}

	state := h.client.stateStore.Update(stateParam, func(s *OAuthState) {
		if acceptedRedirect != "" {
			s.RedirectURI = acceptedRedirect
		}
	})
	if state == nil {
		// Retrying this link can never succeed: its state is gone, so only a
		// new sign-in (a new link) helps.
		h.renderErrorPage(w, summaryExpiredLink, "")
		return
	}
	if state.AuthorizationURL == "" {
		logging.Warn("OAuth", "State has no authorization URL: nonce=%s", state.Nonce)
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}
	if identity != nil && !boundToBrowser(r, state) {
		h.sendToIdentityCheck(w, r, identity, state)
		return
	}

	// The target is the upstream authorization URL stored server-side with the
	// state by GenerateAuthURL; the request only supplies the state lookup key.
	http.Redirect(w, r, state.AuthorizationURL, http.StatusFound) //nolint:gosec // G710: server-side stored URL, not request input
}

// HandleCallback handles the OAuth callback endpoint.
// This is called by the browser after the user authenticates with the IdP.
//
// The RFC 9207 `iss` check runs before anything else is read from the
// response: an authorization response that does not come from the expected
// authorization server is rejected whole, including its error parameters.
func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// Extract query parameters
	code := r.URL.Query().Get("code")
	stateParam := r.URL.Query().Get("state")
	issParam := r.URL.Query().Get("iss")
	errorParam := r.URL.Query().Get("error")
	errorDesc := r.URL.Query().Get("error_description")

	if stateParam == "" {
		logging.Warn("OAuth", "OAuth callback missing state parameter")
		h.renderErrorPage(w, summaryIncompleteResponse, "")
		return
	}

	// Validate and extract state
	state := h.client.stateStore.ValidateState(stateParam)
	if state == nil {
		// Browsers and IdP redirect pages deliver the callback navigation
		// more than once (double redirect, prefetch); the first delivery
		// consumed the single-use state. A flow that recently completed
		// re-renders its outcome instead of a scary error over a successful
		// sign-in. The code exchange is NOT re-run — the completion record
		// carries no secrets, only what finishSuccess renders.
		if done := h.client.stateStore.Completed(stateParam); done != nil {
			if !h.onFlowCallback(r, done.Issuer) {
				h.renderErrorPage(w, summaryUnverifiedResponse, "")
				return
			}
			logging.Info("OAuth", "Duplicate OAuth callback delivery for completed flow (server=%s) — re-rendering the outcome", done.ServerName)
			h.finishSuccess(w, r, &OAuthState{ServerName: done.ServerName, RedirectURI: done.RedirectURI})
			return
		}
		logging.Warn("OAuth", "OAuth callback with invalid or expired state")
		h.renderErrorPage(w, summaryExpiredSession, "")
		return
	}

	if state.Issuer == "" {
		logging.Warn("OAuth", "Missing issuer in state for nonce=%s", state.Nonce)
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}

	// A callback from a browser that did not pass the start endpoint's
	// sign-in check for this flow was not completed by the person it was
	// started for, as far as muster can tell: its code is never exchanged.
	if api.GetBrowserIdentity() != nil && !boundToBrowser(r, state) {
		logging.Warn("OAuth", "Rejecting OAuth callback for server=%s: the browser did not pass the sign-in check for this flow", state.ServerName)
		h.renderErrorPage(w, summaryUnverifiedResponse, "")
		return
	}

	// The namespace check comes before anything that acts on the response:
	// the iss check (it fetches the flow issuer's metadata), the drop of a
	// registration on an error, and the code exchange. A code that arrives
	// on another namespace's redirect URI was issued for another client, so
	// its flow's token endpoint must never receive it.
	if !h.onFlowCallback(r, state.Issuer) {
		logging.Warn("OAuth", "Rejecting OAuth callback for server=%s: it arrived on %s, not on the redirect URI of its namespace",
			state.ServerName, r.URL.EscapedPath())
		h.renderErrorPage(w, summaryUnverifiedResponse, "")
		return
	}

	if err := h.validateResponseIssuer(r.Context(), state, issParam); err != nil {
		logging.Warn("OAuth", "Rejecting OAuth callback for server=%s: %v", state.ServerName, err)
		h.renderErrorPage(w, summaryUnverifiedResponse, "")
		return
	}

	// The page shows the OAuth error code but not error_description, which
	// may carry provider details.
	if errorParam != "" {
		logging.Warn("OAuth", "OAuth callback received error: %s - %s", errorParam, errorDesc)
		h.client.ForgetRegistrationOnAuthorizationError(state.Issuer, errorParam)
		h.renderErrorPage(w, summaryDenied, errorParam)
		return
	}

	if code == "" {
		logging.Warn("OAuth", "OAuth callback missing code parameter")
		h.renderErrorPage(w, summaryIncompleteResponse, "")
		return
	}

	logging.Debug("OAuth", "Processing OAuth callback for session=%s server=%s issuer=%s",
		logging.TruncateIdentifier(state.SessionID), state.ServerName, state.Issuer)

	if state.SessionID == "" {
		logging.Warn("OAuth", "Missing session ID in state for nonce=%s (possible rolling-upgrade race)", state.Nonce)
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}

	if state.CodeVerifier == "" {
		logging.Warn("OAuth", "Missing code verifier in state for nonce=%s", state.Nonce)
		h.renderErrorPage(w, summaryInvalidSession, "")
		return
	}

	// Detached from the request's cancellation: browsers and IdP redirect
	// pages re-navigate to the callback, aborting the first request while it
	// is still exchanging the code or establishing the session connection.
	// The single-use state is already consumed — the flow can only complete
	// on this request, so it must run to completion even if the client hangs
	// up. Bounded so nothing leaks.
	ctx, cancelFlow := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancelFlow()

	token, err := h.client.ExchangeCode(ctx, code, state.CodeVerifier, state.Issuer, state.Resource, state.Scope)
	if err != nil {
		logging.Error("OAuth", err, "Failed to exchange authorization code")
		h.renderErrorPage(w, summaryNotCompleted, "")
		return
	}

	h.client.StoreToken(state.SessionID, state.UserID, token)

	// Recorded before the completion callback: a duplicate delivery arrives
	// within milliseconds, while the callback is still establishing the
	// session connection.
	h.client.stateStore.MarkCompleted(stateParam, &CompletedFlow{
		ServerName:  state.ServerName,
		RedirectURI: state.RedirectURI,
		Issuer:      state.Issuer,
	})

	logging.Info("OAuth", "Successfully authenticated session=%s server=%s",
		logging.TruncateIdentifier(state.SessionID), state.ServerName)

	if h.manager != nil {
		h.manager.mu.RLock()
		callback := h.manager.authCompletionCallback
		h.manager.mu.RUnlock()

		if callback != nil {
			if err := callback(ctx, state.SessionID, state.UserID, state.ServerName, token.AccessToken); err != nil {
				logging.Warn("OAuth", "Auth completion callback failed for session=%s server=%s: %v",
					logging.TruncateIdentifier(state.SessionID), state.ServerName, err)
			}
		}
	}

	h.finishSuccess(w, r, state)
}

// onFlowCallback reports whether a callback request arrived on the redirect
// URI of the flow filed under issuerKey: the plain callback for muster's
// namespace, the namespace's own callback for a server outside it. The
// namespace comes from the stored state only (the issuer key), never from the
// state parameter the authorization server hands back, and the path is
// compared as it arrived, without decoding. A flow stored before namespaces
// existed carries a plain issuer, so it is a flow of muster's namespace.
func (h *Handler) onFlowCallback(r *http.Request, issuerKey string) bool {
	return r.URL.EscapedPath() == h.client.callbackPathFor(issuerKey)
}

// validateResponseIssuer applies the RFC 9207 check to an authorization
// response. A present `iss` must equal the issuer identifier of the
// authorization server the flow was sent to, by simple string comparison
// against the issuer the server publishes in its own metadata. An absent
// `iss` is refused when the server advertises
// authorization_response_iss_parameter_supported, and accepted otherwise:
// most deployed authorization servers still omit the parameter.
//
// The expected value is the issuer from the server's own metadata. A pin may
// name the identifier the server really sends (IssuerPin.ExpectedIssuer) when
// the pinned identity is not it: several MCPServers pinned to one server under
// different identities keep separate grants, and the server's own identifier
// is what arrives on the callback. When the metadata is not reachable the
// comparison falls back to the issuer recorded with the state. Both of those
// values are operator-configured rather than published by the server, so the
// comparison against them ignores a trailing slash on either side.
func (h *Handler) validateResponseIssuer(ctx context.Context, state *OAuthState, iss string) error {
	metadata, err := h.client.DiscoverMetadata(ctx, state.Issuer)
	if (err != nil || metadata == nil) && iss == "" {
		return fmt.Errorf("no iss on the response and AS metadata for %q is unavailable: %v", state.Issuer, err)
	}

	if iss == "" {
		if metadata.AuthorizationResponseIssParameterSupported {
			return fmt.Errorf("no iss on the response although %q advertises authorization_response_iss_parameter_supported", state.Issuer)
		}
		return nil
	}

	if expected := h.client.expectedResponseIssuer(state.Issuer); expected != "" {
		if strings.TrimSuffix(iss, "/") != strings.TrimSuffix(expected, "/") {
			return fmt.Errorf("iss mismatch: response carried %q, expected %q", iss, expected)
		}
		return nil
	}

	if err == nil && metadata != nil && metadata.Issuer != "" {
		if iss != metadata.Issuer {
			return fmt.Errorf("iss mismatch: response carried %q, expected %q", iss, metadata.Issuer)
		}
		return nil
	}

	// The metadata is unreachable, so the recorded issuer is all there is to
	// compare against. It is operator-configured and may carry a trailing
	// slash the authorization server does not put on its own identifier, so
	// that one difference is ignored here. Nothing else is normalized.
	recorded := api.IssuerURL(state.Issuer)
	if strings.TrimSuffix(iss, "/") != strings.TrimSuffix(recorded, "/") {
		return fmt.Errorf("iss mismatch: response carried %q, expected %q", iss, recorded)
	}
	return nil
}

// renderSuccessPage renders the page shown after signing in to serverName,
// naming the server by its display name where it has one.
func (h *Handler) renderSuccessPage(ctx context.Context, w http.ResponseWriter, serverName string) {
	pagekit.Render(w, http.StatusOK, signinpage.SignedIn(serverDisplayName(ctx, serverName)))
}

// renderErrorPage renders a sign-in error page. errorCode is the error
// parameter of the authorization response, shown when it is a known code.
func (h *Handler) renderErrorPage(w http.ResponseWriter, summary, errorCode string) {
	pagekit.Render(w, http.StatusBadRequest, signinpage.Failed(summary, errorCode))
}

// displayNameTimeout bounds the display name lookup, which reads the
// MCPServer from its definition source while the success page waits.
const displayNameTimeout = 2 * time.Second

// serverDisplayName returns the MCPServer's display name, or name itself when
// the server has none or cannot be read in time.
func serverDisplayName(ctx context.Context, name string) string {
	if app, ok := strings.CutPrefix(name, api.ProviderAppFlowPrefix); ok {
		return app
	}
	manager := api.GetMCPServerManager()
	if manager == nil {
		return name
	}
	ctx, cancel := context.WithTimeout(ctx, displayNameTimeout)
	defer cancel()
	info, err := manager.GetMCPServer(ctx, name)
	if err != nil || info == nil {
		return name
	}
	if displayName := strings.TrimSpace(info.DisplayName); displayName != "" {
		return displayName
	}
	return name
}

// ServeHTTP implements http.Handler for the OAuth handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.HandleCallback(w, r)
}

// ServeCIMD handles GET requests to serve the Client ID Metadata Document (CIMD).
// This allows muster to self-host its own CIMD without requiring external static hosting.
// The CIMD is dynamically generated from the OAuth configuration.
func (h *Handler) ServeCIMD(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get the CIMD from the client, which includes configurable scopes
	cimd := h.client.GetClientMetadata()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour
	w.Header().Set("Access-Control-Allow-Origin", "*")      // Allow cross-origin requests

	if err := json.NewEncoder(w).Encode(cimd); err != nil {
		logging.Error("OAuth", err, "Failed to encode CIMD")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logging.Debug("OAuth", "Served CIMD for client_id=%s", cimd.ClientID)
}
