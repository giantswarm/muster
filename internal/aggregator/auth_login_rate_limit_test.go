package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// The per-user authentication rate limit counts OAuth flows. A client that
// checks a server's connection with core_auth_login (the portal does before
// it acts for the person) gets "already authenticated" and spends no attempt,
// so a run of such checks never locks the person out of a real sign-in.

func newRateLimitedLoginServer(t *testing.T) *AggregatorServer {
	t.Helper()
	a := newSuspensionTestServer(t)
	a.authRateLimiter = NewAuthRateLimiter(DefaultAuthRateLimiterConfig())
	t.Cleanup(a.authRateLimiter.Stop)
	return a
}

func TestHandleAuthLogin_ConnectedAnswerSpendsNoAttempt(t *testing.T) {
	a := newRateLimitedLoginServer(t)
	registerSuspendableServer(t, a.registry, "miro", false, api.StateAuthRequired)
	require.NoError(t, a.authStore.MarkAuthenticated(context.Background(), "test-session", "miro"))

	provider := NewAuthToolProvider(a)
	limit := DefaultAuthRateLimiterConfig().MaxAttempts
	for i := 0; i < 3*limit; i++ {
		result, err := provider.handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
		require.NoError(t, err)
		require.False(t, result.IsError, "check %d: %v", i+1, result.Content)
		require.Equal(t, "Server 'miro' is already authenticated.", result.Content[0], "check %d", i+1)
	}
	assert.Equal(t, limit, a.authRateLimiter.RemainingAttempts("test-user"),
		"no attempt is charged for an answer that starts no OAuth flow")
}

func TestHandleAuthLogin_ChallengesStayLimited(t *testing.T) {
	a := newRateLimitedLoginServer(t)
	registerSuspendableServer(t, a.registry, "miro", false, api.StateAuthRequired)

	oauthHandler := &challengeCountingOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}}
	api.RegisterOAuthHandler(oauthHandler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	provider := NewAuthToolProvider(a)
	limit := DefaultAuthRateLimiterConfig().MaxAttempts
	for i := 0; i < limit; i++ {
		result, err := provider.handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
		require.NoError(t, err)
		require.False(t, result.IsError, "challenge %d: %v", i+1, result.Content)
	}

	result, err := provider.handleAuthLogin(testSessionCtx(), map[string]any{"server": "miro"})
	require.NoError(t, err)
	assert.True(t, result.IsError, "the attempt over the limit is refused")
	assert.Contains(t, result.Content[0], "Rate limit exceeded")
	assert.Equal(t, limit, oauthHandler.challenges, "the refused attempt creates no challenge")
}
