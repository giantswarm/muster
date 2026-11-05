package aggregator

import (
	"context"
	"log/slog"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// DeregisterServer removes a backend MCP server from the aggregator.
//
// This method cleanly removes a backend server from the aggregator, which will
// cause all tools, resources, and prompts from that server to become unavailable.
// The backend client connection is closed as part of the deregistration process.
//
// Additionally, this method cleans up any stale session connections for the server.
// This is critical for handling MCPServer renames, where the old server is deleted
// and a new one is created. Without this cleanup, session connections stored under
// the old server name would persist and cause stale auth status displays.
//
// Args:
//   - name: Unique identifier of the server to remove
//
// Returns an error if the server is not found or deregistration fails.
func (a *AggregatorServer) DeregisterServer(name string) error {
	_, err := a.deregisterServer(name, false, false, true)
	return err
}

// DeregisterServerOnStateChange is DeregisterServer for the aggregator's
// service state-change handling: it never removes an entry that requires
// per-session authentication, whatever the (possibly stale) event said. Such
// entries are owned by the auth-required hook and the periodic pending-auth
// heal, and removing one leaves core_auth_login answering "server not found"
// and every session without the server until the next heal. reconnecting
// says the service is still alive and re-establishing its connection (failed
// health probes, a reconnect due): calls in the window until it is registered
// again wait for it or are answered as retryable (awaitServer). Returns
// whether an entry was removed.
func (a *AggregatorServer) DeregisterServerOnStateChange(name string, reconnecting bool) (bool, error) {
	return a.deregisterServer(name, true, reconnecting, false)
}

// deregisterServer removes a server's entry. forget also forgets what a
// tenant server's sign-ins stored (forgetTenantServer): its service is gone.
func (a *AggregatorServer) deregisterServer(name string, keepSessionAuth, reconnecting, forget bool) (bool, error) {
	logging.InfoWithAttrs("Aggregator", "DeregisterServer called",
		slog.String("server", name))

	// Snapshot the request time before the slow store cleanup below. The
	// registry delete at the end only removes entries older than this, so a
	// registration that lands while cleanup runs (e.g. the pending-auth entry
	// created right after a 401 while a stale state=starting event is still
	// being processed) survives instead of being clobbered.
	requestedAt := time.Now()

	// Remove auth state, pooled connections and capabilities for this server
	// across all sessions.
	a.revokeServerSessions(context.Background(), name)
	if a.capabilityStore != nil {
		if err := a.capabilityStore.DeleteServer(context.Background(), name); err != nil {
			logging.WarnWithAttrs("Aggregator", "Failed to delete server from capability store",
				slog.String("server", name), slog.String("error", err.Error()))
		}
	}

	// The entry's description of its authorization server, read before the
	// delete: a removed server releases the authorization server it described.
	var previousDescription *api.MCPServerAuthAuthorizationServer
	info, registered := a.registry.GetServerInfo(name)
	if registered {
		previousDescription = serverDescription(info)
	}

	// A reconnecting server is tracked before its entry goes, so no call
	// finds it neither registered nor reconnecting. A later event of the same
	// reconnect (starting, another failed attempt) finds no entry and leaves
	// the tracking as it is; a stop or removal ends it.
	tracked := reconnecting && registered
	if tracked {
		a.reconnects.lost(name)
	}

	// A definition that goes takes the issuer it discovered with it, before
	// the release: else the definition, still listed, would describe the
	// issuer from memory and keep its pin. Without a registry entry that
	// issuer is the description to release.
	if !keepSessionAuth {
		if issuer := a.discovered.forget(name); issuer != "" && !registered {
			previousDescription = &api.MCPServerAuthAuthorizationServer{Issuer: issuer, GrantScope: api.GrantScopeSubject}
		}
	}

	removed := true
	var err error
	if keepSessionAuth {
		removed, err = a.registry.DeregisterUnlessSessionAuth(name, requestedAt)
	} else {
		err = a.registry.DeregisterRequestedAt(name, requestedAt)
	}
	if (err == nil && removed) || (!keepSessionAuth && !registered) {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		a.releaseAuthorizationServerDescription(releaseCtx, name, previousDescription)
		cancel()
	}
	if forget {
		// The service is gone: the definition was deleted. Also
		// when it never reached the registry: a session may hold sign-ins of
		// an earlier registration.
		forgetCtx, cancel := context.WithTimeout(context.Background(), tenantForgetTimeout)
		a.forgetTenantServer(forgetCtx, name)
		cancel()
		forgetServerPinDeferrals(name)
	}
	if !reconnecting || (tracked && (err != nil || !removed)) {
		a.reconnects.settled(name)
	}
	return removed, err
}
