package aggregator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// numberingOAuthHandler issues challenges with links of their own, so a test
// tells a reused challenge from a new one.
type numberingOAuthHandler struct {
	issuerMockOAuthHandler
	challenges int
}

func (m *numberingOAuthHandler) CreateAuthChallenge(_ context.Context, _ api.AuthChallengeParams) (*api.AuthChallenge, error) {
	m.challenges++
	return &api.AuthChallenge{AuthURL: fmt.Sprintf("https://muster.example.com/oauth/proxy/start?state=%d", m.challenges)}, nil
}

// pollFixture is a session signed out of a manual-login server, polling one
// of its tools, with the person's sign-in rate limit in force and a clock the
// test moves.
type pollFixture struct {
	*answerFixture
	handler *numberingOAuthHandler
	now     time.Time
}

func newPollFixture(t *testing.T) *pollFixture {
	t.Helper()
	pinned := &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: answerIssuer, Scopes: "repo"}}
	f := &pollFixture{answerFixture: newAnswerFixture(t, pinned, nil), now: time.Unix(1_700_000_000, 0)}
	f.handler = &numberingOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}}
	api.RegisterOAuthHandler(f.handler)
	f.agg.authRateLimiter = NewAuthRateLimiter(DefaultAuthRateLimiterConfig())
	t.Cleanup(f.agg.authRateLimiter.Stop)
	f.agg.pendingSignIns.now = func() time.Time { return f.now }
	require.NoError(t, f.agg.authStore.Revoke(context.Background(), answerSession, answerServer))
	f.agg.connPool.Evict(answerSession, answerServer)
	return f
}

func authURLOf(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.True(t, result.IsError, "the tool did not run")
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "auth_required", structured["status"])
	url, _ := structured["authUrl"].(string)
	require.NotEmpty(t, url, "the answer carries a sign-in link: %s", mcpText(result))
	return url
}

// A client polling a gated tool far more often than the person's sign-in
// limit allows gets the same challenge every time, spends one attempt and is
// never locked out.
func TestPendingSignIn_PollingReusesOneChallenge(t *testing.T) {
	f := newPollFixture(t)
	limit := DefaultAuthRateLimiterConfig().MaxAttempts

	first := authURLOf(t, f.call(t))
	for i := 0; i < 3*limit; i++ {
		result := f.call(t)
		assert.NotContains(t, mcpText(result), "Rate limit exceeded", "poll %d", i+1)
		assert.Equal(t, first, authURLOf(t, result), "poll %d", i+1)
	}
	assert.Equal(t, 1, f.handler.challenges)
	assert.Equal(t, limit-1, f.agg.authRateLimiter.RemainingAttempts(answerSubject), "one attempt for one sign-in")
	assert.Equal(t, 0, f.client.callCount)
}

// The metatools' answer for a signed-out server (call_tool) shares the
// pending challenge with the direct call.
func TestPendingSignIn_AnswerSignInSharesTheChallenge(t *testing.T) {
	f := newPollFixture(t)
	first := authURLOf(t, f.call(t))

	ctx := api.WithSubject(api.WithSessionID(context.Background(), answerSession), answerSubject)
	result, connected, err := f.agg.AnswerSignIn(ctx, answerServer)
	require.NoError(t, err)
	assert.False(t, connected)
	assert.Equal(t, first, authURLOf(t, result))
	assert.Equal(t, 1, f.handler.challenges)
}

// An expired challenge is replaced by a new one, which is reused in turn.
func TestPendingSignIn_ExpiredChallengeIsReplaced(t *testing.T) {
	f := newPollFixture(t)
	first := authURLOf(t, f.call(t))

	f.now = f.now.Add(pendingSignInTTL - time.Second)
	assert.Equal(t, first, authURLOf(t, f.call(t)), "still pending")

	f.now = f.now.Add(time.Second)
	second := authURLOf(t, f.call(t))
	assert.NotEqual(t, first, second, "expired")
	assert.Equal(t, second, authURLOf(t, f.call(t)))
	assert.Equal(t, 2, f.handler.challenges)
}

