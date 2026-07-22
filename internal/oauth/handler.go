package oauth

import (
	"bytes"
	"embed"
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"

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

// redirectAllowed reports whether a caller-supplied post-login redirect
// target matches the operator allowlist: absolute http(s), no userinfo,
// scheme/host equal to an entry, and the entry's path extended at a segment
// boundary. Matching uses the escaped path form so percent-encoded slashes
// cannot fake a boundary, and dot segments are rejected outright: browsers
// resolve them before navigation, which would void the path constraint. The
// target's query is unconstrained so front-ends can carry their own
// correlation state.
func (h *Handler) redirectAllowed(raw string) bool {
	target, err := url.Parse(raw)
	if err != nil || target.User != nil || target.Host == "" ||
		(target.Scheme != "https" && target.Scheme != "http") {
		return false
	}
	targetPath := target.EscapedPath()
	if hasDotSegment(targetPath) {
		return false
	}
	for _, entry := range h.postLoginRedirectAllowlist {
		if target.Scheme == entry.Scheme && target.Host == entry.Host &&
			pathExtendsPrefix(targetPath, entry.EscapedPath()) {
			return true
		}
	}
	return false
}

// hasDotSegment reports whether an escaped URL path contains a "." or ".."
// segment, in plain or %2e-encoded form (browsers treat "%2e%2e" and mixed
// spellings as dot segments during navigation).
func hasDotSegment(escapedPath string) bool {
	for segment := range strings.SplitSeq(escapedPath, "/") {
		decoded := strings.ReplaceAll(strings.ToLower(segment), "%2e", ".")
		if decoded == "." || decoded == ".." {
			return true
		}
	}
	return false
}

// pathExtendsPrefix reports whether an escaped target path equals an escaped
// entry path or extends it at a segment boundary: entry "/connectors"
// matches "/connectors" and "/connectors/complete" but not "/connectorsevil".
// An entry path of "" or "/" admits every path on the entry's host.
func pathExtendsPrefix(targetPath, entryPath string) bool {
	entryPath = strings.TrimSuffix(entryPath, "/")
	if entryPath == "" {
		return true
	}
	return targetPath == entryPath || strings.HasPrefix(targetPath, entryPath+"/")
}

// HandleCallback handles the OAuth callback endpoint.
// This is called by the browser after the user authenticates with the IdP.
func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// Extract query parameters
	code := r.URL.Query().Get("code")
	stateParam := r.URL.Query().Get("state")
	errorParam := r.URL.Query().Get("error")
	errorDesc := r.URL.Query().Get("error_description")

	// Handle OAuth errors - use generic message to avoid leaking sensitive info
	if errorParam != "" {
		logging.Warn("OAuth", "OAuth callback received error: %s - %s", errorParam, errorDesc)
		h.renderErrorPage(w, "Authentication was denied or failed. Please try again.")
		return
	}

	// Validate required parameters
	if code == "" || stateParam == "" {
		logging.Warn("OAuth", "OAuth callback missing code or state parameter")
		h.renderErrorPage(w, "Invalid callback: missing required parameters")
		return
	}

	// Validate and extract state
	state := h.client.stateStore.ValidateState(stateParam)
	if state == nil {
		logging.Warn("OAuth", "OAuth callback with invalid or expired state")
		h.renderErrorPage(w, "Authentication session expired. Please try again.")
		return
	}

	logging.Debug("OAuth", "Processing OAuth callback for session=%s server=%s issuer=%s",
		logging.TruncateIdentifier(state.SessionID), state.ServerName, state.Issuer)

	if state.SessionID == "" {
		logging.Warn("OAuth", "Missing session ID in state for nonce=%s (possible rolling-upgrade race)", state.Nonce)
		h.renderErrorPage(w, "Authentication session invalid. Please try again.")
		return
	}

	if state.Issuer == "" {
		logging.Warn("OAuth", "Missing issuer in state for nonce=%s", state.Nonce)
		h.renderErrorPage(w, "Authentication session invalid. Please try again.")
		return
	}
	if state.CodeVerifier == "" {
		logging.Warn("OAuth", "Missing code verifier in state for nonce=%s", state.Nonce)
		h.renderErrorPage(w, "Authentication session invalid. Please try again.")
		return
	}

	token, err := h.client.ExchangeCode(r.Context(), code, state.CodeVerifier, state.Issuer)
	if err != nil {
		logging.Error("OAuth", err, "Failed to exchange authorization code")
		h.renderErrorPage(w, "Failed to complete authentication. Please try again.")
		return
	}

	h.client.StoreToken(state.SessionID, state.UserID, token)

	logging.Info("OAuth", "Successfully authenticated session=%s server=%s",
		logging.TruncateIdentifier(state.SessionID), state.ServerName)

	if h.manager != nil {
		h.manager.mu.RLock()
		callback := h.manager.authCompletionCallback
		h.manager.mu.RUnlock()

		if callback != nil {
			if err := callback(r.Context(), state.SessionID, state.UserID, state.ServerName, token.AccessToken); err != nil {
				logging.Warn("OAuth", "Auth completion callback failed for session=%s server=%s: %v",
					logging.TruncateIdentifier(state.SessionID), state.ServerName, err)
			}
		}
	}

	h.finishSuccess(w, r, state)
}

// finishSuccess completes a successful callback: a redirect to the flow's
// recorded post-login target when the start endpoint accepted one, the
// static success page otherwise. The target was allowlist-validated at the
// start endpoint; the callback never reads it from request input.
func (h *Handler) finishSuccess(w http.ResponseWriter, r *http.Request, state *OAuthState) {
	if state.RedirectURI != "" {
		target, err := url.Parse(state.RedirectURI)
		if err == nil {
			http.Redirect(w, r, postLoginRedirectTarget(target, state.ServerName), http.StatusSeeOther) //nolint:gosec // G710: allowlist-validated at the start endpoint, stored server-side
			return
		}
		logging.Warn("OAuth", "Ignoring unparseable post-login redirect target: %v", err)
	}
	h.renderSuccessPage(w, state.ServerName)
}

// postLoginRedirectTarget appends the connected server's name to the
// post-login redirect URL, preserving any query parameters already on it
// (front-ends carry their own correlation state there).
func postLoginRedirectTarget(base *url.URL, serverName string) string {
	target := *base
	query := target.Query()
	query.Set("server", serverName)
	target.RawQuery = query.Encode()
	return target.String()
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
