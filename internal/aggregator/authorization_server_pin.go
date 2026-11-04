package aggregator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// One description of an authorization server is pinned per issuer key
// (api.ServerIssuerKey): the client, endpoints and grant scope every sign-in
// against the key uses. Several MCPServers of one namespace may name the same
// issuer. Of those that pin something, the oldest MCPServer owns the key
// (issuerOwner); another server may share the key only with the same
// description. A server that describes the key differently is refused: it pins
// nothing, and neither signs in nor connects with the owner's grants.

// A server that discovers its authorization server (no
// spec.auth.authorizationServer.issuer) and files its grants under the person
// (spec.auth.grantScope: subject) describes the issuer it discovered as a pin
// with grantScope: subject and nothing else (discoveredDescription), from the
// moment muster knows that issuer. The issuer is the backend's choice, not the
// operator's, so such a description never takes an issuer key from an
// operator: when an MCPServer of the namespace names the issuer in
// spec.auth.authorizationServer -- whatever it pins, and however old either
// server is -- the operator's description stands and the discovered server
// goes along with it, session-scoped unless that description is
// subject-scoped. It is never refused for it. Only for an issuer no operator
// names does the discovered description own the key; all discovered
// descriptions of one key are the same, so they never conflict.

// issuerOwner returns the server whose description is pinned under an issuer
// key (issuerOwnerOf).
func issuerOwner(described map[string]*ServerInfo, issuer string) *ServerInfo {
	owner, _ := issuerOwnerOf(described, issuer)
	return owner
}

// issuerOwnerOf returns the server whose description is pinned under an
// issuer key, and whether an operator's MCPServer names the key in
// spec.auth.authorizationServer.
//
// An operator's description wins over a discovered one (see above). The owner
// is the oldest operator description that pins something under the key --
// the one whose MCPServer was created first, then the first by name; Gateway
// API orders conflicting routes the same way. With none, a key an operator
// names without pinning anything has no owner. Only a key no operator names
// is owned by the oldest discovered description. Nil when nothing pins under
// the key.
//
// The empty key has no owner: it is no issuer, and a server that pins nothing
// reports it too (pinnedDescription).
func issuerOwnerOf(described map[string]*ServerInfo, issuer string) (owner *ServerInfo, named bool) {
	if issuer == "" {
		return nil, false
	}
	var discoveredOwner *ServerInfo
	for _, info := range described {
		as := authorizationServerOf(info.AuthConfig)
		discovered := false
		if as == nil {
			as, discovered = discoveredDescription(info), true
		}
		if as == nil || namedIssuerKey(info.Name, as) != issuer {
			continue
		}
		if !discovered {
			named = true
		}
		if !pinsSomething(as) {
			continue
		}
		if discovered {
			if discoveredOwner == nil || describedBefore(info, discoveredOwner) {
				discoveredOwner = info
			}
			continue
		}
		if owner == nil || describedBefore(info, owner) {
			owner = info
		}
	}
	if owner != nil || named {
		return owner, named
	}
	return discoveredOwner, false
}

func describedBefore(a, b *ServerInfo) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.Name < b.Name
}

// issuerPinOwner returns the server whose description is pinned under the
// issuer key of info; nil when info pins nothing or a provider App owns the
// key. It returns api.ErrAuthorizationServerConflict when info's description
// is not the owner's.
//
// A discovered description is never refused, and owns nothing for a server
// that is neither registered nor defined any more: a lookup or a listing
// that straddles the MCPServer's deletion must not pin for it.
func (a *AggregatorServer) issuerPinOwner(info *ServerInfo) (*ServerInfo, error) {
	as, issuer := pinnedDescription(info)
	if as == nil {
		return nil, nil
	}
	if appOwnsIssuer(issuer) {
		// A provider App describes this issuer: its pin stands, and the
		// server's grants are the App's.
		logging.Debug("Aggregator", "Server %s pins issuer %s, which provider App owns; the App's pin stands", info.Name, issuer)
		return nil, nil
	}
	discovered := isDiscoveredDescription(info)
	described := a.describedServers()
	if _, known := described[info.Name]; discovered && !known {
		logging.Debug("Aggregator", "Server %s is gone; its discovered issuer %s is not pinned", info.Name, issuer)
		return nil, nil
	}
	described[info.Name] = info
	owner, named := issuerOwnerOf(described, issuer)
	if discovered && named {
		// An operator names the issuer: its description stands, and the
		// discovered server goes along with it.
		logging.Debug("Aggregator", "Server %s discovered issuer %s, which an operator's MCPServer names; that description stands", info.Name, issuer)
		return nil, nil
	}
	if ownerAS, _ := pinnedDescription(owner); !samePin(as, ownerAS) {
		return nil, fmt.Errorf("server %s: %w: server %s, whose description of %s is used; give both the same spec.auth.authorizationServer",
			info.Name, api.ErrAuthorizationServerConflict, owner.Name, api.IssuerURL(issuer))
	}
	return owner, nil
}

