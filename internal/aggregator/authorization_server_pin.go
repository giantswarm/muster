package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
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
		SubjectScoped:         as.SubjectScoped(),
	}
	if as.ClientCredentialsSecretRef != nil {
		secrets := api.GetSecretCredentialsHandler()
		if secrets == nil {
			return fmt.Errorf("server %s references client credentials but no secret handler is registered", serverInfo.Name)
		}
		creds, err := secrets.LoadClientCredentials(ctx, as.ClientCredentialsSecretRef, serverInfo.GetNamespace())
		if err != nil {
			return fmt.Errorf("load client credentials for server %s: %w", serverInfo.Name, err)
		}
		pin.ClientID, pin.ClientSecret = creds.ClientID, creds.ClientSecret
	}

	issuer := strings.TrimSuffix(as.Issuer, "/")
	pinner.PinIssuer(issuer, pin)
	logging.InfoWithAttrs("Aggregator", "oauth_authorization_server_pinned",
		slog.String("server", serverInfo.Name),
		slog.String("issuer", issuer),
		slog.Bool("preregisteredClient", pin.ClientID != ""),
		slog.Bool("pinnedEndpoints", as.HasPinnedEndpoints()),
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
// server are the same: issuer, endpoints, client Secret reference, grant scope
// and scopes. A rotated Secret behind an unchanged reference is not a change
// here; the re-pin every login performs picks that up.
func sameAuthorizationServer(a, b *api.MCPServerAuthAuthorizationServer) bool {
	if a == nil || b == nil {
		return a == b
	}
	if strings.TrimSuffix(a.Issuer, "/") != strings.TrimSuffix(b.Issuer, "/") ||
		a.AuthorizationEndpoint != b.AuthorizationEndpoint ||
		a.TokenEndpoint != b.TokenEndpoint ||
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
// or removed; previous is the auth config the registry held for the server
// before. When that configuration described an authorization server and the
// server's current registration (if any) describes it differently or not at
// all, the description is released: replaced by another server's description
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
	var current *api.MCPServerAuthAuthorizationServer
	if info, ok := a.registry.GetServerInfo(name); ok {
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
	issuer := strings.TrimSuffix(prev.Issuer, "/")

	// Another server may still describe the issuer; its description takes
	// over. This server's own current description is applied by the caller.
	for other, info := range a.registry.GetAllServers() {
		if other == name {
			continue
		}
		as := authorizationServerOf(info.AuthConfig)
		if as == nil || strings.TrimSuffix(as.Issuer, "/") != issuer {
			continue
		}
		pinner.UnpinIssuer(issuer)
		if err := pinAuthorizationServer(ctx, info); err != nil {
			logging.Warn("Aggregator", "Authorization server pin of %s could not be re-applied after %s changed: %v", other, name, err)
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
