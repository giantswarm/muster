package aggregator

import (
	"context"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/giantswarm/muster/v5/internal/api"
)

// HeaderCallerKind tells a server muster's governance configuration names
// (agent-manager, the Platform Manager, the connector manager) that the call
// comes from an agent: muster forwards the person's identity for agents and
// people alike, so the server cannot tell them apart. agent-manager keeps no
// exception on a save made by an agent. muster sends it only for agent
// callers, only to those servers; a client's own headers never reach a
// server, so a client cannot set it.
const HeaderCallerKind = "X-Muster-Caller-Kind"

// callerKindHeaderFunc returns the header function adding HeaderCallerKind
// to an agent caller's requests to a server the governance configuration
// names, nil for every other server.
func callerKindHeaderFunc(sessionID string, serverInfo *ServerInfo) transport.HTTPHeaderFunc {
	if serverInfo == nil || !api.GetGovernance().NamesServer(serverInfo.Name) {
		return nil
	}
	return func(ctx context.Context) map[string]string {
		if !api.IsAgentCaller(api.CallerKind(ctx, sessionID)) {
			return nil
		}
		return map[string]string{HeaderCallerKind: "agent"}
	}
}
