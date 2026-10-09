package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	"golang.org/x/oauth2"
)

// AgentTokenStore is a thin context-binder that implements mcp-go's
// transport.TokenStore interface by binding a server URL to the agent's
// file-based TokenStore.
//
// It has no storage of its own -- all reads and writes go through the
// underlying TokenStore. The only local state is the ID token, because
// mcp-go's transport.Token doesn't track ID tokens: a cached copy of the
// stored one, and the one a refresh returned (recorded by idTokenRecorder),
// which SaveToken persists in place of the cached copy.
//
// mcp-go owns token refresh and 401 handling. This store returns the
// current token as-is and persists whatever mcp-go writes back after
// a successful refresh.
type AgentTokenStore struct {
	serverURL  string
	issuerURL  string
	tokenStore *TokenStore

	mu               sync.RWMutex
	idToken          string
	refreshedIDToken string
}

// NewAgentTokenStore creates a new token store that binds the given
// server URL to the agent's file-based token store.
func NewAgentTokenStore(serverURL string, tokenStore *TokenStore) *AgentTokenStore {
	return &AgentTokenStore{
		serverURL:  serverURL,
		tokenStore: tokenStore,
	}
}

// GetToken returns the current OAuth token from the file-based store.
// Returns transport.ErrNoToken when no token is available, which signals
// mcp-go to initiate the OAuth authorization flow.
//
// The expiry handed to mcp-go is the store's own: the stored one less
// tokenExpiryBuffer. mcp-go refreshes only a token it sees as expired, while
// the store calls a token invalid tokenExpiryBuffer earlier; with the stored
// expiry, a connection in that last minute went out with the old token
// unrefreshed and the store then answered auth_required for a grant the
// person still holds.
func (s *AgentTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	storedToken := s.tokenStore.GetTokenIncludingExpiring(s.serverURL)
	if storedToken == nil || storedToken.AccessToken == "" {
		return nil, transport.ErrNoToken
	}

	s.mu.Lock()
	if storedToken.IDToken != "" {
		s.idToken = storedToken.IDToken
	}
	if storedToken.IssuerURL != "" {
		s.issuerURL = storedToken.IssuerURL
	}
	s.mu.Unlock()

	return &transport.Token{
		AccessToken:  storedToken.AccessToken,
		TokenType:    storedToken.TokenType,
		RefreshToken: storedToken.RefreshToken,
		ExpiresAt:    validUntil(storedToken.Expiry),
	}, nil
}

// validUntil is the moment the store stops calling a token with this expiry
// valid (isTokenValid); zero, no expiry, stays zero.
func validUntil(expiry time.Time) time.Time {
	if expiry.IsZero() {
		return expiry
	}
	return expiry.Add(-tokenExpiryBuffer)
}

// SaveToken persists a refreshed token to the file-based store.
// mcp-go calls this after a successful token refresh.
//
// The cached IDToken is preserved because refresh responses typically
// don't include ID tokens.
func (s *AgentTokenStore) SaveToken(ctx context.Context, token *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if s.tokenStore == nil || token == nil {
		return nil
	}

	s.mu.Lock()
	idToken := s.idToken
	if s.refreshedIDToken != "" {
		idToken = s.refreshedIDToken
		s.idToken = idToken
		s.refreshedIDToken = ""
	}
	issuerURL := s.issuerURL
	s.mu.Unlock()

	oauth2Token := &oauth2.Token{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		Expiry:       token.ExpiresAt,
	}

	if idToken != "" {
		oauth2Token = oauth2Token.WithExtra(map[string]interface{}{
			"id_token": idToken,
		})
	}

	return s.tokenStore.StoreToken(s.serverURL, issuerURL, oauth2Token)
}

// GetIDToken returns the last cached ID token. mcp-go's transport.Token
// doesn't track ID tokens, so we cache them from the file store on
// each GetToken() call for SSO forwarding.
func (s *AgentTokenStore) GetIDToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.idToken
}

// Ensure AgentTokenStore implements transport.TokenStore at compile time.
var _ transport.TokenStore = (*AgentTokenStore)(nil)

// idTokenRecorder is the round tripper of mcp-go's OAuth handler. It takes the
// ID token from a token endpoint response before mcp-go decodes the response
// into a transport.Token, which has no field for it, and saves it: without it
// every refresh would store the new access token next to the old ID token.
type idTokenRecorder struct {
	base  http.RoundTripper
	store *AgentTokenStore
}

func (r idTokenRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err != nil || req.Method != http.MethodPost || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	var tokenResponse struct {
		IDToken string `json:"id_token"`
	}
	if json.Unmarshal(body, &tokenResponse) == nil && tokenResponse.IDToken != "" {
		r.store.mu.Lock()
		r.store.refreshedIDToken = tokenResponse.IDToken
		r.store.mu.Unlock()
	}
	return resp, nil
}
