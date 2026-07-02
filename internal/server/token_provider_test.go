package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallerTokens_RoundTrip(t *testing.T) {
	want := CallerTokens{IDToken: "id-tok", Bearer: "bearer-tok"}

	ctx := ContextWithCallerTokens(context.Background(), want)

	require.Equal(t, want, CallerTokensFromContext(ctx))
	id, ok := GetIDTokenFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "id-tok", id)
	require.Equal(t, "bearer-tok", GetBearerTokenFromContext(ctx))
}