// pinAuthorizationServer hands the description of the authorization server
// under a server's issuer key to the OAuth handler: the owner's (issuerOwner),
// which is the server's own or the same. It returns
// api.ErrAuthorizationServerConflict, and pins nothing, for a server that
// describes the key differently. A discovered description goes through
// pinDiscoveredIssuer, which skips the work when its pin, or another
// description's, already stands.
//
// Idempotent. It runs when the server registers and again before every login,
// so a rotated Secret is picked up without a restart and a server that
// registered before OAuth was ready still gets its pin.
func (a *AggregatorServer) pinAuthorizationServer(ctx context.Context, serverInfo *ServerInfo) error {
	if isDiscoveredDescription(serverInfo) {
		return a.pinDiscoveredIssuer(ctx, serverInfo)
	}
	return a.pinOwnerDescription(ctx, serverInfo)
}

// pinOwnerDescription is pinAuthorizationServer without the fast path of a
// discovered description.
//
// A server that names its issuer without pinning anything also removes a
// discovered server's subject-scoped description from the key (an operator's
// naming wins over a backend's choice), and so does an operator's description
// that cannot be pinned (its client Secret unreadable): nothing then stands
// under the key, as before discovered descriptions existed, until it can be.
func (a *AggregatorServer) pinOwnerDescription(ctx context.Context, serverInfo *ServerInfo) error {
	owner, err := a.issuerPinOwner(serverInfo)
	if err != nil {
		return err
	}
	if owner == nil {
		a.withdrawDiscoveredPin(serverInfo)
		return nil
	}
	if err := pinDescription(ctx, owner); err != nil {
		if !isDiscoveredDescription(owner) {
			_, issuer := pinnedDescription(owner)
			unpinDiscovered(issuer, "the operator's description of the issuer cannot be pinned")
		}
		return err
	}
	return nil
}

// AuthorizationServerConflict implements api.AuthorizationServerConflictChecker.
// A server that pins nothing, or describes a discovered issuer, never
// conflicts, and is answered without looking at every other MCPServer.
func (a *AggregatorServer) AuthorizationServerConflict(serverName string) error {
	info, ok := a.registry.GetServerInfo(serverName)
	if !ok {
		info = a.describedServers()[serverName]
	}
	if as, _ := pinnedDescription(info); as == nil || isDiscoveredDescription(info) {
		return nil
	}
	_, err := a.issuerPinOwner(info)
	return err
}

// connectionConflict returns the error issuerPinOwner returns for a server
// about to connect. A server whose description is the one pinned under its
// issuer key is let through without looking at every other MCPServer.
func (a *AggregatorServer) connectionConflict(info *ServerInfo) error {
	as, issuer := pinnedDescription(info)
	if as == nil || isDiscoveredDescription(info) {
		// A discovered description never conflicts (see above).
		return nil
	}
	if pinnedUnder(issuer, descriptionKey(as)) {
		return nil
	}
	_, err := a.issuerPinOwner(info)
	return err
}

// pinnedUnder reports whether the pin under an issuer key carries the given
// IssuerPin.DescriptionKey.
func pinnedUnder(issuer, key string) bool {
	pinner, err := issuerPinner()
	if err != nil {
		return false
	}
	pinned, ok := pinner.PinnedDescriptionKey(issuer)
	return ok && pinned == key
}

// issuerPinner returns the OAuth handler that records pins.
func issuerPinner() (api.IssuerPinner, error) {
	handler := api.GetOAuthHandler()
	if handler == nil || !handler.IsEnabled() {
		return nil, errors.New("the OAuth proxy is disabled")
	}
	pinner, ok := handler.(api.IssuerPinner)
	if !ok {
		return nil, errors.New("the OAuth handler cannot record pins")
	}
	return pinner, nil
}

