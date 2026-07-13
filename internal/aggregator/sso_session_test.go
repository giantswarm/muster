package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/server"
)

func TestSSOSessionFromContext_CapturesCallerTokens(t *testing.T) {
	ctx := api.WithSubject(context.Background(), "alice")
	ctx = server.ContextWithBearerToken(ctx, "user-bearer")

	sso := ssoSessionFromContext(ctx, "session-1")

	require.Equal(t, "alice", sso.userID)
	require.Equal(t, "user-bearer", sso.tokens.Bearer)
}

func TestCanBootstrapSSO(t *testing.T) {
	jwtBearer := unsignedJWT(t, map[string]any{"sub": "alice"})
	tests := []struct {
		name   string
		tokens server.CallerTokens
		want   bool
	}{
		{"no tokens", server.CallerTokens{}, false},
		{"ID token only", server.CallerTokens{IDToken: "id"}, true},
		{"forwardable JWT bearer only", server.CallerTokens{Bearer: jwtBearer}, true},
		// An opaque bearer cannot be forwarded downstream: a session holding
		// only one has lost its upstream credential, and counting it would
		// make the broken-refresh-chain eviction in onAuthenticated
		// unreachable (every authenticated request carries a bearer).
		{"opaque bearer only", server.CallerTokens{Bearer: "opaque-access-token"}, false},
		{"ID token with opaque bearer", server.CallerTokens{IDToken: "id", Bearer: "opaque"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ssoSession{tokens: tc.tokens}.canBootstrapSSO())
		})
	}
}
