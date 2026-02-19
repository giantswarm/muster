package oauth

import (
	"context"
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"

	"golang.org/x/oauth2"
)

// AgentTokenStore is a thin context-binder that implements mcp-go's
// transport.TokenStore interface by binding a server URL to the agent's
// file-based TokenStore.
//
// It has no storage of its own -- all reads and writes go through the
// underlying TokenStore. The only local state is a cached copy of the
// ID token, because mcp-go's transport.Token doesn't track ID tokens.
//
// mcp-go owns token refresh and 401 handling. This store returns the
// current token as-is and persists whatever mcp-go writes back after
// a successful refresh.
type AgentTokenStore struct {
	serverURL  string
	issuerURL  string
	tokenStore *TokenStore

	mu      sync.RWMutex
	idToken string
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
		ExpiresAt:    storedToken.Expiry,
	}, nil
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

	s.mu.RLock()
	cachedIDToken := s.idToken
	issuerURL := s.issuerURL
	s.mu.RUnlock()

	oauth2Token := &oauth2.Token{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		Expiry:       token.ExpiresAt,
	}

	if cachedIDToken != "" {
		oauth2Token = oauth2Token.WithExtra(map[string]interface{}{
			"id_token": cachedIDToken,
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
