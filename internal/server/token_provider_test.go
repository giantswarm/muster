package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallerTokens_RoundTrip(t *testing.T) {
	want := CallerTokens{IDToken: "id-tok", Bearer: "bearer-tok", Actor: "actor-tok"}

	ctx := ContextWithCallerTokens(context.Background(), want)

	require.Equal(t, want, CallerTokensFromContext(ctx))
	// The per-field getters must observe identical values so the non-bootstrap
	// paths that read them directly are unaffected.
	id, ok := GetIDTokenFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "id-tok", id)
	require.Equal(t, "bearer-tok", GetBearerTokenFromContext(ctx))
	require.Equal(t, "actor-tok", GetActorTokenFromContext(ctx))
}

// A dropped actor is the bug this bundle exists to prevent: the round trip must
// carry every credential token, not just subject/idToken.
func TestCallerTokens_CarriesActor(t *testing.T) {
	ctx := ContextWithCallerTokens(context.Background(), CallerTokens{Bearer: "b", Actor: "a"})
	require.Equal(t, "a", GetActorTokenFromContext(ctx))
	require.Equal(t, "a", CallerTokensFromContext(ctx).Actor)
}

// Empty fields must not overwrite values already present on the context.
func TestContextWithCallerTokens_EmptyFieldsPreserveExisting(t *testing.T) {
	ctx := ContextWithActorToken(context.Background(), "pre-existing-actor")

	ctx = ContextWithCallerTokens(ctx, CallerTokens{Bearer: "b"})

	require.Equal(t, "pre-existing-actor", GetActorTokenFromContext(ctx), "empty Actor must not clear an existing one")
	require.Equal(t, "b", GetBearerTokenFromContext(ctx))
}
