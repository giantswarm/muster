package aggregator

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestURLElicitation_CoreAuthLoginOfAnAppRecordsItsChallenge(t *testing.T) {
	f := newAppFixture(t)
	ctx := api.WithSubject(api.WithSessionID(t.Context(), "session-1"), appSubject)
	ctx, recorder := withChallengeRecorder(ctx)

	result, err := NewAuthToolProvider(f.agg).handleAuthLogin(ctx, map[string]any{providerAppArg: "github"})
	require.NoError(t, err)

	challenge := recorder.challenge.Load()
	require.NotNil(t, challenge, "an App's sign-in link is muster's challenge too")
	require.NotEmpty(t, challenge.authURL)
	require.Equal(t, challengeURL(result), challenge.authURL)
	require.Equal(t, api.ProviderAppFlowPrefix+"github", challenge.server, "completed by the App's OAuth callback")
	require.Equal(t, "session-1", challenge.sessionID)
}

func TestURLElicitation_AppHeldGrantRecordsNothing(t *testing.T) {
	f := newAppFixture(t)
	f.handler.grant(githubAppIssuer, "ghu_alice")
	ctx := api.WithSubject(api.WithSessionID(t.Context(), "session-1"), appSubject)
	ctx, recorder := withChallengeRecorder(ctx)

	_, err := NewAuthToolProvider(f.agg).handleAuthLogin(ctx, map[string]any{providerAppArg: "github"})
	require.NoError(t, err)
	require.Nil(t, recorder.challenge.Load())
}

func TestElicitSignIn_NamesTheAppOfAnAppFlow(t *testing.T) {
	agg := &AggregatorServer{}
	_, err := agg.elicitSignIn(t.Context(), signInChallenge{
		sessionID: "session-1",
		server:    api.ProviderAppFlowPrefix + "github",
		authURL:   "https://muster.example.com/oauth/proxy/start?for=app:github",
	})
	var required mcp.URLElicitationRequiredError
	require.ErrorAs(t, err, &required)
	require.Len(t, required.Elicitations, 1)
	require.Equal(t, "Sign in to github to continue.", required.Elicitations[0].Message)
}
