package oauth

import (
	"bytes"
	"embed"
	"encoding/json"
	"html"
	"html/template"
	"net/http"

	"muster/pkg/logging"
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
		state.SessionID, state.ServerName, state.Issuer)

	// Validate we have the required data stored with the state
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

	// Exchange the authorization code for tokens
	token, err := h.client.ExchangeCode(r.Context(), code, state.CodeVerifier, state.Issuer)
	if err != nil {
		logging.Error("OAuth", err, "Failed to exchange authorization code")
		h.renderErrorPage(w, "Failed to complete authentication. Please try again.")
		return
	}

	// Store the token
	h.client.StoreToken(state.SessionID, token)

	logging.Info("OAuth", "Successfully authenticated session=%s server=%s",
		state.SessionID, state.ServerName)

	// Call the auth completion callback to establish session connection
	if h.manager != nil {
		h.manager.mu.RLock()
		callback := h.manager.authCompletionCallback
		h.manager.mu.RUnlock()

		if callback != nil {
			if err := callback(r.Context(), state.SessionID, state.ServerName, token.AccessToken); err != nil {
				// Log the error but don't fail the OAuth flow - the token is already stored
				// and can be used on the next request
				logging.Warn("OAuth", "Auth completion callback failed for session=%s server=%s: %v",
					state.SessionID, state.ServerName, err)
			}
		}
	}

	// Render success page
	h.renderSuccessPage(w, state.ServerName)
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
	w.Write(buf.Bytes())
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
	w.Write(buf.Bytes())
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
