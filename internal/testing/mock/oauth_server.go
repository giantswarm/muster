package mock

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// jwtHeader is the pre-computed base64-encoded JWT header for unsigned tokens.
// We use "none" algorithm since this is for testing only.
// Value: base64url({"alg":"none","typ":"JWT"})
//
// SECURITY WARNING: This generates unsigned JWTs with alg:none for TESTING ONLY.
// Production OAuth servers and downstream MCP servers MUST:
//  1. Use proper cryptographic signing (RS256, ES256, etc.)
//  2. Explicitly reject tokens with alg:none to prevent signature bypass attacks
//  3. Validate token signatures against the IdP's JWKS endpoint
//
// If you see alg:none tokens in production, it indicates a security misconfiguration.
const jwtHeader = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0"

// idTokenClaims represents the claims in an ID token.
type idTokenClaims struct {
	Iss    string   `json:"iss"`              // Issuer
	Sub    string   `json:"sub"`              // Subject (user ID)
	Aud    string   `json:"aud"`              // Audience (client ID)
	Exp    int64    `json:"exp"`              // Expiration time
	Iat    int64    `json:"iat"`              // Issued at
	Email  string   `json:"email,omitempty"`  // User email
	Name   string   `json:"name,omitempty"`   // User name
	Groups []string `json:"groups,omitempty"` // User groups for RBAC
}

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

	// UseTLS enables HTTPS mode with a self-signed certificate.
	// This is required for testing muster's OAuth server integration,
	// as the Dex provider enforces HTTPS for security.
	UseTLS bool

	// TrustedIssuers maps connector IDs to trusted issuer URLs for RFC 8693 token exchange.
	// When a token exchange request is received with a connector_id, this map is used
	// to look up the trusted issuer and validate the subject token.
	// Key: connector_id, Value: issuer URL
	TrustedIssuers map[string]string
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

	// TLS certificate and CA for HTTPS mode
	tlsCert   *tls.Certificate
	caCertPEM []byte // PEM-encoded CA certificate for clients
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

	s.port = listener.Addr().(*net.TCPAddr).Port

	// Generate self-signed certificate for TLS mode
	if s.config.UseTLS {
		cert, caPEM, err := generateSelfSignedCert()
		if err != nil {
			listener.Close()
			return 0, fmt.Errorf("failed to generate self-signed certificate: %w", err)
		}
		s.tlsCert = cert
		s.caCertPEM = caPEM

		// Wrap listener with TLS
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{*cert},
			MinVersion:   tls.VersionTLS12,
		}
		listener = tls.NewListener(listener, tlsConfig)
	}

	s.listener = listener

	// Update issuer with actual port if it's a placeholder
	if s.config.Issuer == "" {
		if s.config.UseTLS {
			s.config.Issuer = fmt.Sprintf("https://localhost:%d", s.port)
		} else {
			s.config.Issuer = fmt.Sprintf("http://localhost:%d", s.port)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleMetadata)
	mux.HandleFunc("/.well-known/openid-configuration", s.handleMetadata)
	mux.HandleFunc("/authorize", s.handleAuthorize)
	mux.HandleFunc("/token", s.handleToken)
	mux.HandleFunc("/userinfo", s.handleUserInfo)
	mux.HandleFunc("/jwks", s.handleJWKS)
	mux.HandleFunc("/callback", s.handleCallback)

	// Create server with error log that discards TLS handshake errors
	// These are common during test startup when clients probe connections
	s.httpServer = &http.Server{
		Handler:  mux,
		ErrorLog: log.New(io.Discard, "", 0),
	}

	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			if s.config.Debug {
				fmt.Fprintf(os.Stderr, "OAuth server error: %v\n", err)
			}
		}
	}()

	s.running = true

	if s.config.Debug {
		protocol := "http"
		if s.config.UseTLS {
			protocol = "https"
		}
		fmt.Fprintf(os.Stderr, "🔐 Mock OAuth server started on port %d (%s, issuer: %s)\n", s.port, protocol, s.config.Issuer)
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

// GetCACertPEM returns the PEM-encoded CA certificate for TLS mode.
// This can be used by clients to trust the self-signed certificate.
// Returns nil if the server is not running in TLS mode.
func (s *OAuthServer) GetCACertPEM() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.caCertPEM
}

