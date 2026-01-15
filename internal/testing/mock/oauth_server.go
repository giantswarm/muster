package mock

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// OAuthServerConfig configures the mock OAuth server behavior
type OAuthServerConfig struct {
	// Issuer is the OAuth issuer identifier (e.g., "http://localhost:9999")
	Issuer string

	// AcceptedScopes lists scopes the server will accept
	AcceptedScopes []string

	// TokenLifetime is how long tokens remain valid
	TokenLifetime time.Duration

	// SimulateErrors can be set to simulate various error conditions
	SimulateErrors *OAuthErrorSimulation

	// Debug enables debug logging
	Debug bool

	// PKCERequired enforces PKCE flow
	PKCERequired bool

	// AutoApprove skips user consent in tests
	AutoApprove bool

	// ClientID is the expected OAuth client ID
	ClientID string

	// ClientSecret is the expected OAuth client secret (optional)
	ClientSecret string

	// Clock is the clock to use for time operations (defaults to RealClock)
	// Set this to a MockClock for testing token expiry without waiting
	Clock Clock
}

// OAuthErrorSimulation allows simulating error conditions
type OAuthErrorSimulation struct {
	// TokenEndpointError returns this error from /token
	TokenEndpointError string

	// AuthorizeEndpointDelay adds delay to /authorize
	AuthorizeEndpointDelay time.Duration

	// InvalidGrant rejects all token exchanges
	InvalidGrant bool

	// InvalidToken rejects all token validations
	InvalidToken bool
}

// OAuthServer is a mock OAuth 2.1 authorization server
type OAuthServer struct {
	config     OAuthServerConfig
	httpServer *http.Server
	listener   net.Listener
	port       int
	running    bool
	mu         sync.RWMutex

	// State tracking
	authCodes    map[string]*authCodeEntry // code -> entry
	issuedTokens map[string]*issuedToken   // access_token -> token info

	// clock is the clock used for time operations
	clock Clock
}

type authCodeEntry struct {
	ClientID        string
	RedirectURI     string
	Scope           string
	State           string
	CodeChallenge   string
	ChallengeMethod string
	CreatedAt       time.Time
}

type issuedToken struct {
	AccessToken  string
	RefreshToken string
	Scope        string
	ClientID     string
	ExpiresAt    time.Time
}

// TokenResponse is the OAuth token response
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
}

// NewOAuthServer creates a new mock OAuth server
func NewOAuthServer(config OAuthServerConfig) *OAuthServer {
	if config.TokenLifetime == 0 {
		config.TokenLifetime = 1 * time.Hour
	}
	if len(config.AcceptedScopes) == 0 {
		config.AcceptedScopes = []string{"openid", "profile", "email"}
	}
	if config.ClientID == "" {
		config.ClientID = "test-client"
	}

	// Use the provided clock or default to RealClock
	clock := config.Clock
	if clock == nil {
		clock = RealClock{}
	}

	return &OAuthServer{
		config:       config,
		authCodes:    make(map[string]*authCodeEntry),
		issuedTokens: make(map[string]*issuedToken),
		clock:        clock,
	}
}

// Start starts the OAuth server on a random available port
func (s *OAuthServer) Start(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return s.port, nil
	}

	// Listen on random port
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("failed to listen: %w", err)
	}

	s.listener = listener
	s.port = listener.Addr().(*net.TCPAddr).Port

	// Update issuer with actual port if it's a placeholder
	if s.config.Issuer == "" {
		s.config.Issuer = fmt.Sprintf("http://localhost:%d", s.port)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleMetadata)
	mux.HandleFunc("/.well-known/openid-configuration", s.handleMetadata)
	mux.HandleFunc("/authorize", s.handleAuthorize)
	mux.HandleFunc("/token", s.handleToken)
	mux.HandleFunc("/userinfo", s.handleUserInfo)
	mux.HandleFunc("/jwks", s.handleJWKS)
	mux.HandleFunc("/callback", s.handleCallback)

	s.httpServer = &http.Server{Handler: mux}

	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			if s.config.Debug {
				fmt.Fprintf(os.Stderr, "OAuth server error: %v\n", err)
			}
		}
	}()

	s.running = true

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Mock OAuth server started on port %d (issuer: %s)\n", s.port, s.config.Issuer)
	}

	return s.port, nil
}

// Stop stops the OAuth server
func (s *OAuthServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Stopping mock OAuth server on port %d\n", s.port)
	}

	err := s.httpServer.Shutdown(ctx)
	s.running = false
	return err
}

