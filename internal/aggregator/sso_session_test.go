package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/server"
)

// ssoSessionFromContext must lift the inbound actor token so the SSO bootstrap
// can re-present it on the detached context. Dropping it here makes the
// bootstrap-established localMint connection mint without an actor, which the
// broker authorizes on the human subject (token_exchange_audience_not_allowed).
func TestSSOSessionFromContext_CapturesActorToken(t *testing.T) {
	ctx := api.WithSubject(context.Background(), "alice")
	ctx = server.ContextWithBearerToken(ctx, "user-bearer")
	ctx = server.ContextWithActorToken(ctx, "agent-sa-token")

	sso := ssoSessionFromContext(ctx, "session-1")

	require.Equal(t, "alice", sso.userID)
	require.Equal(t, "user-bearer", sso.bearer)
	require.Equal(t, "agent-sa-token", sso.actorToken)
}

func TestSSOSessionFromContext_NoActorToken(t *testing.T) {
	ctx := api.WithSubject(context.Background(), "alice")
	ctx = server.ContextWithBearerToken(ctx, "user-bearer")

	sso := ssoSessionFromContext(ctx, "session-1")

	require.Empty(t, sso.actorToken)
}