// IsTLS returns whether the server is running in TLS mode.
func (s *OAuthServer) IsTLS() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.UseTLS
}

// generateSelfSignedCert generates a self-signed TLS certificate for testing.
// It returns the TLS certificate and the PEM-encoded CA certificate.
func generateSelfSignedCert() (*tls.Certificate, []byte, error) {
	// Generate ECDSA private key (P-256 curve)
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(24 * time.Hour) // Valid for 24 hours

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Muster Test"},
			CommonName:   "localhost",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // Self-signed, so it's its own CA
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	// Create self-signed certificate
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	// Encode private key to PEM
	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	// Parse into tls.Certificate
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse key pair: %w", err)
	}

	return &cert, certPEM, nil
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

	// Generate ID token for SSO token forwarding support
	idToken := s.generateIDToken(entry.ClientID, entry.Scope)

	token := &issuedToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scope:        entry.Scope,
		ClientID:     entry.ClientID,
		ExpiresAt:    s.clock.Now().Add(s.config.TokenLifetime),
	}

	s.mu.Lock()
	s.issuedTokens[accessToken] = token
	// Also store the ID token for SSO token forwarding validation.
	// When muster forwards the ID token to downstream MCP servers, those servers
	// need to be able to validate it against this OAuth server.
	if idToken != "" {
		s.issuedTokens[idToken] = token
	}
	delete(s.authCodes, code)
	s.mu.Unlock()

	return &TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.config.TokenLifetime.Seconds()),
		Scope:        entry.Scope,
		IDToken:      idToken,
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

// GenerateTestToken generates a test access token directly without going through the OAuth flow.
// This is useful for testing scenarios where the test framework needs to authenticate
// with muster's OAuth server without implementing the full browser-based OAuth flow.
func (s *OAuthServer) GenerateTestToken(clientID, scope string) *TokenResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	if clientID == "" {
		clientID = s.config.ClientID
	}
	if scope == "" {
		scope = "openid profile email"
	}

	accessToken := s.generateAccessToken(clientID, scope)
	refreshToken := generateOpaqueToken()
	idToken := s.generateIDToken(clientID, scope)

	token := &issuedToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scope:        scope,
		ClientID:     clientID,
		ExpiresAt:    s.clock.Now().Add(s.config.TokenLifetime),
	}

	s.issuedTokens[accessToken] = token
	// Also store the ID token for SSO token forwarding validation.
	if idToken != "" {
		s.issuedTokens[idToken] = token
	}

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Generated test token for client %s (scope: %s)\n", clientID, scope)
	}

	return &TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.config.TokenLifetime.Seconds()),
		Scope:        scope,
		IDToken:      idToken,
	}
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
		"grant_types_supported":                 []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:token-exchange"},
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
	case "urn:ietf:params:oauth:grant-type:token-exchange":
		s.handleTokenExchange(w, r)
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

	// Generate ID token for SSO token forwarding support
	idToken := s.generateIDToken(entry.ClientID, entry.Scope)

	token := &issuedToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scope:        entry.Scope,
		ClientID:     entry.ClientID,
		ExpiresAt:    s.clock.Now().Add(s.config.TokenLifetime),
	}

	s.mu.Lock()
	s.issuedTokens[accessToken] = token
	// Also store the ID token for SSO token forwarding validation.
	// When muster forwards the ID token to downstream MCP servers, those servers
	// need to be able to validate it against this OAuth server.
	if idToken != "" {
		s.issuedTokens[idToken] = token
	}
	s.mu.Unlock()

	response := TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.config.TokenLifetime.Seconds()),
		Scope:        entry.Scope,
		IDToken:      idToken,
	}

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Issued tokens for client %s (with ID token)\n", entry.ClientID)
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

	// Generate new ID token for SSO token forwarding support
	newIDToken := s.generateIDToken(originalToken.ClientID, originalToken.Scope)

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
		IDToken:      newIDToken,
	})
}

