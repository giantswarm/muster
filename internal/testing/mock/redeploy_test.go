package mock

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

// mcpPost sends one JSON-RPC body to endpoint with the given bearer and
// session id (either may be empty) and returns the status and the session id
// the server answered with.
func mcpPost(t *testing.T, endpoint, body, bearer, session string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Mcp-Session-Id"), string(data)
}

func startPlainMock(t *testing.T) *HTTPServer {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "plain.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("tools:\n  - name: probe\n    description: probe\n    responses:\n      - response: {alive: true}\n"), 0o600))
	srv, err := NewHTTPServerFromConfig(cfg, HTTPTransportStreamableHTTP, false)
	require.NoError(t, err)
	ctx := context.Background()
	_, err = srv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(ctx) })
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, srv.WaitForReady(readyCtx))
	return srv
}

// TestHTTPServerRedeployForgetsSessionsKeepsPortAndTools: after Redeploy the
// old session id is unknown (404), the port never stopped answering, and a
// tool added at runtime is still there for a fresh session.
func TestHTTPServerRedeployForgetsSessionsKeepsPortAndTools(t *testing.T) {
	srv := startPlainMock(t)
	endpoint := srv.Endpoint()
	port := srv.Port()

	status, session, _ := mcpPost(t, endpoint, initializeBody, "", "")
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, session)

	srv.AddDynamicTool(ToolConfig{Name: "added", Description: "added at runtime", Responses: []ToolResponse{{Response: map[string]interface{}{"ok": true}}}})

	require.NoError(t, srv.Redeploy(ToolSetChange{}))
	require.Equal(t, port, srv.Port())
	require.True(t, srv.IsRunning())

	status, _, body := mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "", session)
	require.Equal(t, http.StatusNotFound, status, "the redeployed process must not know the old session")
	require.Contains(t, body, "Invalid session ID")

	status, fresh, _ := mcpPost(t, endpoint, initializeBody, "", "")
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, fresh)
	require.NotEqual(t, session, fresh)
	status, _, body = mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, "", fresh)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"probe"`)
	require.Contains(t, body, `"added"`, "tools added at runtime survive a redeploy")
}

func TestHTTPServerRedeployNeedsARunningServer(t *testing.T) {
	srv := startPlainMock(t)
	require.NoError(t, srv.Stop(context.Background()))
	require.ErrorContains(t, srv.Redeploy(ToolSetChange{}), "not running")
}

func startProtectedMock(t *testing.T, anonymous bool) (*ProtectedMCPServer, *OAuthServer) {
	t.Helper()
	ctx := context.Background()
	oauthServer := NewOAuthServer(OAuthServerConfig{TokenLifetime: time.Hour, AutoApprove: true})
	_, err := oauthServer.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = oauthServer.Stop(ctx) })

	srv, err := NewProtectedMCPServer(ProtectedMCPServerConfig{
		Name:           "rolling",
		OAuthServer:    oauthServer,
		Issuer:         oauthServer.GetIssuerURL(),
		Transport:      HTTPTransportStreamableHTTP,
		StartAnonymous: anonymous,
		Tools:          []ToolConfig{{Name: "op", Description: "op", Responses: []ToolResponse{{Response: map[string]interface{}{"status": "ok"}}}}},
	})
	require.NoError(t, err)
	_, err = srv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(ctx) })
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, srv.WaitForReady(readyCtx))
	return srv, oauthServer
}

func wellKnownStatus(t *testing.T, srv *ProtectedMCPServer) int {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/.well-known/oauth-protected-resource", srv.Port()))
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestProtectedMCPServerAuthFlip: a server started anonymous accepts a
// token-less initialize and publishes no resource metadata; flipped to
// required it answers 401 with the RFC 9728 challenge and serves the
// metadata; flipped back it is anonymous again. The flag survives a Redeploy.
func TestProtectedMCPServerAuthFlip(t *testing.T) {
	srv, _ := startProtectedMock(t, true)
	endpoint := srv.Endpoint()
	require.False(t, srv.AuthRequired())

	status, _, _ := mcpPost(t, endpoint, initializeBody, "", "")
	require.Equal(t, http.StatusOK, status, "an anonymous pod accepts the token-less probe")
	require.Equal(t, http.StatusNotFound, wellKnownStatus(t, srv), "an anonymous pod publishes no resource metadata")

	srv.SetAuthRequired(true)
	require.True(t, srv.AuthRequired())
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(initializeBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Contains(t, resp.Header.Get("WWW-Authenticate"), "resource_metadata=")
	require.Equal(t, http.StatusOK, wellKnownStatus(t, srv))

	require.NoError(t, srv.Redeploy(ToolSetChange{}))
	require.True(t, srv.AuthRequired(), "a redeploy keeps the auth requirement")
	status, _, _ = mcpPost(t, endpoint, initializeBody, "", "")
	require.Equal(t, http.StatusUnauthorized, status)

	srv.SetAuthRequired(false)
	status, _, _ = mcpPost(t, endpoint, initializeBody, "", "")
	require.Equal(t, http.StatusOK, status)
}

// TestProtectedMCPServerRedeployForgetsSessions: a session established with a
// valid token is unknown after Redeploy while the token is still accepted.
func TestProtectedMCPServerRedeployForgetsSessions(t *testing.T) {
	srv, oauthServer := startProtectedMock(t, false)
	token := oauthServer.GenerateTestToken("test-client", "openid profile").AccessToken
	endpoint := srv.Endpoint()

	status, session, _ := mcpPost(t, endpoint, initializeBody, token, "")
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, session)

	srv.AddDynamicTool(ToolConfig{Name: "added", Description: "added", Responses: []ToolResponse{{Response: map[string]interface{}{"ok": true}}}})
	srv.RemoveDynamicTool("op")
	require.NoError(t, srv.Redeploy(ToolSetChange{}))

	status, _, body := mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, token, session)
	require.Equal(t, http.StatusNotFound, status)
	require.Contains(t, body, "Invalid session ID")

	status, fresh, _ := mcpPost(t, endpoint, initializeBody, token, "")
	require.Equal(t, http.StatusOK, status)
	status, _, body = mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, token, fresh)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"added"`, "a tool added at runtime survives the redeploy")
	require.NotContains(t, body, `"name":"op"`, "a tool removed at runtime stays removed")

	require.NoError(t, srv.Stop(context.Background()))
	require.ErrorContains(t, srv.Redeploy(ToolSetChange{}), "not running")
}

