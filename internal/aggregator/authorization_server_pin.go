package aggregator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"

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

// pinnedDescription returns the description a server pins and the issuer key
// it is pinned under; nil when the server pins nothing. A plain issuer
// override, without a client, endpoints or subject-scoped grants, pins
// nothing; nor does a server that discovers its authorization server, unless
// its grants are subject-scoped and muster knows the issuer it discovered.
func pinnedDescription(info *ServerInfo) (*api.MCPServerAuthAuthorizationServer, string) {
	as := serverDescription(info)
	if as == nil || (as.ClientCredentialsSecretRef == nil && !as.HasPinnedEndpoints() && !as.SubjectScoped()) {
		return nil, ""
	}
	return as, api.ServerIssuerKey(info.Name, strings.TrimSuffix(as.Issuer, "/"))
}

// serverDescription returns how a server describes its authorization server:
// its pin (authorizationServerOf), else the description of the issuer it
// discovered (discoveredDescription); nil for neither.
func serverDescription(info *ServerInfo) *api.MCPServerAuthAuthorizationServer {
	if info == nil {
		return nil
	}
	if as := authorizationServerOf(info.AuthConfig); as != nil {
		return as
	}
	return discoveredDescription(info)
}

// issuerOwner returns the server whose description is pinned under an issuer
// key: of the described servers that pin something under it, the one whose
// MCPServer was created first, then the first by name. Gateway API orders
// conflicting routes the same way. Nil when no server pins under the key.
//
// An operator's description wins over a discovered one (see above): the owner
// is the oldest operator pin under the key; with none, a key that an operator
// names without pinning anything has no owner; only a key no operator names
// is owned by the oldest discovered description.
//
// The empty key has no owner: it is no issuer, and a server that pins nothing
// reports it too (pinnedDescription).
func issuerOwner(described map[string]*ServerInfo, issuer string) *ServerInfo {
	if issuer == "" {
		return nil
	}
	var owner, discoveredOwner *ServerInfo
	named := false
	for _, info := range described {
		if as := authorizationServerOf(info.AuthConfig); as != nil && api.ServerIssuerKey(info.Name, strings.TrimSuffix(as.Issuer, "/")) == issuer {
			named = true
		}
		if _, key := pinnedDescription(info); key != issuer {
			continue
		}
		if isDiscoveredDescription(info) {
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
		return owner
	}
	return discoveredOwner
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
	described := a.describedServers()
	described[info.Name] = info
	owner := issuerOwner(described, issuer)
	if isDiscoveredDescription(info) && (owner == nil || !isDiscoveredDescription(owner)) {
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
// describes the key differently.
//
// Idempotent. It runs when the server registers and again before every login,
// so a rotated Secret is picked up without a restart and a server that
// registered before OAuth was ready still gets its pin.
//
// A server that names its issuer without pinning anything also removes a
// discovered server's subject-scoped description from the key (an operator's
// naming wins over a backend's choice).
func (a *AggregatorServer) pinAuthorizationServer(ctx context.Context, serverInfo *ServerInfo) error {
	owner, err := a.issuerPinOwner(serverInfo)
	if err != nil {
		return err
	}
	if owner == nil {
		a.withdrawDiscoveredPin(serverInfo)
		return nil
	}
	return pinDescription(ctx, owner)
}

// AuthorizationServerConflict implements api.AuthorizationServerConflictChecker.
func (a *AggregatorServer) AuthorizationServerConflict(serverName string) error {
	_, err := a.issuerPinOwner(a.describedServers()[serverName])
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
	if pinner, err := issuerPinner(); err == nil {
		if pinned, ok := pinner.PinnedDescriptionKey(issuer); ok && pinned == pinnedDescriptionKey(info, as) {
			return nil
		}
	}
	_, err := a.issuerPinOwner(info)
	return err
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

// authorizationServerOf returns the authorization server an MCPServer's auth
// config describes (spec.auth.authorizationServer with an issuer), or nil.
// spec.auth.grantScope: subject applies to the pinned issuer as well, so the
// description returned carries it.
func authorizationServerOf(auth *api.MCPServerAuth) *api.MCPServerAuthAuthorizationServer {
	if auth == nil || auth.AuthorizationServer == nil || auth.AuthorizationServer.Issuer == "" {
		return nil
	}
	if auth.SubjectScoped() && !auth.AuthorizationServer.SubjectScoped() {
		as := *auth.AuthorizationServer
		as.GrantScope = api.GrantScopeSubject
		return &as
	}
	return auth.AuthorizationServer
}

// descriptionKey identifies what a description pins: endpoints (revocation
// included), expected issuer, client Secret reference and grant scope. A
// rotated Secret behind an unchanged reference keeps the key; the re-pin
// every login performs picks that up.
func descriptionKey(as *api.MCPServerAuthAuthorizationServer) string {
	fields := []string{as.AuthorizationEndpoint, as.TokenEndpoint, as.ExpectedIssuer, as.RevocationEndpoint,
		strconv.FormatBool(as.SubjectScoped())}
	if ref := as.ClientCredentialsSecretRef; ref != nil {
		fields = append(fields, ref.Name, ref.Namespace, ref.ClientIDKey, ref.ClientSecretKey)
	}
	return strings.Join(fields, "\x00")
}

// pinnedDescriptionKey is the IssuerPin.DescriptionKey a server's description
// is pinned with: descriptionKey, and for a discovered description
// discoveredPinKey, which an operator's pin that says the same never has.
func pinnedDescriptionKey(info *ServerInfo, as *api.MCPServerAuthAuthorizationServer) string {
	if isDiscoveredDescription(info) {
		return discoveredPinKey()
	}
	return descriptionKey(as)
}

// samePin reports whether two descriptions pin the same (descriptionKey).
func samePin(a, b *api.MCPServerAuthAuthorizationServer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return descriptionKey(a) == descriptionKey(b)
}

// sameAuthorizationServer reports whether two descriptions of an authorization
// server are the same: issuer, scopes and what they pin (samePin).
func sameAuthorizationServer(a, b *api.MCPServerAuthAuthorizationServer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.TrimSuffix(a.Issuer, "/") == strings.TrimSuffix(b.Issuer, "/") &&
		a.Scopes == b.Scopes &&
		samePin(a, b)
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
	issuer := api.ServerIssuerKey(name, strings.TrimSuffix(prev.Issuer, "/"))
	if issuer == "" {
		// No issuer key: nothing was pinned under it.
		return
	}
	if appOwnsIssuer(issuer) {
		// The pin is a provider App's and outlives every MCPServer.
		return
	}

	if owner := issuerOwner(described, issuer); owner != nil {
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

// releaseAuthorizationServerPin is releaseAuthorizationServerDescription for
// the authorization server a previous auth config pinned.
func (a *AggregatorServer) releaseAuthorizationServerPin(ctx context.Context, name string, previous *api.MCPServerAuth) {
	a.releaseAuthorizationServerDescription(ctx, name, authorizationServerOf(previous))
}

// describedServers returns every server that describes its authorization
// server to muster: the MCPServer definitions, connected or not, overlaid by
// the registry's entries. A definition whose backend is down still describes
// its authorization server, so a deregistration on a failed connection keeps
// the pin.
func (a *AggregatorServer) describedServers() map[string]*ServerInfo {
	described := make(map[string]*ServerInfo)
	if a.definedServers != nil {
		maps.Copy(described, a.definedServers())
	}
	maps.Copy(described, a.registry.GetAllServers())
	return described
}