// handleTokenExchange implements RFC 8693 OAuth 2.0 Token Exchange.
// This allows exchanging a token from a trusted issuer for a token valid on this server.
func (s *OAuthServer) handleTokenExchange(w http.ResponseWriter, r *http.Request) {
	subjectToken := r.FormValue("subject_token")
	subjectTokenType := r.FormValue("subject_token_type")
	scope := r.FormValue("scope")

	// Dex uses "connector_id" as the audience parameter for token exchange.
	// RFC 8693 uses "audience". Accept both for compatibility.
	connectorID := r.FormValue("connector_id")
	audience := r.FormValue("audience")
	if connectorID != "" && audience == "" {
		audience = connectorID
	}

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Token exchange request: connector_id=%s, audience=%s, token_type=%s\n",
			connectorID, audience, subjectTokenType)
	}

	// Validate required parameters
	if subjectToken == "" {
		s.tokenExchangeError(w, "invalid_request", "subject_token is required")
		return
	}
	if subjectTokenType == "" {
		subjectTokenType = "urn:ietf:params:oauth:token-type:id_token"
	}
	if audience == "" {
		s.tokenExchangeError(w, "invalid_request", "audience or connector_id is required")
		return
	}

	// Check if we have a trusted issuer for this connector_id
	trustedIssuer, ok := s.config.TrustedIssuers[audience]
	if !ok {
		s.tokenExchangeError(w, "invalid_target", fmt.Sprintf("no trusted issuer configured for connector_id: %s", audience))
		return
	}

	// Validate the subject token (extract and verify issuer)
	tokenIssuer := s.extractIssuerFromToken(subjectToken)
	if tokenIssuer == "" {
		s.tokenExchangeError(w, "invalid_grant", "could not extract issuer from subject_token")
		return
	}

	// Verify the token is from the trusted issuer
	if tokenIssuer != trustedIssuer {
		if s.config.Debug {
			fmt.Fprintf(os.Stderr, "🔐 Token exchange failed: issuer mismatch (got: %s, expected: %s)\n",
				tokenIssuer, trustedIssuer)
		}
		s.tokenExchangeError(w, "invalid_grant", "subject_token issuer does not match trusted issuer")
		return
	}

	// Extract user info from the subject token for the new token
	userID := s.extractSubFromToken(subjectToken)
	if userID == "" {
		userID = "test-user-123" // fallback
	}

	// Default scope if not provided
	if scope == "" {
		scope = "openid profile email groups"
	}

	// Issue new tokens for this server
	accessToken := s.generateAccessToken(s.config.ClientID, scope)
	idToken := s.generateIDTokenWithSub(s.config.ClientID, scope, userID)

	token := &issuedToken{
		AccessToken:  accessToken,
		RefreshToken: "", // No refresh token for exchanged tokens (per RFC 8693 best practice)
		Scope:        scope,
		ClientID:     s.config.ClientID,
		ExpiresAt:    s.clock.Now().Add(s.config.TokenLifetime),
	}

	s.mu.Lock()
	s.issuedTokens[accessToken] = token
	s.mu.Unlock()

	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Token exchange successful: issued token for user %s\n", userID)
	}

	// RFC 8693 response format
	response := map[string]interface{}{
		"access_token":      accessToken,
		"issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
		"token_type":        "Bearer",
		"expires_in":        int(s.config.TokenLifetime.Seconds()),
		"scope":             scope,
		"id_token":          idToken,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// tokenExchangeError sends an RFC 8693 compliant error response
func (s *OAuthServer) tokenExchangeError(w http.ResponseWriter, errorCode, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{
		"error":             errorCode,
		"error_description": description,
	})
}

// extractIssuerFromToken extracts the issuer (iss) claim from a JWT token
func (s *OAuthServer) extractIssuerFromToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	return claims.Iss
}

// extractSubFromToken extracts the subject (sub) claim from a JWT token
func (s *OAuthServer) extractSubFromToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	return claims.Sub
}

