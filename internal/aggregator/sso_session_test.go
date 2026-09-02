package aggregator

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"testing"

	"github.com/giantswarm/mcp-oauth/providers"
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

// TestSSOSession_FormattingRedactsTokens pins the property that no way of
// printing an ssoSession reveals the tokens it carries. The struct holds the
// caller's ID token and bearer; before String/GoString existed, a %v printed
// both verbatim (muster 5.7.8 did exactly that from initSSOForSession, landing
// full 30-minute JWTs in pod logs). Every fmt verb and both slog handlers are
// exercised so a regression in any one rendering path fails here.
func TestSSOSession_FormattingRedactsTokens(t *testing.T) {
	const idToken = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.id-token-signature"
	const bearer = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImFjdCI6e319.bearer-signature"
	sso := ssoSession{
		userID:      "alice@giantswarm.io",
		sessionID:   "session-1234567890",
		tokens:      server.CallerTokens{IDToken: idToken, Bearer: bearer},
		tokenSource: providers.TokenSourceOAuth,
	}

	rendered := map[string]string{
		"%v":         fmt.Sprintf("%v", sso),
		"%+v":        fmt.Sprintf("%+v", sso),
		"%#v":        fmt.Sprintf("%#v", sso),
		"%s":         fmt.Sprintf("%s", sso), //nolint:staticcheck // S1025: the %s verb path is what is under test
		"%v pointer": fmt.Sprintf("%v", &sso),
		"Sprint":     fmt.Sprint(sso),
		"Sprintln":   fmt.Sprintln(sso),
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("sso", slog.Any("session", sso))
	rendered["slog text"] = buf.String()
	buf.Reset()
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("sso", slog.Any("session", sso))
	rendered["slog json"] = buf.String()

	for name, out := range rendered {
		require.NotContains(t, out, "eyJ", "%s leaks a JWT prefix: %s", name, out)
		require.NotContains(t, out, idToken, "%s leaks the ID token", name)
		require.NotContains(t, out, bearer, "%s leaks the bearer", name)
		// The redacted view still carries what an operator needs to correlate.
		require.Contains(t, out, "alice@gi...", "%s should keep the truncated userID", name)
		require.Contains(t, out, "session-...", "%s should keep the truncated sessionID", name)
		require.Contains(t, out, strconv.Itoa(len(idToken)), "%s should report the ID token length", name)
		require.Contains(t, out, strconv.Itoa(len(bearer)), "%s should report the bearer length", name)
		require.Contains(t, out, "oauth", "%s should keep the token source", name)
	}
}
