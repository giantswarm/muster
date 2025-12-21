package oauth

import (
	"fmt"
	"html"
	"net/http"

	"muster/pkg/logging"
)

// Handler provides HTTP handlers for OAuth callback endpoints.
type Handler struct {
	client *Client
}

// NewHandler creates a new OAuth HTTP handler.
func NewHandler(client *Client) *Handler {
	return &Handler{
		client: client,
	}
}

// HandleCallback handles the OAuth callback endpoint.
// This is called by the browser after the user authenticates with the IdP.
func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// Extract query parameters
	code := r.URL.Query().Get("code")
	stateParam := r.URL.Query().Get("state")
	errorParam := r.URL.Query().Get("error")
	errorDesc := r.URL.Query().Get("error_description")

	// Handle OAuth errors
	if errorParam != "" {
		logging.Warn("OAuth", "OAuth callback received error: %s - %s", errorParam, errorDesc)
		h.renderErrorPage(w, fmt.Sprintf("Authentication failed: %s", errorDesc))
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

// renderSuccessPage renders an HTML page indicating successful authentication.
func (h *Handler) renderSuccessPage(w http.ResponseWriter, serverName string) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// Escape server name to prevent XSS attacks
	safeServerName := html.EscapeString(serverName)

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Authentication Successful - Muster</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            background: linear-gradient(135deg, #1a1a2e 0%%, #16213e 50%%, #0f3460 100%%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            color: #e8e8e8;
        }
        .container {
            text-align: center;
            padding: 3rem;
            background: rgba(255, 255, 255, 0.05);
            border-radius: 16px;
            border: 1px solid rgba(255, 255, 255, 0.1);
            backdrop-filter: blur(10px);
            max-width: 500px;
            margin: 1rem;
        }
        .checkmark {
            width: 80px;
            height: 80px;
            margin: 0 auto 1.5rem;
            background: linear-gradient(135deg, #00d4aa 0%%, #00a896 100%%);
            border-radius: 50%%;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 2.5rem;
        }
        h1 {
            font-size: 1.75rem;
            font-weight: 600;
            margin-bottom: 0.5rem;
            color: #fff;
        }
        .server-name {
            color: #00d4aa;
            font-weight: 500;
        }
        p {
            color: #a0a0a0;
            line-height: 1.6;
            margin-top: 1rem;
        }
        .footer {
            margin-top: 2rem;
            padding-top: 1.5rem;
            border-top: 1px solid rgba(255, 255, 255, 0.1);
            font-size: 0.875rem;
            color: #666;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="checkmark">✓</div>
        <h1>Authentication Successful</h1>
        <p>You have been authenticated to <span class="server-name">%s</span>.</p>
        <p>You can now close this window and return to your IDE.</p>
        <p>Retry the previous command to continue.</p>
        <div class="footer">
            Powered by Muster
        </div>
    </div>
</body>
</html>`, safeServerName)

	w.Write([]byte(htmlContent))
}

// renderErrorPage renders an HTML page indicating an authentication error.
func (h *Handler) renderErrorPage(w http.ResponseWriter, message string) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)

	// Escape message to prevent XSS attacks
	safeMessage := html.EscapeString(message)

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Authentication Failed - Muster</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            background: linear-gradient(135deg, #1a1a2e 0%%, #16213e 50%%, #0f3460 100%%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            color: #e8e8e8;
        }
        .container {
            text-align: center;
            padding: 3rem;
            background: rgba(255, 255, 255, 0.05);
            border-radius: 16px;
            border: 1px solid rgba(255, 255, 255, 0.1);
            backdrop-filter: blur(10px);
            max-width: 500px;
            margin: 1rem;
        }
        .error-icon {
            width: 80px;
            height: 80px;
            margin: 0 auto 1.5rem;
            background: linear-gradient(135deg, #ff6b6b 0%%, #ee5a5a 100%%);
            border-radius: 50%%;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 2.5rem;
        }
        h1 {
            font-size: 1.75rem;
            font-weight: 600;
            margin-bottom: 0.5rem;
            color: #fff;
        }
        .message {
            color: #ff6b6b;
            font-weight: 500;
            margin-top: 1rem;
        }
        p {
            color: #a0a0a0;
            line-height: 1.6;
            margin-top: 1rem;
        }
        .footer {
            margin-top: 2rem;
            padding-top: 1.5rem;
            border-top: 1px solid rgba(255, 255, 255, 0.1);
            font-size: 0.875rem;
            color: #666;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="error-icon">✕</div>
        <h1>Authentication Failed</h1>
        <p class="message">%s</p>
        <p>Please return to your IDE and try again.</p>
        <div class="footer">
            Powered by Muster
        </div>
    </div>
</body>
</html>`, safeMessage)

	w.Write([]byte(htmlContent))
}

// ServeHTTP implements http.Handler for the OAuth handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.HandleCallback(w, r)
}
