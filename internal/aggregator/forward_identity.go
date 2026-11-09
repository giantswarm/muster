package aggregator

import (
	"context"

	"github.com/mark3labs/mcp-go/client/transport"

	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// identityHeaderFunc returns the per-request header function of a session's
// connection to a server with spec.auth.forwardIdentity, or nil for every
// other server, which then never receives the header.
//
// The function sends the session's upstream ID token in HeaderMusterIDToken
// next to the pinned grant the OAuth handler puts in Authorization, which it
// leaves alone. The token is the one a forwardToken server would receive in
// Authorization. It resolves the session's provider ID token from the request
// context or OAuth store and refreshes when needed. A resource bearer is never
// sent in this header. A session without a provider ID token sends no header.
//
// mcp-go calls the function concurrently for the listening GET and for tool
// calls; it holds no state of its own, and the refresh is coalesced per user
// by the provider's single-flight.
func (a *AggregatorServer) identityHeaderFunc(sessionID string, serverInfo *ServerInfo) transport.HTTPHeaderFunc {
	if serverInfo == nil || !serverInfo.AuthConfig.ForwardsIdentity() {
		return nil
	}
	musterIssuer := a.getMusterIssuer()
	refresher := a.sessionRefresher()
	return func(ctx context.Context) map[string]string {
		token := getIDTokenForForwarding(ctx, sessionID, musterIssuer, refresher)
		if token == "" {
			return nil
		}
		return map[string]string{pkgoauth.HeaderMusterIDToken: token}
	}
}
