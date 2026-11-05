package aggregator

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// A server whose sign-in muster refuses (api.IsSignInRefused) loses its
// registry entry and the pins it owns, so core_auth_login answers with the
// refusal (definedSignInNotReady) instead of signing in with what was pinned
// before.

// DeregisterRefusedServer removes a server whose sign-in muster refuses,
// pending-auth entry or not, and releases the authorization server it was
// registered with. Unlike DeregisterServer it keeps what people hold for a
// tenant server: the definition stays, and the refusal may be lifted. A
// server already removed is not removed again on each failed retry of its
// backoff: only a pin it still owns goes, and the answer is the registry's
// "not found", so no event says its tools went again.
func (a *AggregatorServer) DeregisterRefusedServer(name string) error {
	info, registered := a.registry.GetServerInfo(name)
	if !registered {
		a.unpinRefused(name, nil)
		return fmt.Errorf("server %s not found", name)
	}
	_, err := a.deregisterServer(name, false, false, false)
	a.unpinRefused(name, info.AuthConfig)
	return err
}

// refusedPins returns the pinned issuer keys a refused server owns: of the
// description it was registered with (previous) and of the one its
// definition holds now. A key another MCPServer or a provider App owns is
// not the server's.
func (a *AggregatorServer) refusedPins(name string, previous *api.MCPServerAuth) []string {
	pinner, err := issuerPinner()
	if err != nil {
		return nil
	}
	described := a.describedServers()
	var keys []string
	for _, auth := range []*api.MCPServerAuth{previous, describedAuth(described, name)} {
		as, key := pinnedDescription(&ServerInfo{Name: name, AuthConfig: auth})
		if as == nil || appOwnsIssuer(key) {
			continue
		}
		if owner := issuerOwner(described, key); owner != nil && owner.Name != name {
			continue
		}
		if _, pinned := pinner.PinnedDescriptionKey(key); pinned && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// unpinRefused drops the pins a refused server owns (refusedPins): its
// client may not be used, and a pin read before the refusal would still sign
// in with it.
func (a *AggregatorServer) unpinRefused(name string, previous *api.MCPServerAuth) {
	pinner, err := issuerPinner()
	if err != nil {
		return
	}
	for _, key := range a.refusedPins(name, previous) {
		pinner.UnpinIssuer(key)
		logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_unpinned",
			slog.String("server", name), slog.String("issuer", key), slog.String("reason", "sign-in refused"))
	}
}

// describedAuth returns the auth config of a described server, or nil.
func describedAuth(described map[string]*ServerInfo, name string) *api.MCPServerAuth {
	if info, ok := described[name]; ok {
		return info.AuthConfig
	}
	return nil
}

// signInRefused reports whether a server's service failed because muster
// refuses its sign-in (api.IsSignInRefused): its client Secret, or its
// description of the authorization server. Its pending-auth entry, and the
// pin it was registered with, describe a sign-in that no longer applies, so
// the entry goes; core_auth_login then answers with the refusal
// (definedSignInNotReady) instead of signing in with the old pin.
func (am *AggregatorManager) signInRefused(serverName string) bool {
	if am.serviceRegistry == nil {
		return false
	}
	service, exists := am.serviceRegistry.Get(serverName)
	if !exists || service == nil {
		return false
	}
	switch service.GetState() {
	case api.StateFailed, api.StateUnreachable:
		// A policy not in force refuses every Secret read, often only
		// while a chart upgrade replaces it: the server is Failed and
		// answers with the reason, but keeps its entry and pins.
		err := service.GetLastError()
		return api.IsSignInRefused(err) && !errors.Is(err, api.ErrSecretReferencePolicyInactive)
	}
	return false
}
