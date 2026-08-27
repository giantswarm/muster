package oauth

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

//go:embed templates/*.html
var templateFS embed.FS

// Parsed templates - initialized once at package load
var (
	successTemplate *template.Template
	errorTemplate   *template.Template
)

func init() {
	var err error
	successTemplate, err = template.ParseFS(templateFS, "templates/success.html")
	if err != nil {
		panic("failed to parse success template: " + err.Error())
	}
	errorTemplate, err = template.ParseFS(templateFS, "templates/error.html")
	if err != nil {
		panic("failed to parse error template: " + err.Error())
	}
}

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

// HandleStart handles the OAuth proxy start endpoint. Auth challenges point
// the browser here; it redirects to the upstream authorization URL stored
// with the flow's state. An optional "redirect" query parameter, validated
// against the operator allowlist, is recorded on the state so a successful
// callback sends the browser there instead of the static success page. An
// unacceptable redirect target is dropped (the login still proceeds).
func (h *Handler) HandleStart(w http.ResponseWriter, r *http.Request) {
	stateParam := r.URL.Query().Get("state")
	if stateParam == "" {
		logging.Warn("OAuth", "Start endpoint called without state parameter")
		h.renderErrorPage(w, "Invalid sign-in link: missing required parameters")
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
		h.renderErrorPage(w, "Authentication session expired. Please try again.")
		return
	}
	if state.AuthorizationURL == "" {
		logging.Warn("OAuth", "State has no authorization URL: nonce=%s", state.Nonce)
		h.renderErrorPage(w, "Authentication session invalid. Please try again.")
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
		h.renderErrorPage(w, "Invalid callback: missing required parameters")
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
			logging.Info("OAuth", "Duplicate OAuth callback delivery for completed flow (server=%s) — re-rendering the outcome", done.ServerName)
			h.finishSuccess(w, r, &OAuthState{ServerName: done.ServerName, RedirectURI: done.RedirectURI})
			return
		}
		logging.Warn("OAuth", "OAuth callback with invalid or expired state")
		h.renderErrorPage(w, "Authentication session expired. Please try again.")
		return
	}

	if state.Issuer == "" {
		logging.Warn("OAuth", "Missing issuer in state for nonce=%s", state.Nonce)
		h.renderErrorPage(w, "Authentication session invalid. Please try again.")
		return
	}

	if err := h.validateResponseIssuer(r.Context(), state, issParam); err != nil {
		logging.Warn("OAuth", "Rejecting OAuth callback for server=%s: %v", state.ServerName, err)
		h.renderErrorPage(w, "Authentication response could not be verified. Please try again.")
		return
	}

	// Handle OAuth errors - use generic message to avoid leaking sensitive info
	if errorParam != "" {
		logging.Warn("OAuth", "OAuth callback received error: %s - %s", errorParam, errorDesc)
		h.renderErrorPage(w, "Authentication was denied or failed. Please try again.")
		return
	}

	if code == "" {
		logging.Warn("OAuth", "OAuth callback missing code parameter")
		h.renderErrorPage(w, "Invalid callback: missing required parameters")
		return
	}

	logging.Debug("OAuth", "Processing OAuth callback for session=%s server=%s issuer=%s",
		logging.TruncateIdentifier(state.SessionID), state.ServerName, state.Issuer)

	if state.SessionID == "" {
		logging.Warn("OAuth", "Missing session ID in state for nonce=%s (possible rolling-upgrade race)", state.Nonce)
		h.renderErrorPage(w, "Authentication session invalid. Please try again.")
		return
	}

	if state.CodeVerifier == "" {
		logging.Warn("OAuth", "Missing code verifier in state for nonce=%s", state.Nonce)
		h.renderErrorPage(w, "Authentication session invalid. Please try again.")
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

	token, err := h.client.ExchangeCode(ctx, code, state.CodeVerifier, state.Issuer, state.Resource)
	if err != nil {
		logging.Error("OAuth", err, "Failed to exchange authorization code")
		h.renderErrorPage(w, "Failed to complete authentication. Please try again.")
		return
	}

	h.client.StoreToken(state.SessionID, state.UserID, token)

	// Recorded before the completion callback: a duplicate delivery arrives
	// within milliseconds, while the callback is still establishing the
	// session connection.
	h.client.stateStore.MarkCompleted(stateParam, &CompletedFlow{
		ServerName:  state.ServerName,
		RedirectURI: state.RedirectURI,
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

// validateResponseIssuer applies the RFC 9207 check to an authorization
// response. A present `iss` must equal the issuer identifier of the
// authorization server the flow was sent to, by simple string comparison
// against the issuer the server publishes in its own metadata. An absent
// `iss` is refused when the server advertises
// authorization_response_iss_parameter_supported, and accepted otherwise:
// most deployed authorization servers still omit the parameter.
//
// The expected value is the issuer from the server's own metadata. When the
// metadata is not reachable the comparison falls back to the issuer recorded
// with the state, and then ignores a trailing slash on either side, because
// that value is operator-configured rather than published by the server.
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
	if strings.TrimSuffix(iss, "/") != strings.TrimSuffix(state.Issuer, "/") {
		return fmt.Errorf("iss mismatch: response carried %q, expected %q", iss, state.Issuer)
	}
	return nil
}

// setSecurityHeaders sets recommended security headers for HTML responses.
// These headers help prevent XSS, clickjacking, and MIME sniffing attacks.
func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
}

// templateData holds data for HTML template rendering.
type templateData struct {
	ServerName string
	Message    string
}

// renderSuccessPage renders an HTML page indicating successful authentication.
func (h *Handler) renderSuccessPage(w http.ResponseWriter, serverName string) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Escape server name to prevent XSS attacks
	data := templateData{
		ServerName: html.EscapeString(serverName),
	}

	var buf bytes.Buffer
	if err := successTemplate.Execute(&buf, data); err != nil {
		logging.Error("OAuth", err, "Failed to render success template")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// renderErrorPage renders an HTML page indicating an authentication error.
func (h *Handler) renderErrorPage(w http.ResponseWriter, message string) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Escape message to prevent XSS attacks
	data := templateData{
		Message: html.EscapeString(message),
	}

	var buf bytes.Buffer
	if err := errorTemplate.Execute(&buf, data); err != nil {
		logging.Error("OAuth", err, "Failed to render error template")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write(buf.Bytes())
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
