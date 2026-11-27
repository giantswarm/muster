package aggregator

import (
	"context"

	"github.com/giantswarm/muster/v5/internal/api"
	internalmcp "github.com/giantswarm/muster/v5/internal/mcpserver"
)

// HeaderCallerKind tells a server muster's governance configuration names
// (for example an agent manager or a connector manager) that the call
// comes from an agent: muster forwards the person's identity for agents and
// people alike, so the server cannot tell them apart. agent-manager keeps no
// exception on a save made by an agent. muster sends it only for agent
// callers, only to those servers; a client's own headers never reach a
// server, so a client cannot set it.
const HeaderCallerKind = "X-Muster-Caller-Kind"

// withCallerKind returns the context a call to server runs under: for an
// agent caller's call to a server the governance configuration names, it
// carries HeaderCallerKind, which every HTTP-based client adds to its
// requests whatever connection the call takes (shared, per session, token
// exchange or forwarding). A server reached over stdio cannot carry it;
// agent callers are refused there (serverToolApproval).
func withCallerKind(ctx context.Context, server string) context.Context {
	if !api.GetGovernance().NamesServer(server) || !api.IsAgentCaller(api.CallerKind(ctx, getSessionIDFromContext(ctx))) {
		return ctx
	}
	return internalmcp.WithCallHeaders(ctx, map[string]string{HeaderCallerKind: "agent"})
}