// generateIDTokenWithSub generates an ID token with a specific subject
func (s *OAuthServer) generateIDTokenWithSub(clientID, scope, subject string) string {
	now := s.clock.Now()

	claims := idTokenClaims{
		Iss:   s.config.Issuer,
		Sub:   subject,
		Aud:   clientID,
		Exp:   now.Add(s.config.TokenLifetime).Unix(),
		Iat:   now.Unix(),
		Email: "test@example.com",
		Name:  "Test User",
	}

	// Include groups claim if the "groups" scope was requested
	if strings.Contains(scope, "groups") {
		claims.Groups = []string{"test-group", "developers"}
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		panic(fmt.Errorf("failed to marshal ID token claims: %w", err))
	}

	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	return fmt.Sprintf("%s.%s.", jwtHeader, payload)
}

func (s *OAuthServer) handleUserInfo(w http.ResponseWriter, r *http.Request) {
	token := ExtractBearerToken(r.Header.Get("Authorization"))
	if token == "" || !s.ValidateToken(token) {
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

// generateAccessToken generates an opaque access token.
// Note: clientID and scope are accepted for API consistency but not used
// in token generation since we use opaque tokens for testing simplicity.
func (s *OAuthServer) generateAccessToken(_, _ string) string {
	return generateOpaqueToken()
}

// generateOpaqueToken generates a random opaque token.
// Panics if crypto/rand fails, which should never happen in practice.
func generateOpaqueToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("crypto/rand failed: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// generateIDToken generates a mock JWT ID token for testing.
//
// SECURITY WARNING: This generates UNSIGNED tokens (alg: none) for TESTING ONLY.
// These tokens have no cryptographic signature and MUST NOT be used in production.
// Production environments MUST:
//   - Use proper JWT signing (RS256, ES256)
//   - Reject tokens with alg: none
//   - Verify signatures against the IdP's JWKS
func (s *OAuthServer) generateIDToken(clientID, scope string) string {
	now := s.clock.Now()

	claims := idTokenClaims{
		Iss:   s.config.Issuer,
		Sub:   "test-user-123",
		Aud:   clientID,
		Exp:   now.Add(s.config.TokenLifetime).Unix(),
		Iat:   now.Unix(),
		Email: "test@example.com",
		Name:  "Test User",
	}

	// Include groups claim if the "groups" scope was requested
	if strings.Contains(scope, "groups") {
		claims.Groups = []string{"test-group", "developers"}
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		// This should never happen with our fixed struct
		panic(fmt.Errorf("failed to marshal ID token claims: %w", err))
	}

	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)

	// Return unsigned JWT (header.payload.)
	// The trailing dot indicates no signature (alg: none)
	return fmt.Sprintf("%s.%s.", jwtHeader, payload)
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

// GetScopes returns the accepted scopes for this OAuth server
func (s *OAuthServer) GetScopes() []string {
	return s.config.AcceptedScopes
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

// SetTrustedIssuers sets the trusted issuers for RFC 8693 token exchange.
// This should be called after all OAuth servers are started, so their issuer URLs are known.
func (s *OAuthServer) SetTrustedIssuers(trustedIssuers map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.TrustedIssuers = trustedIssuers
	if s.config.Debug && len(trustedIssuers) > 0 {
		fmt.Fprintf(os.Stderr, "🔐 Configured %d trusted issuers for token exchange\n", len(trustedIssuers))
	}
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

// RevokeToken removes a token from the server, making it invalid for future requests.
// This is used for testing scenarios where a token is revoked server-side but the
// client still has the token cached locally. Returns true if the token was found
// and revoked, false if the token didn't exist.
func (s *OAuthServer) RevokeToken(accessToken string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.issuedTokens[accessToken]; exists {
		delete(s.issuedTokens, accessToken)
		if s.config.Debug {
			fmt.Fprintf(os.Stderr, "🔐 Revoked token: %s...\n", accessToken[:min(16, len(accessToken))])
		}
		return true
	}
	return false
}

// RevokeAllTokens removes all tokens from the server.
// This is used for testing scenarios where all tokens need to be invalidated.
// Returns the number of tokens that were revoked.
func (s *OAuthServer) RevokeAllTokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := len(s.issuedTokens)
	s.issuedTokens = make(map[string]*issuedToken)
	if s.config.Debug {
		fmt.Fprintf(os.Stderr, "🔐 Revoked all tokens (%d tokens)\n", count)
	}
	return count
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
