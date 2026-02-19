package mcpserver

import (
	"context"
	"sync"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/client/transport"
)

// MusterTokenStore is a thin context-binder that implements mcp-go's
// transport.TokenStore interface by binding {sessionID, issuer} context
// to the single backing store exposed through api.OAuthHandler.
//
// It has no storage of its own -- all reads and writes go through
// api.OAuthHandler. The only local state is a cached copy of the ID token,
// because mcp-go's transport.Token doesn't track ID tokens.
//
// mcp-go owns token refresh and 401 handling. This store simply returns
// the current token as-is and persists whatever mcp-go writes back after
// a successful refresh.
type MusterTokenStore struct {
	sessionID    string
	issuer       string
	oauthHandler api.OAuthHandler

	mu      sync.RWMutex
	idToken string
}

// NewMusterTokenStore creates a new token store that binds the given
// session and issuer context to the api.OAuthHandler backing store.
func NewMusterTokenStore(sessionID, issuer string, oauthHandler api.OAuthHandler) *MusterTokenStore {
	return &MusterTokenStore{
		sessionID:    sessionID,
		issuer:       issuer,
		oauthHandler: oauthHandler,
	}
}

// GetToken returns the current OAuth token from the backing store.
// Returns transport.ErrNoToken when no token is available, which signals
// mcp-go to initiate the OAuth authorization flow.
//
// Unlike the previous implementation, this does NOT call RefreshTokenIfNeeded.
// mcp-go decides when to refresh based on the ExpiresAt field.
func (s *MusterTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if s.oauthHandler == nil || !s.oauthHandler.IsEnabled() {
		return nil, transport.ErrNoToken
	}

	fullToken := s.oauthHandler.GetFullTokenByIssuer(s.sessionID, s.issuer)
	if fullToken == nil || fullToken.AccessToken == "" {
		return nil, transport.ErrNoToken
	}

	if fullToken.IDToken != "" {
		s.mu.Lock()
		s.idToken = fullToken.IDToken
		s.mu.Unlock()
	}

	return &transport.Token{
		AccessToken:  fullToken.AccessToken,
		TokenType:    "Bearer",
		RefreshToken: fullToken.RefreshToken,
		ExpiresAt:    fullToken.ExpiresAt,
	}, nil
}

// SaveToken persists a refreshed token to the backing store via
// api.OAuthHandler.StoreToken. mcp-go calls this after a successful
// token refresh.
//
// The cached IDToken is preserved because refresh responses typically
// don't include ID tokens.
func (s *MusterTokenStore) SaveToken(ctx context.Context, token *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if s.oauthHandler == nil || token == nil {
		return nil
	}

	s.mu.RLock()
	cachedIDToken := s.idToken
	s.mu.RUnlock()

	s.oauthHandler.StoreToken(s.sessionID, s.issuer, &api.OAuthToken{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.ExpiresAt,
		IDToken:      cachedIDToken,
		Issuer:       s.issuer,
	})

	return nil
}

// GetIDToken returns the last cached ID token. mcp-go's transport.Token
// doesn't track ID tokens, so we cache them from the backing store
// on each GetToken() call for SSO forwarding.
func (s *MusterTokenStore) GetIDToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.idToken
}

// Ensure MusterTokenStore implements transport.TokenStore at compile time.
var _ transport.TokenStore = (*MusterTokenStore)(nil)
