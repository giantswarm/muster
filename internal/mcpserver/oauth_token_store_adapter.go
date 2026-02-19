package mcpserver

import (
	"context"
	"sync"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/client/transport"
)

// MCPGoTokenStore adapts muster's session-scoped OAuth token management
// to mcp-go's transport.TokenStore interface. This bridges muster's
// OAuthHandler (which manages tokens by sessionID/issuer) to the
// transport-level TokenStore that mcp-go uses for automatic bearer
// token injection and 401 handling.
//
// The adapter also caches the ID token from muster's full token on each
// GetToken() call, making it available for downstream SSO forwarding
// via GetIDToken(). This keeps ID token concerns in muster's
// orchestration layer while delegating the basic OAuth transport flow
// to mcp-go.
type MCPGoTokenStore struct {
	sessionID    string
	issuer       string
	scope        string
	oauthHandler api.OAuthHandler

	mu      sync.RWMutex
	idToken string
}

// NewMCPGoTokenStore creates a new token store adapter that bridges muster's
// session-scoped OAuth token management to mcp-go's transport.TokenStore.
func NewMCPGoTokenStore(sessionID, issuer, scope string, oauthHandler api.OAuthHandler) *MCPGoTokenStore {
	return &MCPGoTokenStore{
		sessionID:    sessionID,
		issuer:       issuer,
		scope:        scope,
		oauthHandler: oauthHandler,
	}
}

// GetToken returns the current OAuth access token, refreshing if needed.
// Returns transport.ErrNoToken when no token is available, which signals
// mcp-go to initiate the OAuth authorization flow.
func (s *MCPGoTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if s.oauthHandler == nil || !s.oauthHandler.IsEnabled() {
		return nil, transport.ErrNoToken
	}

	accessToken := s.oauthHandler.RefreshTokenIfNeeded(ctx, s.sessionID, s.issuer)
	if accessToken == "" {
		return nil, transport.ErrNoToken
	}

	fullToken := s.oauthHandler.GetFullTokenByIssuer(s.sessionID, s.issuer)
	if fullToken != nil && fullToken.IDToken != "" {
		s.mu.Lock()
		s.idToken = fullToken.IDToken
		s.mu.Unlock()
	}

	return &transport.Token{
		AccessToken: accessToken,
		TokenType:   "Bearer",
	}, nil
}

// SaveToken is called by mcp-go after token refresh. Since muster manages
// tokens via its own store (OAuthHandler), this is a no-op.
func (s *MCPGoTokenStore) SaveToken(ctx context.Context, _ *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// GetIDToken returns the last cached ID token. mcp-go's transport.Token
// doesn't track ID tokens, so we cache them from muster's full token
// on each GetToken() call for SSO forwarding.
func (s *MCPGoTokenStore) GetIDToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.idToken
}

// Ensure MCPGoTokenStore implements transport.TokenStore at compile time.
var _ transport.TokenStore = (*MCPGoTokenStore)(nil)
