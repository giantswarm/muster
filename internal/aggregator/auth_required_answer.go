package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// sessionNotAuthenticatedError says the calling session holds no
// authentication for the server: no authenticated mark, and no grant of the
// person's that could connect it. cause is the failure of connecting with such
// a grant, when one was tried.
type sessionNotAuthenticatedError struct {
	server string
	cause  error
}

func (e *sessionNotAuthenticatedError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("user not authenticated to server %s (connecting with the person's existing grant failed: %v)", e.server, e.cause)
	}
	return fmt.Sprintf("user not authenticated to server %s", e.server)
}

func (e *sessionNotAuthenticatedError) Unwrap() error { return e.cause }

// reason words the error for the auth_required answer.
func (e *sessionNotAuthenticatedError) reason() string {
	if e.cause != nil {
		return fmt.Sprintf("connecting with your existing grant failed: %v", e.cause)
	}
	return "this session is not authenticated to it"
}

// credentialRefused reports whether err says the backend refused the
// credential a session's connection presented: a 401, in any of the shapes
// the transports report it, or the auth-required error the connect path
// builds from one.
func credentialRefused(err error) bool {
	return is401Error(err) || api.IsAuthRequiredError(err)
}

// authRequiredAnswer answers a tool call the session cannot make because it is
// not authenticated to the server -- it never was, or the backend just refused
// its credential (reason says which). The answer is the auth_required
// challenge core_auth_login gives for the server: the sign-in link in the text
// and in structuredContent.authUrl, marked isError because the tool did not
// run. A client that keys its sign-in on that shape -- an agent, a portal --
// therefore sees the same answer whether the session is new or lost its
// connection mid-way, never a transport error (#1276).
//
// The login is core_auth_login's own: rate limit, the operator's pin, the
// suspension check. When it connects the session without a browser -- a
// stored grant the server still accepts, the pooled connection alone was
// dead -- the call is made once on the new connection instead.
//
// An SSO server (token forwarding, token exchange) has no login of its own:
// its connection is made from the session's muster token, so the answer says
// to sign in to muster again.
func (a *AggregatorServer) authRequiredAnswer(ctx context.Context, serverName, originalToolName string, args map[string]any, sessionID, sub, reason string) (*mcp.CallToolResult, error) {
	info, ok := a.registry.GetServerInfo(serverName)
	if !ok {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}
	if !needsManualLogin(info) {
		return authRequiredResult(serverName, reason, &api.CallToolResult{
			Content: []any{fmt.Sprintf("Server '%s' is connected with your muster session (SSO) and has no sign-in of its own. "+
				"Sign in to muster again to reconnect it.", serverName)},
		}), nil
	}

	login, err := NewAuthToolProvider(a).handleAuthLogin(ctx, map[string]any{resourceServerArg: serverName})
	if err != nil {
		return nil, fmt.Errorf("server %s requires authentication and the sign-in could not be started: %w", serverName, err)
	}
	if !login.IsError && loginConnected(login) {
		logging.InfoWithAttrs("Aggregator", "Session reconnected to the server from its stored grant, calling the tool on the new connection",
			slog.String("sessionID", logging.TruncateIdentifier(sessionID)),
			slog.String("server", serverName))
		client, cleanup, err := a.getOrCreateClientForToolCall(ctx, serverName, sessionID, sub)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to server %s: %w", serverName, err)
		}
		defer cleanup()
		return client.CallTool(ctx, originalToolName, args)
	}
	return authRequiredResult(serverName, reason, login), nil
}

// loginConnected reports whether a core_auth_login answer says the session is
// connected to the server: it was already, or a stored grant connected it.
func loginConnected(login *api.CallToolResult) bool {
	text := contentText(login)
	return strings.Contains(text, api.AuthMsgSuccessfullyConnected) ||
		strings.Contains(text, api.AuthMsgAlreadyConnected) ||
		strings.Contains(text, api.AuthMsgAlreadyAuthenticated)
}

// authRequiredResult is the tool result carrying an auth_required answer:
// the marker and the server first, then what the login said -- the challenge
// with its sign-in link, or why no sign-in could be started. The login's
// structuredContent (authUrl, clientIdMethod) is kept, with status and server
// added, so a client reads the link without parsing the text.
func authRequiredResult(serverName, reason string, login *api.CallToolResult) *mcp.CallToolResult {
	structured := map[string]any{
		"status": "auth_required",
		"server": serverName,
	}
	if login != nil {
		if fields, ok := login.StructuredContent.(map[string]any); ok {
			maps.Copy(structured, fields)
		}
	}
	text := fmt.Sprintf("auth_required: server '%s' requires authentication before its tools can be called (%s).", serverName, reason)
	if said := contentText(login); said != "" {
		text += "\n\n" + said
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(text)},
		IsError:           true,
		StructuredContent: structured,
	}
}

// contentText joins the text parts of a tool result.
func contentText(result *api.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, c := range result.Content {
		if s, ok := c.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}
