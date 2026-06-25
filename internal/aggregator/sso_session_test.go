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
	ctx = server.ContextWithActorToken(ctx, "agent-sa-token")

	sso := ssoSessionFromContext(ctx, "session-1")

	require.Equal(t, "alice", sso.userID)
	require.Equal(t, "user-bearer", sso.tokens.Bearer)
	require.Equal(t, "agent-sa-token", sso.tokens.Actor)
}

func TestSSOSessionFromContext_NoActorToken(t *testing.T) {
	ctx := api.WithSubject(context.Background(), "alice")
	ctx = server.ContextWithBearerToken(ctx, "user-bearer")

	sso := ssoSessionFromContext(ctx, "session-1")

	require.Empty(t, sso.tokens.Actor)
}
