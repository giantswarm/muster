package aggregator

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// serverSuspended reports whether the MCPServer's spec holds its service down:
// spec.suspended=true, written by core_service_stop (the portal's Deactivate)
// and cleared by core_service_start. The reconciler stops the service and keeps
// it stopped for as long as the field is set, so nothing a session does can
// make the server usable meanwhile: a sign-in would only produce a connection
// the reconciler tears down within milliseconds, and a state flip the event
// handler cannot act on (issue #1211).
//
// The flag is read from the definition source through the registered MCPServer
// manager — the same source the reconciler acts on; the aggregator keeps no
// copy of it. Without a manager (servers registered directly, as in tests) or
// for a name the manager does not know (a registry entry that outlived its CR)
// the server is not suspended. Any other lookup error is returned: the caller
// cannot tell whether a sign-in makes sense and must not guess.
func serverSuspended(ctx context.Context, serverName string) (bool, error) {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return false, nil
	}
	info, err := manager.GetMCPServer(ctx, serverName)
	if err != nil {
		if api.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("look up MCPServer %s: %w", serverName, err)
	}
	return info != nil && info.Suspended, nil
}

// suspendedServers returns, for the given registry entries, the names whose
// MCPServer spec holds the service down. The spec is read once and only when
// a server is down — that is when the flag has something to explain; a server
// that is up is not suspended, or not for longer than the reconciler needs to
// stop it. A failed read leaves every flag unset: auth://status is still
// served from what the aggregator knows itself.
func suspendedServers(ctx context.Context, servers map[string]*ServerInfo) map[string]bool {
	anyDown := false
	for _, info := range servers {
		if info.IsDown() {
			anyDown = true
			break
		}
	}
	if !anyDown {
		return nil
	}

	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil
	}
	definitions, err := manager.ListMCPServers(ctx)
	if err != nil {
		logging.Debug("Aggregator", "auth://status: cannot read which servers are suspended: %v", err)
		return nil
	}

	suspended := make(map[string]bool)
	for _, definition := range definitions {
		if definition.Suspended {
			suspended[definition.Name] = true
		}
	}
	return suspended
}

// suspendedServerMessage is the answer to a sign-in attempt on a suspended
// server: the state and the way out, in the words core_service_restart uses
// for the same state.
func suspendedServerMessage(serverName string) string {
	return fmt.Sprintf("Server '%s' is deactivated (spec.suspended=true); activate it with core_service_start before signing in.", serverName)
}