// A completed sign-in uses the challenge up: once the connection it made is
// refused, the answer is a new challenge.
func TestPendingSignIn_CompletedChallengeIsReplaced(t *testing.T) {
	f := newPollFixture(t)
	first := authURLOf(t, f.call(t))

	// The person signed in with the link; the connection the sign-in made
	// is later refused by the backend.
	ctx := context.Background()
	require.NoError(t, f.agg.authStore.MarkAuthenticated(ctx, answerSession, answerServer))
	f.client.callToolErr = bare401()
	f.agg.connPool.Put(answerSession, answerServer, f.client)

	second := authURLOf(t, f.call(t))
	assert.NotEqual(t, first, second)
	assert.Contains(t, mcpText(f.call(t)), "not authenticated to it", "the refused connection is gone")
	assert.Equal(t, 2, f.handler.challenges)
}

// The OAuth callback of a completed sign-in forgets the session's challenge.
func TestPendingSignIn_AuthCompletionForgetsTheChallenge(t *testing.T) {
	a := newSuspensionTestServer(t)
	registerSuspendableServer(t, a.registry, "miro", true, api.StateDisconnected)
	am := &AggregatorManager{aggregatorServer: a}
	key := pendingSignInKey{sessionID: "test-session", server: "miro"}
	a.pendingSignIns.put(key, &api.CallToolResult{StructuredContent: map[string]any{"authUrl": "https://link"}})

	require.NoError(t, am.handleAuthCompletion(context.Background(), "test-session", "test-user", "miro", "access-token"))

	_, pending := a.pendingSignIns.get(key)
	assert.False(t, pending)
}

// A logout forgets the session's challenge for the server.
func TestPendingSignIn_LogoutForgetsTheChallenge(t *testing.T) {
	f := newPollFixture(t)
	first := authURLOf(t, f.call(t))

	f.agg.disconnectSessionServer(context.Background(), answerSession, answerServer)

	assert.NotEqual(t, first, authURLOf(t, f.call(t)))
	assert.Equal(t, 2, f.handler.challenges)
}

// Each session and each server has a challenge of its own.
func TestPendingSignIn_ChallengesAreNotShared(t *testing.T) {
	var p pendingSignIns
	logins := 0
	login := func() (*api.CallToolResult, error) {
		logins++
		return &api.CallToolResult{StructuredContent: map[string]any{"authUrl": fmt.Sprintf("https://link/%d", logins)}}, nil
	}
	answer := func(session, server string) string {
		result, err := p.answer(session, server, login)
		require.NoError(t, err)
		return result.StructuredContent.(map[string]any)["authUrl"].(string)
	}

	a1 := answer("s1", "a")
	assert.Equal(t, a1, answer("s1", "a"))
	b1 := answer("s1", "b")
	a2 := answer("s2", "a")
	assert.NotEqual(t, a1, b1, "another server")
	assert.NotEqual(t, a1, a2, "another session")
	assert.NotEqual(t, b1, a2)
	assert.Equal(t, 3, logins)
}

// Only a challenge is kept: a login that connected the session, refused the
// sign-in or failed runs again on the next answer.
func TestPendingSignIn_KeepsOnlyChallenges(t *testing.T) {
	answers := map[string]func() (*api.CallToolResult, error){
		"connected": func() (*api.CallToolResult, error) {
			return &api.CallToolResult{Content: []any{"Server 'a' is already authenticated."}}, nil
		},
		"refused": func() (*api.CallToolResult, error) {
			return &api.CallToolResult{Content: []any{"Rate limit exceeded."}, IsError: true}, nil
		},
		"failed": func() (*api.CallToolResult, error) { return nil, fmt.Errorf("boom") },
	}
	for name, answer := range answers {
		t.Run(name, func(t *testing.T) {
			var p pendingSignIns
			logins := 0
			login := func() (*api.CallToolResult, error) { logins++; return answer() }
			_, _ = p.answer("s", "a", login)
			_, _ = p.answer("s", "a", login)
			assert.Equal(t, 2, logins)
		})
	}
}
