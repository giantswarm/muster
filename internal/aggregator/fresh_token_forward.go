package aggregator

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
)

// A session connected to a token-forwarding server presents the person's
// current token on every request of its pooled connection: a tool call, a
// re-listing of the server's capabilities. Right after the issuer rotated its
// signing key, a new login's token is refused there with 401 until the
// backend (or the gateway in front of it) fetched the new key, just as on a
// connect (refusedFreshToken). Such a refusal does not end the session's
// connection: the call is answered with a transient error and the re-listing
// is retried by muster until the token is too old to be the key's fault.

// tokenForwardingClient is the client of a token-forwarding connection. It
// remembers the token its last request presented, which its header func
// resolves anew per request, so a refusal is judged on the token the backend
// actually saw.
type tokenForwardingClient struct {
	*internalmcp.StreamableHTTPClient
	presented *atomic.Pointer[string]
}

// presentingHeaderFunc wraps a token-forwarding header func so the token it
// resolves for each request is recorded in presented.
func presentingHeaderFunc(headerFunc func(context.Context) map[string]string, presented *atomic.Pointer[string]) func(context.Context) map[string]string {
	return func(ctx context.Context) map[string]string {
		headers := headerFunc(ctx)
		token := strings.TrimPrefix(headers[pkgoauth.HeaderAuthorization], pkgoauth.SchemeBearer+" ")
		presented.Store(&token)
		return headers
	}
}

// presentedToken returns the token the client's last request presented, or
// "" before its first request.
func (c *tokenForwardingClient) presentedToken() string {
	if token := c.presented.Load(); token != nil {
		return *token
	}
	return ""
}

// refusedFreshForward reports whether err is a token-forwarding backend's 401
// on a request of client for a token issued within ssoFreshTokenWindow
// (refusedFreshToken). A client of any other kind never is.
func refusedFreshForward(client MCPClient, err error) bool {
	forwarding, ok := client.(*tokenForwardingClient)
	return ok && refusedFreshToken(forwarding.presentedToken(), err)
}

// freshTokenRefusedAnswer answers a tool call the backend refused with a
// fresh-token 401: a transient error, not the sign-in challenge, since the
// person's login is valid and signing in again would not help.
func freshTokenRefusedAnswer(serverName string) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("Server '%s' refused a token issued moments ago: "+
		"it may not know the identity provider's new signing key yet. "+
		"The session stays connected; retry the call in a minute.", serverName))
}

// relistSession re-lists a per-session server through the session's client
// (refreshSessionCapabilities). When the backend refuses the session's fresh
// token, muster re-lists again on its own after the short transport backoff
// (refusals counts the refusals so far), as long as the session still holds
// that client and its token is fresh: once the backend knows the key, the
// session's capabilities are current again without a request from the person.
func (a *AggregatorServer) relistSession(serverName, sessionID string, client MCPClient, trigger refreshTrigger, refusals int) {
	ctx := a.refreshContext()
	err := a.refreshSessionCapabilities(ctx, serverName, sessionID, client, trigger)
	if err == nil {
		return
	}
	if !refusedFreshForward(client, err) {
		logging.Warn("Aggregator", "Session capability refresh (%s): failed to list tools for %s (session %s): %v",
			trigger, serverName, logging.TruncateIdentifier(sessionID), err)
		return
	}

	delay := ssoTransportBackoffDuration(refusals + 1)
	logging.Warn("Aggregator", "Session capability refresh (%s): fresh token refused by %s (session %s): the backend may not know the issuer's new signing key yet, re-listing in %s: %v",
		trigger, serverName, logging.TruncateIdentifier(sessionID), delay, err)
	a.retryAfter(delay, func() {
		if a.ctx != nil && a.ctx.Err() != nil {
			return
		}
		if a.connPool == nil || !a.connPool.Holds(sessionID, serverName, client) {
			return
		}
		_, _, _ = a.notifRefreshGroup.Do(sessionRefreshKey(sessionID, serverName), func() (any, error) {
			a.relistSession(serverName, sessionID, client, refreshByRetry, refusals+1)
			return nil, nil
		})
	})
}
