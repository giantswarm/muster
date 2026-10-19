package aggregator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// Visibility of MCP servers (agent-permissions D9). When muster serves the
// MCPServer objects of every namespace, a person sees a server only when
// Kubernetes RBAC lets them list mcpservers in its namespace
// (api.CallerVisibility). A server the caller may not see is absent: from the
// tool, resource and prompt listings, the servers awaiting a sign-in, the
// core tools that list or name servers, and the events. muster refuses a call
// to it and never connects the session to it, forwards a token to it or
// exchanges a token for it. Without the setting every caller sees every
// server and nothing here changes anything.

// seesFunc returns the predicate of the caller's view, nil when the caller
// sees every server.
func seesFunc(view api.ServerVisibility) func(string) bool {
	if !view.Restricted() {
		return nil
	}
	return view.Sees
}

// unseenError is the answer to a request that names something of a server
// the caller may not see: the answer a missing server gets, what, or -- when
// the caller sees no server at all -- why, and that the person must sign in
// again.
func unseenError(view api.ServerVisibility, what string) error {
	if view.Reason != "" {
		return fmt.Errorf("%s. %s", what, view.Reason)
	}
	return errors.New(what)
}

// refuseUnseenServer reports whether the caller of ctx may not see the
// server, and logs the refusal of action for it.
func refuseUnseenServer(ctx context.Context, serverName, action string) bool {
	if api.CallerVisibility(ctx).Sees(serverName) {
		return false
	}
	logging.DebugWithAttrs("Aggregator", "Refused for a server the caller may not see",
		slog.String("server", serverName),
		slog.String("action", action),
		slog.String("sessionID", logging.TruncateIdentifier(getSessionIDFromContext(ctx))))
	return true
}

// seenResources keeps the resources of the servers the caller may see.
func seenResources(view api.ServerVisibility, resources []api.ResourceOrigin) []api.ResourceOrigin {
	if !view.Restricted() {
		return resources
	}
	kept := resources[:0:0]
	for _, r := range resources {
		if view.Sees(r.Server) {
			kept = append(kept, r)
		}
	}
	return kept
}

// seenPrompts keeps the prompts of the servers the caller may see.
func seenPrompts(view api.ServerVisibility, prompts []api.PromptOrigin) []api.PromptOrigin {
	if !view.Restricted() {
		return prompts
	}
	kept := prompts[:0:0]
	for _, p := range prompts {
		if view.Sees(p.Server) {
			kept = append(kept, p)
		}
	}
	return kept
}

// seenNames keeps the server names the caller may see.
func seenNames(view api.ServerVisibility, names []string) []string {
	if !view.Restricted() {
		return names
	}
	kept := names[:0:0]
	for _, name := range names {
		if view.Sees(name) {
			kept = append(kept, name)
		}
	}
	return kept
}
