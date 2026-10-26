package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// pinAuthorizationServer hands the operator's description of an MCPServer's
// authorization server (spec.auth.authorizationServer) to the OAuth handler:
// the client registered with it out of band, read from its Secret; the
// endpoints of an AS that publishes no discovery document; and whether its
// grants belong to the person. A pin with none of those is a plain issuer
// override and needs no registration.
//
// Idempotent. It runs when the server registers and again before every login,
// so a rotated Secret is picked up without a restart and a server that
// registered before OAuth was ready still gets its pin.
func pinAuthorizationServer(ctx context.Context, serverInfo *ServerInfo) error {
	if serverInfo == nil || serverInfo.AuthConfig == nil {
		return nil
	}
	as := serverInfo.AuthConfig.AuthorizationServer
	if as == nil || (as.ClientCredentialsSecretRef == nil && !as.HasPinnedEndpoints() && !as.SubjectScoped()) {
		return nil
	}
	// Filed under the issuer key: a pin of a server outside muster's
	// namespace applies to no server outside that namespace.
	issuer := api.ServerIssuerKey(serverInfo.Name, strings.TrimSuffix(as.Issuer, "/"))
	if appOwnsIssuer(issuer) {
		// A provider App describes this issuer: its pin stands, and the
		// server's grants are the App's.
		logging.Debug("Aggregator", "Server %s pins issuer %s, which provider App owns; the App's pin stands", serverInfo.Name, issuer)
		return nil
	}

	handler := api.GetOAuthHandler()
	if handler == nil || !handler.IsEnabled() {
		return fmt.Errorf("server %s pins its authorization server but the OAuth proxy is disabled", serverInfo.Name)
	}
	pinner, ok := handler.(api.IssuerPinner)
	if !ok {
		return fmt.Errorf("server %s pins its authorization server but the OAuth handler cannot record pins", serverInfo.Name)
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

// sameAuthorizationServer reports whether two descriptions of an authorization
// server are the same: issuer, endpoints (revocation included), expected issuer, client Secret
// reference, grant scope and scopes. A rotated Secret behind an unchanged reference is not a change
// here; the re-pin every login performs picks that up.
func sameAuthorizationServer(a, b *api.MCPServerAuthAuthorizationServer) bool {
	if a == nil || b == nil {
		return a == b
	}
	if strings.TrimSuffix(a.Issuer, "/") != strings.TrimSuffix(b.Issuer, "/") ||
		a.AuthorizationEndpoint != b.AuthorizationEndpoint ||
		a.TokenEndpoint != b.TokenEndpoint ||
		a.ExpectedIssuer != b.ExpectedIssuer ||
		a.RevocationEndpoint != b.RevocationEndpoint ||
		a.GrantScope != b.GrantScope ||
		a.Scopes != b.Scopes {
		return false
	}
	ra, rb := a.ClientCredentialsSecretRef, b.ClientCredentialsSecretRef
	if ra == nil || rb == nil {
		return ra == rb
	}
	return *ra == *rb
}

// releaseAuthorizationServerPin runs when a server's registration is replaced
// or removed, or its MCPServer definition changes or goes; previous is the
// auth config the server was pinned with before. When that configuration
// described an authorization server and the server's current registration or
// definition (if any) describes it differently or not at all, the description
// is released: replaced by another server's description
// of the same issuer, or forgotten by the OAuth handler. The caller applies
// the server's current description afterwards (pinAuthorizationServer).
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
	handler := api.GetOAuthHandler()
	if handler == nil {
		return
	}
	pinner, ok := handler.(api.IssuerPinner)
	if !ok {
		return
	}
	issuer := api.ServerIssuerKey(name, strings.TrimSuffix(prev.Issuer, "/"))
	if appOwnsIssuer(issuer) {
		// The pin is a provider App's and outlives every MCPServer.
		return
	}

	// Another server may still describe the issuer under the same key (its
	// namespace and issuer); its description takes over. This server's own
	// current description is applied by the caller.
	for other, info := range described {
		if other == name {
			continue
		}
		as := authorizationServerOf(info.AuthConfig)
		if as == nil || api.ServerIssuerKey(other, strings.TrimSuffix(as.Issuer, "/")) != issuer {
			continue
		}
		pinner.UnpinIssuer(issuer)
		if err := pinAuthorizationServer(ctx, info); err != nil {
			logPinDeferred(info, err)
		}
		logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_pin_replaced",
			slog.String("server", name),
			slog.String("issuer", issuer),
			slog.String("replacedBy", other))
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
		for name, info := range a.definedServers() {
			described[name] = info
		}
	}
	for name, info := range a.registry.GetAllServers() {
		described[name] = info
	}
	return described
}
