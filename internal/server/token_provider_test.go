package server

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
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

// TestCallerTokens_FormattingRedactsTokens pins that no fmt verb and no slog
// handler prints the credentials a CallerTokens carries -- only their lengths.
func TestCallerTokens_FormattingRedactsTokens(t *testing.T) {
	const idToken = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.id-token-signature"
	const bearer = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.bearer-signature"
	tokens := CallerTokens{IDToken: idToken, Bearer: bearer}

	rendered := map[string]string{
		"%v":         fmt.Sprintf("%v", tokens),
		"%+v":        fmt.Sprintf("%+v", tokens),
		"%#v":        fmt.Sprintf("%#v", tokens),
		"%s":         fmt.Sprintf("%s", tokens), //nolint:staticcheck // S1025: the %s verb path is what is under test
		"%v pointer": fmt.Sprintf("%v", &tokens),
		"Sprint":     fmt.Sprint(tokens),
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("tokens", slog.Any("tokens", tokens))
	rendered["slog text"] = buf.String()
	buf.Reset()
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("tokens", slog.Any("tokens", tokens))
	rendered["slog json"] = buf.String()

	for name, out := range rendered {
		require.NotContains(t, out, "eyJ", "%s leaks a JWT prefix: %s", name, out)
		require.NotContains(t, out, idToken, "%s leaks the ID token", name)
		require.NotContains(t, out, bearer, "%s leaks the bearer", name)
		require.Contains(t, out, strconv.Itoa(len(idToken)), "%s should report the ID token length", name)
		require.Contains(t, out, strconv.Itoa(len(bearer)), "%s should report the bearer length", name)
	}
}