// Port returns the port the server is listening on
func (s *OAuthServer) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// IsRunning returns whether the server is currently running
func (s *OAuthServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// GetIssuerURL returns the full issuer URL
func (s *OAuthServer) GetIssuerURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Issuer
}

// GetMetadataURL returns the OAuth metadata URL
func (s *OAuthServer) GetMetadataURL() string {
	return s.GetIssuerURL() + "/.well-known/oauth-authorization-server"
}

// GetAuthorizeURL returns the authorization endpoint URL
func (s *OAuthServer) GetAuthorizeURL() string {
	return s.GetIssuerURL() + "/authorize"
}

// GetTokenURL returns the token endpoint URL
func (s *OAuthServer) GetTokenURL() string {
	return s.GetIssuerURL() + "/token"
}

// GenerateAuthCode generates an authorization code for testing
// This simulates a user completing the OAuth flow
func (s *OAuthServer) GenerateAuthCode(clientID, redirectURI, scope, state, codeChallenge, codeChallengeMethod string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	code := generateOpaqueToken()

	s.authCodes[code] = &authCodeEntry{
		ClientID:        clientID,
		RedirectURI:     redirectURI,
		Scope:           scope,
		State:           state,
		CodeChallenge:   codeChallenge,
		ChallengeMethod: codeChallengeMethod,
		CreatedAt:       s.clock.Now(),
	}

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Generated auth code for client %s: %s\n", clientID, code[:16]+"...")
	}

	return code
}

// SimulateCallback simulates a user completing OAuth flow
// This is called by tests to complete authentication without a real browser
func (s *OAuthServer) SimulateCallback(code string) (*TokenResponse, error) {
	s.mu.RLock()
	entry, exists := s.authCodes[code]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("invalid authorization code")
	}

	// Exchange code for tokens
	accessToken := s.generateAccessToken(entry.ClientID, entry.Scope)
	refreshToken := generateOpaqueToken()

	token := &issuedToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scope:        entry.Scope,
		ClientID:     entry.ClientID,
		ExpiresAt:    s.clock.Now().Add(s.config.TokenLifetime),
	}

	s.mu.Lock()
	s.issuedTokens[accessToken] = token
	delete(s.authCodes, code)
	s.mu.Unlock()

	return &TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.config.TokenLifetime.Seconds()),
		Scope:        entry.Scope,
	}, nil
}

// ValidateToken checks if a token is valid
func (s *OAuthServer) ValidateToken(accessToken string) bool {
	if s.config.SimulateErrors != nil && s.config.SimulateErrors.InvalidToken {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	token, exists := s.issuedTokens[accessToken]
	if !exists {
		return false
	}

	return s.clock.Now().Before(token.ExpiresAt)
}

// GetTokenInfo returns information about a token
func (s *OAuthServer) GetTokenInfo(accessToken string) *issuedToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.issuedTokens[accessToken]
}

