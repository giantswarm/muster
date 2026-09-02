package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/giantswarm/mcp-oauth/storage"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/giantswarm/muster/pkg/logging"
)

// keyEchoingTokenStore fails every lookup with an error that embeds the key it
// was asked for, the way mcp-oauth's in-memory store does
// (fmt.Errorf("%w: %s", storage.ErrTokenNotFound, key)). For getProviderToken
// that key is the caller's bearer.
type keyEchoingTokenStore struct {
	storage.TokenStore
}

func (keyEchoingTokenStore) GetToken(_ context.Context, key string) (*oauth2.Token, error) {
	return nil, fmt.Errorf("%w: %s", storage.ErrTokenNotFound, key)
}

// TestGetProviderToken_StoreErrorDoesNotLogBearer pins that a token-store
// failure is logged without the bearer the store echoed back. Before the
// redaction, every request whose bearer had no stored provider token (an agent
// arriving with a trusted-issuer JWT, for one) wrote that JWT to the logs at
// WARN level.
func TestGetProviderToken_StoreErrorDoesNotLogBearer(t *testing.T) {
	const bearer = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.live-signature"

	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)

	s := &OAuthHTTPServer{tokenStore: keyEchoingTokenStore{}}
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)

	require.Nil(t, s.getProviderToken(context.Background(), req))

	logged := logBuf.String()
	require.Contains(t, logged, "Failed to get provider token from store", "the failure must still be logged")
	require.Contains(t, logged, "token not found", "the store's reason must survive redaction")
	require.NotContains(t, logged, bearer, "the bearer must not reach the logs: %s", logged)
	require.NotContains(t, logged, "eyJ", "no JWT prefix may reach the logs: %s", logged)
}
