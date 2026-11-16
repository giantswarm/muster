package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		wantErr  bool
	}{
		{"trusted-issuer bearer, not stored", store, jwt, false, false},
		{"muster access token, not stored", store, "opaque-access-token", true, false},
		{"trusted-issuer bearer, store unavailable", failingTokenStore{err: errors.New("valkey: connection refused")}, jwt, true, false},
		{"muster access token, store unavailable", failingTokenStore{err: errors.New("valkey: connection refused")}, "opaque-access-token", true, true},
		{"muster access token, provider token expired", failingTokenStore{err: storage.ErrTokenExpired}, "opaque-access-token", true, false},
		{"muster access token, undecryptable", failingTokenStore{err: fmt.Errorf("failed to decrypt token: %w", storage.ErrTokenUndecryptable)}, "opaque-access-token", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logging.InitForCLI(logging.LevelDebug, &logBuf)

			s := &OAuthHTTPServer{tokenStore: tc.store}
			r := httptest.NewRequest("POST", "/mcp", nil)
			r.Header.Set("Authorization", "Bearer "+tc.bearer)

			token, err := s.getProviderToken(context.Background(), r)
			assert.Nil(t, token)
			assert.Equal(t, tc.wantErr, errors.Is(err, oauthserver.ErrStorageUnavailable), err)
			assert.Equal(t, tc.wantErr, err != nil, err)
			out := logBuf.String()
			assert.Equal(t, 1, bytes.Count(logBuf.Bytes(), []byte("Failed to get provider token from store")), out)
			assert.Equal(t, tc.wantWarn, bytes.Contains(logBuf.Bytes(), []byte("WARN")), out)
			assert.NotContains(t, out, tc.bearer, "the bearer never reaches the logs")
		})
	}
}

// TestAccessTokenInjector_StoreErrorIsRetryable: a token-store failure says
// nothing about the person's credentials. The request is answered 503 with
// Retry-After, and the session is left alone: firing onAuthenticated without
// a subject token would revoke it and hold the person's servers back.
func TestAccessTokenInjector_StoreErrorIsRetryable(t *testing.T) {
	s := &OAuthHTTPServer{tokenStore: failingTokenStore{err: context.DeadlineExceeded}}
	fired := false
	s.SetOnAuthenticated(func(context.Context, string) { fired = true })
	served := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true })

	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer opaque-access-token")
	ctx := oauthhandler.ContextWithUserInfo(r.Context(), &providers.UserInfo{ID: "alice", Email: "alice@example.com"})
	ctx = oauthhandler.ContextWithSessionID(ctx, "family-1")
	rec := httptest.NewRecorder()
	s.createAccessTokenInjectorMiddleware(next).ServeHTTP(rec, r.WithContext(ctx))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
	require.False(t, fired, "onAuthenticated must not run without the subject token")
	require.False(t, served)
}

// TestAccessTokenInjector_ForwardedTokenUnavailableIsRetryable: a forwarded
// JWT whose check could not reach the token store or the identity provider is
// answered 503 with Retry-After, not handled as a token without a subject.
func TestAccessTokenInjector_ForwardedTokenUnavailableIsRetryable(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	jwt := enc(`{"alg":"none"}`) + "." + enc(`{"sub":"alice"}`) + ".sig"
	for _, unavailable := range []error{oauthserver.ErrStorageUnavailable, oauthserver.ErrProviderUnavailable} {
		t.Run(unavailable.Error(), func(t *testing.T) {
			defer stubAcceptForwardedIDToken(func(context.Context, string) (*oauthserver.ForwardedIDTokenAcceptance, error) {
				return nil, fmt.Errorf("%w: check token revocation", unavailable)
			})()
			store := memory.New()
			t.Cleanup(store.Stop)
			s := &OAuthHTTPServer{tokenStore: store}
			fired := false
			s.SetOnAuthenticated(func(context.Context, string) { fired = true })
			served := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true })

			r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			r.Header.Set("Authorization", "Bearer "+jwt)
			ctx := oauthhandler.ContextWithUserInfo(r.Context(), &providers.UserInfo{ID: "alice", Email: "alice@example.com"})
			ctx = oauthhandler.ContextWithSessionID(ctx, "family-1")
			rec := httptest.NewRecorder()
			s.createAccessTokenInjectorMiddleware(next).ServeHTTP(rec, r.WithContext(ctx))

			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.NotEmpty(t, rec.Header().Get("Retry-After"))
			require.Empty(t, rec.Header().Get("WWW-Authenticate"))
			require.False(t, fired, "onAuthenticated must not run without the subject token")
			require.False(t, served)
		})
	}
}
