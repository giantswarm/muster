package aggregator

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
)

// bare401 is the error a pooled client without an OAuth handler -- a
// forwardToken connection -- returns when the backend answers 401: mcp-go's
// AuthorizationRequiredError inside its transport.Error, wrapped by the
// client's own call path. Its text is the incident's
// "failed to call tool: transport error: authorization required" (#1276).
func bare401() error {
	return fmt.Errorf("failed to call tool: %w", transport.NewError(&transport.AuthorizationRequiredError{}))
}

func TestCredentialRefused(t *testing.T) {
	assert.True(t, credentialRefused(bare401()), "the streamable transport's bare 401")
	assert.True(t, credentialRefused(fmt.Errorf("x: %w", transport.ErrUnauthorized)), "the SSE transport's bare 401")
	assert.True(t, credentialRefused(fmt.Errorf("x: %w", transport.ErrOAuthAuthorizationRequired)), "no token in the store")
	assert.True(t, credentialRefused(&transport.OAuthAuthorizationRequiredError{}), "the OAuth transport's 401")
	assert.False(t, credentialRefused(errors.New("connection refused")))
	assert.False(t, credentialRefused(nil))
}

// answerFixture is an aggregator with one manual-login server pinned to an
// authorization server, a challenge-answering OAuth handler, and a session
// whose pooled client answers what the test configures.
type answerFixture struct {
	agg     *AggregatorServer
	handler *challengeCountingOAuthHandler
	client  *callToolMockClient
}

const (
	answerSession = "session-1"
	answerSubject = "alice"
	answerServer  = "svc"
	answerIssuer  = "https://github.example.com/login/oauth"
)

func newAnswerFixture(t *testing.T, auth *api.MCPServerAuth, callErr error) *answerFixture {
	t.Helper()
	a := newTestAggregatorWithPool(t)
	require.NoError(t, a.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: answerServer, ToolPrefix: answerServer},
		URL:                "https://svc.example.com/mcp",
		AuthInfo:           &AuthInfo{Issuer: answerIssuer, Scope: "repo"},
		AuthConfig:         auth,
	}))
	handler := &challengeCountingOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}}
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	ctx := context.Background()
	require.NoError(t, a.authStore.MarkAuthenticated(ctx, answerSession, answerServer))
	require.NoError(t, a.capabilityStore.Set(ctx, answerSession, answerServer, &oauthstore.Capabilities{
		Tools: []mcp.Tool{{Name: "op"}},
	}))
	client := &callToolMockClient{callToolErr: callErr, callToolResult: &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("ran")}}}
	a.connPool.Put(answerSession, answerServer, client)
	return &answerFixture{agg: a, handler: handler, client: client}
}

func (f *answerFixture) call(t *testing.T) *mcp.CallToolResult {
	t.Helper()
	ctx := api.WithSubject(api.WithSessionID(context.Background(), answerSession), answerSubject)
	result, err := f.agg.callToolWithTokenExchangeRetry(ctx, answerServer, "op", map[string]any{}, answerSession, answerSubject)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func mcpText(result *mcp.CallToolResult) string {
	var text string
	for _, c := range result.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			text += tc.Text
		}
	}
	return text
}

// The incident: a session connected under the previous configuration calls a
// tool of a server now pinned to an authorization server; the backend answers
// the stale credential with a bare 401. The answer is the server's
// auth_required challenge with the sign-in link, the session is back to
// auth_required for the server, and its cached tools stay resolvable.
func TestCallTool_Backend401AnswersAuthRequiredChallenge(t *testing.T) {
	pinned := &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: answerIssuer, Scopes: "repo"}}
	f := newAnswerFixture(t, pinned, bare401())

	result := f.call(t)

	assert.True(t, result.IsError, "the tool did not run")
	text := mcpText(result)
	assert.Contains(t, text, "auth_required: server 'svc'")
	assert.Contains(t, text, "rejected this session's credentials")
	assert.Contains(t, text, "https://muster.example.com/oauth/proxy/start", "the sign-in link is in the text")
	assert.NotContains(t, text, "transport error")
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "auth_required", structured["status"])
	assert.Equal(t, "svc", structured["server"])
	assert.Equal(t, "https://muster.example.com/oauth/proxy/start?state=x", structured["authUrl"])
	assert.Equal(t, 1, f.handler.challenges, "one challenge, from core_auth_login's own path")

	ctx := context.Background()
	authenticated, _ := f.agg.authStore.IsAuthenticated(ctx, answerSession, answerServer)
	assert.False(t, authenticated, "the refused credential's mark is revoked")
	_, pooled := f.agg.connPool.Get(answerSession, answerServer)
	assert.False(t, pooled, "the refused connection is closed")
	caps, _ := f.agg.capabilityStore.Exists(ctx, answerSession, answerServer)
	assert.True(t, caps, "the cached tools stay resolvable for the next call")
	assert.Equal(t, 1, f.client.callCount)
}

// A session that is not authenticated to a manual-login server -- the state
// the reset leaves it in -- gets the same challenge on its next call, not
// "user not authenticated to server".
func TestCallTool_NotAuthenticatedAnswersAuthRequiredChallenge(t *testing.T) {
	pinned := &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: answerIssuer, Scopes: "repo"}}
	f := newAnswerFixture(t, pinned, nil)
	require.NoError(t, f.agg.authStore.Revoke(context.Background(), answerSession, answerServer))
	f.agg.connPool.Evict(answerSession, answerServer)

	result := f.call(t)

	assert.True(t, result.IsError)
	text := mcpText(result)
	assert.Contains(t, text, "auth_required: server 'svc'")
	assert.Contains(t, text, "not authenticated to it")
	assert.Contains(t, text, "https://muster.example.com/oauth/proxy/start")
	assert.Equal(t, 0, f.client.callCount, "nothing was called on the retired client")
}

// An SSO server's connection is made from the session's muster token: a
// refused credential answers auth_required without a sign-in link and says
// to sign in to muster again; the session is back to auth_required for it.
func TestCallTool_Backend401OnSSOServerAnswersAuthRequiredWithoutChallenge(t *testing.T) {
	f := newAnswerFixture(t, &api.MCPServerAuth{Type: "oauth", ForwardToken: true}, bare401())

	result := f.call(t)

	assert.True(t, result.IsError)
	text := mcpText(result)
	assert.Contains(t, text, "auth_required: server 'svc'")
	assert.Contains(t, text, "Sign in to muster again")
	assert.NotContains(t, text, "transport error")
	assert.Equal(t, 0, f.handler.challenges, "no core_auth_login challenge for an SSO server")
	authenticated, _ := f.agg.authStore.IsAuthenticated(context.Background(), answerSession, answerServer)
	assert.False(t, authenticated)
	_, pooled := f.agg.connPool.Get(answerSession, answerServer)
	assert.False(t, pooled)
}

// Any other failure of the call is returned as it is.
func TestCallTool_OtherErrorsPassThrough(t *testing.T) {
	pinned := &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{Issuer: answerIssuer}}
	f := newAnswerFixture(t, pinned, errors.New("backend exploded"))

	ctx := api.WithSubject(api.WithSessionID(context.Background(), answerSession), answerSubject)
	_, err := f.agg.callToolWithTokenExchangeRetry(ctx, answerServer, "op", map[string]any{}, answerSession, answerSubject)
	require.EqualError(t, err, "backend exploded")
	authenticated, _ := f.agg.authStore.IsAuthenticated(context.Background(), answerSession, answerServer)
	assert.True(t, authenticated, "a failure that is not a refusal keeps the session's authentication")
	assert.Equal(t, 0, f.handler.challenges)
}