func TestOffsetClockFollowsTheSystemTime(t *testing.T) {
	c := NewOffsetClock()
	require.WithinDuration(t, time.Now(), c.Now(), 50*time.Millisecond)
	c.Advance(time.Hour)
	require.WithinDuration(t, time.Now().Add(time.Hour), c.Now(), 50*time.Millisecond)
	var _ Advancer = c
	var _ Advancer = NewMockClock(time.Time{})
}

// TestOAuthServerDefaultClockIsAdvanceable: a server without a configured
// clock runs on an OffsetClock, so test_advance_clock can move it.
func TestOAuthServerDefaultClockIsAdvanceable(t *testing.T) {
	srv := NewOAuthServer(OAuthServerConfig{TokenLifetime: time.Hour})
	advancer, ok := srv.GetClock().(Advancer)
	require.True(t, ok)
	before := srv.GetClock().Now()
	advancer.Advance(2 * time.Hour)
	require.True(t, srv.GetClock().Now().Sub(before) >= 2*time.Hour)
}

// TestHTTPServerRedeployWithAnotherToolSet: the process that takes over
// offers other tools -- probe gone, report new -- and announces it to nobody:
// the old session is still unknown, a fresh session lists the new set and
// calls the new tool.
func TestHTTPServerRedeployWithAnotherToolSet(t *testing.T) {
	srv := startPlainMock(t)
	endpoint := srv.Endpoint()

	status, session, _ := mcpPost(t, endpoint, initializeBody, "", "")
	require.Equal(t, http.StatusOK, status)

	require.NoError(t, srv.Redeploy(ToolSetChange{
		Add:    []ToolConfig{{Name: "report", Description: "new image", Responses: []ToolResponse{{Response: map[string]interface{}{"report": "ok"}}}}},
		Remove: []string{"probe"},
	}))

	status, _, _ = mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "", session)
	require.Equal(t, http.StatusNotFound, status, "the redeployed process must not know the old session")

	status, fresh, _ := mcpPost(t, endpoint, initializeBody, "", "")
	require.Equal(t, http.StatusOK, status)
	status, _, body := mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, "", fresh)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"name":"report"`, "the new image's tool is served")
	require.NotContains(t, body, `"name":"probe"`, "the dropped tool is gone")
	status, _, body = mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"report","arguments":{}}}`, "", fresh)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `report`)
	require.Contains(t, body, `ok`)
}

// TestProtectedMCPServerRedeployWithAnotherToolSet: the same roll behind an
// OAuth-protected backend, the token still accepted.
func TestProtectedMCPServerRedeployWithAnotherToolSet(t *testing.T) {
	srv, oauthServer := startProtectedMock(t, false)
	token := oauthServer.GenerateTestToken("test-client", "openid profile").AccessToken
	endpoint := srv.Endpoint()

	status, session, _ := mcpPost(t, endpoint, initializeBody, token, "")
	require.Equal(t, http.StatusOK, status)

	require.NoError(t, srv.Redeploy(ToolSetChange{
		Add:    []ToolConfig{{Name: "report", Description: "new image", Responses: []ToolResponse{{Response: map[string]interface{}{"report": "ok"}}}}},
		Remove: []string{"op"},
	}))

	status, _, _ = mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, token, session)
	require.Equal(t, http.StatusNotFound, status)

	status, fresh, _ := mcpPost(t, endpoint, initializeBody, token, "")
	require.Equal(t, http.StatusOK, status)
	status, _, body := mcpPost(t, endpoint, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, token, fresh)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"name":"report"`)
	require.NotContains(t, body, `"name":"op"`)
}

func TestToolSetChangeApplyAndSummary(t *testing.T) {
	served := []ToolConfig{{Name: "probe"}, {Name: "status"}}

	require.Equal(t, "tools kept", ToolSetChange{}.Summary())
	require.Equal(t, served, ToolSetChange{}.apply(served))

	change := ToolSetChange{Add: []ToolConfig{{Name: "report"}, {Name: "status", Description: "replaced"}}, Remove: []string{"probe", "unknown"}}
	require.Equal(t, "tools changed: +report +status -probe -unknown", change.Summary())
	require.Equal(t, []ToolConfig{{Name: "report"}, {Name: "status", Description: "replaced"}}, change.apply(served),
		"removed names gone, a same-named tool replaced once, the rest kept")
}