// handleMetadata returns OAuth 2.1 server metadata
func (s *OAuthServer) handleMetadata(w http.ResponseWriter, r *http.Request) {
	metadata := map[string]interface{}{
		"issuer":                                s.config.Issuer,
		"authorization_endpoint":                s.config.Issuer + "/authorize",
		"token_endpoint":                        s.config.Issuer + "/token",
		"userinfo_endpoint":                     s.config.Issuer + "/userinfo",
		"jwks_uri":                              s.config.Issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                      s.config.AcceptedScopes,
		"code_challenge_methods_supported":      []string{"S256", "plain"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metadata)
}

// handleAuthorize handles authorization requests
func (s *OAuthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.config.SimulateErrors != nil && s.config.SimulateErrors.AuthorizeEndpointDelay > 0 {
		time.Sleep(s.config.SimulateErrors.AuthorizeEndpointDelay)
	}

	clientID := r.URL.Query().Get("client_id")
	redirectURI := r.URL.Query().Get("redirect_uri")
	scope := r.URL.Query().Get("scope")
	state := r.URL.Query().Get("state")
	codeChallenge := r.URL.Query().Get("code_challenge")
	codeChallengeMethod := r.URL.Query().Get("code_challenge_method")
	responseType := r.URL.Query().Get("response_type")

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Authorization request: client_id=%s, redirect_uri=%s, scope=%s\n",
			clientID, redirectURI, scope)
	}

	// Validate response type
	if responseType != "code" {
		http.Error(w, "unsupported_response_type", http.StatusBadRequest)
		return
	}

	// Validate client ID
	if s.config.ClientID != "" && clientID != s.config.ClientID {
		http.Error(w, "invalid_client", http.StatusBadRequest)
		return
	}

	// Check PKCE requirement
	if s.config.PKCERequired && codeChallenge == "" {
		http.Error(w, "PKCE required: code_challenge missing", http.StatusBadRequest)
		return
	}

	// Generate authorization code
	code := s.GenerateAuthCode(clientID, redirectURI, scope, state, codeChallenge, codeChallengeMethod)

	if s.config.AutoApprove {
		// Auto-redirect with code (simulating user approval)
		redirectURL, err := url.Parse(redirectURI)
		if err != nil {
			http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
			return
		}

		q := redirectURL.Query()
		q.Set("code", code)
		if state != "" {
			q.Set("state", state)
		}
		redirectURL.RawQuery = q.Encode()

		if s.config.Debug {
			fmt.Fprintf(os.Stderr, "🔐 Auto-approving and redirecting to: %s\n", redirectURL.String())
		}

		http.Redirect(w, r, redirectURL.String(), http.StatusFound)
		return
	}

	// Return HTML page for manual testing (shows code for test to capture)
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Mock OAuth Server</title></head>
<body>
<h1>Mock OAuth Server Authorization</h1>
<p>Client ID: <code>%s</code></p>
<p>Requested Scopes: <code>%s</code></p>
<p>Authorization Code: <code id="code">%s</code></p>
<p>State: <code>%s</code></p>
<form action="%s" method="GET">
<input type="hidden" name="code" value="%s">
<input type="hidden" name="state" value="%s">
<button type="submit">Authorize</button>
</form>
</body>
</html>`, clientID, scope, code, state, redirectURI, code, state)
}

// handleToken handles token exchange requests
func (s *OAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	grantType := r.FormValue("grant_type")

	if s.config.SimulateErrors != nil {
		if s.config.SimulateErrors.TokenEndpointError != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error":             "server_error",
				"error_description": s.config.SimulateErrors.TokenEndpointError,
			})
			return
		}
		if s.config.SimulateErrors.InvalidGrant {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error":             "invalid_grant",
				"error_description": "authorization code is invalid",
			})
			return
		}
	}

	switch grantType {
	case "authorization_code":
		s.handleAuthCodeExchange(w, r)
	case "refresh_token":
		s.handleRefreshToken(w, r)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "unsupported_grant_type",
			"error_description": fmt.Sprintf("grant_type %s not supported", grantType),
		})
	}
}

func (s *OAuthServer) handleAuthCodeExchange(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	codeVerifier := r.FormValue("code_verifier")
	clientID := r.FormValue("client_id")

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Token exchange request: code=%s..., client_id=%s\n",
			code[:min(16, len(code))], clientID)
	}

	s.mu.Lock()
	entry, exists := s.authCodes[code]
	if exists {
		delete(s.authCodes, code)
	}
	s.mu.Unlock()

	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "authorization code not found or expired",
		})
		return
	}

	// PKCE verification
	if entry.CodeChallenge != "" {
		if codeVerifier == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error":             "invalid_grant",
				"error_description": "code_verifier required",
			})
			return
		}

		// Verify the code verifier
		if !s.verifyPKCE(entry.CodeChallenge, entry.ChallengeMethod, codeVerifier) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error":             "invalid_grant",
				"error_description": "code_verifier verification failed",
			})
			return
		}
	}

	// Issue tokens
	accessToken := s.generateAccessToken(entry.ClientID, entry.Scope)
	refreshToken := generateOpaqueToken()

	token := &issuedToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scope:        entry.Scope,
		ClientID:     entry.ClientID,
		ExpiresAt:    s.clock.Now().Add(s.config.TokenLifetime),
	}

	s.mu.Lock()
	s.issuedTokens[accessToken] = token
	s.mu.Unlock()

	response := TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.config.TokenLifetime.Seconds()),
		Scope:        entry.Scope,
	}

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Issued tokens for client %s\n", entry.ClientID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *OAuthServer) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.FormValue("refresh_token")

	// Find the token by refresh token
	var originalToken *issuedToken
	s.mu.RLock()
	for _, token := range s.issuedTokens {
		if token.RefreshToken == refreshToken {
			originalToken = token
			break
		}
	}
	s.mu.RUnlock()

	if originalToken == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "refresh token not found",
		})
		return
	}

	// Generate new tokens
	newAccessToken := s.generateAccessToken(originalToken.ClientID, originalToken.Scope)
	newRefreshToken := generateOpaqueToken()

	newToken := &issuedToken{
		AccessToken:  newAccessToken,
		RefreshToken: newRefreshToken,
		Scope:        originalToken.Scope,
		ClientID:     originalToken.ClientID,
		ExpiresAt:    s.clock.Now().Add(s.config.TokenLifetime),
	}

	s.mu.Lock()
	// Remove old token
	delete(s.issuedTokens, originalToken.AccessToken)
	// Add new token
	s.issuedTokens[newAccessToken] = newToken
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TokenResponse{
		AccessToken:  newAccessToken,
		RefreshToken: newRefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.config.TokenLifetime.Seconds()),
		Scope:        originalToken.Scope,
	})
}

func (s *OAuthServer) handleUserInfo(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if auth == "" || len(auth) < 7 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	token := auth[7:] // Remove "Bearer "
	if !s.ValidateToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	userInfo := map[string]interface{}{
		"sub":   "test-user-123",
		"name":  "Test User",
		"email": "test@example.com",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userInfo)
}

func (s *OAuthServer) handleJWKS(w http.ResponseWriter, r *http.Request) {
	// Return empty JWKS (we use opaque tokens for testing simplicity)
	jwks := map[string]interface{}{
		"keys": []interface{}{},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jwks)
}

func (s *OAuthServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	// This endpoint is used by the test framework to receive callbacks
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errorParam := r.URL.Query().Get("error")

	if errorParam != "" {
		errorDesc := r.URL.Query().Get("error_description")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>OAuth Error</title></head>
<body>
<h1>OAuth Error</h1>
<p>Error: %s</p>
<p>Description: %s</p>
</body>
</html>`, errorParam, errorDesc)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>OAuth Success</title></head>
<body>
<h1>OAuth Authorization Successful</h1>
<p>You have successfully authorized the application.</p>
<p>Code: <code>%s</code></p>
<p>State: <code>%s</code></p>
<p>You can close this window now.</p>
</body>
</html>`, code, state)
}

// verifyPKCE verifies the PKCE code verifier against the challenge
func (s *OAuthServer) verifyPKCE(challenge, method, verifier string) bool {
	switch method {
	case "S256":
		// SHA256 hash the verifier and compare with challenge
		hash := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(hash[:])
		return computed == challenge
	case "plain", "":
		// Plain comparison
		return verifier == challenge
	default:
		return false
	}
}

// generateAccessToken generates an opaque access token
func (s *OAuthServer) generateAccessToken(clientID, scope string) string {
	// Use opaque tokens for testing simplicity
	return generateOpaqueToken()
}

// generateOpaqueToken generates a random opaque token
func generateOpaqueToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// WaitForReady waits for the OAuth server to be ready
func (s *OAuthServer) WaitForReady(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if s.IsRunning() {
				// Try to connect to verify it's really ready
				conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", s.Port()), 1*time.Second)
				if err == nil {
					conn.Close()
					return nil
				}
			}
		}
	}
}

// GetClientID returns the configured client ID
func (s *OAuthServer) GetClientID() string {
	return s.config.ClientID
}

// GetClock returns the clock used by this server
func (s *OAuthServer) GetClock() Clock {
	return s.clock
}

// SetClock sets the clock used by this server (primarily for testing)
func (s *OAuthServer) SetClock(clock Clock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = clock
}

// WWWAuthenticateHeader returns the WWW-Authenticate header value for this server
func (s *OAuthServer) WWWAuthenticateHeader() string {
	return fmt.Sprintf(`Bearer realm="%s", authz_server="%s"`, s.config.Issuer, s.config.Issuer)
}

// AddToken directly adds a token to the server (for testing)
func (s *OAuthServer) AddToken(accessToken, refreshToken, scope, clientID string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.issuedTokens[accessToken] = &issuedToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scope:        scope,
		ClientID:     clientID,
		ExpiresAt:    expiresAt,
	}
}

// Helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetPendingAuthCode returns a pending auth code if one exists for the given state
func (s *OAuthServer) GetPendingAuthCode(state string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for code, entry := range s.authCodes {
		if entry.State == state {
			return code
		}
	}
	return ""
}

// ExtractBearerToken extracts a bearer token from an Authorization header
func ExtractBearerToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(authHeader, "Bearer ")
}
