package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const (
	testSignInKey   = "muster_sign_in"
	testSignInURL   = "https://dex.example.com/auth?state=abc"
	testSignInState = `{"opaque":"state, echoed verbatim"}`
)

// signInServer answers call_tool the way muster answers a call that needs a
// sign-in: with an input_required URL elicitation to a client that declared
// elicitation.url, with an auth_required text to any other, and, on a retry
// carrying the answer, by running the call (accept) or refusing it.
type signInServer struct {
	mu       sync.Mutex
	versions []string
	declared []bool
	retries  []mcp.MultiRoundTripParams
	elicit   mcp.ElicitationParams
}

func (s *signInServer) handle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions = append(s.versions, server.RequestProtocolVersion(ctx))
	declared := declaresURLElicitation(ctx)
	s.declared = append(s.declared, declared)
	if !declared {
		return mcp.NewToolResultText(`{"status":"auth_required","authUrl":"` + testSignInURL + `"}`), nil
	}
	answer := server.ElicitationResponse(req.Params.InputResponses, testSignInKey)
	if answer == nil {
		return server.NewInputRequestBuilder(testSignInState).Elicit(testSignInKey, s.elicit).ToolResult(), nil
	}
	s.retries = append(s.retries, req.Params.MultiRoundTripParams)
	if answer.Action != mcp.ElicitationResponseActionAccept {
		return mcp.NewToolResultError("The tool did not run: the person answered " + string(answer.Action) + " to signing in to github."), nil
	}
	return mcp.NewToolResultText("ran"), nil
}

func declaresURLElicitation(ctx context.Context) bool {
	var capabilities mcp.ClientCapabilities
	if info := server.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern {
		if info.ClientCapabilities != nil {
			capabilities = *info.ClientCapabilities
		}
	} else if session, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo); ok {
		capabilities = session.GetClientCapabilities()
	}
	return capabilities.Elicitation != nil && capabilities.Elicitation.URL != nil
}

// newSignInServer serves call_tool over streamable HTTP. A legacy server
// answers server/discover as a server older than 2026-07-28 does: the
// method is unknown.
func newSignInServer(t *testing.T, legacy bool) (*signInServer, string) {
	t.Helper()
	s := &signInServer{elicit: mcp.ElicitationParams{
		Mode:    mcp.ElicitationModeURL,
		Message: "Sign in to github to continue.",
		URL:     testSignInURL,
	}}
	mcpServer := server.NewMCPServer("muster", "1.0.0")
	mcpServer.AddTool(mcp.NewTool("call_tool"), s.handle)
	streamable := server.NewStreamableHTTPServer(mcpServer)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if legacy && r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			if bytes.Contains(body, []byte(`"method":"server/discover"`)) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":0,"error":{"code":-32601,"message":"Method not found"}}`)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		streamable.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return s, ts.URL + "/mcp"
}

// recordingElicitor answers every URL elicitation with action and keeps what
// it was asked.
type recordingElicitor struct {
	action mcp.ElicitationResponseAction
	err    error
	asked  [][]URLElicitation
}

func (r *recordingElicitor) answer(_ context.Context, elicitations []URLElicitation) ([]mcp.ElicitationResponseAction, error) {
	r.asked = append(r.asked, elicitations)
	if r.err != nil {
		return nil, r.err
	}
	actions := make([]mcp.ElicitationResponseAction, len(elicitations))
	for i := range actions {
		actions[i] = r.action
	}
	return actions, nil
}