// pinDescription pins a server's description of its authorization server,
// which must pin something (pinnedDescription): the client registered with it
// out of band, read from its Secret now; the endpoints of an AS that publishes
// no discovery document; and whether its grants belong to the person.
func pinDescription(ctx context.Context, serverInfo *ServerInfo) error {
	as, issuer := pinnedDescription(serverInfo)
	if as == nil || issuer == "" {
		return fmt.Errorf("server %s pins no authorization server", serverInfo.Name)
	}
	pinner, err := issuerPinner()
	if err != nil {
		return fmt.Errorf("server %s pins its authorization server but %w", serverInfo.Name, err)
	}

	pin := api.IssuerPin{
		DescriptionKey:        pinnedDescriptionKey(serverInfo, as),
		AuthorizationEndpoint: as.AuthorizationEndpoint,
		TokenEndpoint:         as.TokenEndpoint,
		ExpectedIssuer:        as.ExpectedIssuer,
		SubjectScoped:         as.SubjectScoped(),
		RevocationEndpoint:    as.RevocationEndpoint,
	}
	if as.ClientCredentialsSecretRef != nil {
		secrets := api.GetSecretCredentialsHandler()
		if secrets == nil {
			return fmt.Errorf("server %s references client credentials but no secret handler is registered", serverInfo.Name)
		}
		creds, err := secrets.LoadMCPServerClientCredentials(ctx, serverInfo.SecretOwner(), api.SecretFieldAuthorizationServer, as.ClientCredentialsSecretRef)
		if err != nil {
			return fmt.Errorf("load client credentials for server %s: %w", serverInfo.Name,
				tenantSecretReadError(serverInfo.Name, serverInfo.GetNamespace(), as.ClientCredentialsSecretRef.Name, err))
		}
		pin.ClientID, pin.ClientSecret = creds.ClientID, creds.ClientSecret
	}

	pinner.PinIssuer(issuer, pin)
	forgetPinDeferral(serverInfo)
	logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_pinned",
		slog.String("server", serverInfo.Name),
		slog.String("issuer", issuer),
		slog.Bool("preregisteredClient", pin.ClientID != ""),
		slog.Bool("pinnedEndpoints", as.HasPinnedEndpoints()),
		slog.String("expectedIssuer", pin.ExpectedIssuer),
		slog.String("revocationEndpoint", pin.RevocationEndpoint),
		slog.Bool("subjectScoped", pin.SubjectScoped))
	return nil
}

// releaseAuthorizationServerDescription runs when a server's registration is
// replaced or removed, or its MCPServer definition changes or goes; prev is how
// the server described its authorization server before (serverDescription):
// its pin, or the issuer it discovered with subject-scoped grants. When it
// described one and the server's current registration or definition (if any)
// describes it differently or not at all, the issuer key
// gets the description of its owner now (issuerOwner) in one replacement, or
// is unpinned when no server pins under it any more. The caller applies the
// server's current description afterwards (pinAuthorizationServer).
//
// Without this a pin outlives the MCPServer that placed it -- the OAuth
// handler keeps identifying with the old client, and metadata pinned with
// explicit endpoints keeps standing in for discovery -- until the process
// restarts (giantswarm/muster#1175).
func (a *AggregatorServer) releaseAuthorizationServerDescription(ctx context.Context, name string, prev *api.MCPServerAuthAuthorizationServer) {
	if prev == nil {
		return
	}
	// A re-registration that describes the same is answered from its entry,
	// without looking at every other MCPServer.
	if info, ok := a.registry.GetServerInfo(name); ok && sameAuthorizationServer(prev, serverDescription(info)) {
		return
	}
	described := a.describedServers()
	var current *api.MCPServerAuthAuthorizationServer
	if info, ok := described[name]; ok {
		current = serverDescription(info)
	}
	if sameAuthorizationServer(prev, current) {
		return
	}
	pinner, err := issuerPinner()
	if err != nil {
		return
	}
	issuer := namedIssuerKey(name, prev)
	if issuer == "" {
		// No issuer key: nothing was pinned under it.
		return
	}
	if appOwnsIssuer(issuer) {
		// The pin is a provider App's and outlives every MCPServer.
		return
	}

	if owner := issuerOwner(described, issuer); owner != nil {
		ownerAS, _ := pinnedDescription(owner)
		if pinnedUnder(issuer, pinnedDescriptionKey(owner, ownerAS)) {
			// The owner's description already stands (the server that went
			// only went along with it): nothing to re-read or replace.
			return
		}
		if err := pinDescription(ctx, owner); err != nil {
			// The description pinned until now is no longer the owner's.
			pinner.UnpinIssuer(issuer)
			logPinDeferred(owner, err)
			return
		}
		logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_pin_replaced",
			slog.String("server", name),
			slog.String("issuer", issuer),
			slog.String("replacedBy", owner.Name))
		return
	}

	pinner.UnpinIssuer(issuer)
	logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_unpinned",
		slog.String("server", name),
		slog.String("issuer", issuer))
}
