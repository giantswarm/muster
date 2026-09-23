package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/giantswarm/mcp-oauth/storage"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"

	"github.com/giantswarm/muster/v5/pkg/logging"
)

// failingTokenStore answers every lookup with err.
type failingTokenStore struct {
	storage.TokenStore
	err error
}

func (s failingTokenStore) GetToken(context.Context, string) (*oauth2.Token, error) {
	return nil, s.err
}

// TestGetProviderToken_MissLogLevel: muster's token store holds provider
// tokens for muster's own opaque access tokens only. A trusted-issuer bearer
// (a JWT) misses by construction, so its miss is one DEBUG line; a miss for an
// opaque token, and a store failure for any bearer, stay WARN.
func TestGetProviderToken_MissLogLevel(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	jwt := enc(`{"alg":"none"}`) + "." + enc(`{"sub":"agent"}`) + ".sig"

	store := memory.New()
	t.Cleanup(store.Stop)
	cases := []struct {
		name     string
		store    storage.TokenStore
		bearer   string
		wantWarn bool
	}{
		{"trusted-issuer bearer, not stored", store, jwt, false},
		{"muster access token, not stored", store, "opaque-access-token", true},
		{"trusted-issuer bearer, store unavailable", failingTokenStore{err: errors.New("valkey: connection refused")}, jwt, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logging.InitForCLI(logging.LevelDebug, &logBuf)

			s := &OAuthHTTPServer{tokenStore: tc.store}
			r := httptest.NewRequest("POST", "/mcp", nil)
			r.Header.Set("Authorization", "Bearer "+tc.bearer)

			assert.Nil(t, s.getProviderToken(context.Background(), r))
			out := logBuf.String()
			assert.Equal(t, 1, bytes.Count(logBuf.Bytes(), []byte("Failed to get provider token from store")), out)
			assert.Equal(t, tc.wantWarn, bytes.Contains(logBuf.Bytes(), []byte("WARN")), out)
			assert.NotContains(t, out, tc.bearer, "the bearer never reaches the logs")
		})
	}
}
