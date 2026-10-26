package aggregator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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

// pinnedDescription returns the description a server pins and the issuer key
// it is pinned under; nil when the server pins nothing. A plain issuer
// override, without a client, endpoints or subject-scoped grants, pins
// nothing.
func pinnedDescription(info *ServerInfo) (*api.MCPServerAuthAuthorizationServer, string) {
	if info == nil {
		return nil, ""
	}
	as := authorizationServerOf(info.AuthConfig)
	if as == nil || (as.ClientCredentialsSecretRef == nil && !as.HasPinnedEndpoints() && !as.SubjectScoped()) {
		return nil, ""
	}
	return as, api.ServerIssuerKey(info.Name, strings.TrimSuffix(as.Issuer, "/"))
}

// issuerOwner returns the server whose description is pinned under an issuer
// key: of the described servers that pin something under it, the one whose
// MCPServer was created first, then the first by name. Gateway API orders
// conflicting routes the same way. Nil when no server pins under the key.
func issuerOwner(described map[string]*ServerInfo, issuer string) *ServerInfo {
	var owner *ServerInfo
	for _, info := range described {
		if _, key := pinnedDescription(info); key != issuer {
			continue
		}
		if owner == nil || describedBefore(info, owner) {
			owner = info
		}
	}
	return owner
}

func describedBefore(a, b *ServerInfo) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.Name < b.Name
}

// errAuthorizationServerConflict marks a server whose description of its
// authorization server is not the one pinned under its issuer key.
var errAuthorizationServerConflict = errors.New("another MCPServer describes this authorization server differently")

// issuerPinOwner returns the server whose description is pinned under the
// issuer key of info; nil when info pins nothing or a provider App owns the
// key. It returns errAuthorizationServerConflict when info's description is
// not the owner's.
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
	if ownerAS, _ := pinnedDescription(owner); !samePin(as, ownerAS) {
		return nil, fmt.Errorf("server %s: %w: server %s, whose description of %s is used; give both the same spec.auth.authorizationServer",
			info.Name, errAuthorizationServerConflict, owner.Name, api.IssuerURL(issuer))
	}
	return owner, nil
}

// pinAuthorizationServer hands the description of the authorization server
// under a server's issuer key to the OAuth handler: the owner's (issuerOwner),
// which is the server's own or the same. It returns
// errAuthorizationServerConflict, and pins nothing, for a server that
// describes the key differently.
//
// Idempotent. It runs when the server registers and again before every login,
// so a rotated Secret is picked up without a restart and a server that
// registered before OAuth was ready still gets its pin.
func (a *AggregatorServer) pinAuthorizationServer(ctx context.Context, serverInfo *ServerInfo) error {
	owner, err := a.issuerPinOwner(serverInfo)
	if owner == nil || err != nil {
		return err
	}
	return pinDescription(ctx, owner)
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
	pinner, err := issuerPinner()
	if err != nil {
		return fmt.Errorf("server %s pins its authorization server but %w", serverInfo.Name, err)
	}

	pin := api.IssuerPin{
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
func authorizationServerOf(auth *api.MCPServerAuth) *api.MCPServerAuthAuthorizationServer {
	if auth == nil || auth.AuthorizationServer == nil || auth.AuthorizationServer.Issuer == "" {
		return nil
	}
	return auth.AuthorizationServer
}

// samePin reports whether two descriptions pin the same: endpoints (revocation
// included), expected issuer, client Secret reference and grant scope. A
// rotated Secret behind an unchanged reference is not a difference here; the
// re-pin every login performs picks that up.
func samePin(a, b *api.MCPServerAuthAuthorizationServer) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.AuthorizationEndpoint != b.AuthorizationEndpoint ||
		a.TokenEndpoint != b.TokenEndpoint ||
		a.ExpectedIssuer != b.ExpectedIssuer ||
		a.RevocationEndpoint != b.RevocationEndpoint ||
		a.SubjectScoped() != b.SubjectScoped() {
		return false
	}
	ra, rb := a.ClientCredentialsSecretRef, b.ClientCredentialsSecretRef
	if ra == nil || rb == nil {
		return ra == rb
	}
	return *ra == *rb
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

// releaseAuthorizationServerPin runs when a server's registration is replaced
// or removed, or its MCPServer definition changes or goes; previous is the
// auth config the server was pinned with before. When that configuration
// described an authorization server and the server's current registration or
// definition (if any) describes it differently or not at all, the issuer key
// gets the description of its owner now (issuerOwner) in one replacement, or
// is unpinned when no server pins under it any more. The caller applies the
// server's current description afterwards (pinAuthorizationServer).
//
// Without this a pin outlives the MCPServer that placed it -- the OAuth
// handler keeps identifying with the old client, and metadata pinned with
// explicit endpoints keeps standing in for discovery -- until the process
// restarts (giantswarm/muster#1175).
func (a *AggregatorServer) releaseAuthorizationServerPin(ctx context.Context, name string, previous *api.MCPServerAuth) {
	prev := authorizationServerOf(previous)
	if prev == nil {
		return
	}
	described := a.describedServers()
	var current *api.MCPServerAuthAuthorizationServer
	if info, ok := described[name]; ok {
		current = authorizationServerOf(info.AuthConfig)
	}
	if sameAuthorizationServer(prev, current) {
		return
	}
	pinner, err := issuerPinner()
	if err != nil {
		return
	}
	issuer := api.ServerIssuerKey(name, strings.TrimSuffix(prev.Issuer, "/"))
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