func connectedClient(t *testing.T, endpoint string, handler URLElicitationHandler) *Client {
	t.Helper()
	c := NewClient(endpoint, nil, TransportStreamableHTTP)
	c.SetConnectRetryBudget(0)
	if handler != nil {
		c.SetURLElicitationHandler(handler)
	}
	require.NoError(t, c.Connect(t.Context()))
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestURLElicitationAcceptRetriesWithTheAnswerAndTheStateVerbatim(t *testing.T) {
	srv, endpoint := newSignInServer(t, false)
	elicitor := &recordingElicitor{action: mcp.ElicitationResponseActionAccept}
	c := connectedClient(t, endpoint, elicitor.answer)

	result, err := c.CallTool(t.Context(), "call_tool", map[string]any{"name": "x_github_list"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "ran", result.Content[0].(mcp.TextContent).Text)

	require.Equal(t, [][]URLElicitation{{{Key: testSignInKey, Message: "Sign in to github to continue.", URL: testSignInURL}}}, elicitor.asked)
	require.Equal(t, []string{api.MultiRoundTripProtocolVersion, api.MultiRoundTripProtocolVersion}, srv.versions)
	require.Equal(t, []bool{true, true}, srv.declared)
	require.Len(t, srv.retries, 1)
	require.Equal(t, testSignInState, srv.retries[0].RequestState)
	require.Equal(t, mcp.ElicitationResponseActionAccept, server.ElicitationResponse(srv.retries[0].InputResponses, testSignInKey).Action)
}

func TestURLElicitationRefusalIsTheServersAnswer(t *testing.T) {
	for _, action := range []mcp.ElicitationResponseAction{mcp.ElicitationResponseActionDecline, mcp.ElicitationResponseActionCancel} {
		t.Run(string(action), func(t *testing.T) {
			srv, endpoint := newSignInServer(t, false)
			c := connectedClient(t, endpoint, (&recordingElicitor{action: action}).answer)

			result, err := c.CallTool(t.Context(), "call_tool", nil)
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.Contains(t, result.Content[0].(mcp.TextContent).Text, "answered "+string(action))
			require.Len(t, srv.retries, 1)
			require.Equal(t, testSignInState, srv.retries[0].RequestState)
		})
	}
}

func TestURLElicitationHandlerErrorEndsTheCallWithoutAnswering(t *testing.T) {
	srv, endpoint := newSignInServer(t, false)
	refusal := errors.New("no terminal")
	c := connectedClient(t, endpoint, (&recordingElicitor{err: refusal}).answer)

	_, err := c.CallTool(t.Context(), "call_tool", nil)
	require.ErrorIs(t, err, refusal)
	require.Empty(t, srv.retries)
}

func TestURLElicitationAsksAgainUpToTheBound(t *testing.T) {
	elicitation := mcp.ElicitationParams{Mode: mcp.ElicitationModeURL, Message: "again", URL: testSignInURL}
	mcpServer := server.NewMCPServer("muster", "1.0.0")
	mcpServer.AddTool(mcp.NewTool("call_tool"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return server.NewInputRequestBuilder("s").Elicit(testSignInKey, elicitation).ToolResult(), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(mcpServer))
	t.Cleanup(ts.Close)
	elicitor := &recordingElicitor{action: mcp.ElicitationResponseActionAccept}
	c := connectedClient(t, ts.URL+"/mcp", elicitor.answer)

	_, err := c.CallTool(t.Context(), "call_tool", nil)
	require.ErrorContains(t, err, "still asks for input")
	require.Len(t, elicitor.asked, maxInputRoundTrips)
}

func TestURLElicitationFormRequestIsUnanswerable(t *testing.T) {
	mcpServer := server.NewMCPServer("muster", "1.0.0")
	mcpServer.AddTool(mcp.NewTool("call_tool"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return server.NewInputRequestBuilder("s").Elicit("form", mcp.ElicitationParams{Message: "name?"}).ToolResult(), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(mcpServer))
	t.Cleanup(ts.Close)
	elicitor := &recordingElicitor{action: mcp.ElicitationResponseActionAccept}
	c := connectedClient(t, ts.URL+"/mcp", elicitor.answer)

	_, err := c.CallTool(t.Context(), "call_tool", nil)
	require.ErrorIs(t, err, ErrUnanswerableInput)
	require.Empty(t, elicitor.asked)
}

func TestURLElicitationSendsTheLastAllowedAnswer(t *testing.T) {
	answers := 0
	mcpServer := server.NewMCPServer("muster", "1.0.0")
	mcpServer.AddTool(mcp.NewTool("call_tool"), func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if server.ElicitationResponse(request.Params.InputResponses, testSignInKey) != nil {
			answers++
		}
		if answers == maxInputRoundTrips {
			return mcp.NewToolResultText("ran"), nil
		}
		return server.NewInputRequestBuilder("s").Elicit(testSignInKey, mcp.ElicitationParams{
			Mode: mcp.ElicitationModeURL, Message: "Continue", URL: testSignInURL,
		}).ToolResult(), nil
	})
	testServer := httptest.NewServer(server.NewStreamableHTTPServer(mcpServer))
	t.Cleanup(testServer.Close)
	elicitor := &recordingElicitor{action: mcp.ElicitationResponseActionAccept}
	client := connectedClient(t, testServer.URL+"/mcp", elicitor.answer)

	result, err := client.CallTool(t.Context(), "call_tool", nil)
	require.NoError(t, err)
	require.Equal(t, "ran", result.Content[0].(mcp.TextContent).Text)
	require.Len(t, elicitor.asked, maxInputRoundTrips)
}

func TestURLElicitationNotDeclaredWithoutAHandler(t *testing.T) {
	srv, endpoint := newSignInServer(t, false)
	c := connectedClient(t, endpoint, nil)

	result, err := c.CallTool(t.Context(), "call_tool", nil)
	require.NoError(t, err)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, "auth_required")
	require.Equal(t, []bool{false}, srv.declared)
	require.Equal(t, api.ClientProtocolVersion, c.negotiatedProtocolVersion())
}

func TestURLElicitationOlderServerKeepsTheAuthRequiredAnswer(t *testing.T) {
	srv, endpoint := newSignInServer(t, true)
	elicitor := &recordingElicitor{action: mcp.ElicitationResponseActionAccept}
	c := connectedClient(t, endpoint, elicitor.answer)

	result, err := c.CallTool(t.Context(), "call_tool", nil)
	require.NoError(t, err)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, "auth_required")
	require.Equal(t, []bool{false}, srv.declared)
	require.Empty(t, elicitor.asked)
	require.False(t, mcp.IsModernProtocol(c.negotiatedProtocolVersion()))
}
